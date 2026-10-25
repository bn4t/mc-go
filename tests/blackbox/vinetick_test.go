package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestVineGrowthParity drives the GrowingPlant random-tick family:
// weeping and cave vines hanging down, twisting vines climbing up.
// Heads grow at 10% per tick into air, carrying age+1 onto the new
// head and converting the old head to its plant body; a bounded
// column converges to a deterministic tip age. A mid-column break
// then pins the updateShape rewrites: the upper side pops cascading
// up through the attach checks, and the surviving lower tip turns
// back into a head with a fresh random age (not probed — any age).
// A floating /setblock'd head pops through the placement fold.
func TestVineGrowthParity(t *testing.T) {
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
		// Weeping vine: netherrack ceiling at -55, head at -56, a
		// stone floor at -59 caps growth at three segments.
		`setblock 40 -55 44 minecraft:netherrack`,
		`setblock 40 -56 44 minecraft:weeping_vines[age=0]`,
		`setblock 40 -59 44 minecraft:stone`,
		// Twisting vine: grass floor at -61, head at -60, stone roof
		// at -57 caps it at three segments.
		`setblock 44 -61 44 minecraft:grass_block`,
		`setblock 44 -60 44 minecraft:twisting_vines[age=0]`,
		`setblock 44 -57 44 minecraft:stone`,
		// Cave vine: deepslate ceiling at -55, head at -56, stone
		// floor at -59.
		`setblock 48 -55 44 minecraft:deepslate`,
		`setblock 48 -56 44 minecraft:cave_vines[age=0,berries=false]`,
		`setblock 48 -59 44 minecraft:stone`,
		// Floating weeping head — no attach support — pops at
		// placement (tick-family fold).
		`setblock 52 -58 44 minecraft:weeping_vines[age=0]`,
		// Kelp: a 1x1 stone shaft holding a water column; kelp on dirt
		// climbs through the water and stops where the water ends.
		`fill 55 -62 43 57 -55 45 minecraft:stone`,
		`fill 56 -61 44 56 -55 44 minecraft:air`,
		`setblock 56 -61 44 minecraft:dirt`,
		`fill 56 -60 44 56 -57 44 minecraft:water`,
		`setblock 56 -60 44 minecraft:kelp[age=0]`,
		// Floating kelp head over air — attach-side canSurvive fails —
		// pops at placement into its water legacy, which then falls
		// into the dug-out cell below.
		`setblock 72 -61 44 minecraft:air`,
		`setblock 72 -60 44 minecraft:kelp[age=0]`,
		`tp @s 44.5 -60 44.5`,
	}
	breaks := []string{
		`setblock 40 -57 44 minecraft:air`,
		`setblock 44 -59 44 minecraft:air`,
		`setblock 48 -57 44 minecraft:air`,
		`setblock 56 -59 44 minecraft:air`,
	}
	probes := []struct{ name, spec string }{
		{"weeping top became plant", "40 -56 44 minecraft:weeping_vines_plant"},
		{"weeping mid became plant", "40 -57 44 minecraft:weeping_vines_plant"},
		{"weeping tip head age 2", "40 -58 44 minecraft:weeping_vines[age=2]"},
		{"weeping floored at -58", "40 -59 44 minecraft:stone"},
		{"twisting base became plant", "44 -60 44 minecraft:twisting_vines_plant"},
		{"twisting mid became plant", "44 -59 44 minecraft:twisting_vines_plant"},
		{"twisting tip head age 2", "44 -58 44 minecraft:twisting_vines[age=2]"},
		{"cave top became plant", "48 -56 44 minecraft:cave_vines_plant"},
		{"cave mid became plant", "48 -57 44 minecraft:cave_vines_plant"},
		// Berries is a wildcard: the spec subset-matches just age.
		{"cave tip head age 2", "48 -58 44 minecraft:cave_vines[age=2]"},
		{"floating head popped", "52 -58 44 minecraft:air"},
		{"kelp base became plant", "56 -60 44 minecraft:kelp_plant"},
		{"kelp mid became plant", "56 -59 44 minecraft:kelp_plant"},
		{"kelp upper became plant", "56 -58 44 minecraft:kelp_plant"},
		{"kelp tip head age 3", "56 -57 44 minecraft:kelp[age=3]"},
		{"kelp stopped in air", "56 -56 44 minecraft:air"},
		{"floating kelp popped to water", "72 -60 44 minecraft:water"},
		{"kelp water fell below", "72 -61 44 minecraft:water"},
	}
	cutProbes := []struct{ name, spec string }{
		{"weeping stump is head again", "40 -56 44 minecraft:weeping_vines"},
		{"weeping break cell is air", "40 -57 44 minecraft:air"},
		{"weeping tip popped", "40 -58 44 minecraft:air"},
		{"twisting stump is head again", "44 -60 44 minecraft:twisting_vines"},
		{"twisting break cell is air", "44 -59 44 minecraft:air"},
		{"twisting tip popped", "44 -58 44 minecraft:air"},
		{"cave stump is head again", "48 -56 44 minecraft:cave_vines"},
		{"cave break cell is air", "48 -57 44 minecraft:air"},
		{"cave tip popped", "48 -58 44 minecraft:air"},
		{"kelp stump is head again", "56 -60 44 minecraft:kelp"},
		// Popped kelp leaves its fluid: the broken cell refills from
		// above, and the cascade cells are water sources again.
		{"kelp break cell refills", "56 -59 44 minecraft:water"},
		{"kelp above popped to water", "56 -58 44 minecraft:water"},
		{"kelp tip popped to water", "56 -57 44 minecraft:water"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZV-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZV-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZVK%d", i))
		marker := fmt.Sprintf("ZVK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZV-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZV-miss-%d", i)) {
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
		time.Sleep(400 * time.Millisecond)
		sendCommand(t, pw, `gamerule random_tick_speed 4096`)
		time.Sleep(45 * time.Second)
		sendCommand(t, pw, `gamerule random_tick_speed 0`)
		time.Sleep(500 * time.Millisecond)

		var out []string
		for i, p := range probes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		for _, cmd := range breaks {
			sendCommand(t, pw, cmd)
		}
		// Vanilla's pops ride one-tick scheduled ticks; let the
		// cascade finish.
		time.Sleep(3 * time.Second)
		sink.take()
		for i, p := range cutProbes {
			out = append(out, probe(pw, sink, p.spec, len(probes)+i))
		}
		return out
	}

	want := run(targets.vanilla)
	got := run(targets.mcgo)
	names := make([]string, 0, len(probes)+len(cutProbes))
	for _, p := range probes {
		names = append(names, p.name)
	}
	for _, p := range cutProbes {
		names = append(names, p.name)
	}
	for i, n := range names {
		t.Logf("%-32s vanilla=%s mcgo=%s", n, want[i], got[i])
		if want[i] != got[i] {
			t.Errorf("probe %q: vanilla %s, mc-go %s", n, want[i], got[i])
		}
	}
}
