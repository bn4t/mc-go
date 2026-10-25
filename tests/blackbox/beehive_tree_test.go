// TestBeehiveTreeParity pins TreeGrower's hasFlowers gate and
// BeehiveDecorator against vanilla (26.2
// net.minecraft.world.level.levelgen.feature.TreeGrower +
// treedecorators.BeehiveDecorator): getConfiguredFeature draws ONE
// roll — below secondaryChance the secondary spec wins, else the
// primary — and a #flowers block in the 5x3x5 around the sapling
// swaps the chosen spec for its *_bees_005 variant (same placers +
// only [beehive 0.05]). BeehiveDecorator picks the nest row
// max(leaf0.Y-1, log0.Y+1) and writes the first shuffled air
// neighbor of a log at that height. Grown-side comparisons are
// floors — each placement rolls its own RNG on each server.
package blackbox

import (
	"strings"
	"testing"
	"time"
)

type beehiveRun struct {
	flowerNests int
	flowerLogs  int
	plainNests  int
	plainLogs   int
	placedLogs  int
}

func TestBeehiveTreeParity(t *testing.T) {
	const probe = "BeesParity"
	tg := requireBlackboxFiles(t,
		map[string]string{
			"command-spam-threshold-seconds": "0",
		},
		map[string]string{"ops.json": opsJSONFor(probe)},
		nil)
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runBeehiveScenario(t, tg.vanilla, probe)
	got := runBeehiveScenario(t, tg.mcgo, probe)

	// ~345 flowered saplings x .05 — the nest floor just has to clear
	// zero on both servers; per-run counts drift with the RNG stream.
	if want.flowerNests < 1 || got.flowerNests < 1 {
		t.Errorf("nests beside flowered saplings: vanilla=%d mc-go=%d (want >=1)",
			want.flowerNests, got.flowerNests)
	}
	// Unflowered saplings take the plain feature — no beehive
	// decorator, so zero nests is the exact pin.
	if want.plainNests != 0 || got.plainNests != 0 {
		t.Errorf("nests beside plain saplings: vanilla=%d mc-go=%d (want 0)",
			want.plainNests, got.plainNests)
	}
	if want.flowerLogs < 30 || got.flowerLogs < 30 {
		t.Errorf("flower-run logs: vanilla=%d mc-go=%d (want >=30)",
			want.flowerLogs, got.flowerLogs)
	}
	if want.plainLogs < 30 || got.plainLogs < 30 {
		t.Errorf("plain-run logs: vanilla=%d mc-go=%d (want >=30)",
			want.plainLogs, got.plainLogs)
	}
	// All four /place'd BEES_005 features must produce trees.
	if want.placedLogs < 10 || got.placedLogs < 10 {
		t.Errorf("/place *_bees_005 logs: vanilla=%d mc-go=%d (want >=10)",
			want.placedLogs, got.placedLogs)
	}
	t.Logf("beehive trees: nests v=%d g=%d, plain nests v=%d g=%d, flower logs v=%d g=%d, plain logs v=%d g=%d, placed logs v=%d g=%d",
		want.flowerNests, got.flowerNests, want.plainNests, got.plainNests,
		want.flowerLogs, got.flowerLogs, want.plainLogs, got.plainLogs,
		want.placedLogs, got.placedLogs)
}

// runBeehiveScenario grows a dense birch grid under a dandelion
// ceiling (every sapling flowery) beside an identical grid without
// flowers, places the four BEES_005 features, then counts nests and
// logs via fill-replace.
func runBeehiveScenario(t *testing.T, addr, name string) beehiveRun {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	sendCmd(t, pw, "time set day")
	sendCmd(t, pw, "gamerule random_tick_speed 600")
	sendCmd(t, pw, "tick freeze")

	// Flowered pad: dirt at -61, birch saplings on the even-x columns,
	// dandelions on the odd-x columns at the same y — flowers need soil
	// to stand on, and a neighbor column sits inside every sapling's
	// 5x3x5 hasFlowers box (x+-2, y-1..+1, z+-2).
	sendCmd(t, pw, "fill -2 -61 -62 20 -61 -48 minecraft:dirt")
	for x := 0; x <= 18; x += 2 {
		sendCmd(t, pw, "fill "+itoa(int32(x))+" -60 -62 "+itoa(int32(x))+" -60 -48 minecraft:birch_sapling")
	}
	for x := -1; x <= 19; x += 2 {
		sendCmd(t, pw, "fill "+itoa(int32(x))+" -60 -62 "+itoa(int32(x))+" -60 -48 minecraft:dandelion")
	}

	// Control pad: identical sapling columns, no flowers — clear of
	// the flowered boxes' +-2 reach.
	sendCmd(t, pw, "fill -2 -61 -42 20 -61 -28 minecraft:dirt")
	for x := 0; x <= 18; x += 2 {
		sendCmd(t, pw, "fill "+itoa(int32(x))+" -60 -42 "+itoa(int32(x))+" -60 -28 minecraft:birch_sapling")
	}

	// The four BEES_005 configured features place like their base
	// trees.
	sendCmd(t, pw, "place feature minecraft:oak_bees_005 40 -60 -20")
	sendCmd(t, pw, "place feature minecraft:birch_bees_005 55 -60 -20")
	sendCmd(t, pw, "place feature minecraft:cherry_bees_005 70 -60 -20")
	sendCmd(t, pw, "place feature minecraft:fancy_oak_bees_005 85 -60 -20")
	waitDrained(t, pw, sink)

	sprintTicksLoose(t, pw, sink, 300)

	var out beehiveRun
	// Nests before logs: removing a log cell pops nothing a nest
	// needs, but keep the decoration-first order anyway.
	out.flowerNests = countFill(t, pw, sink, "minecraft:bee_nest",
		-2, -61, -62, 20, -40, -48)
	out.plainNests = countFill(t, pw, sink, "minecraft:bee_nest",
		-2, -61, -42, 20, -40, -28)
	out.flowerLogs = countFill(t, pw, sink, "minecraft:birch_log",
		-2, -61, -62, 20, -40, -48)
	out.plainLogs = countFill(t, pw, sink, "minecraft:birch_log",
		-2, -61, -42, 20, -40, -28)
	out.placedLogs = countFill(t, pw, sink, "minecraft:oak_log",
		30, -61, -30, 95, -30, -10)
	out.placedLogs += countFill(t, pw, sink, "minecraft:birch_log",
		30, -61, -30, 95, -30, -10)
	out.placedLogs += countFill(t, pw, sink, "minecraft:cherry_log",
		30, -61, -30, 95, -30, -10)
	// Fail markers — /place reports its result on the command stream.
	for _, l := range sink.take() {
		if strings.Contains(l, "Failed to place feature") {
			t.Logf("place feature failed: %s", l)
		}
	}
	return out
}
