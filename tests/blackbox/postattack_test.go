package blackbox

// TestMobPostAttackParity pins the innate post-attack riders vanilla
// 26.2's doHurtTarget overrides land on a melee victim:
//   - husk (bare hands): hunger, round(effectiveDifficulty*140) ticks —
//     the flat harness world resolves the regional factor to half the
//     difficulty id, so NORMAL pins 140t, amplifier 0, flags 0x06.
//   - cave_spider: poison only on NORMAL+ — 140t at NORMAL.
//   - iron_golem: every swing plays entity.iron_golem.attack; a landed
//     hit broadcasts entity-event 4, launches the victim
//     (dv.y += 0.4*(1-kbr) — the player takes the full 0.4) and rolls
//     attack_damage/2 + rand(attack_damage) = 7.5..21.5 raw.
//
// The golem is neutral on vanilla — the leg provokes it with a player
// attack before waiting for the swing. mc-go's chase-melee targets the
// nearest survival player either way; the provoke keeps both servers
// on the same code path. Each leg runs on a fresh join so a leg's
// damage/death never bleeds into the next.
import (
	"fmt"
	"net"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type paObs struct {
	health   float32
	selfEID  int32 // P's own entity id — joins_game field 0
	dmgEvent int
	effects  map[int32][2]int32 // effect id -> [amplifier, duration] first seen
	effFlags map[int32]byte
	events   map[int32][]byte // eid -> entity-event bytes seen
	motion   map[int32][]float64
	spawned  map[int32]int32 // entity type -> eid
}

// paPump reads P's conn for d, folding post-attack signals into obs.
func paPump(t *testing.T, c net.Conn, pr *protocol.Reader, pw *protocol.Writer, obs *paObs, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(120 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		r := protocol.NewPacketReader(p.Data)
		switch p.ID {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlaySyncPos:
			if id, err := r.VarInt(); err == nil {
				var b protocol.Buffer
				b.VarInt(id)
				_ = pw.WritePacket(server.IDPlayTeleportConfirmSB, b.Bytes())
			}
		case server.IDPlaySetHealth:
			if f, err := r.Float(); err == nil {
				obs.health = f
			}
		case server.IDPlayJoinGame:
			if id, err := r.Int(); err == nil {
				obs.selfEID = id
			}
		case server.IDPlayDamageEvent:
			// Only P's own damage events count — /kill and mob-hurt
			// broadcasts land here too for tracked entities.
			if eid, err := r.VarInt(); err == nil && eid == obs.selfEID {
				obs.dmgEvent++
			}
		case server.IDPlayUpdateMobEffect:
			_, _ = r.VarInt()
			eff, _ := r.VarInt()
			amp, _ := r.VarInt()
			dur, _ := r.VarInt()
			fl, _ := r.Byte()
			if _, seen := obs.effects[eff]; !seen {
				obs.effects[eff] = [2]int32{amp, dur}
				obs.effFlags[eff] = fl
			}
		case server.IDPlayEntityEvent:
			eid, _ := r.Int() // int32, not VarInt
			ev, _ := r.Byte()
			obs.events[eid] = append(obs.events[eid], ev)
		case server.IDPlayEntityMotion:
			eid, err := r.VarInt()
			if err != nil {
				continue
			}
			x, y, z, err := r.ReadLpVec3()
			if err != nil {
				continue
			}
			obs.motion[eid] = []float64{x, y, z}
		case server.IDPlaySpawnEntity:
			eid, err := r.VarInt()
			if err != nil {
				continue
			}
			if _, err := r.UUID(); err != nil {
				continue
			}
			if ty, err := r.VarInt(); err == nil {
				obs.spawned[ty] = eid
			}
		}
	}
}

func TestMobPostAttackParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{"ops.json": opsJSONFor("P")}, nil)
	defer targets.cleanup()

	run := func(addr string) []string {
		var out []string

		newObs := func() *paObs {
			return &paObs{
				health:   20,
				effects:  map[int32][2]int32{},
				effFlags: map[int32]byte{},
				events:   map[int32][]byte{},
				motion:   map[int32][]float64{},
				spawned:  map[int32]int32{},
			}
		}
		join := func() (net.Conn, *protocol.Reader, *protocol.Writer) {
			c, pr, pw := joinPlayBlackbox(t, addr, "P")
			_ = c.SetDeadline(time.Now().Add(120 * time.Second))
			sendCommand(t, pw, "gamemode survival P")
			sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration false")
			sendCommand(t, pw, "time set night")
			// A dead player anchors nothing — `~2 ~ ~` fails silently
			// and the death state suppresses entity tracking. A huge
			// absorb pool keeps P alive through every leg's hits; the
			// asserted riders land regardless.
			sendCommand(t, pw, "effect give P minecraft:absorption 600 250")
			sendCommand(t, pw, "kill @e[type=!minecraft:player]")
			return c, pr, pw
		}

		fxLeg := func(mob string, wantEff int32) string {
			c, pr, pw := join()
			defer c.Close()
			obs := newObs()
			paPump(t, c, pr, pw, obs, 900*time.Millisecond)
			ty, _ := server.EntityTypeID(mob)
			sendCommand(t, pw, fmt.Sprintf("summon minecraft:%s ~2 ~ ~", mob))
			paPump(t, c, pr, pw, obs, 9*time.Second)
			if obs.spawned[ty] == 0 {
				t.Fatalf("%s: %s never spawned (saw %v)", addr, mob, obs.spawned)
			}
			got, ok := obs.effects[wantEff]
			return fmt.Sprintf("%s eff=%v amp=%d dur=%d flags=%x hit=%v",
				mob, ok, got[0], got[1], obs.effFlags[wantEff], obs.dmgEvent > 0)
		}

		hungerID, _ := server.MobEffectID("hunger")
		poisonID, _ := server.MobEffectID("poison")
		out = append(out, fxLeg("husk", hungerID))
		out = append(out, fxLeg("cave_spider", poisonID))

		// --- iron golem: provoke with a fist attack, then the counter
		// swing must land event 4, the launch motion and a hit.
		c, pr, pw := join()
		defer c.Close()
		obs := newObs()
		paPump(t, c, pr, pw, obs, 900*time.Millisecond)
		ty, _ := server.EntityTypeID("iron_golem")
		sendCommand(t, pw, "summon minecraft:iron_golem ~2 ~ ~")
		paPump(t, c, pr, pw, obs, 900*time.Millisecond)
		g := obs.spawned[ty]
		if g == 0 {
			t.Fatalf("%s: iron_golem never spawned", addr)
		}
		var atk protocol.Buffer
		atk.VarInt(g)
		if err := pw.WritePacket(server.IDPlayAttackSB, atk.Bytes()); err != nil {
			t.Fatal(err)
		}
		paPump(t, c, pr, pw, obs, 9*time.Second)
		ev4 := false
		for _, b := range obs.events[g] {
			if b == 4 {
				ev4 = true
			}
		}
		vy := 0.0
		for _, m := range obs.motion {
			if m[1] > vy {
				vy = m[1]
			}
		}
		// The landed hit shows as a damage_event on P's own conn —
		// health deltas are unreliable when the golem can outright
		// kill (respawn resets to 20).
		out = append(out, fmt.Sprintf("golem ev4=%v launch=%v hit=%v",
			ev4, vy > 0.3, obs.dmgEvent > 0))
		return out
	}

	van := run(targets.vanilla)
	mc := run(targets.mcgo)
	for i := range van {
		t.Logf("leg %d vanilla: %s", i, van[i])
		t.Logf("leg %d mc-go:   %s", i, mc[i])
	}
	if len(van) != len(mc) {
		t.Fatalf("leg count %d vs %d", len(van), len(mc))
	}
	for i := range van {
		if van[i] != mc[i] {
			t.Errorf("leg %d mismatch: vanilla %q mc-go %q", i, van[i], mc[i])
		}
	}
}
