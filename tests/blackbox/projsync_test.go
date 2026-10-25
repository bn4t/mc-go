// Projectile sendChanges parity — vanilla (ServerEntity.sendChanges
// javap + live probe) emits move packets at the per-type
// updateInterval cadence plus the drift-gated set_entity_motion emit;
// it NEVER teleports a projectile per tick. Pins the per-family emit
// counts for a NoGravity arrow (move packets only) and a gravity
// arrow (move + motion at each gate) for identical windows on both
// servers, plus zero entity_teleport packets.
package blackbox

import (
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
)

func TestProjectileSyncParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("Sync"),
	}, nil)
	defer targets.cleanup()

	type counts struct {
		pos      int // move_entity_pos — arrows must never emit it
		moves    int // move_entity_pos_rot/rot for the arrow's eid
		motion   int // set_entity_motion for the arrow's eid
		teleport int // entity_teleport for the arrow's eid
	}
	got := map[string][]*counts{}
	eids := map[string][]int32{}

	firstVarInt := func(line string) (int32, bool) {
		_, raw, ok := strings.Cut(line, ":")
		if !ok {
			return 0, false
		}
		id, err := protocol.NewPacketReader([]byte(raw)).VarInt()
		return id, err == nil
	}

	for _, tg := range []struct{ tag, addr string }{
		{"vanilla", targets.vanilla},
		{"mc-go", targets.mcgo},
	} {
		conn, pr, pw := joinPlayBlackbox(t, tg.addr, "Sync")
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "tp @s 0 -60 0 0 0")
		time.Sleep(400 * time.Millisecond)
		sink.take()

		sendCommand(t, pw, "summon minecraft:arrow 4 -30 0 {Motion:[0.3d,0.0d,0.0d],NoGravity:1b}")
		sendCommand(t, pw, "summon minecraft:arrow 4 -30 4 {Motion:[0.3d,0.4d,0.0d]}")
		time.Sleep(1500 * time.Millisecond)

		perID := map[int32]*counts{}
		for _, l := range sink.take() {
			pref, _, ok := strings.Cut(l, ":")
			if !ok {
				continue
			}
			switch pref {
			case "0x1": // add_entity: VarInt id + UUID16 + VarInt type
				r := protocol.NewPacketReader([]byte(strings.SplitN(l, ":", 2)[1]))
				id, e1 := r.VarInt()
				_, e2 := r.Read(16)
				typ, e3 := r.VarInt()
				if e1 == nil && e2 == nil && e3 == nil && typ == 6 {
					perID[id] = &counts{}
				}
			case "0x35": // move_entity_pos
				if id, ok := firstVarInt(l); ok {
					if c, yes := perID[id]; yes {
						c.pos++
					}
				}
			case "0x36", "0x38": // move_entity_pos_rot/rot
				if id, ok := firstVarInt(l); ok {
					if c, yes := perID[id]; yes {
						c.moves++
					}
				}
			case "0x65": // set_entity_motion
				if id, ok := firstVarInt(l); ok {
					if c, yes := perID[id]; yes {
						c.motion++
					}
				}
			case "0x23": // entity_teleport
				if id, ok := firstVarInt(l); ok {
					if c, yes := perID[id]; yes {
						c.teleport++
					}
				}
			}
		}
		if len(perID) != 2 {
			conn.Close()
			t.Fatalf("%s: expected 2 arrows, tracked %d", tg.tag, len(perID))
		}
		// eids are monotonic: the lower one is the first (NoGravity)
		// summon on each leg.
		for id := range perID {
			eids[tg.tag] = append(eids[tg.tag], id)
		}
		if eids[tg.tag][0] > eids[tg.tag][1] {
			eids[tg.tag][0], eids[tg.tag][1] = eids[tg.tag][1], eids[tg.tag][0]
		}
		got[tg.tag] = []*counts{perID[eids[tg.tag][0]], perID[eids[tg.tag][1]]}
		conn.Close()
	}

	for i, name := range []string{"nogravity", "gravity"} {
		v, g := got["vanilla"][i], got["mc-go"][i]
		t.Logf("%s arrow: vanilla %+v mc-go %+v", name, *v, *g)
		if v.pos != 0 || g.pos != 0 {
			t.Fatalf("%s: move_entity_pos emitted for an arrow — vanilla forces PosRot (vanilla %d, mc-go %d)", name, v.pos, g.pos)
		}
		if v.moves == 0 || g.moves == 0 {
			t.Fatalf("%s: no move packets (vanilla %d, mc-go %d)", name, v.moves, g.moves)
		}
		if v.moves != g.moves || v.motion != g.motion || v.teleport != g.teleport {
			t.Fatalf("%s: emit counts diverge (vanilla %+v, mc-go %+v)", name, *v, *g)
		}
		if name == "gravity" && (v.motion == 0 || g.motion == 0) {
			t.Fatalf("gravity: drift motion emits missing (vanilla %d, mc-go %d)", v.motion, g.motion)
		}
	}
}
