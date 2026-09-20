package blackbox

import (
	"fmt"
	"math"
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// wearObs is the observable client-side trace of one wear phase: the
// slot resyncs that arrived (window-0 only), the entity-event status
// bytes, and the Award Statistics rows the next request produced.
type wearObs struct {
	slots []string
	evs   []int32
	stats []statEntry
}

// drainWear reads server traffic for d, keeping the session alive and
// folding container-slot resyncs, entity events and the latest spawn
// entity id into obs.
func drainWear(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, pos *[3]float64, d time.Duration, obs *wearObs, lastSpawn *int32) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(time.Until(deadline)))
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
				if x, err := r.Double(); err == nil {
					y, _ := r.Double()
					z, _ := r.Double()
					pos[0], pos[1], pos[2] = x, y, z
				}
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		case server.IDPlayContainerSlot:
			r := protocol.NewPacketReader(p.Data)
			wid, e1 := r.VarInt()
			_, e2 := r.VarInt()
			slot, e3 := r.Short()
			it, e4 := readSlotBB(r)
			if e1 != nil || e2 != nil || e3 != nil || e4 != nil || wid != 0 {
				continue
			}
			obs.slots = append(obs.slots, fmt.Sprintf("%d=%s", slot, stackKey(it)))
		case server.IDPlayEntityEvent:
			r := protocol.NewPacketReader(p.Data)
			if _, err := r.Int(); err != nil {
				continue
			}
			if status, err := r.Byte(); err == nil {
				obs.evs = append(obs.evs, int32(status))
			}
		case server.IDPlaySpawnEntity:
			r := protocol.NewPacketReader(p.Data)
			if eid, err := r.VarInt(); err == nil && lastSpawn != nil {
				*lastSpawn = eid
			}
		}
	}
}

// runWearScenario executes the tool-wear script on one target: mine
// dirt twice, mine a torch (instant: no wear, still a use), mine one
// more dirt to break the pickaxe, then hit a summoned pig with a
// sword.
func runWearScenario(t *testing.T, addr, name string) []wearObs {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(120 * time.Second))
	var pos [3]float64
	obs := wearObs{}

	// Setup: known spot, a damaged pick and a fresh sword.
	drainWear(t, c, pr, pw, &pos, 1200*time.Millisecond, &obs, nil)
	sendCommand(t, pw, "tp @s 6 -60 2 0 0")
	drainWear(t, c, pr, pw, &pos, 400*time.Millisecond, &obs, nil)
	sendCommand(t, pw, "item replace entity @s hotbar.0 with minecraft:stone_pickaxe[minecraft:damage=128]")
	sendCommand(t, pw, "item replace entity @s hotbar.1 with minecraft:stone_sword")
	drainWear(t, c, pr, pw, &pos, 400*time.Millisecond, &obs, nil)

	x, y, z := int(math.Floor(pos[0])), int(math.Floor(pos[1])), int(math.Floor(pos[2]))
	// All dirt digs mine the same adjacent spot so every drop lands
	// inside the pickup radius on either implementation; the torch
	// digs a supported instant-break block two blocks out whose drop
	// lands too far to be collected by anyone.
	sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d dirt", x, y, z+1))
	sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d dirt", x, y, z+2))
	sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d torch", x, y+1, z+2))
	drainWear(t, c, pr, pw, &pos, 400*time.Millisecond, &obs, nil)

	var phases []wearObs
	dig := func(bx, by, bz int, resetDirt bool) {
		sendDig(t, pw, 0, int32(bx), int32(by), int32(bz), 1)
		sendDig(t, pw, 2, int32(bx), int32(by), int32(bz), 1)
		po := wearObs{}
		// The dig takes ~15 ticks and the drop's pickup delay another
		// 10 before it lands in the inventory; 2s covers both.
		drainWear(t, c, pr, pw, &pos, 2*time.Second, &po, nil)
		po.stats = drainStats(t, c, pr, pw, &pos, 5*time.Second)
		if resetDirt {
			// Restore the adjacent dirt for the next phase; the
			// command traffic folds into this phase's drain tail.
			sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d dirt", x, y, z+1))
			drainWear(t, c, pr, pw, &pos, 300*time.Millisecond, &po, nil)
		}
		phases = append(phases, po)
	}
	dig(x, y, z+1, true)    // -> damage 129
	dig(x, y+1, z+2, false) // torch: instant, used++ but no wear
	dig(x, y, z+1, true)    // -> damage 130
	dig(x, y, z+1, false)   // -> damage 131: break

	// Sword phase: hotbar slot 2 (index 1), a pig on top of us, one
	// swing.
	var hb protocol.Buffer
	hb.Short(1) // set_carried_item carries a Short slot
	if err := pw.WritePacket(server.IDPlayHeldChangeSB, hb.Bytes()); err != nil {
		t.Fatal(err)
	}
	sendCommand(t, pw, "summon minecraft:pig ~ ~ ~")
	var pigEID int32
	po := wearObs{}
	drainWear(t, c, pr, pw, &pos, 1500*time.Millisecond, &po, &pigEID)
	if pigEID == 0 {
		t.Fatalf("%s: pig never spawned", name)
	}
	var atk protocol.Buffer
	atk.VarInt(pigEID)
	if err := pw.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
		t.Fatal(err)
	}
	drainWear(t, c, pr, pw, &pos, 1200*time.Millisecond, &po, nil)
	po.stats = drainStats(t, c, pr, pw, &pos, 5*time.Second)
	phases = append(phases, po)
	return phases
}

// TestToolWearParity pins durability behaviour end to end: each mined
// non-instant block costs the tool its damage_per_block, an
// instant-mine block costs nothing but still counts a use, reaching
// max damage broadcasts the break event and empties the slot, and a
// landed hit wears a weapon by item_damage_per_attack. The emitted
// packets and the statistics rows must equal vanilla's.
func TestToolWearParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{
		"ops.json": opsJSONFor("WearV", "WearM"),
	}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
	})
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runWearScenario(t, tg.vanilla, "WearV")
	got := runWearScenario(t, tg.mcgo, "WearM")

	if len(want) != len(got) {
		t.Fatalf("phase count: vanilla %d mc-go %d", len(want), len(got))
	}
	for i := range want {
		w, g := want[i], got[i]
		if len(w.slots) != len(g.slots) {
			t.Errorf("phase %d slot resyncs: vanilla %v mc-go %v", i, w.slots, g.slots)
		} else {
			for j := range w.slots {
				if w.slots[j] != g.slots[j] {
					t.Errorf("phase %d slot[%d]: vanilla %s mc-go %s", i, j, w.slots[j], g.slots[j])
				}
			}
		}
		if len(w.evs) != len(g.evs) {
			t.Errorf("phase %d entity events: vanilla %v mc-go %v", i, w.evs, g.evs)
		} else {
			for j := range w.evs {
				if w.evs[j] != g.evs[j] {
					t.Errorf("phase %d event[%d]: vanilla %d mc-go %d", i, j, w.evs[j], g.evs[j])
				}
			}
		}
		if len(w.stats) != len(g.stats) {
			t.Errorf("phase %d stats rows: vanilla %v mc-go %v", i, w.stats, g.stats)
			continue
		}
		for j := range w.stats {
			wr, gr := w.stats[j], g.stats[j]
			if wr.cat != gr.cat || wr.id != gr.id {
				t.Errorf("phase %d stat %d key: vanilla %d:%d mc-go %d:%d", i, j, wr.cat, wr.id, gr.cat, gr.id)
				continue
			}
			if isTimedStat(wr) {
				continue
			}
			if wr.val != gr.val {
				t.Errorf("phase %d stat[%d] %d:%d value: vanilla %d mc-go %d", i, j, wr.cat, wr.id, wr.val, gr.val)
			}
		}
	}
}
