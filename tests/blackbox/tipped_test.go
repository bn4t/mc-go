//go:build blackbox

// Tipped-arrow parity: 26.2's Stray.getArrow and Bogged.getArrow each
// addEffect on the fresh arrow — MobEffectInstance(SLOWNESS, 600) and
// MobEffectInstance(POISON, 100). A mob-built arrow stack carries no
// potion_duration_scale, so the victim gets the FULL duration (the
// 1/8 tipped-arrow scaling only rides crafted arrows). The effect
// lands via update_mob_effect on the victim after a hurting hit.
// The arrow's DATA_ID_EFFECT_COLOR (meta idx 9, int) comes with the
// first sync: slowness 9154528, poison 8889187.

package blackbox

import (
	"fmt"
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type tipObs struct {
	spawnAll map[int32]int
	selfEID  int32
	spawned  bool
	effects  []tipEff
	arrows   int
	arrowIDs map[int32]bool
	colors   map[int32]int32 // arrow eid -> meta idx-9 color
}

type tipEff struct {
	target int32
	id     int32
	dur    int32
}

func tipPump(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *tipObs, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlaySyncPos:
			r := protocol.NewPacketReader(p.Data)
			id, _ := r.VarInt()
			var b protocol.Buffer
			b.VarInt(id)
			_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
		case server.IDPlaySpawnEntity:
			r := protocol.NewPacketReader(p.Data)
			eid, _ := r.VarInt()
			_, _ = r.Read(16)
			typ, _ := r.VarInt()
			obs.spawnAll[typ]++
			if typ == 6 { // arrow
				obs.arrows++
				obs.arrowIDs[eid] = true
			}
		case server.IDPlayEntityMetadata:
			r := protocol.NewPacketReader(p.Data)
			eid, _ := r.VarInt()
			for {
				idx, err := r.Byte()
				if err != nil || idx == 0xFF {
					break
				}
				ser, _ := r.VarInt()
				switch ser {
				case 0: // byte
					v, _ := r.Byte()
					_ = v
				case 1: // int (varint)
					v, _ := r.VarInt()
					if idx == 9 && obs.arrowIDs[eid] {
						obs.colors[eid] = v
					}
				default:
					// Only the two scalars above appear for an arrow's
					// color sync; bail on anything richer.
					idx = 0xFF
					_ = idx
				}
				if idx == 0xFF {
					break
				}
			}
		case server.IDPlayJoinGame:
			r := protocol.NewPacketReader(p.Data)
			obs.selfEID, _ = r.Int()
		case server.IDPlayUpdateMobEffect:
			r := protocol.NewPacketReader(p.Data)
			target, _ := r.VarInt()
			id, _ := r.VarInt()
			_, _ = r.Byte()
			dur, _ := r.VarInt()
			obs.effects = append(obs.effects, tipEff{target, id, dur})
		}
	}
}

// TestTippedArrowParity pins the effect on the victim: stray ->
// slowness(1) 600, bogged -> poison(18) 100, plain skeleton -> no
// update_mob_effect at all.
func TestTippedArrowParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	run := func(addr string) []string {
		var out []string
		c, pr, pw := joinPlayBlackbox(t, addr, "P")
		_ = c.SetDeadline(time.Now().Add(240 * time.Second))
		obs := &tipObs{arrowIDs: map[int32]bool{}, colors: map[int32]int32{}, spawnAll: map[int32]int{}}
		tipPump(t, c, pr, pw, obs, 400*time.Millisecond)
		// selfEID from the join packet — read until it arrives.
		for i := 0; i < 200 && obs.selfEID == 0; i++ {
			_ = c.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			if p.ID == server.IDPlayJoinGame {
				r := protocol.NewPacketReader(p.Data)
				obs.selfEID, _ = r.Int()
			}
			if p.ID == server.IDPlayKeepAliveCB {
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			}
		}
		sendCommand(t, pw, "gamemode survival P")
		sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration false")
		sendCommand(t, pw, "effect give P minecraft:absorption 600 250")
		sendCommand(t, pw, "effect give P minecraft:health_boost 600 250")
		sendCommand(t, pw, "kill @e[type=!minecraft:player]")
		sendCommand(t, pw, "time set night")
		tipPump(t, c, pr, pw, obs, 600*time.Millisecond)

		leg := func(name, typ string, wantEff int32, wantDur, durTol int32) {
			obs.effects = nil
			obs.arrows = 0
			obs.arrowIDs = map[int32]bool{}
			obs.colors = map[int32]int32{}
			obs.spawnAll = map[int32]int{}
			sendCommand(t, pw, fmt.Sprintf("summon minecraft:%s ~8 ~ ~", typ))
			sendCommand(t, pw, "execute if entity @e[type=minecraft:"+typ+"]")
			tipPump(t, c, pr, pw, obs, 14*time.Second)
			var eff tipEff
			seen := false
			for _, ef := range obs.effects {
				if ef.target == obs.selfEID {
					eff = ef
					seen = true
					break
				}
			}
			color := int32(0)
			for _, v := range obs.colors {
				color = v
			}
			if wantEff == 0 {
				out = append(out, fmt.Sprintf("%s arrows>0=%v eff=%v", name, obs.arrows > 0, seen))
			} else {
				durOK := eff.dur >= wantDur-durTol && eff.dur <= wantDur+durTol
				out = append(out, fmt.Sprintf("%s arrows>0=%v eff=%v id=%v dur=%v color>0=%v",
					name, obs.arrows > 0, seen && eff.id == wantEff && durOK, wantEff, wantDur, color > 0))
			}
			sendCommand(t, pw, fmt.Sprintf("kill @e[type=minecraft:%s]", typ))
			sendCommand(t, pw, "kill @e[type=minecraft:arrow]")
			sendCommand(t, pw, "effect clear P")
			sendCommand(t, pw, "effect give P minecraft:absorption 600 250")
			tipPump(t, c, pr, pw, obs, 400*time.Millisecond)
		}

		leg("stray", "stray", 1, 600, 200)
		leg("bogged", "bogged", 18, 100, 60)
		leg("skeleton", "skeleton", 0, 0, 0)
		return out
	}

	van := run(targets.vanilla)
	got := run(targets.mcgo)
	for k := range van {
		t.Logf("leg %d vanilla: %s", k, van[k])
		t.Logf("leg %d mc-go:   %s", k, got[k])
		if van[k] != got[k] {
			t.Errorf("leg %d mismatch: %q vs %q", k, van[k], got[k])
		}
	}
}
