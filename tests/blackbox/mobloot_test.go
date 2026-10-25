package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// lootObserve summons one mob, kills it with a player-sourced
// /damage (which arms the lastHurtByPlayer window), and counts the
// item and orb spawns over the window.
func lootObserve(t *testing.T, tag, addr, name, mob, nbt string, window time.Duration) (items, orbs int) {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(window + 30*time.Second))
	drainChunkWindow(t, tag, c, pr, pw, 400*time.Millisecond)
	sendCommand(t, pw, "tp @s 4.5 -60 8.5 90 0")
	var mv protocol.Buffer
	mv.Double(4.5)
	mv.Double(-60)
	mv.Double(8.5)
	mv.Byte(0x01)
	_ = pw.WritePacket(server.IDPlayMovePosSB, mv.Bytes())
	time.Sleep(300 * time.Millisecond)
	sendCommand(t, pw, "summon minecraft:"+mob+" 8 -60 8 "+nbt)
	time.Sleep(500 * time.Millisecond)
	// by @s makes the player's entity the direct attacker — vanilla
	// marks the kill player-caused, opening the xp award.
	sendCommand(t, pw, "damage @e[type=minecraft:"+mob+",limit=1] 40 minecraft:player_attack by @s")

	start := time.Now()
	for time.Since(start) < window {
		_ = c.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
		p, err := pr.ReadPacket()
		if err != nil {
			continue
		}
		switch int32(p.ID) {
		case server.IDPlayKeepAliveCB:
			_ = pw.WritePacket(server.IDPlayKeepAliveSB, p.Data)
		case server.IDPlayChunkBatchDone:
			_ = ackChunkBatch(pw)
		case 1:
			r := protocol.NewPacketReader(p.Data)
			r.VarInt()
			r.UUID()
			ty, _ := r.VarInt()
			n, _ := server.EntityTypeName(ty)
			if n == "item" {
				items++
			}
			if n == "experience_orb" {
				orbs++
			}
		}
	}
	sendCommand(t, pw, "kill @e[type=minecraft:item]")
	sendCommand(t, pw, "kill @e[type=minecraft:experience_orb]")
	time.Sleep(1200 * time.Millisecond)
	return items, orbs
}

// TestMobLootParity pins dropAllDeathLoot + dropExperience: an adult
// pig killed by a player drops its loot pools and the 1-3 xp orb,
// while a baby drops neither — shouldDropLoot/shouldDropExperience
// both gate on !isBaby.
func TestMobLootParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{
		"ops.json": opsJSONFor("LOOT"),
	}, nil)
	defer targets.cleanup()

	t.Run("adult_drops", func(t *testing.T) {
		for _, tc := range []struct{ tag, addr string }{
			{"vanilla", targets.vanilla},
			{"mc-go", targets.mcgo},
		} {
			items, orbs := lootObserve(t, tc.tag, tc.addr, "LOOT", "pig", "{}", 4*time.Second)
			t.Logf("%s: items=%d orbs=%d", tc.tag, items, orbs)
			if items < 1 {
				t.Fatalf("%s: adult pig dropped no loot — porkchop pool should roll 1-3", tc.tag)
			}
			if orbs < 1 {
				t.Fatalf("%s: player-killed pig dropped no xp — should award 1-3", tc.tag)
			}
		}
	})

	// A sheared sheep drops only its mutton pool — every wool entry
	// carries type_specific/sheep {sheared:false}.
	t.Run("sheared_no_wool", func(t *testing.T) {
		for _, tc := range []struct{ tag, addr string }{
			{"vanilla", targets.vanilla},
			{"mc-go", targets.mcgo},
		} {
			items, _ := lootObserve(t, tc.tag, tc.addr, "LOOT", "sheep", "{Sheared:1b}", 4*time.Second)
			t.Logf("%s: items=%d", tc.tag, items)
			if items != 1 {
				t.Fatalf("%s: sheared sheep dropped %d items — only the mutton pool should fire", tc.tag, items)
			}
		}
	})

	t.Run("baby_no_drops", func(t *testing.T) {
		for _, tc := range []struct{ tag, addr string }{
			{"vanilla", targets.vanilla},
			{"mc-go", targets.mcgo},
		} {
			items, orbs := lootObserve(t, tc.tag, tc.addr, "LOOT", "pig", "{Age:-24000}", 4*time.Second)
			t.Logf("%s: items=%d orbs=%d", tc.tag, items, orbs)
			if items != 0 || orbs != 0 {
				t.Fatalf("%s: baby pig dropped %d items + %d orbs — babies drop nothing", tc.tag, items, orbs)
			}
		}
	})
}
