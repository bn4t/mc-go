package blackbox

// TestKillAdvParity kills mobs with a melee sword against both servers
// and diffs the update_advancements traffic semantically: the set of
// advancement defs sent, and every progress entry's criteria with their
// obtained flags. Entry order inside the packet is a HashMap iteration
// artifact that varies run to run on the real server, so the compare is
// order-free. The system_chat advancement announcements are compared
// by translate key.
//
// Legs: a zombie (first kill — the adventure tree lights up, Monster
// Hunter announces), a skeleton (one more Monsters Hunted criterion,
// small flush, no announce), and a pig (every matching criterion was
// already granted — vanilla sends nothing at all).

import (
	"bytes"
	"fmt"
	"sort"
	"testing"
	"time"

	"mc-go/internal/data"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type advFlush struct {
	defs       map[string][]byte          // def id -> raw def entry bytes
	prog       map[string]map[string]bool // adv id -> criterion -> obtained
	removed    map[string]bool            // def ids sent in the removed list
	chats      []string                   // system_chat translate keys
	spawnTypes map[int32][]int32          // diagnostic: entity type -> eids seen
	sounds     int                        // diagnostic: sound packets seen
	soundIDs   map[int32]bool             // diagnostic: sound_event ids seen
	removedIDs []int32                    // diagnostic: remove_entities ids seen
	advPkts    int                        // diagnostic: update_advancements packets seen
	codRemoved bool                       // diagnostic: target cod observed removed
	ev35       int                        // diagnostic: entity event 35 (totem pop) seen
	mobFx      map[int32]string           // diagnostic: effect id -> "amp,dur,flags" first seen
	chatRaw    [][]byte                   // diagnostic: raw system_chat payloads
	fxRemoved  []bool                     // diagnostic: remove_mob_effect packets seen
}

// decodeUpdateAdv parses an update_advancements payload. Def entries
// are sliced by the captured wire table (data.AdvDefWire) — a def whose
// id is absent fails loudly rather than guessing the display layout.
func decodeUpdateAdv(t *testing.T, d []byte, out *advFlush) {
	t.Helper()
	r := protocol.NewPacketReader(d)
	if _, err := r.Bool(); err != nil { // reset flag
		t.Fatalf("adv packet reset flag: %v", err)
	}
	ndefs, err := r.VarInt()
	if err != nil {
		t.Fatalf("adv def count: %v", err)
	}
	for i := 0; i < int(ndefs); i++ {
		before := r.Remaining()
		id, err := r.String()
		if err != nil {
			t.Fatalf("adv def id: %v", err)
		}
		entry, ok := data.AdvDefWire[id]
		if !ok {
			t.Fatalf("def %s not in the captured wire table", id)
		}
		skip := len(entry) - (before - r.Remaining())
		if skip < 0 {
			t.Fatalf("def %s entry shorter than its id", id)
		}
		if _, err := r.Read(skip); err != nil {
			t.Fatalf("def %s body: %v", id, err)
		}
		if prev, dup := out.defs[id]; dup && !bytes.Equal(prev, entry) {
			t.Fatalf("def %s resent with different bytes", id)
		}
		out.defs[id] = entry
	}
	nrem, err := r.VarInt()
	if err != nil {
		t.Fatalf("adv removed count: %v", err)
	}
	for i := 0; i < int(nrem); i++ {
		id, _ := r.String()
		out.removed[id] = true
	}
	nprog, err := r.VarInt()
	if err != nil {
		t.Fatalf("adv progress count: %v", err)
	}
	for i := 0; i < int(nprog); i++ {
		id, err := r.String()
		if err != nil {
			t.Fatalf("adv progress id: %v", err)
		}
		nc, err := r.VarInt()
		if err != nil {
			t.Fatalf("adv %s criteria count: %v", id, err)
		}
		if out.prog[id] == nil {
			out.prog[id] = map[string]bool{}
		}
		for j := 0; j < int(nc); j++ {
			name, err := r.String()
			if err != nil {
				t.Fatalf("adv %s criterion name: %v", id, err)
			}
			obt, err := r.Bool()
			if err != nil {
				t.Fatalf("adv %s criterion flag: %v", id, err)
			}
			if obt {
				if _, err := r.Long(); err != nil { // timestamp
					t.Fatalf("adv %s criterion time: %v", id, err)
				}
			}
			out.prog[id][name] = obt
		}
	}
	if _, err := r.Bool(); err != nil { // showAdvancements
		t.Fatalf("adv packet trailing flag: %v", err)
	}
	if r.Remaining() != 0 {
		t.Fatalf("adv packet %d trailing bytes", r.Remaining())
	}
}

// chatKey reads the translate key off a system_chat payload's
// component, "literal" when the root is a bare string.
func chatKey(d []byte) string {
	v, err := nbt.ReadUnnamed(bytes.NewReader(d))
	if err != nil {
		return "<undecodable>"
	}
	if m, ok := v.(map[string]any); ok {
		if tr, ok := m["translate"].(string); ok {
			return tr
		}
	}
	return "literal"
}

func diffAdvFlush(want, got *advFlush) []string {
	var diffs []string
	for id, e := range want.defs {
		g, ok := got.defs[id]
		if !ok {
			diffs = append(diffs, "missing def "+id)
		} else if !bytes.Equal(g, e) {
			diffs = append(diffs, "def bytes differ "+id)
		}
	}
	for id := range got.defs {
		if _, ok := want.defs[id]; !ok {
			diffs = append(diffs, "extra def "+id)
		}
	}
	for id, wantCrits := range want.prog {
		g, ok := got.prog[id]
		if !ok {
			diffs = append(diffs, "missing progress "+id)
			continue
		}
		for c, ob := range wantCrits {
			if gob, ok := g[c]; !ok {
				diffs = append(diffs, fmt.Sprintf("%s missing criterion %s", id, c))
			} else if gob != ob {
				diffs = append(diffs, fmt.Sprintf("%s criterion %s obtained %v != %v", id, c, gob, ob))
			}
		}
		for c := range g {
			if _, ok := wantCrits[c]; !ok {
				diffs = append(diffs, fmt.Sprintf("%s extra criterion %s", id, c))
			}
		}
	}
	for id := range got.prog {
		if _, ok := want.prog[id]; !ok {
			diffs = append(diffs, "extra progress "+id)
		}
	}
	for id := range want.removed {
		if !got.removed[id] {
			diffs = append(diffs, "missing removed "+id)
		}
	}
	for id := range got.removed {
		if !want.removed[id] {
			diffs = append(diffs, "extra removed "+id)
		}
	}
	w, g := append([]string{}, want.chats...), append([]string{}, got.chats...)
	sort.Strings(w)
	sort.Strings(g)
	if fmt.Sprint(w) != fmt.Sprint(g) {
		diffs = append(diffs, fmt.Sprintf("chat keys %v != %v", w, g))
	}
	return diffs
}

func TestKillAdvParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("KAdv"),
	}, func(s *server.Server) {
		// The vanilla harness boots gamemode=creative; matching that
		// makes `gamemode survival` an actual switch on both sides.
		s.GameMode = server.GameModeCreative
	})
	defer targets.cleanup()

	run := func(addr string) []*advFlush {
		c, pr, pw := joinPlayBlackbox(t, addr, "KAdv")
		defer c.Close()
		setup := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		collect := func(d time.Duration, f *advFlush) {
			end := time.Now().Add(d)
			for time.Now().Before(end) {
				_ = c.SetReadDeadline(time.Now().Add(time.Until(end)))
				p, err := pr.ReadPacket()
				if err != nil {
					return
				}
				switch p.ID {
				case server.IDPlayKeepAliveCB:
					var kb protocol.Buffer
					kb.Raw(p.Data)
					_ = pw.WritePacket(server.IDPlayKeepAliveSB, kb.Bytes())
				case server.IDPlaySyncPos:
					r := protocol.NewPacketReader(p.Data)
					if id, err := r.VarInt(); err == nil {
						var b protocol.Buffer
						b.VarInt(id)
						_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
					}
				case server.IDPlayUpdateAdvance:
					decodeUpdateAdv(t, p.Data, f)
				case server.IDPlaySystemChatCB:
					f.chats = append(f.chats, chatKey(p.Data))
				}
			}
		}
		// The location trigger's first fire lands anywhere inside the
		// post-join seconds depending on tick alignment; every drain in
		// this window funnels into `setup`.
		collect(1200*time.Millisecond, setup)
		sendCommand(t, pw, "gamemode survival @s")
		sendCommand(t, pw, "tp @s 4 -60 2 -90 0")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with minecraft:stone_sword")
		collect(900*time.Millisecond, setup)

		movePos := func(ground bool) {
			var b protocol.Buffer
			b.Double(4)
			b.Double(-60)
			b.Double(2)
			if ground {
				b.Byte(0x01)
			}
			_ = pw.WritePacket(0x1E, b.Bytes())
		}

		spawnEID := func(f *advFlush, name string, x, y, z float64) int32 {
			sendCommand(t, pw, fmt.Sprintf("summon minecraft:%s %v %v %v {NoAI:1b}", name, x, y, z))
			end := time.Now().Add(1200 * time.Millisecond)
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
				case server.IDPlayUpdateAdvance:
					decodeUpdateAdv(t, p.Data, f)
				case server.IDPlaySystemChatCB:
					f.chats = append(f.chats, chatKey(p.Data))
				}
				if p.ID != server.IDPlaySpawnEntity {
					continue
				}
				r := protocol.NewPacketReader(p.Data)
				eid, _ := r.VarInt()
				r.UUID()
				typ, _ := r.VarInt()
				ex, _ := r.Double()
				ey, _ := r.Double()
				ez, _ := r.Double()
				_ = typ
				if int32(ex) == int32(x) && int32(ey) == int32(y) && int32(ez) == int32(z) {
					return eid
				}
			}
			t.Fatalf("%s never spawned", name)
			return 0
		}

		// kill swings at eid until the entity-event-3 death notice or
		// the swing budget runs out, collecting the flush meanwhile.
		kill := func(eid int32, f *advFlush) {
			for swing := 0; swing < 10; swing++ {
				movePos(true)
				var ab protocol.Buffer
				ab.VarInt(eid)
				_ = pw.WritePacket(0x01, ab.Bytes())
				died := false
				end := time.Now().Add(700 * time.Millisecond)
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
					case server.IDPlaySyncPos:
						r := protocol.NewPacketReader(p.Data)
						if id, err := r.VarInt(); err == nil {
							var b protocol.Buffer
							b.VarInt(id)
							_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
						}
					case server.IDPlayUpdateAdvance:
						decodeUpdateAdv(t, p.Data, f)
					case server.IDPlaySystemChatCB:
						f.chats = append(f.chats, chatKey(p.Data))
					case server.IDPlayEntityEvent:
						r := protocol.NewPacketReader(p.Data)
						if e, _ := r.Int(); int32(e) == eid {
							if st, _ := r.Byte(); st == 3 {
								died = true
							}
						}
					}
				}
				if died {
					return
				}
			}
			t.Fatalf("entity %d never died", eid)
		}

		legs := make([]*advFlush, 3)
		// Setup traffic (the location trigger's first flush on either
		// side) counts toward the cumulative session state.
		legs[0] = setup
		for i := 1; i < 3; i++ {
			legs[i] = &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		}
		movePos(true)
		kill(spawnEID(legs[0], "zombie", 6.5, -60, 2.5), legs[0])
		kill(spawnEID(legs[1], "skeleton", 6.5, -60, 0.5), legs[1])
		movePos(true)
		kill(spawnEID(legs[2], "pig", 6.5, -60, 3.5), legs[2])
		// Settle: any trailing flush lands inside the last drain anyway,
		// this is just a heartbeat margin for the next leg's readers.
		collect(300*time.Millisecond, legs[2])
		return legs
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)

	// Flushes lag the kill by one server tick and can straddle a leg
	// boundary on either side, so the compare is cumulative: the union
	// of every def, progress entry, and chat announce across the whole
	// run must match. Only the pig leg keeps a local assertion — it
	// must be silent on both servers.
	union := func(legs []*advFlush) *advFlush {
		u := &advFlush{defs: map[string][]byte{}, prog: map[string]map[string]bool{}, removed: map[string]bool{}}
		for _, f := range legs {
			for id, e := range f.defs {
				u.defs[id] = e
			}
			for id, crits := range f.prog {
				if u.prog[id] == nil {
					u.prog[id] = map[string]bool{}
				}
				for c, ob := range crits {
					u.prog[id][c] = u.prog[id][c] || ob
				}
			}
			u.chats = append(u.chats, f.chats...)
		}
		return u
	}
	for i, tag := range []string{"zombie", "skeleton", "pig"} {
		t.Logf("%s vanilla: defs=%d prog=%d chats=%v", tag, len(want[i].defs), len(want[i].prog), want[i].chats)
		t.Logf("%s mcgo:    defs=%d prog=%d chats=%v", tag, len(got[i].defs), len(got[i].prog), got[i].chats)
	}
	if len(want[2].prog)+len(want[2].defs) != 0 || len(got[2].prog)+len(got[2].defs) != 0 {
		t.Fatalf("pig leg expected silence, got vanilla=%d/%d mcgo=%d/%d",
			len(want[2].defs), len(want[2].prog), len(got[2].defs), len(got[2].prog))
	}
	if diffs := diffAdvFlush(union(want), union(got)); len(diffs) > 0 {
		t.Fatalf("advancement parity: %d diffs\n%s", len(diffs), joinLines(diffs))
	}
}
