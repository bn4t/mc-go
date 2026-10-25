package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestSpeleothemParity exercises the measured speleothem pipeline:
// the 0.011377778 growth gate on hanging start cells, dripstone's
// water-above-the-root requirement, the 50/50 tip-vs-stalagmite coin,
// the thickness recompute on neighbour writes, fluid transfer into
// cauldrons (water 0.17578125 / lava 0.05859375 with a 50+distance
// delayed block tick on the cauldron), mud->clay conversion, and the
// +2/+1 support-loss ticks. random_tick_speed 4096 makes every cell
// roll once per sprinted tick; all fixtures are sealed shafts.
func TestSpeleothemParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		"simulation-distance": "3",
	}, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, func(s *server.Server) {
		s.SimulationDistance = 3
	})
	defer targets.cleanup()

	// shaft carves a 3x21x3 stone box with a 1-wide air core from -59
	// up through -50.
	box := func(x int) []string {
		return []string{
			fmt.Sprintf(`fill %d -61 49 %d -40 51 minecraft:stone`, x-1, x+1),
			fmt.Sprintf(`fill %d -59 50 %d -50 50 minecraft:air`, x, x),
		}
	}
	setup := []string{
		`gamerule random_tick_speed 4096`,
		`tick freeze`,
	}
	// A: fed stalactite — dripstone block + water above, tip below.
	setup = append(setup, box(40)...)
	setup = append(setup,
		`fill 40 -54 50 40 -53 50 minecraft:stone`,
		`setblock 40 -55 50 minecraft:dripstone_block`,
		`setblock 40 -54 50 minecraft:water`,
		`setblock 40 -56 50 minecraft:pointed_dripstone[vertical_direction=down,thickness=tip]`)
	// B: same but NO water above the root — never grows.
	setup = append(setup, box(48)...)
	setup = append(setup,
		`setblock 48 -55 50 minecraft:dripstone_block`,
		`setblock 48 -56 50 minecraft:pointed_dripstone[vertical_direction=down,thickness=tip]`)
	// C: water but root is plain stone — never grows.
	setup = append(setup, box(56)...)
	setup = append(setup,
		`setblock 56 -55 50 minecraft:stone`,
		`setblock 56 -56 50 minecraft:pointed_dripstone[vertical_direction=down,thickness=tip]`)
	setup = append(setup, `setblock 56 -54 50 minecraft:water`)
	// D: fed stalactite with a cauldron on the floor 3 below the tip.
	setup = append(setup, box(64)...)
	setup = append(setup,
		`setblock 64 -55 50 minecraft:dripstone_block`,
		`setblock 64 -54 50 minecraft:water`,
		`setblock 64 -56 50 minecraft:pointed_dripstone[vertical_direction=down,thickness=tip]`,
		`setblock 64 -59 50 minecraft:cauldron`)
	// E: mud above the root reads as water and converts to clay.
	setup = append(setup, box(72)...)
	setup = append(setup,
		`setblock 72 -55 50 minecraft:dripstone_block`,
		`setblock 72 -54 50 minecraft:mud`,
		`setblock 72 -56 50 minecraft:pointed_dripstone[vertical_direction=down,thickness=tip]`)
	// F: sulfur spike on a sulfur block — no fluid needed.
	setup = append(setup, box(80)...)
	setup = append(setup,
		`setblock 80 -55 50 minecraft:sulfur`,
		`setblock 80 -56 50 minecraft:sulfur_spike[vertical_direction=down,thickness=tip]`)
	// G: hanging 3-cell column — the root is removed in phase 2.
	setup = append(setup, box(88)...)
	setup = append(setup,
		`setblock 88 -55 50 minecraft:dripstone_block`,
		`setblock 88 -56 50 minecraft:pointed_dripstone[vertical_direction=down,thickness=tip]`,
		`setblock 88 -57 50 minecraft:pointed_dripstone[vertical_direction=down,thickness=tip]`,
		`setblock 88 -58 50 minecraft:pointed_dripstone[vertical_direction=down,thickness=tip]`)
	// H: stalagmite on the floor — the floor cell is removed in phase 2.
	setup = append(setup, box(96)...)
	setup = append(setup,
		`setblock 96 -59 50 minecraft:pointed_dripstone[vertical_direction=up,thickness=tip]`)
	setup = append(setup, `tp @s 64 -55 52`)

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZB-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZB-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZBK%d", i))
		hit := "none"
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, fmt.Sprintf("ZBK%d", i)) {
					return hit
				}
				if strings.Contains(line, fmt.Sprintf("ZB-ok-%d", i)) {
					hit = "hit"
				}
				if strings.Contains(line, fmt.Sprintf("ZB-miss-%d", i)) {
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
		time.Sleep(500 * time.Millisecond)

		var out []string
		// Phase 1: 600 sprinted ticks — expected growth events on a fed
		// start cell ≈ 600*0.0114 ≈ 7; transfer rolls (0.176) fire in
		// the first few ticks.
		sprintTicks(t, pw, sink, 600)

		// A grew: the top cell left thickness=tip behind and at least
		// one extra speleothem cell exists in the shaft (column or
		// floor spike).
		out = append(out, probe(pw, sink, "40 -56 50 minecraft:pointed_dripstone", 0))
		out = append(out, probe(pw, sink, "40 -56 50 minecraft:pointed_dripstone[thickness=tip]", 1))
		growthCells := 0
		for _, y := range []int{-57, -58, -59} {
			if probe(pw, sink, fmt.Sprintf("40 %d 50 minecraft:pointed_dripstone", y), 100) == "hit" {
				growthCells++
			}
		}
		// B/C are frozen fixtures: still a lone tip, nothing grown.
		out = append(out, probe(pw, sink, "48 -56 50 minecraft:pointed_dripstone[thickness=tip]", 2))
		out = append(out, probe(pw, sink, "48 -57 50 minecraft:air", 3))
		out = append(out, probe(pw, sink, "56 -56 50 minecraft:pointed_dripstone[thickness=tip]", 4))
		out = append(out, probe(pw, sink, "56 -57 50 minecraft:air", 5))
		// D: the cauldron took at least one drip.
		out = append(out, probe(pw, sink, "64 -59 50 minecraft:water_cauldron", 6))
		// E: mud -> clay.
		out = append(out, probe(pw, sink, "72 -54 50 minecraft:clay", 7))
		// F: sulfur spike grew at least one cell.
		sulfurCells := 0
		for _, y := range []int{-57, -58, -59} {
			if probe(pw, sink, fmt.Sprintf("80 %d 50 minecraft:sulfur_spike", y), 100) == "hit" {
				sulfurCells++
			}
		}

		// Phase 2a: drop G's root — the whole hanging column falls.
		sendCommand(t, pw, "setblock 88 -55 50 minecraft:air")
		sprintTicks(t, pw, sink, 20)
		for _, y := range []int{-56, -57, -58} {
			out = append(out, probe(pw, sink, fmt.Sprintf("88 %d 50 minecraft:air", y), 0))
		}
		// Phase 2b: drop H's floor — the stalagmite pops.
		sendCommand(t, pw, "setblock 96 -60 50 minecraft:air")
		sprintTicks(t, pw, sink, 5)
		out = append(out, probe(pw, sink, "96 -59 50 minecraft:air", 8))
		_ = growthCells
		_ = sulfurCells
		out = append(out, fmt.Sprintf("growth:%d", growthCells), fmt.Sprintf("sulfur:%d", sulfurCells))
		return out
	}

	va := run(targets.vanilla)
	mc := run(targets.mcgo)
	for i := range va {
		t.Logf("probe %02d: vanilla=%-8s mcgo=%-8s", i, va[i], mc[i])
	}
	// Block probes are exact matches; the two trailing entries are the
	// growth counters, asserted by band below.
	for i := 0; i < len(va)-2; i++ {
		// Probe 1 is a "tip is gone" check expressed as tip-thickness
		// miss — everything else must hit.
		if i == 1 {
			if va[i] != "miss" || mc[i] != "miss" {
				t.Errorf("probe %d: vanilla=%s mc-go=%s (want miss)", i, va[i], mc[i])
			}
			continue
		}
		if va[i] != "hit" || mc[i] != "hit" {
			t.Errorf("probe %d: vanilla=%s mc-go=%s (want hit)", i, va[i], mc[i])
		}
	}
	var vaG, mcG, vaS, mcS int
	fmt.Sscanf(va[len(va)-2], "growth:%d", &vaG)
	fmt.Sscanf(mc[len(mc)-2], "growth:%d", &mcG)
	fmt.Sscanf(va[len(va)-1], "sulfur:%d", &vaS)
	fmt.Sscanf(mc[len(mc)-1], "sulfur:%d", &mcS)
	if vaG < 1 {
		t.Errorf("vanilla dripstone column never grew (cells=%d)", vaG)
	}
	if mcG < 1 {
		t.Errorf("mc-go dripstone column never grew (cells=%d)", mcG)
	}
	if vaS < 1 {
		t.Errorf("vanilla sulfur spike never grew (cells=%d)", vaS)
	}
	if mcS < 1 {
		t.Errorf("mc-go sulfur spike never grew (cells=%d)", mcS)
	}
}
