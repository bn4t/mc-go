package blackbox

import (
	"strings"
	"testing"
	"time"
)

// TestDarkOakParity places dark_oak features on both servers and
// compares the parts that are not luck: the trunk writes a 2x2 at
// the origin, the canopy volume is large, a block beside the trunk
// at ground level is invisible to the space check (three_layers'
// y=0 layer is the single cell), a block inside the middle 3x3
// fails the feature, a lone dark_oak sapling can never grow, and a
// 2x2 sapling quad takes the mega path.
//
// Measured on 26.2: dark_oak.json — DarkOakTrunkPlacer {base 6,
// randA 2, randB 1}; a 2x2 trunk that leans at most two cells from
// h-intn(4), each layer gated on its NW cell being air-or-leaves,
// plus a 1-in-3 fringe of 2..4 stems hanging from the origin's x/z
// ring; DarkOakFoliagePlacer {radius 0, offset 0} draws (2,-1),
// (3,0), (2,1) and a 50% cap on the double-trunk top, (2,-1),(1,0)
// per fringe stem. three_layers {limit 1, upper_limit 1, lower 0,
// middle 1, upper 2}: column-only at y=0, 3x3 middle, 5x5 top.
// TreeGrower scans the four quads the ticked sapling can anchor —
// dark_oak has no single feature, so a lone sapling never grows.
func TestDarkOakParity(t *testing.T) {
	const probe = "DarkOakParity"
	tg := requireBlackboxFiles(t,
		map[string]string{
			"command-spam-threshold-seconds": "0",
			"random_tick_speed":              "600",
		},
		map[string]string{"ops.json": opsJSONFor(probe)},
		nil)
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runDarkOakScenarios(t, tg.vanilla, probe)
	got := runDarkOakScenarios(t, tg.mcgo, probe)

	if !sameNames(want.names, got.names) {
		t.Errorf("probe cells: mc-go and vanilla disagree\n%s\nmc-go:\n%s",
			renderNameDiff(want.names, got.names), renderNames(got.names))
	}
	if want.failed != got.failed {
		t.Errorf("blocked middle: vanilla failed=%v mc-go failed=%v", want.failed, got.failed)
	}
	if want.groundOK != got.groundOK {
		t.Errorf("ground-layer stone: vanilla grew=%v mc-go grew=%v", want.groundOK, got.groundOK)
	}
	if want.quad != got.quad {
		t.Errorf("2x2 sapling: vanilla grew=%v mc-go grew=%v", want.quad, got.quad)
	}
	for i := 0; i < darkOakCount; i++ {
		// Every layer writes its 2x2 while the NW cell is air —
		// height >= 6 → >= 24 cells minus skipped layers.
		if want.logs[i] < 20 {
			t.Errorf("dark oak %d: vanilla has only %d dark_oak_log cells", i, want.logs[i])
		}
		if got.logs[i] < 20 {
			t.Errorf("dark oak %d: mc-go has only %d dark_oak_log cells", i, got.logs[i])
		}
		// The main attachment alone draws ~120 cells.
		if want.leaves[i] < 60 {
			t.Errorf("dark oak %d: vanilla has only %d dark_oak_leaves cells", i, want.leaves[i])
		}
		if got.leaves[i] < 60 {
			t.Errorf("dark oak %d: mc-go has only %d dark_oak_leaves cells", i, got.leaves[i])
		}
	}
}

const darkOakCount = 3

type darkOakRun struct {
	names    map[[3]int]string
	logs     [darkOakCount]int
	leaves   [darkOakCount]int
	failed   bool
	groundOK bool
	quad     bool
}

func runDarkOakScenarios(t *testing.T, addr, name string) darkOakRun {
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
	out := darkOakRun{names: map[[3]int]string{}}
	xs := [darkOakCount]int{4, 18, 32}
	for _, x := range xs {
		sendCmd(t, pw, "place feature minecraft:dark_oak "+
			itoa(int32(x))+" "+itoa(int32(baseY))+" -44")
	}
	waitDrained(t, pw, sink)

	// three_layers: y=0 checks the single cell, so a stone beside
	// the trunk is invisible and the feature still grows.
	sendCmd(t, pw, "setblock -48 "+itoa(int32(baseY))+" -48 minecraft:stone")
	sink.take()
	sendCmd(t, pw, "place feature minecraft:dark_oak -50 "+itoa(int32(baseY))+" -48")
	lines := collectUntil(sink, "place.feature", 10*time.Second)
	out.groundOK = sawMarker(lines, "place.feature.success") ||
		sawMarker(lines, "Placed")
	waitDrained(t, pw, sink)

	// A stone inside the middle 3x3 (y>=1) kills the feature.
	sendCmd(t, pw, "setblock -69 "+itoa(int32(baseY+3))+" -48 minecraft:stone")
	sink.take()
	sendCmd(t, pw, "place feature minecraft:dark_oak -70 "+itoa(int32(baseY))+" -48")
	lines = collectUntil(sink, "place.feature", 10*time.Second)
	out.failed = sawMarker(lines, "place.feature.failed") ||
		sawMarker(lines, "Failed to place feature")

	// A lone dark_oak sapling can never grow (no single feature);
	// a 2x2 quad takes the mega path and its NW cell becomes a log.
	sendCmd(t, pw, "setblock -50 "+itoa(int32(baseY))+" -56 minecraft:dark_oak_sapling")
	sendCmd(t, pw, "setblock -70 "+itoa(int32(baseY))+" -56 minecraft:dark_oak_sapling")
	sendCmd(t, pw, "setblock -69 "+itoa(int32(baseY))+" -56 minecraft:dark_oak_sapling")
	sendCmd(t, pw, "setblock -70 "+itoa(int32(baseY))+" -55 minecraft:dark_oak_sapling")
	sendCmd(t, pw, "setblock -69 "+itoa(int32(baseY))+" -55 minecraft:dark_oak_sapling")
	waitDrained(t, pw, sink)
	sendCommand(t, pw, "say QUADWAIT")
	_ = waitForMarker(t, sink, "QUADWAIT", 45*time.Second)

	out.names = scanNames(t, pw, sink,
		[]string{"minecraft:dark_oak_log", "minecraft:dark_oak_leaves",
			"minecraft:dark_oak_sapling", "minecraft:air",
			"minecraft:stone", "minecraft:dirt"}, [][3]int{
			{4, baseY, -44},
			{5, baseY, -43},       // the trunk's SE cell
			{-50, baseY, -48},     // grew beside the stone: log
			{-48, baseY, -48},     // the stone itself, untouched
			{-70, baseY, -48},     // failed: air
			{-70, baseY + 1, -48}, // failed: air
			{-50, baseY, -56},     // lone sapling: stays sapling
			{-70, baseY, -56},     // quad NW corner: log when grown
		})
	{
		v, ok := out.names[[3]int{-70, baseY, -56}]
		out.quad = ok && strings.Contains(v, "dark_oak_log")
	}

	// Grove scan: the trunk can lean ~2 cells and fringe stems hang
	// from the ring around the origin.
	for i, x := range xs {
		var box [][3]int
		for dx := -6; dx <= 7; dx++ {
			for dz := -6; dz <= 7; dz++ {
				for y := baseY - 2; y <= baseY+12; y++ {
					box = append(box, [3]int{x + dx, y, -44 + dz})
				}
			}
		}
		names := scanNames(t, pw, sink, []string{
			"minecraft:dark_oak_log", "minecraft:dark_oak_leaves",
		}, box)
		for _, v := range names {
			if strings.Contains(v, "dark_oak_log") {
				out.logs[i]++
			}
			if strings.Contains(v, "dark_oak_leaves") {
				out.leaves[i]++
			}
		}
	}
	return out
}
