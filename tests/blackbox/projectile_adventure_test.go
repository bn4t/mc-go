package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Adventure mode preserves the player's damageability. A summoned arrow
// exercises collision independently of item use restrictions in that mode.
func TestProjectileAdventureParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("Arrow", "Faller")}, nil)
	defer targets.cleanup()
	type result struct {
		health     float32
		damageType int32
		hit        bool
	}
	run := func(addr string, fall bool) result {
		out := result{health: 20}
		name := "Arrow"
		if fall {
			name = "Faller"
		}
		c, pr, pw := joinPlayBlackbox(t, addr, name)
		defer c.Close()
		if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
			t.Fatal(err)
		}
		obs := &fleeObs{}
		sendCommand(t, pw, "fill -3 -60 -3 3 -48 3 air")
		sendCommand(t, pw, "fill -3 -61 -3 3 -61 3 stone")
		sendCommand(t, pw, "tp @s 0.5 -60 0.5")
		sendCommand(t, pw, "gamemode adventure @s")
		sendCommand(t, pw, "gamerule minecraft:natural_health_regeneration false")
		fleePump(t, c, pr, pw, obs, 3500*time.Millisecond)
		if fall {
			sendCommand(t, pw, "tp @s 0.5 -50.5 0.5")
			fleePump(t, c, pr, pw, obs, 500*time.Millisecond)
			for y := -51.0; y >= -60; y -= 0.5 {
				var b protocol.Buffer
				b.Double(0.5)
				b.Double(y)
				b.Double(0.5)
				b.Bool(y == -60)
				_ = pw.WritePacket(server.IDPlayMovePosSB, b.Bytes())
				time.Sleep(55 * time.Millisecond)
			}
		} else {
			sendCommand(t, pw, `summon minecraft:arrow -1.5 -59 0.5 {Motion:[1.0d,0.0d,0.0d],NoGravity:1b}`)
		}
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
			p, err := pr.ReadPacket()
			if err != nil {
				continue
			}
			if p.ID == server.IDPlayKeepAliveCB {
				_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
			}
			if p.ID == server.IDPlayDamageEvent {
				rd := protocol.NewPacketReader(p.Data)
				_, _ = rd.VarInt()
				out.damageType, _ = rd.VarInt()
				out.hit = true
			}
			if p.ID == server.IDPlaySetHealth {
				out.health, _ = protocol.NewPacketReader(p.Data).Float()
			}
			if out.hit && out.health < 20 {
				return out
			}
		}
		return out
	}
	for _, fall := range []bool{false, true} {
		v, m := run(targets.vanilla, fall), run(targets.mcgo, fall)
		t.Logf("fall=%v vanilla=%+v mcgo=%+v", fall, v, m)
		if fall && v.health != 14 {
			t.Fatalf("vanilla 9.5m fall health=%v, want14", v.health)
		}
		if !v.hit || v.health >= 20 || v != m {
			t.Fatalf("adventure damage fall=%v vanilla=%+v mcgo=%+v", fall, v, m)
		}
	}
}
