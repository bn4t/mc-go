package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestColumnGrowthParity drives the upward-growing random-tick family:
// sugar cane and cactus columns, bamboo stalks and saplings, and the
// chorus flower's grow-up / branch / die paths. Probes are planted with
// the sampler frozen (random_tick_speed 0), the speed is cranked high
// so every roll converges, and each probe is read back with `execute if
// block`. Only converged or gate-blocked states are pinned — the one
// genuinely stochastic outcome (a cactus flower) is probed in a way
// that reads "miss" either way.
func TestColumnGrowthParity(t *testing.T) {
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
		`gamerule advance_weather false`,
		`weather clear`,
		// Sugar cane on grass needs water or frosted ice beside the base
		// to survive vanilla's placement update; frosted ice does not
		// spread.
		`setblock 20 -61 45 minecraft:frosted_ice`,
		`setblock 20 -60 44 minecraft:sugar_cane[age=0]`,
		// A capped cane never even ages — the above-air gate runs first.
		`setblock 24 -61 45 minecraft:frosted_ice`,
		`setblock 24 -60 44 minecraft:sugar_cane[age=0]`,
		`setblock 24 -59 44 minecraft:stone`,
		// Cactus on sand. The age-8 flower roll is fundamentally
		// stochastic (a flower above a grown stalk freezes the column
		// under it), so only cells guaranteed on every outcome are
		// probed: -59 is always a stalk or a flower, never air.
		`setblock 28 -61 44 minecraft:sand`,
		`setblock 28 -60 44 minecraft:cactus[age=0]`,
		// A three-tall column whose top is already 15 stops entirely:
		// vanilla's in-loop return skips the age-8 flower roll forever.
		`setblock 36 -61 44 minecraft:sand`,
		`setblock 36 -60 44 minecraft:cactus[age=15]`,
		`setblock 36 -59 44 minecraft:cactus[age=15]`,
		`setblock 36 -58 44 minecraft:cactus[age=15]`,
		// A sapling grows the first stalk and converts to the column's
		// base stalk; the column tops out somewhere in 12..16 (the
		// height>=11 writes carry a 25% terminal roll), so only the
		// guaranteed cells are probed.
		`setblock 40 -60 44 minecraft:bamboo_sapling`,
		// A bare stalk grows the same kind of column.
		`setblock 48 -60 44 minecraft:bamboo`,
		// Chorus flower on end stone always grows up first.
		`setblock 56 -61 44 minecraft:end_stone`,
		`setblock 56 -60 44 minecraft:chorus_flower[age=0]`,
		// Flower stacked five plants deep can never climb — vanilla
		// grows up only when run <= nextInt(...), unreachable at run 5 —
		// so with its sides walled the only remaining path is death.
		`setblock 64 -62 44 minecraft:end_stone`,
		`setblock 64 -61 44 minecraft:chorus_plant`,
		`setblock 64 -60 44 minecraft:chorus_plant`,
		`setblock 64 -59 44 minecraft:chorus_plant`,
		`setblock 64 -58 44 minecraft:chorus_plant`,
		`setblock 64 -57 44 minecraft:chorus_plant`,
		`setblock 64 -56 44 minecraft:chorus_flower[age=0]`,
		`setblock 63 -56 44 minecraft:stone`,
		`setblock 65 -56 44 minecraft:stone`,
		`setblock 64 -56 43 minecraft:stone`,
		`setblock 64 -56 45 minecraft:stone`,
		// A flower whose cell above is occupied never ticks.
		`setblock 70 -61 44 minecraft:end_stone`,
		`setblock 70 -60 44 minecraft:chorus_flower[age=0]`,
		`setblock 70 -59 44 minecraft:stone`,
	}

	probes := []struct{ name, spec string }{
		{"sugar cane reaches 2", "20 -59 44 minecraft:sugar_cane[age=0]"},
		{"sugar cane reaches 3", "20 -58 44 minecraft:sugar_cane[age=0]"},
		{"sugar cane caps at 3", "20 -57 44 minecraft:sugar_cane"},
		{"bottom cane resets to 0", "20 -60 44 minecraft:sugar_cane[age=0]"},
		{"capped cane stays 1 tall", "24 -59 44 minecraft:sugar_cane"},
		{"capped cane stays age 0", "24 -60 44 minecraft:sugar_cane[age=0]"},
		{"cactus grows or flowers", "28 -59 44 minecraft:air"},
		{"frozen cactus adds nothing", "36 -57 44 minecraft:air"},
		{"frozen cactus top keeps 15", "36 -58 44 minecraft:cactus[age=15]"},
		{"sapling becomes base stalk", "40 -60 44 minecraft:bamboo"},
		{"sapling base ripples to 1", "40 -60 44 minecraft:bamboo[age=1,leaves=none,stage=0]"},
		{"column reaches 11", "40 -50 44 minecraft:bamboo"},
		{"mid-column state", "40 -55 44 minecraft:bamboo[age=1,leaves=none,stage=0]"},
		{"column never tops 16", "40 -44 44 minecraft:air"},
		{"stalk column reaches 11", "48 -50 44 minecraft:bamboo"},
		{"stalk mid-column state", "48 -55 44 minecraft:bamboo[age=1,leaves=none,stage=0]"},
		{"planted stalk ripples to 1", "48 -60 44 minecraft:bamboo[age=1,leaves=none,stage=0]"},
		{"stalk column never tops 16", "48 -44 44 minecraft:air"},
		{"flower grows up into plant", "56 -60 44 minecraft:chorus_plant[down=true,east=false,north=false,south=false,up=true,west=false]"},
		{"boxed flower dies at 5", "64 -56 44 minecraft:chorus_flower[age=5]"},
		{"boxed plant stays", "64 -61 44 minecraft:chorus_plant"},
		{"capped flower stays 0", "70 -60 44 minecraft:chorus_flower[age=0]"},
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
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, `tp @s 45.5 -60 44.5`)
		sendCommand(t, pw, `gamerule random_tick_speed 4096`)
		time.Sleep(45 * time.Second)
		sendCommand(t, pw, `gamerule random_tick_speed 0`)
		time.Sleep(500 * time.Millisecond)
		sink.take()

		var out []string
		for i, p := range probes {
			sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZC-ok-%d", p.spec, i))
			sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZC-miss-%d", p.spec, i))
			sendCommand(t, pw, fmt.Sprintf("say ZMK%d", i))
			marker := fmt.Sprintf("ZMK%d", i)
			hit := "none"
			deadline := time.Now().Add(4 * time.Second)
		probe:
			for time.Now().Before(deadline) {
				for _, line := range sink.take() {
					if strings.Contains(line, marker) {
						break probe
					}
					if strings.Contains(line, fmt.Sprintf("ZC-ok-%d", i)) {
						hit = "hit"
					}
					if strings.Contains(line, fmt.Sprintf("ZC-miss-%d", i)) {
						hit = "miss"
					}
				}
				time.Sleep(40 * time.Millisecond)
			}
			out = append(out, hit)
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	for i, p := range probes {
		t.Logf("%-34s vanilla=%s mcgo=%s", p.name, want[i], got[i])
		if want[i] != got[i] {
			t.Errorf("probe %q: vanilla %s, mc-go %s", p.name, want[i], got[i])
		}
	}
}
