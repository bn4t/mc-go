package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestLeafDecayParity drives LeavesBlock: distance settles through
// scheduled self-ticks (min over six neighbours of getDistanceAt + 1,
// logs and woods reading 0 via #prevents_nearby_leaf_decay), and a
// leaf at distance 7 that isn't persistent crumbles on a random tick.
// Leaves attached to a log hold distance 1 and never decay; breaking
// the log cascades the whole chain to 7 and it all falls. A
// persistent leaf is exempt. Probes read state on both servers.
func TestLeafDecayParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		"simulation-distance": "3",
	}, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, func(s *server.Server) {
		s.SimulationDistance = 3
	})
	defer targets.cleanup()

	setup := []string{
		`gamerule random_tick_speed 0`,
		// Floating leaf: no log anywhere near, distance settles at 7.
		`setblock 40 -60 44 minecraft:oak_leaves`,
		// Leaf riding on a log: distance 1, never decays.
		`setblock 44 -61 44 minecraft:oak_log`,
		`setblock 44 -60 44 minecraft:oak_leaves`,
		// A seven-leaf chain off a log: distances 1..7; the far tip is
		// doomed, the rest hold.
		`setblock 48 -61 44 minecraft:oak_log`,
		`setblock 48 -60 44 minecraft:oak_leaves`,
		`setblock 49 -60 44 minecraft:oak_leaves`,
		`setblock 50 -60 44 minecraft:oak_leaves`,
		`setblock 51 -60 44 minecraft:oak_leaves`,
		`setblock 52 -60 44 minecraft:oak_leaves`,
		`setblock 53 -60 44 minecraft:oak_leaves`,
		`setblock 54 -60 44 minecraft:oak_leaves`,
		// Player-placed leaves are persistent and exempt.
		`setblock 58 -60 44 minecraft:oak_leaves[persistent=true]`,
		// Log the hand-placed leaf lands next to, stones to click on
		// (mc-go's void gen has no grass there), plus a 16x16 patch
		// of doomed leaves whose decay must drop loot.
		`setblock 62 -60 44 minecraft:oak_log`,
		`setblock 63 -61 44 minecraft:stone`,
		`setblock 66 -61 44 minecraft:stone`,
		`fill 70 -60 44 85 -60 59 minecraft:oak_leaves`,
		`give @s minecraft:oak_leaves 2`,
		`tp @s 62.5 -60 44.5 90 0`,
	}
	breaks := []string{
		`setblock 48 -61 44 minecraft:air`,
	}
	probes := []struct{ name, spec string }{
		{"floating leaf at distance 7", "block 40 -60 44 minecraft:oak_leaves[distance=7]"},
		{"leaf on log holds", "block 44 -60 44 minecraft:oak_leaves[distance=1]"},
		{"chain tip leaf distance 7", "block 54 -60 44 minecraft:oak_leaves[distance=7]"},
		{"chain mid distance 3", "block 51 -60 44 minecraft:oak_leaves[distance=3]"},
		{"hand leaf persistent", "block 63 -60 44 minecraft:oak_leaves[persistent=true]"},
		{"hand leaf by log distance 1", "block 63 -60 44 minecraft:oak_leaves[distance=1]"},
		{"hand leaf far persistent", "block 66 -60 44 minecraft:oak_leaves[persistent=true]"},
	}
	probesAfterTick := []struct{ name, spec string }{
		{"floating leaf decayed", "block 40 -60 44 minecraft:air"},
		{"leaf on log still alive", "block 44 -60 44 minecraft:oak_leaves"},
		{"chain base survives", "block 48 -60 44 minecraft:oak_leaves"},
		{"chain near-tip survives", "block 53 -60 44 minecraft:oak_leaves"},
		{"chain tip decayed", "block 54 -60 44 minecraft:air"},
		{"persistent leaf survives", "block 58 -60 44 minecraft:oak_leaves[persistent=true]"},
		{"decay dropped a sapling", `entity @e[type=item,nbt={Item:{id:"minecraft:oak_sapling"}}]`},
		{"decay dropped a stick", `entity @e[type=item,nbt={Item:{id:"minecraft:stick"}}]`},
		{"hand leaves still alive", "block 63 -60 44 minecraft:oak_leaves"},
		{"hand far leaf still alive", "block 66 -60 44 minecraft:oak_leaves"},
	}
	cutProbes := []struct{ name, spec string }{
		{"chain base decayed", "block 48 -60 44 minecraft:air"},
		{"chain mid decayed", "block 51 -60 44 minecraft:air"},
		{"other log leaf still alive", "block 44 -60 44 minecraft:oak_leaves"},
		{"persistent leaf still holds", "block 58 -60 44 minecraft:oak_leaves[persistent=true]"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if %s run say ZL-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless %s run say ZL-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZLK%d", i))
		marker := fmt.Sprintf("ZLK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZL-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZL-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	run := func(addr string) []string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		// player_loaded opens world interactions at once; on the
		// frozen harness server the 60-tick grace never lapses, so a
		// use_item_on without it is dropped whole.
		_ = pw.WritePacket(server.IDPlayPlayerLoadedSB, nil)
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(4 * time.Second)
		sink.take()
		for _, cmd := range setup {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(1500 * time.Millisecond)
		// Hand-place two leaves off the held oak_leaves stack: one on
		// the grass beside the log (distance settles to 1 on the spot),
		// one on open grass (distance 7, still persistent).
		sendUseItemOn(t, pw, 63, -61, 44, 1, 1)
		sendUseItemOn(t, pw, 66, -61, 44, 1, 2)
		time.Sleep(500 * time.Millisecond)

		var out []string
		for i, p := range probes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		sendCommand(t, pw, `gamerule random_tick_speed 4096`)
		time.Sleep(20 * time.Second)
		sendCommand(t, pw, `gamerule random_tick_speed 0`)
		time.Sleep(500 * time.Millisecond)
		for i, p := range probesAfterTick {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		for _, cmd := range breaks {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(1500 * time.Millisecond)
		sendCommand(t, pw, `gamerule random_tick_speed 4096`)
		time.Sleep(20 * time.Second)
		sendCommand(t, pw, `gamerule random_tick_speed 0`)
		for i, p := range cutProbes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append(probes, probesAfterTick...), cutProbes...)
	for i, p := range all {
		t.Logf("%-28s vanilla=%s mcgo=%s", p.name, vanilla[i], mcgo[i])
		if vanilla[i] == "hit" && mcgo[i] != "hit" {
			t.Errorf("probe %q: vanilla hit, mc-go %s", p.name, mcgo[i])
		}
		if mcgo[i] == "hit" && vanilla[i] != "hit" {
			t.Errorf("probe %q: mc-go hit, vanilla %s", p.name, vanilla[i])
		}
	}
}
