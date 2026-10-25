package blackbox

import (
	"strings"
	"testing"
	"time"
)

// TestCherryParity places cherry features on both servers and
// compares the parts that are not luck: every cherry keeps at least
// one branch (the first horizontal step always lands off-column into
// air and writes a directional-axis log), the column holds at least
// three cells, one attachment's canopy rows fill at least thirty
// leaves, and a block inside the 5x5 space check fails the feature.
//
// Measured on 26.2: cherry.json — CherryTrunkPlacer {base 7, randA 1,
// randB 0} with branch_start U{-4..-3}/U{-4..-4} (bumped past the
// first on collision), branch_end U{-1..0}, horizontal length
// U{2..4}, weighted 1/2/3 branch count, and one or two opposite
// branches that random-walk to their end cell; CherryFoliagePlacer
// {radius 4, offset 0, height 5, wide_bottom .25, corner .25,
// hanging .1667, hang_ext .3334} — square rows (1,2),(2,1),(3,0)
// plus hanging rows (3,-1),(2,-2) that drop below the perimeter at
// the roll; two_layers upper_size 2 — every layer of the space check
// covers the whole 5x5, so one block beside the column kills the
// feature.
func TestCherryParity(t *testing.T) {
	const probe = "CherryParity"
	tg := requireBlackboxFiles(t,
		map[string]string{"command-spam-threshold-seconds": "0"},
		map[string]string{"ops.json": opsJSONFor(probe)},
		nil)
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runCherryScenarios(t, tg.vanilla, probe)
	got := runCherryScenarios(t, tg.mcgo, probe)

	if !sameNames(want.names, got.names) {
		t.Errorf("probe cells: mc-go and vanilla disagree\n%s\nmc-go:\n%s",
			renderNameDiff(want.names, got.names), renderNames(got.names))
	}
	if want.failed != got.failed {
		t.Errorf("blocked column: vanilla failed=%v mc-go failed=%v", want.failed, got.failed)
	}
	if want.groundOK != got.groundOK {
		t.Errorf("ground-layer stone: vanilla grew=%v mc-go grew=%v", want.groundOK, got.groundOK)
	}
	for i := 0; i < cherryCount; i++ {
		// The shortest column is first+1 with first >= 2.
		if want.logs[i] < 3 {
			t.Errorf("cherry %d: vanilla has only %d cherry_log cells", i, want.logs[i])
		}
		if got.logs[i] < 3 {
			t.Errorf("cherry %d: mc-go has only %d cherry_log cells", i, got.logs[i])
		}
		// One attachment's rows y=0..2 hold ~60 cells before holes.
		if want.leaves[i] < 30 {
			t.Errorf("cherry %d: vanilla has only %d cherry_leaves cells", i, want.leaves[i])
		}
		if got.leaves[i] < 30 {
			t.Errorf("cherry %d: mc-go has only %d cherry_leaves cells", i, got.leaves[i])
		}
		// The first branch's horizontal step always writes a
		// directional-axis log off the column — the only source of
		// non-y cherry_log a tree can make.
		if want.axis[i] < 1 {
			t.Errorf("cherry %d: vanilla has no horizontal-axis cherry_log", i)
		}
		if got.axis[i] < 1 {
			t.Errorf("cherry %d: mc-go has no horizontal-axis cherry_log", i)
		}
	}
}

const cherryCount = 3

type cherryRun struct {
	names    map[[3]int]string
	logs     [cherryCount]int
	leaves   [cherryCount]int
	axis     [cherryCount]int
	failed   bool
	groundOK bool
}

func runCherryScenarios(t *testing.T, addr, name string) cherryRun {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	sendCmd(t, pw, "time set day")
	waitDrained(t, pw, sink)

	baseY := -60
	out := cherryRun{names: map[[3]int]string{}}
	xs := [cherryCount]int{4, 14, 24}
	for _, x := range xs {
		sendCmd(t, pw, "place feature minecraft:cherry "+
			itoa(int32(x))+" "+itoa(int32(baseY))+" -44")
	}
	waitDrained(t, pw, sink)

	// two_layers' default limit is 1: the space check at y=0 is the
	// column cell alone, so a stone beside the column at y=0 is
	// invisible and the feature still grows; at y=2 the check covers
	// the whole 5x5 and the same stone kills it.
	sendCmd(t, pw, "setblock -48 "+itoa(int32(baseY))+" -48 minecraft:stone")
	sink.take()
	sendCmd(t, pw, "place feature minecraft:cherry -50 "+itoa(int32(baseY))+" -48")
	lines := collectUntil(sink, "place.feature", 10*time.Second)
	out.groundOK = sawMarker(lines, "place.feature.success") ||
		sawMarker(lines, "Placed")
	waitDrained(t, pw, sink)

	sendCmd(t, pw, "setblock -68 "+itoa(int32(baseY+2))+" -48 minecraft:stone")
	sink.take()
	sendCmd(t, pw, "place feature minecraft:cherry -70 "+itoa(int32(baseY))+" -48")
	lines = collectUntil(sink, "place.feature", 10*time.Second)
	out.failed = sawMarker(lines, "place.feature.failed") ||
		sawMarker(lines, "Failed to place feature")

	out.names = scanNames(t, pw, sink,
		[]string{"minecraft:cherry_log", "minecraft:cherry_leaves", "minecraft:air",
			"minecraft:stone", "minecraft:dirt"}, [][3]int{
			{4, baseY, -44},
			{-50, baseY, -48},     // grew beside the stone: log
			{-48, baseY, -48},     // the stone itself, untouched
			{-70, baseY, -48},     // failed: air
			{-70, baseY + 1, -48}, // failed: air
		})

	// Grove scan: branches reach ~6 cells out and canopy rows span
	// y -2..+2 around each attachment.
	for i, x := range xs {
		var box [][3]int
		for dx := -6; dx <= 6; dx++ {
			for dz := -6; dz <= 6; dz++ {
				for y := baseY - 4; y <= baseY+11; y++ {
					box = append(box, [3]int{x + dx, y, -44 + dz})
				}
			}
		}
		names := scanNames(t, pw, sink, []string{
			"minecraft:cherry_log", "minecraft:cherry_log[axis=x]",
			"minecraft:cherry_log[axis=z]", "minecraft:cherry_leaves",
		}, box)
		// Multi-candidate hits join with "+" — count by substring.
		for _, v := range names {
			if strings.Contains(v, "cherry_log") {
				out.logs[i]++
			}
			if strings.Contains(v, "axis=") {
				out.axis[i]++
			}
			if strings.Contains(v, "cherry_leaves") {
				out.leaves[i]++
			}
		}
	}
	return out
}
