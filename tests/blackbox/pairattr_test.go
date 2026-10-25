package blackbox

// TestPairingAttrsParity pins vanilla's update_attributes placement in
// the pairing burst: ServerEntity.sendPairingData emits
// AttributeMap.getSyncableAttributes — only attribute INSTANCES that
// exist (the AttributeMap creates them lazily on first access) AND are
// client-syncable. Measured on vanilla 26.2:
//
//   - zombie-family (zombie, husk, drowned — setBaby's baby roll touches
//     movement_speed): in-bundle [movement_speed]
//   - AbstractHorse (randomizes speed/jump/health at finalizeSpawn):
//     in-bundle [movement_speed, jump_strength, max_health]
//   - wolf (tamable max_health touch): in-bundle [max_health]
//   - every other living mob: no attributes in the bundle; a loose
//     [movement_speed] update_attributes follows ~one tick after spawn
//     once the first AI tick materializes the instance.
//
// Vanilla's emit order is fastutil hash order — the attribute id SETS
// are compared, never the sequence.

import (
	"net"
	"sort"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type wirePk struct {
	id   int32
	eid  int32
	typ  string // decoded entity type for add_entity
	atts []int  // decoded attribute ids for update_attributes
	raw  []byte // packet body (for packets needing deeper decode)
	bund bool   // inside a bundle delimiter pair
}

// entityStream collects play packets for d and decodes the two shapes
// the pairing assertions need: add_entity (id+type) and
// update_attributes (eid+attr ids).
func entityStream(t *testing.T, tag string, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, d time.Duration) []wirePk {
	t.Helper()
	var seq []wirePk
	deadline := time.Now().Add(d)
	inBundle := false
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
		for {
			p, err := pr.ReadPacket()
			if err != nil {
				break
			}
			switch p.ID {
			case server.IDPlayKeepAliveCB:
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			case server.IDPlayChunkBatchDone:
				_ = ackChunkBatch(pw)
			case 0x00: // bundle_delimiter toggles bundle scope
				inBundle = !inBundle
				continue
			}
			w := wirePk{id: int32(p.ID), eid: -1, bund: inBundle, raw: p.Data}
			r := protocol.NewPacketReader(p.Data)
			switch p.ID {
			case server.IDPlaySpawnEntity:
				w.eid, _ = r.VarInt()
				r.UUID()
				ty, _ := r.VarInt()
				w.typ, _ = server.EntityTypeName(ty)
			case server.IDPlayUpdateAttributes:
				w.eid, _ = r.VarInt()
				n, _ := r.VarInt()
				for i := 0; i < int(n); i++ {
					a, _ := r.VarInt()
					w.atts = append(w.atts, int(a))
					r.Double()
					nm, _ := r.VarInt()
					for j := 0; j < int(nm); j++ {
						r.UUID()
						r.Double()
						r.Byte()
					}
				}
			}
			seq = append(seq, w)
		}
	}
	return seq
}

// pairAttrSets maps entity id -> the attribute-id set its bundled
// update_attributes carried (empty bundle slot counts as empty set).
func pairAttrSets(seq []wirePk) map[int32][]int {
	out := map[int32][]int{}
	for _, w := range seq {
		if w.id != int32(server.IDPlayUpdateAttributes) || !w.bund {
			continue
		}
		out[w.eid] = w.atts
	}
	return out
}

// looseAttrEmits lists eids that got an update_attributes outside any
// bundle.
func looseAttrEmits(seq []wirePk) map[int32][][]int {
	out := map[int32][][]int{}
	for _, w := range seq {
		if w.id == int32(server.IDPlayUpdateAttributes) && !w.bund {
			out[w.eid] = append(out[w.eid], w.atts)
		}
	}
	return out
}

func TestPairingAttrsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Trk"),
	}, nil)
	defer targets.cleanup()

	// Summons spread out enough that their bundles can't interleave.
	summons := []struct{ typ, cmd string }{
		{"zombie", "summon minecraft:zombie 8 -60 8"},
		{"pig", "summon minecraft:pig 8 -60 12"},
		{"wolf", "summon minecraft:wolf 10 -60 8"},
		{"horse", "summon minecraft:horse 10 -60 12"},
		{"sheep", "summon minecraft:sheep 12 -60 8"},
	}

	run := func(tag, addr string) []wirePk {
		c, pr, pw := joinPlayBlackbox(t, addr, "Trk")
		defer c.Close()
		drainChunkWindow(t, tag, c, pr, pw, 300*time.Millisecond)
		done := make(chan []wirePk, 1)
		go func() { done <- entityStream(t, tag, c, pr, pw, 4*time.Second) }()
		time.Sleep(400 * time.Millisecond)
		for _, s := range summons {
			sendCommand(t, pw, s.cmd)
			time.Sleep(250 * time.Millisecond)
		}
		return <-done
	}

	want := run("vanilla", targets.vanilla)
	got := run("mc-go", targets.mcgo)

	wantSets := pairAttrSets(want)
	gotSets := pairAttrSets(got)

	// Pair each summoned entity's in-bundle attr set across servers by
	// summoning order: match add_entity type name -> eid.
	typeSet := func(seq []wirePk, sets map[int32][]int, typ string) ([]int, bool) {
		for _, w := range seq {
			if w.id == int32(server.IDPlaySpawnEntity) && w.typ == typ {
				v, ok := sets[w.eid]
				return v, ok
			}
		}
		return nil, false
	}
	for _, s := range summons {
		wl, _ := typeSet(want, wantSets, s.typ)
		gl, _ := typeSet(got, gotSets, s.typ)
		sort.Ints(wl)
		sort.Ints(gl)
		if len(wl) != len(gl) {
			t.Fatalf("%s in-bundle attr set: vanilla=%v mc-go=%v", s.typ, wl, gl)
		}
		for i := range wl {
			if wl[i] != gl[i] {
				t.Fatalf("%s in-bundle attr set: vanilla=%v mc-go=%v", s.typ, wl, gl)
			}
		}
	}

	// The lazy emit: every living mob gets a loose movement_speed (id
	// 26) update_attributes within the window on vanilla.
	wantLoose := looseAttrEmits(want)
	gotLoose := looseAttrEmits(got)
	eidByType := func(seq []wirePk, typ string) int32 {
		for _, w := range seq {
			if w.id == int32(server.IDPlaySpawnEntity) && w.typ == typ {
				return w.eid
			}
		}
		return -1
	}
	for _, s := range summons {
		we, ge := eidByType(want, s.typ), eidByType(got, s.typ)
		if _, ok := wantLoose[we]; !ok {
			t.Fatalf("vanilla emitted no loose update_attributes for %s", s.typ)
		}
		if _, ok := gotLoose[ge]; !ok {
			t.Fatalf("mc-go emitted no loose update_attributes for %s", s.typ)
		}
	}
}
