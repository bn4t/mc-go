package blackbox

import (
	"bytes"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// metaEnt is one decoded entity_data entry: serializer type id plus
// the raw value bytes (for particles, the list split into blobs).
type metaEnt struct {
	typ       byte
	val       []byte
	particles [][]byte
}

// decodeMetaEntries parses an entity_data packet body (after the
// leading entity VarInt) into index -> entry.
func decodeMetaEntries(t *testing.T, body []byte) map[int]metaEnt {
	t.Helper()
	r := protocol.NewPacketReader(body)
	out := map[int]metaEnt{}
	for {
		idx, err := r.Read(1)
		if err != nil || idx[0] == 0xFF {
			break
		}
		typ, err := r.Read(1)
		if err != nil {
			t.Fatalf("meta entry idx=%d missing type", idx[0])
		}
		e := metaEnt{typ: typ[0]}
		switch typ[0] {
		case 0: // byte
			b, _ := r.Read(1)
			e.val = b
		case 1: // varint
			v, _ := r.VarInt()
			var vb protocol.Buffer
			vb.VarInt(v)
			e.val = vb.Bytes()
		case 2: // long
			v, _ := r.VarLong()
			var vb protocol.Buffer
			vb.VarLong(v)
			e.val = vb.Bytes()
		case 3: // float
			b, _ := r.Read(4)
			e.val = b
		case 6: // optional component: bool + NBT tag
			b, _ := r.Read(1)
			e.val = b
			if len(b) == 1 && b[0] == 1 {
				rest, _ := r.Read(r.Remaining())
				br := bytes.NewReader(rest)
				if _, err := nbt.ReadUnnamed(br); err != nil {
					t.Fatalf("component tag at idx %d unreadable", idx[0])
				}
				e.val = append(e.val, rest[:len(rest)-br.Len()]...)
				r = protocol.NewPacketReader(rest[len(rest)-br.Len():])
			}
		case 8: // bool
			b, _ := r.Read(1)
			e.val = b
		case 0x10: // single particle holder: VarInt id + Int32 payload
			pid, _ := r.VarInt()
			b, _ := r.Read(4)
			e.val = append([]byte{byte(pid)}, b...)
		case 0x11: // particles list: VarInt count + (VarInt id + Int32) each
			n, _ := r.VarInt()
			for i := int32(0); i < n; i++ {
				pid, _ := r.VarInt()
				b, _ := r.Read(4)
				blob := append([]byte{byte(pid)}, b...)
				e.particles = append(e.particles, blob)
			}
		case 0x12: // villager_data: three VarInts
			for i := 0; i < 3; i++ {
				v, _ := r.VarInt()
				e.val = append(e.val, byte(v))
			}
		case 13: // optional_living_entity_reference: bool + 16-byte uuid
			b, _ := r.Read(1)
			e.val = b
			if len(b) == 1 && b[0] == 1 {
				u, _ := r.Read(16)
				e.val = append(e.val, u...)
			}
		case 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31:
			// Holder-variant serializers (cat/frog/wolf/pig/cow/
			// chicken variant + sound_variant registries): a VarInt
			// registry id on the wire.
			v, _ := r.VarInt()
			var vb protocol.Buffer
			vb.VarInt(v)
			e.val = vb.Bytes()
		default:
			t.Fatalf("unhandled serializer type %d at idx %d (body %x)", typ[0], idx[0], body)
		}
		out[int(idx[0])] = e
	}
	return out
}

// TestCureConversionSyncParity pins the full zombie-villager cure wire
// shape: the zombie's pairing burst carries health, the NoAI mob flag,
// converting, pinned villagerdata and the finalized bit plus its
// syncable movement_speed attribute; the next tick resends everything
// (now with the strength effect's entity_effect particle) and emits a
// zero-delta move_entity_pos. The replacement villager packs
// villagerdata + finalized at its own pairing, copies the live effects
// (strength), adds nausea, and emits the shrunken particles entry once
// strength's duration lapses. Run at night so the zombie never burns.
func TestCureConversionSyncParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("CureS"),
	}, func(s *server.Server) {})
	defer targets.cleanup()

	type mobStream struct {
		typ     int32
		dataSeq [][]byte
		attrs   [][]byte
		moves   [][]byte
	}
	collect := func(addr string) []mobStream {
		c, pr, pw := joinPlayBlackbox(t, addr, "CureS")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "gamerule doDaylightCycle false")
		sendCommand(t, pw, "time set night")
		sendCommand(t, pw, `summon minecraft:zombie_villager 8.5 -60 8.5 {NoAI:1b,VillagerData:{type:"minecraft:plains",profession:"minecraft:farmer",level:2},ConversionTime:10}`)

		var order []int32       // mob entity ids in spawn order
		seen := map[int32]int{} // eid -> index into order
		streams := map[int]*mobStream{}
		stripEID := func(b []byte) []byte {
			rp := protocol.NewPacketReader(b)
			_, _ = rp.VarInt()
			return b[len(b)-rp.Remaining():]
		}
		end := time.Now().Add(6 * time.Second)
		for time.Now().Before(end) {
			_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				var kb protocol.Buffer
				kb.Raw(p.Data)
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case server.IDPlaySpawnEntity:
				rp := protocol.NewPacketReader(p.Data)
				id, _ := rp.VarInt()
				_, _ = rp.Read(16)
				typ, _ := rp.VarInt()
				if typ == 154 || typ == 140 {
					seen[id] = len(order)
					order = append(order, id)
					streams[seen[id]] = &mobStream{typ: typ}
				}
			case 0x63: // set_entity_data
				rp := protocol.NewPacketReader(p.Data)
				eid, _ := rp.VarInt()
				if i, ok := seen[eid]; ok {
					streams[i].dataSeq = append(streams[i].dataSeq, stripEID(p.Data))
				}
			case 0x83: // update_attributes
				rp := protocol.NewPacketReader(p.Data)
				eid, _ := rp.VarInt()
				if i, ok := seen[eid]; ok {
					streams[i].attrs = append(streams[i].attrs, stripEID(p.Data))
				}
			case server.IDPlayEntityUpdatePos: // move_entity_pos
				rp := protocol.NewPacketReader(p.Data)
				eid, _ := rp.VarInt()
				if i, ok := seen[eid]; ok {
					streams[i].moves = append(streams[i].moves, stripEID(p.Data))
				}
			}
		}
		if len(order) != 2 {
			t.Fatalf("expected 2 mob spawns (zombie_villager + villager), got %d", len(order))
		}
		return []mobStream{*streams[0], *streams[1]}
	}

	van := collect(targets.vanilla)
	got := collect(targets.mcgo)

	// compareDataSeqs diffs two per-mob entity_data sequences
	// entry-wise; particles entries compare as unordered sets.
	compareDataSeqs := func(label string, want, g [][]byte) {
		t.Helper()
		if len(want) != len(g) {
			t.Fatalf("%s: entity_data packet count %d, want %d", label, len(g), len(want))
		}
		for i := range want {
			we := decodeMetaEntries(t, want[i])
			ge := decodeMetaEntries(t, g[i])
			if len(we) != len(ge) {
				t.Fatalf("%s: data pkt %d entries %v, want %v", label, i, keysOf(ge), keysOf(we))
			}
			for idx, w := range we {
				g2, ok := ge[idx]
				if !ok {
					t.Fatalf("%s: data pkt %d missing idx %d", label, i, idx)
				}
				if g2.typ != w.typ {
					t.Fatalf("%s: data pkt %d idx %d type %d, want %d", label, i, idx, g2.typ, w.typ)
				}
				if idx == 10 {
					if !sameBlobs(g2.particles, w.particles) {
						t.Fatalf("%s: data pkt %d particles %x, want %x", label, i, g2.particles, w.particles)
					}
					continue
				}
				if string(g2.val) != string(w.val) {
					t.Fatalf("%s: data pkt %d idx %d val %x, want %x", label, i, idx, g2.val, w.val)
				}
			}
		}
	}

	for i, name := range []string{"zombie_villager", "villager"} {
		v, g := van[i], got[i]
		if v.typ != g.typ {
			t.Fatalf("%s: spawn type %d, want %d", name, g.typ, v.typ)
		}
		// Guard against a vacuous pass: vanilla itself must produce
		// the measured burst (pairing data, dirty resend, attributes).
		if len(v.dataSeq) < 2 || len(v.attrs) == 0 || len(v.moves) == 0 {
			t.Fatalf("%s: vanilla stream degenerate (data=%d attrs=%d moves=%d)", name, len(v.dataSeq), len(v.attrs), len(v.moves))
		}
		compareDataSeqs(name, v.dataSeq, g.dataSeq)
		if len(v.attrs) != len(g.attrs) {
			t.Fatalf("%s: update_attributes count %d, want %d", name, len(g.attrs), len(v.attrs))
		}
		for j := range v.attrs {
			if string(v.attrs[j]) != string(g.attrs[j]) {
				t.Fatalf("%s: attr pkt %d %x, want %x", name, j, g.attrs[j], v.attrs[j])
			}
		}
		if len(g.moves) == 0 {
			t.Fatalf("%s: no move_entity_pos seen", name)
		}
		for j, m := range g.moves {
			if string(m) != string(v.moves[j]) && j < len(v.moves) {
				t.Fatalf("%s: move pkt %d %x, want %x", name, j, m, v.moves[j])
			}
		}
	}
}

func keysOf(m map[int]metaEnt) []int {
	out := make([]int, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func sameBlobs(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
	for _, x := range a {
		found := false
		for j, y := range b {
			if !used[j] && string(x) == string(y) {
				used[j] = true
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
