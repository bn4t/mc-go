package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestWallVineParity drives VineBlock.randomTick — a 1/4 roll picks a
// random direction; an unheld horizontal face wraps around corners or
// the cell gains that face onto a solid side; up sprouts a face or a
// thinned copy above; down copies random faces into the cell below —
// all capped at five vines per 9×3×9 — and VineBlock.updateShape
// pruning: any non-below write clears faces whose support vanished,
// air when none remain. Direction choice is random, so fixtures give
// every direction a determined landing cell.
func TestWallVineParity(t *testing.T) {
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
		// A: a vine[south] clinging to a wall face. Sideways picks wrap
		// it along the wall, down copies faces into the air below, up
		// occasionally sprouts overhead.
		`fill 38 -60 45 42 -58 45 minecraft:stone`,
		`setblock 40 -59 44 minecraft:vine[south=true]`,
		// B: single-face vine; breaking the wall pops it to air.
		`setblock 46 -59 45 minecraft:stone`,
		`setblock 46 -59 44 minecraft:vine[south=true]`,
		// C: two faces (wall + ceiling); losing the wall drops the
		// south face but the cell survives on up.
		`setblock 50 -59 45 minecraft:stone`,
		`setblock 50 -58 44 minecraft:stone`,
		`setblock 50 -59 44 minecraft:vine[south=true,up=true]`,
		// U: a vine boxed in stone holding all four horizontal faces —
		// sideways picks no-op, below is solid, so only the up branch
		// can act: a thinned copy sprouts into the carved air hole.
		`fill 29 -60 43 31 -58 45 minecraft:stone`,
		`setblock 30 -59 44 minecraft:air`,
		`setblock 30 -58 44 minecraft:air`,
		`setblock 30 -59 44 minecraft:vine[north=true,south=true,east=true,west=true]`,
		// D: spread_vines=false freezes the family.
		`setblock 56 -59 45 minecraft:stone`,
		`setblock 56 -59 44 minecraft:vine[south=true]`,
		`tp @s 40.5 -58 44.5`,
	}
	breaks := []string{
		`setblock 46 -59 45 minecraft:air`,
		`setblock 50 -59 45 minecraft:air`,
	}
	preProbes := []struct{ name, spec string }{
		{"seed vine clings", "40 -59 44 minecraft:vine[south=true]"},
		{"prune seed sits", "46 -59 44 minecraft:vine[south=true]"},
		{"two-face seed sits", "50 -59 44 minecraft:vine[up=true,south=true]"},
		{"frozen seed sits", "56 -59 44 minecraft:vine[south=true]"},
	}
	tickProbes := []struct{ name, spec string }{
		{"spread down", "40 -60 44 minecraft:vine[south=true]"},
		{"sprouted into ceiling hole", "30 -58 44 minecraft:vine"},
		{"boxed seed keeps faces", "30 -59 44 minecraft:vine[north=true,south=true,east=true,west=true]"},
		{"seed still alive", "40 -59 44 minecraft:vine"},
	}
	// Sideways wrap lands somewhere along the wall face but the 5-per-
	// 9x3x9 cap saturates first, so which cells fill is RNG — count
	// hits over the flanks and require both servers grew something.
	wrapCells := []string{
		"38 -59 44", "39 -59 44", "41 -59 44", "42 -59 44",
	}
	cutProbes := []struct{ name, spec string }{
		{"wall-less vine popped", "46 -59 44 minecraft:air"},
		{"wall face pruned keeps up", "50 -59 44 minecraft:vine[up=true,south=false]"},
	}
	frozenProbes := []struct{ name, spec string }{
		{"frozen seed still sits", "56 -59 44 minecraft:vine[south=true]"},
		{"frozen spread stays air", "57 -59 44 minecraft:air"},
		{"frozen spread stays air 2", "55 -59 44 minecraft:air"},
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

	var wrapCounts []int
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
		sendCommand(t, pw, `say ZSYNC`)
		deadline := time.Now().Add(15 * time.Second)
	sync:
		for time.Now().Before(deadline) {
			for _, line := range sink.take() {
				if strings.Contains(line, "ZSYNC") {
					break sync
				}
			}
			time.Sleep(100 * time.Millisecond)
		}

		var out []string
		for i, p := range preProbes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		sendCommand(t, pw, `gamerule random_tick_speed 4096`)
		time.Sleep(45 * time.Second)
		sendCommand(t, pw, `gamerule random_tick_speed 0`)
		time.Sleep(500 * time.Millisecond)
		for i, p := range tickProbes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		wrapCount := 0
		for j, cell := range wrapCells {
			if probe(pw, sink, fmt.Sprintf("%s minecraft:vine", cell), 100+j) == "hit" {
				wrapCount++
			}
		}
		wrapCounts = append(wrapCounts, wrapCount)
		for _, cmd := range breaks {
			sendCommand(t, pw, cmd)
		}
		time.Sleep(1500 * time.Millisecond)
		for i, p := range cutProbes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		sendCommand(t, pw, `gamerule spread_vines false`)
		sendCommand(t, pw, `gamerule random_tick_speed 4096`)
		time.Sleep(20 * time.Second)
		sendCommand(t, pw, `gamerule random_tick_speed 0`)
		for i, p := range frozenProbes {
			out = append(out, probe(pw, sink, p.spec, i))
		}
		return out
	}

	vanilla := run(targets.vanilla)
	mcgo := run(targets.mcgo)
	all := append(append(preProbes, tickProbes...), append(cutProbes, frozenProbes...)...)
	for i, p := range all {
		t.Logf("%-30s vanilla=%s mcgo=%s", p.name, vanilla[i], mcgo[i])
		if vanilla[i] == "hit" && mcgo[i] != "hit" {
			t.Errorf("probe %q: vanilla hit, mc-go %s", p.name, mcgo[i])
		}
		if mcgo[i] == "hit" && vanilla[i] != "hit" {
			t.Errorf("probe %q: mc-go hit, vanilla %s", p.name, vanilla[i])
		}
	}
	t.Logf("wall wrap count: vanilla=%d mcgo=%d", wrapCounts[0], wrapCounts[1])
	if wrapCounts[0] == 0 && wrapCounts[1] == 0 {
		t.Errorf("no wall wrap on either server — fixture broken")
	}
	if (wrapCounts[0] > 0) != (wrapCounts[1] > 0) {
		t.Errorf("wrap divergence vanilla=%d mcgo=%d", wrapCounts[0], wrapCounts[1])
	}
}
