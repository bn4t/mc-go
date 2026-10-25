//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestAmethystParity drives BuddingAmethystBlock.randomTick — 20% of
// ticks it picks a random face and sprouts a small bud into air or a
// water source, or climbs a bud already facing that way — and
// AmethystClusterBlock.updateShape: a bud folds to air only when the
// write lands on its attach side. Face choice is random, so the
// fixtures wall five faces and leave exactly one open, which makes
// the *location* deterministic while the timing stays random; long
// tick windows push the bud deterministically to full cluster.
func TestAmethystParity(t *testing.T) {
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
		// Fixture A: budding block walled on five faces, only the top
		// open — a bud must sprout at (43,-60) facing up and climb to
		// a full cluster.
		`fill 42 -62 43 44 -61 45 minecraft:stone`,
		`setblock 43 -61 44 minecraft:budding_amethyst`,
		// Fixture B: the single open face holds a water source — the
		// bud there is waterlogged=true and keeps it through upgrades.
		`fill 50 -62 43 52 -61 45 minecraft:stone`,
		`setblock 51 -61 44 minecraft:budding_amethyst`,
		`setblock 52 -61 44 minecraft:water`,
		`setblock 51 -60 44 minecraft:stone`,
		// Fixture C: a bud on a pedestal — popping the pedestal pops
		// the bud (attach-side update).
		`setblock 56 -61 44 minecraft:stone`,
		`setblock 56 -60 44 minecraft:small_amethyst_bud[facing=up]`,
		// Fixture D: a setblock'd floating bud — the return-air fold is
		// discarded on placement, so it just sits there.
		`setblock 60 -60 44 minecraft:small_amethyst_bud[facing=up]`,
		// Fixture E: a medium bud facing the wrong way on the only
		// open face — the facing gate blocks upgrades forever.
		`fill 64 -62 43 66 -61 45 minecraft:stone`,
		`setblock 65 -61 44 minecraft:budding_amethyst`,
		`setblock 65 -60 44 minecraft:stone`,
		`setblock 64 -61 44 minecraft:stone`,
		`setblock 65 -61 43 minecraft:stone`,
		`setblock 65 -61 45 minecraft:stone`,
		`setblock 66 -61 44 minecraft:medium_amethyst_bud[facing=west]`,
		`tp @s 43.5 -59 44.5`,
	}
	breaks := []string{
		`setblock 56 -61 44 minecraft:air`,
	}
	probes := []struct{ name, spec string }{
		{"floating bud stays put", "60 -60 44 minecraft:small_amethyst_bud[facing=up]"},
		{"wrong-facing bud stuck", "66 -61 44 minecraft:medium_amethyst_bud[facing=west]"},
		{"pedestal bud alive", "56 -60 44 minecraft:small_amethyst_bud[facing=up]"},
	}
	tickProbes := []struct{ name, spec string }{
		{"open face reached cluster", "43 -60 44 minecraft:amethyst_cluster[facing=up]"},
		{"water face cluster waterlogged", "52 -61 44 minecraft:amethyst_cluster[waterlogged=true,facing=east]"},
		{"wrong-facing bud still medium", "66 -61 44 minecraft:medium_amethyst_bud[facing=west]"},
	}
	cutProbes := []struct{ name, spec string }{
		{"popped pedestal bud is air", "56 -60 44 minecraft:air"},
		{"floating bud still put", "60 -60 44 minecraft:small_amethyst_bud[facing=up]"},
		{"cluster holds", "43 -60 44 minecraft:amethyst_cluster"},
		{"waterlogged cluster holds", "52 -61 44 minecraft:amethyst_cluster[waterlogged=true]"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZA-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZA-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZAK%d", i))
		marker := fmt.Sprintf("ZAK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZA-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZA-miss-%d", i)) {
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
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(4 * time.Second)
		sink.take()
		for _, cmd := range setup {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(1500 * time.Millisecond)

		var out []string
		for i, p := range probes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		sendCommand(t, pw, `gamerule random_tick_speed 4096`)
		time.Sleep(60 * time.Second)
		sendCommand(t, pw, `gamerule random_tick_speed 0`)
		time.Sleep(500 * time.Millisecond)
		for i, p := range tickProbes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		for _, cmd := range breaks {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(1500 * time.Millisecond)
		for i, p := range cutProbes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append(probes, tickProbes...), cutProbes...)
	for i, p := range all {
		t.Logf("%-32s vanilla=%s mcgo=%s", p.name, vanilla[i], mcgo[i])
		if vanilla[i] == "hit" && mcgo[i] != "hit" {
			t.Errorf("probe %q: vanilla hit, mc-go %s", p.name, mcgo[i])
		}
		if mcgo[i] == "hit" && vanilla[i] != "hit" {
			t.Errorf("probe %q: mc-go hit, vanilla %s", p.name, vanilla[i])
		}
	}
}
