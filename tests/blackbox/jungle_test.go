package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestJungleParity grows jungle trees on both servers and compares
// the parts that are not luck: trunk height inside the measured 4-12,
// the guaranteed non-corner canopy leaf, the rooted dirt — and the
// ABSENCE of vines: a lone jungle sapling grows jungle_tree_no_vine
// (decorators: []), not the worldgen-only jungle_tree.
//
// Measured on 26.2: TreeGrower.JUNGLE maps the 1x1 case to
// TreeFeatures.JUNGLE_TREE_NO_VINE (the vine'd jungle_tree — cocoa
// 0.2, trunk_vine, leave_vine 0.25 — is reached only by biome
// generation; the 2x2 mega_jungle_tree gets trunk_vine + leave_vine).
// jungle_tree_no_vine: StraightTrunkPlacer {base 4, randA 8, randB 0}
// → 4-12 logs; BlobFoliagePlacer {radius 2, offset 0, height 3} — the
// same blob the small oak and birch share; no decorators, so a grown
// tree's trunk ring stays vine-free.
func TestJungleParity(t *testing.T) {
	const probe = "JungleParity"
	tg := requireBlackboxFiles(t,
		map[string]string{
			"command-spam-threshold-seconds": "0",
			"simulation-distance":            "3",
		},
		map[string]string{"ops.json": opsJSONFor(probe)},
		func(s *server.Server) { s.SimulationDistance = 3 })
	defer tg.cleanup()
	defer func() {
		if t.Failed() {
			vanillaLogTail(t)
		}
	}()

	want := runJungleScenarios(t, tg.vanilla, probe)
	got := runJungleScenarios(t, tg.mcgo, probe)

	if !sameNames(want.names, got.names) {
		t.Errorf("trunk zone: mc-go and vanilla disagree\n%s\nmc-go:\n%s",
			renderNameDiff(want.names, got.names), renderNames(got.names))
	}
	for i := 0; i < jungleCount; i++ {
		if want.heights[i] < 4 || want.heights[i] > 12 {
			t.Errorf("jungle %d: vanilla grew a %d-log trunk, outside the measured 4-12", i, want.heights[i])
		}
		if got.heights[i] < 4 || got.heights[i] > 12 {
			t.Errorf("jungle %d: mc-go grew a %d-log trunk, outside the measured 4-12", i, got.heights[i])
		}
		if want.heights[i] >= 4 && !want.leafOK[i] {
			t.Errorf("jungle %d: vanilla has no jungle_leaves beside the trunk top", i)
		}
		if got.heights[i] >= 4 && !got.leafOK[i] {
			t.Errorf("jungle %d: mc-go has no jungle_leaves beside the trunk top", i)
		}
		// jungle_tree_no_vine has no decorators — a grown ring is
		// vine-free on both legs.
		if want.vines[i] != 0 {
			t.Errorf("jungle %d: vanilla trunk ring has %d vines, want 0", i, want.vines[i])
		}
		if got.vines[i] != 0 {
			t.Errorf("jungle %d: mc-go trunk ring has %d vines, want 0", i, got.vines[i])
		}
	}
}

const jungleCount = 3

type jungleRun struct {
	names   map[[3]int]string
	heights [jungleCount]int
	leafOK  [jungleCount]bool
	vines   [jungleCount]int
}

func runJungleScenarios(t *testing.T, addr, name string) jungleRun {
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
	waitDrained(t, pw, sink)

	// Three jungle 10 apart — the canopy is the oak/blob +-4.
	baseY := -60
	xs := [jungleCount]int{4, 14, 24}
	for _, x := range xs {
		sendCmd(t, pw, "setblock "+itoa(int32(x))+" -61 -44 minecraft:dirt")
		sendCmd(t, pw, "setblock "+itoa(int32(x))+" "+itoa(int32(baseY))+" -44 minecraft:jungle_sapling")
	}
	waitDrained(t, pw, sink)

	sprintTicksLoose(t, pw, sink, 300)

	out := jungleRun{names: map[[3]int]string{}}

	candidates := []string{
		"minecraft:jungle_sapling", "minecraft:jungle_log", "minecraft:jungle_leaves",
		"minecraft:dirt", "minecraft:vine", "minecraft:cocoa", "minecraft:air",
	}
	var positions [][3]int
	for _, x := range xs {
		for y := baseY - 1; y <= baseY+3; y++ {
			positions = append(positions, [3]int{x, y, -44})
		}
	}
	out.names = queryNames(t, pw, sink, candidates, positions)

	// Per-leg: contiguous trunk height and the guaranteed canopy leaf.
	for i, x := range xs {
		h := 0
		for y := baseY; y <= baseY+12; y++ {
			more := queryNames(t, pw, sink, candidates, [][3]int{{x, y, -44}})
			if more[[3]int{x, y, -44}] != "minecraft:jungle_log" {
				break
			}
			h++
		}
		out.heights[i] = h
		if h >= 4 {
			leaf := queryNames(t, pw, sink, candidates,
				[][3]int{{x + 1, baseY + h - 1, -44}})
			out.leafOK[i] = leaf[[3]int{x + 1, baseY + h - 1, -44}] == "minecraft:jungle_leaves"
		}
	}

	// Vine scan: the eight ring columns around the trunk for the whole
	// trunk span plus the canopy apron — trunk vines land beside every
	// log and hanging vines descend through these columns too.
	for i, x := range xs {
		var ring [][3]int
		for _, off := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}, {1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
			for y := baseY; y <= baseY+13; y++ {
				ring = append(ring, [3]int{x + off[0], y, -44 + off[1]})
			}
		}
		names := queryNames(t, pw, sink, []string{"minecraft:vine"}, ring)
		for _, v := range names {
			if v == "minecraft:vine" {
				out.vines[i]++
			}
		}
	}
	return out
}
