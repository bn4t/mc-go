package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestMushroomSpreadParity drives MushroomBlock.randomTick — 1/25
// roll, spread into a random-walk target, aborted once 5 same-type
// mushrooms sit inside a 9×3×9 box — and VegetationBlock's
// return-air pop when canSurvive fails (a sunlit mushroom on grass
// pops on the next neighbour write). Spread direction is random, so
// spread probes count hits over a ring instead of pinning a cell.
func TestMushroomSpreadParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		"simulation-distance": "3",
	}, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, func(s *server.Server) {
		s.SimulationDistance = 3
	})
	defer targets.cleanup()

	// Ring cells around each seed mushroom — any non-air cell means a
	// spread landed.
	spreadRings := []struct {
		seed  string
		cells []string
	}{
		{"40 -60 44", []string{
			"39 -60 43", "40 -60 43", "41 -60 43",
			"39 -60 44", "41 -60 44",
			"39 -60 45", "40 -60 45", "41 -60 45",
		}},
		{"67 -60 44", []string{
			"66 -60 43", "67 -60 43", "68 -60 43",
			"66 -60 44", "68 -60 44",
			"66 -60 45", "67 -60 45", "68 -60 45",
		}},
	}

	setup := []string{
		`gamerule random_tick_speed 0`,
		`time set noon`,
		// A: lone mushroom on a mycelium pad — spreads freely.
		`fill 39 -61 43 41 -61 45 minecraft:mycelium`,
		`setblock 40 -60 44 minecraft:brown_mushroom`,
		// B: five seeds inside each other's 9×3×9 — saturated, no
		// spread anywhere.
		`fill 50 -61 43 54 -61 45 minecraft:mycelium`,
		`setblock 50 -60 44 minecraft:brown_mushroom`,
		`setblock 51 -60 44 minecraft:brown_mushroom`,
		`setblock 52 -60 44 minecraft:brown_mushroom`,
		`setblock 53 -60 44 minecraft:brown_mushroom`,
		`setblock 54 -60 44 minecraft:brown_mushroom`,
		// C: mushroom on grass in full daylight — survives placement
		// but pops on the first neighbour write.
		`setblock 62 -60 44 minecraft:brown_mushroom`,
		// D: mushroom on stone under a solid roof — dark, spreads.
		`fill 66 -61 43 68 -61 45 minecraft:stone`,
		`setblock 67 -60 44 minecraft:brown_mushroom`,
		// A 9×9 roof keeps the whole patch under ~light 12 — sky
		// light attenuates one per cell toward open air.
		`fill 63 -57 40 71 -57 48 minecraft:stone`,
		// E: mushroom pops when its mycelium floor breaks.
		`setblock 72 -61 44 minecraft:mycelium`,
		`setblock 72 -60 44 minecraft:brown_mushroom`,
		`tp @s 40.5 -59 44.5`,
	}
	pokes := []string{
		`setblock 62 -60 45 minecraft:stone`,
		`setblock 72 -61 44 minecraft:air`,
	}
	preProbes := []struct{ name, spec string }{
		{"seed mushroom sits on mycelium", "40 -60 44 minecraft:brown_mushroom"},
		{"saturated seed sits", "52 -60 44 minecraft:brown_mushroom"},
		{"sunlit mushroom placed", "62 -60 44 minecraft:brown_mushroom"},
		{"roofed seed sits", "67 -60 44 minecraft:brown_mushroom"},
	}
	postProbes := []struct{ name, spec string }{
		{"roofed seed still sits", "67 -60 44 minecraft:brown_mushroom"},
		{"saturated box stays air", "52 -60 45 minecraft:air"},
		{"saturated box stays air 2", "51 -60 43 minecraft:air"},
		{"sunlit mushroom popped", "62 -60 44 minecraft:air"},
		{"floor-broken mushroom popped", "72 -60 44 minecraft:air"},
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZM-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZM-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZMK%d", i))
		marker := fmt.Sprintf("ZMK%d", i)
		hit := "none"
		deadline := time.Now().Add(4 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, marker) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZM-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZM-miss-%d", i)) {
					hit = "miss"
				}
			}
			time.Sleep(40 * time.Millisecond)
		}
		return hit
	}

	run := func(addr string) ([]string, []int) {
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
		// Sync the command queue: fill bursts take mc-go a couple of
		// seconds; the probe window starts only once the server has
		// answered a marker say.
		sendCommand(t, pw, `say ZSYNC`)
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			found := false
			for _, line := range sink.take() {
				if strings.Contains(line, "ZSYNC") {
					found = true
				}
			}
			if found {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}

		var out []string
		for i, p := range preProbes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		sendCommand(t, pw, `gamerule random_tick_speed 4096`)
		time.Sleep(30 * time.Second)
		sendCommand(t, pw, `gamerule random_tick_speed 0`)

		// Ring counts: how many cells around each seed gained a
		// mushroom (spread landed).
		counts := make([]int, len(spreadRings))
		for ri, ring := range spreadRings {
			for j, cell := range ring.cells {
				res := probe(pw, sink, fmt.Sprintf("%s minecraft:brown_mushroom", cell), 100+10*ri+j)
				if res == "hit" {
					counts[ri]++
				}
			}
		}
		for _, cmd := range pokes {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(1500 * time.Millisecond)
		for i, p := range postProbes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		return out, counts
	}

	vanilla, vCounts := run(targets.vanilla)
	mcgo, mCounts := run(targets.mcgo)
	all := append(preProbes, postProbes...)
	for i, p := range all {
		t.Logf("%-30s vanilla=%s mcgo=%s", p.name, vanilla[i], mcgo[i])
		if vanilla[i] == "hit" && mcgo[i] != "hit" {
			t.Errorf("probe %q: vanilla hit, mc-go %s", p.name, mcgo[i])
		}
		if mcgo[i] == "hit" && vanilla[i] != "hit" {
			t.Errorf("probe %q: mc-go hit, vanilla %s", p.name, vanilla[i])
		}
	}
	for ri, ring := range spreadRings {
		t.Logf("ring %s spread count: vanilla=%d mcgo=%d", ring.seed, vCounts[ri], mCounts[ri])
		if vCounts[ri] == 0 && mCounts[ri] == 0 {
			t.Errorf("ring %s: no spread on either server — fixture may be broken", ring.seed)
		}
		if (vCounts[ri] > 0) != (mCounts[ri] > 0) {
			t.Errorf("ring %s: spread divergence vanilla=%d mcgo=%d", ring.seed, vCounts[ri], mCounts[ri])
		}
	}
}
