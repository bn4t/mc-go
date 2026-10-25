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

// TestBubbleColumnParity drives the bubble-column pipeline: a full
// water source over soul_sand or magma_block arms a 20-tick block tick
// (LiquidBlock.onPlace / neighbourChanged / updateShape), which runs
// updateColumn — one pass that rewrites the whole column from the base
// state upward through every occupiable cell. A bubble cell's own
// updateShape schedules a water fluid tick plus a 5-tick block tick
// when the write came from below or opened an occupiable cell above.
// Both servers are frozen and sprinted — /tick step does not drive
// scheduled block ticks on the real server — so all of this is
// deterministic.
func TestBubbleColumnParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		"simulation-distance": "3",
	}, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, func(s *server.Server) {
		s.SimulationDistance = 3
	})
	defer targets.cleanup()

	// Six sealed 1x1 shafts, eight cells deep, on a stone floor.
	shaft := func(x, z int, base string) []string {
		return []string{
			fmt.Sprintf(`fill %d -60 %d %d -49 %d minecraft:stone`, x-1, z-1, x+1, z+1),
			fmt.Sprintf(`setblock %d -60 %d minecraft:%s`, x, z, base),
			fmt.Sprintf(`fill %d -59 %d %d -52 %d minecraft:air`, x, z, x, z),
		}
	}
	setup := []string{
		`tick freeze`,
	}
	// A: push-up column over soul sand.
	setup = append(setup, shaft(46, 50, "soul_sand")...)
	setup = append(setup, `fill 46 -59 50 46 -52 50 minecraft:water`)
	// B: drag-down column over magma.
	setup = append(setup, shaft(56, 50, "magma_block")...)
	setup = append(setup, `fill 56 -59 50 56 -52 50 minecraft:water`)
	// C: soul-sand column, base swapped to magma in phase 2.
	setup = append(setup, shaft(66, 50, "soul_sand")...)
	setup = append(setup, `fill 66 -59 50 66 -52 50 minecraft:water`)
	// D: soul-sand column, base removed in phase 2.
	setup = append(setup, shaft(76, 50, "soul_sand")...)
	setup = append(setup, `fill 76 -59 50 76 -52 50 minecraft:water`)
	// E: short column — a new water source placed above in phase 2
	// must get pulled into the column.
	setup = append(setup, shaft(86, 50, "soul_sand")...)
	setup = append(setup, `fill 86 -59 50 86 -54 50 minecraft:water`)
	// F: lava over magma — the occupy tag is water-only.
	setup = append(setup, shaft(50, 62, "magma_block")...)
	setup = append(setup, `fill 50 -59 62 50 -56 62 minecraft:lava`)
	// Control: water column over plain stone never converts.
	setup = append(setup, shaft(60, 62, "stone")...)
	setup = append(setup, `fill 60 -59 62 60 -56 62 minecraft:water`)
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
		// Phase 0: before any ticks, shafts still hold plain water.
		out = append(out, probe(pw, sink, "46 -59 50 minecraft:water", 0))
		out = append(out, probe(pw, sink, "46 -52 50 minecraft:water", 1))

		// Phase 1: 25 ticks — conversion tick is +20.
		sprintTicks(t, pw, sink, 25)
		for _, y := range []int{-59, -56, -52} {
			out = append(out, probe(pw, sink,
				fmt.Sprintf("46 %d 50 minecraft:bubble_column[drag=false]", y), 0))
		}
		for _, y := range []int{-59, -56, -52} {
			out = append(out, probe(pw, sink,
				fmt.Sprintf("56 %d 50 minecraft:bubble_column[drag=true]", y), 0))
		}
		out = append(out, probe(pw, sink, "50 -59 62 minecraft:lava", 4))
		out = append(out, probe(pw, sink, "60 -59 62 minecraft:water", 5))
		out = append(out, probe(pw, sink, "60 -56 62 minecraft:water", 6))
		// Phase 2a: swap C's base soul->magma, wait past +20.
		sendCommand(t, pw, "setblock 66 -60 50 minecraft:magma_block")
		sprintTicks(t, pw, sink, 25)
		for _, y := range []int{-59, -55, -52} {
			out = append(out, probe(pw, sink,
				fmt.Sprintf("66 %d 50 minecraft:bubble_column[drag=true]", y), 0))
		}

		// Phase 2b: drop D's base to stone — the column drains to
		// water at the +5 self-tick.
		sendCommand(t, pw, "setblock 76 -60 50 minecraft:stone")
		sprintTicks(t, pw, sink, 10)
		for _, y := range []int{-59, -55, -52} {
			out = append(out, probe(pw, sink,
				fmt.Sprintf("76 %d 50 minecraft:water", y), 0))
		}

		// Phase 2c: extend E — a new source above a live column is
		// pulled in at the +5 self-tick.
		sendCommand(t, pw, "setblock 86 -53 50 minecraft:water")
		sprintTicks(t, pw, sink, 10)
		out = append(out, probe(pw, sink, "86 -53 50 minecraft:bubble_column[drag=false]", 7))
		out = append(out, probe(pw, sink, "86 -59 50 minecraft:bubble_column[drag=false]", 8))
		out = append(out, probe(pw, sink, "86 -52 50 minecraft:air", 9))
		return out
	}

	va := run(targets.vanilla)
	mc := run(targets.mcgo)
	for i := range va {
		t.Logf("probe %02d: vanilla=%-4s mcgo=%-4s", i, va[i], mc[i])
	}
	for i := range va {
		if va[i] != "hit" || mc[i] != "hit" {
			t.Errorf("probe %d: vanilla=%s mc-go=%s (want hit)", i, va[i], mc[i])
		}
	}
}
