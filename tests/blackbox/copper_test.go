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

// TestCopperOxidizeParity drives ChangeOverTimeBlock.changeOverTime on
// the weathering-copper families: a 0.05688889 outer roll, then a
// manhattan-4 scan of neighbouring WeatherState blocks where a
// less-oxidized neighbour vetoes the roll outright and each
// more-oxidized one raises the odds to (more+1)/(more+same+1) squared
// times the stage modifier (0.75 unaffected, 1.0 after). A pass writes
// the next stage with the state's shared properties kept.
// Waxed and oxidized blocks are not weathering copper — they neither
// tick nor count. Both servers are frozen and sprinted so the roll
// count is exact: at random_tick_speed 4096 every copper cell sees
// ~one roll per simulated tick.
func TestCopperOxidizeParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{
		"simulation-distance": "3",
	}, map[string]string{
		"ops.json": opsJSONFor("P"),
	}, func(s *server.Server) {
		s.SimulationDistance = 3
	})
	defer targets.cleanup()

	setup := []string{
		`gamerule random_tick_speed 4096`,
		`tick freeze`,
		`fill 44 -60 36 92 -60 80 minecraft:stone`,
		`fill 44 -59 36 92 -50 80 minecraft:air`,
		// Row L: lone unaffected copper — modifier 0.75, ratio 1.
		`setblock 48 -59 40 minecraft:copper_block`,
		`setblock 56 -59 40 minecraft:copper_block`,
		`setblock 64 -59 40 minecraft:copper_block`,
		`setblock 72 -59 40 minecraft:copper_block`,
		// Row E: exposed centres walled by four oxidized blocks —
		// more=4, same=0, ratio 1, modifier 1.
		`setblock 48 -59 46 minecraft:exposed_copper`,
		`setblock 47 -59 46 minecraft:oxidized_copper`,
		`setblock 49 -59 46 minecraft:oxidized_copper`,
		`setblock 48 -59 45 minecraft:oxidized_copper`,
		`setblock 48 -59 47 minecraft:oxidized_copper`,
		`setblock 56 -59 46 minecraft:exposed_copper`,
		`setblock 55 -59 46 minecraft:oxidized_copper`,
		`setblock 57 -59 46 minecraft:oxidized_copper`,
		`setblock 56 -59 45 minecraft:oxidized_copper`,
		`setblock 56 -59 47 minecraft:oxidized_copper`,
		// Row W: waxed — not weathering copper, never ticks.
		`setblock 48 -59 52 minecraft:waxed_copper_block`,
		`setblock 56 -59 52 minecraft:waxed_copper_block`,
		// Row O: oxidized — terminal stage, never ticks.
		`setblock 48 -59 58 minecraft:oxidized_copper`,
		`setblock 56 -59 58 minecraft:oxidized_copper`,
		// Props row: stage transitions keep shared properties.
		`setblock 48 -59 64 minecraft:cut_copper_stairs[facing=north,half=top]`,
		`setblock 56 -59 64 minecraft:copper_bulb[lit=true,powered=true]`,
		`tp @s 64 -55 52`,
	}
	// Phase-2 fixtures are placed mid-run while frozen, so the
	// less-oxidized gate is freshly armed: unblocking needs the
	// neighbour to finish two stage transitions first.
	gatedSetup := []string{}
	freeSetup := []string{}
	for i := 0; i < 4; i++ {
		x := 48 + 8*i
		gatedSetup = append(gatedSetup,
			fmt.Sprintf(`setblock %d -59 70 minecraft:weathered_copper`, x),
			fmt.Sprintf(`setblock %d -59 71 minecraft:copper_block`, x),
		)
		freeSetup = append(freeSetup,
			fmt.Sprintf(`setblock %d -59 76 minecraft:weathered_copper`, x),
			fmt.Sprintf(`setblock %d -59 77 minecraft:oxidized_copper`, x),
			fmt.Sprintf(`setblock %d -59 75 minecraft:oxidized_copper`, x),
		)
	}

	probe := func(pw *protocol.Writer, sink *chatSink, spec string, i int) string {
		sendCommand(t, pw, fmt.Sprintf("execute if block %s run say ZC-ok-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("execute unless block %s run say ZC-miss-%d", spec, i))
		sendCommand(t, pw, fmt.Sprintf("say ZCK%d", i))
		hit := "none"
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, fmt.Sprintf("ZCK%d", i)) {
					return hit
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
		sprintTicks(t, pw, sink, 100)
		time.Sleep(300 * time.Millisecond)

		var out []string
		// Lone row: each cell advanced past unaffected.
		for i := 0; i < 4; i++ {
			out = append(out, probe(pw, sink,
				fmt.Sprintf("%d -59 40 minecraft:copper_block", 48+8*i), i))
		}
		// Boosted row: each centre advanced past exposed.
		for i := 0; i < 2; i++ {
			out = append(out, probe(pw, sink,
				fmt.Sprintf("%d -59 46 minecraft:exposed_copper", 48+8*i), i))
		}
		// Waxed and oxidized rows hold.
		out = append(out, probe(pw, sink, "48 -59 52 minecraft:waxed_copper_block", 0))
		out = append(out, probe(pw, sink, "56 -59 52 minecraft:waxed_copper_block", 1))
		out = append(out, probe(pw, sink, "48 -59 58 minecraft:oxidized_copper", 2))
		out = append(out, probe(pw, sink, "56 -59 58 minecraft:oxidized_copper", 3))
		// Prop pins: the stairs kept facing=north,half=top across the
		// transition — probe the three later stages and count a hit.
		propHit := false
		for j, st := range []string{"exposed_", "weathered_", "oxidized_"} {
			if probe(pw, sink, fmt.Sprintf("48 -59 64 minecraft:%scut_copper_stairs[facing=north,half=top]", st), 10+j) == "hit" {
				propHit = true
			}
		}
		if propHit {
			out = append(out, "hit")
		} else {
			out = append(out, "miss")
		}
		bulbHit := false
		for j, st := range []string{"exposed_", "weathered_", "oxidized_"} {
			if probe(pw, sink, fmt.Sprintf("56 -59 64 minecraft:%scopper_bulb[lit=true]", st), 20+j) == "hit" {
				bulbHit = true
			}
		}
		if bulbHit {
			out = append(out, "hit")
		} else {
			out = append(out, "miss")
		}

		// Phase 2: gated vs free weathered cells in one 40-tick
		// window — the gated cells can only move once their
		// unaffected neighbour has cleared two transitions (~40
		// rolls on its own), the free ones need ~18.
		for _, cmd := range gatedSetup {
			sendCommand(t, pw, cmd)
		}
		for _, cmd := range freeSetup {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(300 * time.Millisecond)
		sprintTicks(t, pw, sink, 40)
		time.Sleep(300 * time.Millisecond)
		for i := 0; i < 4; i++ {
			out = append(out, probe(pw, sink,
				fmt.Sprintf("%d -59 70 minecraft:weathered_copper", 48+8*i), i))
		}
		for i := 0; i < 4; i++ {
			out = append(out, probe(pw, sink,
				fmt.Sprintf("%d -59 76 minecraft:weathered_copper", 48+8*i), i))
		}
		return out
	}

	va := run(targets.vanilla)
	mc := run(targets.mcgo)
	for i := range va {
		t.Logf("probe %02d: vanilla=%-4s mcgo=%-4s", i, va[i], mc[i])
	}

	// Lone row: >=3 of 4 cells no longer copper_block on both.
	loneMiss := func(out []string) int {
		n := 0
		for i := 0; i < 4; i++ {
			if out[i] == "miss" {
				n++
			}
		}
		return n
	}
	if loneMiss(mc) < 3 || loneMiss(va) < 3 {
		t.Fatalf("lone copper oxidation: mc=%v vanilla=%v", mc[:4], va[:4])
	}
	// Boosted row: both centres past exposed on both.
	for i := 4; i < 6; i++ {
		if mc[i] != "miss" || va[i] != "miss" {
			t.Fatalf("boosted exposed oxidation: mc[%d]=%s vanilla[%d]=%s", i, mc[i], i, va[i])
		}
	}
	// Waxed + oxidized hold (probes 6..9 all hit).
	for i := 6; i < 10; i++ {
		if mc[i] != "hit" || va[i] != "hit" {
			t.Fatalf("terminal/waxed hold: mc[%d]=%s vanilla[%d]=%s", i, mc[i], i, va[i])
		}
	}
	// Prop pins (probes 10, 11).
	for i := 10; i < 12; i++ {
		if mc[i] != "hit" || va[i] != "hit" {
			t.Fatalf("prop-preserving oxidation: mc[%d]=%s vanilla[%d]=%s", i, mc[i], i, va[i])
		}
	}
	// Gated row (probes 12..15): >=3 of 4 still weathered.
	gatedHold := func(out []string) int {
		n := 0
		for i := 12; i < 16; i++ {
			if out[i] == "hit" {
				n++
			}
		}
		return n
	}
	if gatedHold(mc) < 3 || gatedHold(va) < 3 {
		t.Fatalf("gated weathered hold: mc=%v vanilla=%v", mc[12:16], va[12:16])
	}
	// Free row (probes 16..19): >=3 of 4 advanced past weathered.
	freeAdv := func(out []string) int {
		n := 0
		for i := 16; i < 20; i++ {
			if out[i] == "miss" {
				n++
			}
		}
		return n
	}
	if freeAdv(mc) < 3 || freeAdv(va) < 3 {
		t.Fatalf("free weathered advance: mc=%v vanilla=%v", mc[16:20], va[16:20])
	}
}
