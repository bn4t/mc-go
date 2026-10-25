//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestSupportPopParity pins the updateShape survival fan-out: blocks
// whose canSurvive fails after a neighbor change pop to air on the
// next tick (vanilla schedules a tick; mc-go runs the check inline —
// identical a tick later). Each family gets a "pops" probe and a
// "stays" control where the support remains valid.
func TestSupportPopParity(t *testing.T) {
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
		// Sugar cane: frosted ice beside the base keeps it alive until
		// the base block is removed.
		`setblock 20 -61 44 minecraft:dirt`,
		`setblock 20 -61 45 minecraft:frosted_ice`,
		`setblock 20 -60 44 minecraft:sugar_cane`,
		`setblock 20 -59 44 minecraft:sugar_cane`,
		// Cane on a supported base next to a removable decoy block:
		// breaking the decoy must not pop anything.
		`setblock 24 -61 44 minecraft:dirt`,
		`setblock 24 -61 45 minecraft:frosted_ice`,
		`setblock 24 -60 44 minecraft:sugar_cane`,
		`setblock 25 -60 44 minecraft:stone`,
		// Cactus column on sand.
		`setblock 28 -61 44 minecraft:sand`,
		`setblock 28 -60 44 minecraft:cactus`,
		`setblock 28 -59 44 minecraft:cactus`,
		// Bamboo column on grass.
		`setblock 32 -61 44 minecraft:grass_block`,
		`setblock 32 -60 44 minecraft:bamboo`,
		`setblock 32 -59 44 minecraft:bamboo`,
		`setblock 32 -58 44 minecraft:bamboo`,
		// Chorus chain: stone, plant, plant, flower.
		`setblock 36 -62 44 minecraft:end_stone`,
		`setblock 36 -61 44 minecraft:chorus_plant`,
		`setblock 36 -60 44 minecraft:chorus_plant`,
		`setblock 36 -59 44 minecraft:chorus_flower[age=0]`,
		// Wheat on farmland.
		`setblock 40 -61 44 minecraft:farmland`,
		`setblock 40 -60 44 minecraft:wheat[age=0]`,
		// Nether wart on soul sand.
		`setblock 44 -61 44 minecraft:soul_sand`,
		`setblock 44 -60 44 minecraft:nether_wart`,
		// Torch on stone.
		`setblock 48 -61 44 minecraft:stone`,
		`setblock 48 -60 44 minecraft:torch`,
		// Snow layer on grass.
		`setblock 52 -61 44 minecraft:grass_block`,
		`setblock 52 -60 44 minecraft:snow[layers=3]`,
		`tp @s 36.5 -60 44.5`,
	}
	breaks := []string{
		`setblock 20 -61 44 minecraft:air`,
		`setblock 25 -60 44 minecraft:air`,
		`setblock 28 -61 44 minecraft:air`,
		`setblock 32 -60 44 minecraft:air`,
		`setblock 36 -60 44 minecraft:air`,
		`setblock 40 -61 44 minecraft:air`,
		`setblock 44 -61 44 minecraft:air`,
		`setblock 48 -61 44 minecraft:air`,
		`setblock 52 -61 44 minecraft:air`,
	}
	probes := []struct{ name, spec string }{
		{"cane base pops", "20 -60 44 minecraft:air"},
		{"cane above pops too", "20 -59 44 minecraft:air"},
		{"cane beside broken decoy stays", "24 -60 44 minecraft:sugar_cane"},
		{"cactus base pops", "28 -60 44 minecraft:air"},
		{"cactus above pops too", "28 -59 44 minecraft:air"},
		{"bamboo stalk above break pops", "32 -59 44 minecraft:air"},
		{"bamboo top pops too", "32 -58 44 minecraft:air"},
		{"chorus plant above break pops", "36 -59 44 minecraft:air"},
		{"chorus flower pops too", "36 -59 44 minecraft:air"},
		{"wheat pops", "40 -60 44 minecraft:air"},
		{"nether wart pops", "44 -60 44 minecraft:air"},
		{"torch pops", "48 -60 44 minecraft:air"},
		{"snow layer pops", "52 -60 44 minecraft:air"},
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
		time.Sleep(500 * time.Millisecond)
		for _, cmd := range breaks {
			sendCommand(t, pw, cmd)
		}
		// Vanilla's pop rides a one-tick scheduled tick; give the
		// cascade time to finish.
		time.Sleep(3 * time.Second)
		sink.take()

		var out []string
		for i, p := range probes {
			sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZP-ok-%d", p.spec, i))
			sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZP-miss-%d", p.spec, i))
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
					if strings.Contains(line, fmt.Sprintf("ZP-ok-%d", i)) {
						hit = "hit"
					}
					if strings.Contains(line, fmt.Sprintf("ZP-miss-%d", i)) {
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
