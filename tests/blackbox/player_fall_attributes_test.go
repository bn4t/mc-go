package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// Attribute bases, modifier order and effects alter the same 9.5-block drop.
func TestPlayerFallAttributesParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("Faller")}, nil)
	defer targets.cleanup()
	type result struct {
		health     float32
		damageType int32
		hit        bool
	}
	run := func(addr string, commands []string) result {
		out := result{health: 20}
		c, pr, pw := joinPlayBlackbox(t, addr, "Faller")
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
		sendCommand(t, pw, "attribute @s minecraft:safe_fall_distance base set 3")
		sendCommand(t, pw, "attribute @s minecraft:fall_damage_multiplier base set 1")
		sendCommand(t, pw, "attribute @s minecraft:safe_fall_distance modifier remove minecraft:fall_test")
		sendCommand(t, pw, "effect clear @s")
		sendCommand(t, pw, "effect give @s minecraft:instant_health 1 10 true")
		for _, command := range commands {
			sendCommand(t, pw, command)
		}
		{
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
	for _, tc := range []struct {
		name     string
		commands []string
		health   float32
	}{
		{"slow_falling", []string{"effect give @s minecraft:slow_falling 60 0 true"}, 20},
		{"levitation", []string{"effect give @s minecraft:levitation 60 0 true"}, 20},
		{"safe", []string{"attribute @s minecraft:safe_fall_distance base set 6"}, 17},
		{"multiplier", []string{"attribute @s minecraft:fall_damage_multiplier base set 0.5"}, 17},
		{"lower_safe", []string{"attribute @s minecraft:safe_fall_distance base set 0"}, 11},
		{"jump", []string{"effect give @s minecraft:jump_boost 60 1 true"}, 16},
		{"modifier", []string{"attribute @s minecraft:safe_fall_distance modifier add minecraft:fall_test 1 add_multiplied_total", "effect give @s minecraft:jump_boost 60 1 true"}, 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v, m := run(targets.vanilla, tc.commands), run(targets.mcgo, tc.commands)
			t.Logf("vanilla=%+v mcgo=%+v", v, m)
			if v.health != tc.health || v != m {
				t.Fatalf("fall attributes vanilla=%+v mcgo=%+v want health %v", v, m, tc.health)
			}
		})
	}
}
