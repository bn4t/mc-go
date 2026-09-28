package blackbox

import (
	"strings"
	"testing"
	"time"
)

// TestFancyOakParity places fancy_oak features directly on both
// servers and compares the parts that are not luck: the trunk column
// always roots at the origin, the crown volume holds at least the
// first attachment's worth of leaves, horizontal branch limbs write
// non-y-axis logs somewhere in the grove, a column blocked to under
// four free cells fails the whole feature, and a clipped fancy oak
// still grows when exactly four free cells remain.
//
// Measured on 26.2: fancy_oak.json — FancyTrunkPlacer {base 3,
// randA 11, randB 0}; the placer runs on height = clipped+2 with
// trunkHeight = floor(height*0.618), scans foliage spots from the
// crown down while two interpolated limbs pass isFree checks, then
// writes the column plus one limb per kept coord whose branch base
// sits at >=20% of the tree; the limbs' logs take the dominant
// direction's axis. FancyFoliagePlacer {radius 2, offset 4, height 4}
// draws five circular rows (12 on top/bottom, ~32 between) per kept
// attachment. minsize two_layers {limit 0, upper 0,
// min_clipped_height 4} — column-only space check, and a clipped tree
// grows as long as >=4 cells remain.
func TestFancyOakParity(t *testing.T) {
	const probe = "FancyOakParity"
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

	want := runFancyScenarios(t, tg.vanilla, probe)
	got := runFancyScenarios(t, tg.mcgo, probe)

	if !sameNames(want.names, got.names) {
		t.Errorf("probe cells: mc-go and vanilla disagree\n%s\nmc-go:\n%s",
			renderNameDiff(want.names, got.names), renderNames(got.names))
	}
	if want.failed != got.failed {
		t.Errorf("blocked column: vanilla failed=%v mc-go failed=%v", want.failed, got.failed)
	}
	if want.clipped != got.clipped {
		t.Errorf("clipped growth: vanilla grew=%v mc-go grew=%v", want.clipped, got.clipped)
	}
	for i := 0; i < fancyCount; i++ {
		// trunkHeight = floor((clipped+2)*0.618) >= floor(5*0.618) = 3
		// logs on the column alone; limbs add more.
		if want.logs[i] < 3 {
			t.Errorf("fancy %d: vanilla has only %d oak_log cells", i, want.logs[i])
		}
		if got.logs[i] < 3 {
			t.Errorf("fancy %d: mc-go has only %d oak_log cells", i, got.logs[i])
		}
		// The first attachment (always kept) draws five rows with at
		// least the two r=2 circles' 12 cells each, minus cells lost to
		// the trunk/limbs and corner trims — twenty-five is a safe
		// floor for one attachment.
		if want.leaves[i] < 25 {
			t.Errorf("fancy %d: vanilla has only %d oak_leaves cells", i, want.leaves[i])
		}
		if got.leaves[i] < 25 {
			t.Errorf("fancy %d: mc-go has only %d oak_leaves cells", i, got.leaves[i])
		}
	}
	// Branch limbs write the only non-y-axis oak_log states a tree can
	// produce; three trees of height >= 5 virtually always make one.
	if want.axis < 1 {
		t.Errorf("vanilla grove has %d horizontal-axis oak_log cells", want.axis)
	}
	if got.axis < 1 {
		t.Errorf("mc-go grove has %d horizontal-axis oak_log cells", got.axis)
	}
}

const fancyCount = 3

type fancyRun struct {
	names   map[[3]int]string
	logs    [fancyCount]int
	leaves  [fancyCount]int
	axis    int
	failed  bool
	clipped bool
}

func runFancyScenarios(t *testing.T, addr, name string) fancyRun {
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
	out := fancyRun{names: map[[3]int]string{}}
	xs := [fancyCount]int{4, 14, 24}
	for _, x := range xs {
		sendCmd(t, pw, "place feature minecraft:fancy_oak "+
			itoa(int32(x))+" "+itoa(int32(baseY))+" -44")
	}
	waitDrained(t, pw, sink)

	// Blocked column: the stone sits at pos+4, so the space check
	// clips to 2 < min_clipped_height 4 and the whole feature fails
	// without writing a block. The two scenario columns sit far from
	// the grove so no canopy reaches them.
	sendCmd(t, pw, "setblock -50 "+itoa(int32(baseY+4))+" -48 minecraft:stone")
	sink.take()
	sendCmd(t, pw, "place feature minecraft:fancy_oak -50 "+itoa(int32(baseY))+" -48")
	lines := collectUntil(sink, "place.feature", 10*time.Second)
	out.failed = sawMarker(lines, "place.feature.failed") ||
		sawMarker(lines, "Failed to place feature")

	// Clipped column: the stone at pos+6 clips to exactly 4 → the
	// stubby tree grows (height 6, trunkHeight 3, one attachment).
	sendCmd(t, pw, "setblock -50 "+itoa(int32(baseY+6))+" -52 minecraft:stone")
	sink.take()
	sendCmd(t, pw, "place feature minecraft:fancy_oak -50 "+itoa(int32(baseY))+" -52")
	lines = collectUntil(sink, "place.feature", 10*time.Second)
	out.clipped = sawMarker(lines, "place.feature.success") ||
		sawMarker(lines, "Placed")

	// Probe cells: every tree's origin is the bottom log, the blocked
	// feature's origin stays air, and the clipped feature (height 6,
	// trunkHeight 3) writes logs pos+0..pos+3 with leaf cells filling
	// the column's top rows at pos+4 and pos+5.
	out.names = scanNames(t, pw, sink,
		[]string{"minecraft:oak_log", "minecraft:oak_leaves", "minecraft:air",
			"minecraft:stone", "minecraft:dirt"}, [][3]int{
			{4, baseY, -44},
			{-50, baseY, -48},     // failed: air
			{-50, baseY + 1, -48}, // failed: air
			{-50, baseY, -52},     // clipped column, log
			{-50, baseY + 2, -52}, // clipped column, log
			{-50, baseY + 3, -52}, // clipped column top, log
			{-50, baseY + 4, -52}, // canopy on-axis row: leaf
			// pos+5 is leaf only when the sampled height reaches
			// the stone (>=4) — not pinned.
		})

	// Grove scan: trunk limbs reach ~6 cells out, the canopy up to
	// pos+5 vertically.
	for i, x := range xs {
		var box [][3]int
		for dx := -6; dx <= 6; dx++ {
			for dz := -6; dz <= 6; dz++ {
				for y := baseY - 1; y <= baseY+11; y++ {
					box = append(box, [3]int{x + dx, y, -44 + dz})
				}
			}
		}
		names := scanNames(t, pw, sink, []string{
			"minecraft:oak_log", "minecraft:oak_log[axis=x]",
			"minecraft:oak_log[axis=z]", "minecraft:oak_leaves",
		}, box)
		// Every candidate that matches joins the cell's hits with "+",
		// so a horizontal-axis log reads
		// "oak_log+oak_log[axis=x]" — count by substring.
		for _, v := range names {
			if strings.Contains(v, "oak_log") {
				out.logs[i]++
			}
			if strings.Contains(v, "axis=") {
				out.axis++
			}
			if strings.Contains(v, "oak_leaves") {
				out.leaves[i]++
			}
		}
	}
	return out
}

// collectUntil gathers chat lines until one carries the marker or the
// timeout lapses — used for command replies, which a drain helper
// would swallow.
func collectUntil(sink *chatSink, want string, timeout time.Duration) []string {
	deadline := time.Now().Add(timeout)
	var lines []string
	for time.Now().Before(deadline) {
		got := sink.take()
		lines = append(lines, got...)
		if sawMarker(got, want) {
			return lines
		}
		time.Sleep(50 * time.Millisecond)
	}
	return lines
}
