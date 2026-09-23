//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// TestPrecipitationParity pins ServerLevel.tickPrecipitation: each
// random-tick-speed iteration rolls a 1/48 to run it on a random
// column of the chunk — the cell under the motion-blocking top freezes
// to ice in a cold biome, and snow lands/piles at the top cell while
// it rains, capped at min(max_snow_accumulation_height, 8) layers.
// Setup: one chunk fillbiome'd to snowy_plains (water half, dry half),
// a plains/void control chunk beside it, weather rain and a cranked
// random tick speed so every column converges.
func TestPrecipitationParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil,
		map[string]string{
			"ops.json": opsJSONFor("P"),
		}, nil)
	defer targets.cleanup()

	setup := []string{
		`fill 96 -62 112 127 -62 127 minecraft:stone`,
		`fill 96 -61 112 103 -61 127 minecraft:water`,
		`fill 104 -61 112 111 -61 127 minecraft:air`,
		`fill 112 -61 112 115 -61 115 minecraft:water`,
		`fill 104 -60 112 111 -56 127 minecraft:air`,
		`fillbiome 96 -64 112 111 -55 127 minecraft:snowy_plains`,
		`weather rain`,
		`gamerule minecraft:max_snow_accumulation_height 8`,
		`gamerule minecraft:random_tick_speed 4096`,
		`tp P 103.5 -60 119.5`,
	}

	legs := []struct {
		name  string
		probe string
	}{
		{"water froze", `execute if block 99 -61 120 minecraft:ice run say ZP-ok-%d`},
		{"water froze 2", `execute if block 100 -61 121 minecraft:ice run say ZP-ok-%d`},
		{"snow landed", `execute if block 106 -61 120 minecraft:snow run say ZP-ok-%d`},
		{"snow landed 2", `execute if block 109 -61 125 minecraft:snow run say ZP-ok-%d`},
		{"snow piled 8", `execute if block 106 -61 120 minecraft:snow[layers=8] run say ZP-ok-%d`},
		{"control water stays", `execute if block 113 -61 113 minecraft:water run say ZP-ok-%d`},
		{"control no snow", `execute unless block 118 -61 118 minecraft:snow run say ZP-ok-%d`},
		{"biome override", `execute if biome 106 -61 120 minecraft:snowy_plains run say ZP-ok-%d`},
		{"biome control", `execute if biome 118 -61 118 minecraft:the_void run say ZP-ok-%d`},
	}

	run := func(addr string) []string {
		c, pr, pw := joinPlayQuiet(t, addr, "P")
		defer c.Close()
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		sink.cap = 1 << 20
		time.Sleep(3 * time.Second)
		sink.take()
		for _, cmd := range setup {
			sendCommand(t, pw, cmd)
		}
		// Let the precipitation rolls saturate: 4096 speed * 1/48 *
		// ~20 tps over the window hits every column many times.
		time.Sleep(16 * time.Second)
		sendCommand(t, pw, `gamerule minecraft:random_tick_speed 3`)

		var out []string
		for i, leg := range legs {
			sendCommand(t, pw, fmt.Sprintf(leg.probe, i))
			sendCommand(t, pw, fmt.Sprintf("say ZPK%d", i))
			marker := fmt.Sprintf("ZPK%d", i)
			hit := "miss"
			deadline := time.Now().Add(8 * time.Second)
			for time.Now().Before(deadline) {
				done := false
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						done = true
					}
					if strings.Contains(line, fmt.Sprintf("ZP-ok-%d", i)) {
						hit = "hit"
					}
				}
				if done {
					break
				}
				time.Sleep(40 * time.Millisecond)
			}
			out = append(out, hit)
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	for i, leg := range legs {
		t.Logf("%-22s vanilla=%s mcgo=%s", leg.name, vanilla[i], mcgo[i])
		if vanilla[i] != mcgo[i] {
			t.Errorf("leg %q: vanilla %s, mc-go %s", leg.name, vanilla[i], mcgo[i])
		}
	}
}
