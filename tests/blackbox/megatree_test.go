package blackbox

import (
	"strings"
	"testing"
	"time"
)

// TestMegaTreeParity places the mega tree features on both servers
// and compares the parts that are not luck: mega_spruce/mega_pine's
// bare 2x2 giant trunk with a single-cell top layer, the podzol
// alter_ground under grass, the lower_size=1 ground check, mega
// jungle's vines, pale_oak's moss hangers via the placed feature
// and its clean bonemeal variant via a 2x2 sapling quad, and the
// spruce/jungle quad paths.
//
// Measured on 26.2: GiantTrunkPlacer {13,2,14} is a bare 2x2 whose
// top layer collapses to the column cell; MegaJungleTrunkPlacer
// {10,2,19} adds angle-branches at radiusOffset -2. mega_pine
// foliage {crown U{13..17} spruce / U{3..7} pine} and mega_jungle
// foliage {r2, off0, h2} are circular rows with a signed dx+dz>=7
// cut. mega_jungle gets trunk_vine + leave_vine{.25}; the spruces
// get podzol circles beneath_tree_podzol_replaceable; pale_oak the
// sapling path is pale_oak_bonemeal (no decorators) while /place
// pale_oak adds pale_moss hangers {trunk .4, leaves .15}.
// TreeGrower: spruce quad rolls mega_pine at 50%; jungle quad takes
// mega_jungle_tree; pale_oak has only a mega feature.
func TestMegaTreeParity(t *testing.T) {
	const probe = "MegaTreeParity"
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

	want := runMegaTreeScenarios(t, tg.vanilla, probe)
	got := runMegaTreeScenarios(t, tg.mcgo, probe)

	if !sameNames(want.names, got.names) {
		t.Errorf("probe cells: mc-go and vanilla disagree\n%s\nmc-go:\n%s",
			renderNameDiff(want.names, got.names), renderNames(got.names))
	}
	for name, w := range map[string][2]int{
		"spruceBlocked": {boolToInt(want.spruceBlocked), boolToInt(got.spruceBlocked)},
		"jungleBlocked": {boolToInt(want.jungleBlocked), boolToInt(got.jungleBlocked)},
		"paleBesideOK":  {boolToInt(want.paleBesideOK), boolToInt(got.paleBesideOK)},
		"spruceQuad":    {boolToInt(want.spruceQuad), boolToInt(got.spruceQuad)},
		"jungleQuad":    {boolToInt(want.jungleQuad), boolToInt(got.jungleQuad)},
		"paleQuad":      {boolToInt(want.paleQuad), boolToInt(got.paleQuad)},
	} {
		if w[0] != w[1] {
			t.Errorf("%s: vanilla=%v mc-go=%v", name, w[0] == 1, w[1] == 1)
		}
	}
	for i := 0; i < megaCount; i++ {
		if want.spruceLogs[i] < 40 {
			t.Errorf("mega spruce %d: vanilla has only %d spruce_log", i, want.spruceLogs[i])
		}
		if got.spruceLogs[i] < 40 {
			t.Errorf("mega spruce %d: mc-go has only %d spruce_log", i, got.spruceLogs[i])
		}
		if want.spruceLeaves[i] < 25 {
			t.Errorf("mega spruce %d: vanilla has only %d spruce_leaves", i, want.spruceLeaves[i])
		}
		if got.spruceLeaves[i] < 25 {
			t.Errorf("mega spruce %d: mc-go has only %d spruce_leaves", i, got.spruceLeaves[i])
		}
	}
	// Podzol under the grass pad: the four fixed circles guarantee
	// converted substrate cells.
	if want.podzol < 4 {
		t.Errorf("vanilla converted only %d podzol cells under the pad", want.podzol)
	}
	if got.podzol < 4 {
		t.Errorf("mc-go converted only %d podzol cells under the pad", got.podzol)
	}
	// Mega jungle: tall 2x2 + vines somewhere near the tree.
	if want.jungleLogs < 30 || got.jungleLogs < 30 {
		t.Errorf("mega jungle trunk: vanilla=%d mc-go=%d logs (want >=30)", want.jungleLogs, got.jungleLogs)
	}
	if want.jungleLeaves < 80 || got.jungleLeaves < 80 {
		t.Errorf("mega jungle canopy: vanilla=%d mc-go=%d leaves (want >=80)", want.jungleLeaves, got.jungleLeaves)
	}
	if want.vines < 5 || got.vines < 5 {
		t.Errorf("mega jungle vines: vanilla=%d mc-go=%d (want >=5)", want.vines, got.vines)
	}
	// Pale oak placed feature: moss hangers under the canopy.
	if want.moss < 3 || got.moss < 3 {
		t.Errorf("pale_oak hangers: vanilla=%d mc-go=%d (want >=3)", want.moss, got.moss)
	}
	// pale_oak_creaking over the pad strip: ground patch writes
	// pale_moss_block + vegetation; the heart decorator (p=1.0) picks
	// a log whose six neighbours are all logs when one exists — about
	// a third of trees have such a cell, so the floor is over a batch.
	if want.mossBlock < 5 || got.mossBlock < 5 {
		t.Errorf("pale_oak_creaking moss patch: vanilla=%d mc-go=%d (want >=5)", want.mossBlock, got.mossBlock)
	}
	if want.vegetation < 1 || got.vegetation < 1 {
		t.Errorf("pale_oak_creaking vegetation: vanilla=%d mc-go=%d (want >=1)", want.vegetation, got.vegetation)
	}
	t.Logf("creaking: heart v=%d g=%d, mossBlock v=%d g=%d, veg v=%d g=%d",
		want.heart, got.heart, want.mossBlock, got.mossBlock, want.vegetation, got.vegetation)
	if want.heart < 1 {
		t.Errorf("vanilla placed no creaking heart over %d trees", creakingTrees)
	}
	if got.heart < 1 {
		t.Errorf("mc-go placed no creaking heart over %d trees", creakingTrees)
	}
	if want.mossOnBonemeal != got.mossOnBonemeal {
		t.Errorf("bonemeal quad moss: vanilla saw=%v mc-go saw=%v", want.mossOnBonemeal, got.mossOnBonemeal)
	}
}

const megaCount = 3

// creakingTrees is the pale_oak_creaking batch size — the heart lands
// on ~a third of trees, so the >=1 floor needs a batch, not one.
const creakingTrees = 16

type megaTreeRun struct {
	names          map[[3]int]string
	spruceLogs     [megaCount]int
	spruceLeaves   [megaCount]int
	podzol         int
	jungleLogs     int
	jungleLeaves   int
	vines          int
	moss           int
	mossBlock      int
	vegetation     int
	heart          int
	mossOnBonemeal bool
	spruceBlocked  bool
	jungleBlocked  bool
	paleBesideOK   bool
	spruceQuad     bool
	jungleQuad     bool
	paleQuad       bool
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func runMegaTreeScenarios(t *testing.T, addr, name string) megaTreeRun {
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
	out := megaTreeRun{names: map[[3]int]string{}}
	xs := [megaCount]int{4, 18, 32}

	// Grass pad under the first tree's site so alter_ground has
	// substrate to convert.
	for x := 0; x <= 9; x++ {
		for z := -50; z <= -41; z++ {
			sendCmd(t, pw, "setblock "+itoa(int32(x))+" "+itoa(int32(baseY-1))+" "+itoa(int32(z))+" minecraft:grass_block")
		}
	}
	sink.take()
	for _, x := range xs {
		sendCmd(t, pw, "place feature minecraft:mega_spruce "+
			itoa(int32(x))+" "+itoa(int32(baseY))+" -44")
	}
	sendCmd(t, pw, "place feature minecraft:mega_jungle_tree 48 "+
		itoa(int32(baseY))+" -44")
	sendCmd(t, pw, "place feature minecraft:pale_oak 62 "+
		itoa(int32(baseY))+" -44")
	waitDrained(t, pw, sink)

	// Strip pad for pale_oak_creaking's ground patch + heart batch:
	// two rows of eight, spread along x and z.
	for x := 80; x <= 112; x++ {
		for z := -50; z <= -38; z++ {
			sendCmd(t, pw, "setblock "+itoa(int32(x))+" "+itoa(int32(baseY-1))+" "+itoa(int32(z))+" minecraft:grass_block")
		}
	}
	sink.take()
	for i := 0; i < creakingTrees; i++ {
		x := 82 + (i%8)*4
		z := -44
		if i >= 8 {
			z = -38
		}
		sendCmd(t, pw, "place feature minecraft:pale_oak_creaking "+
			itoa(int32(x))+" "+itoa(int32(baseY))+" "+itoa(int32(z)))
	}
	waitDrained(t, pw, sink)

	// two_layers {lower_size 1}: a stone adjacent at y=0 sits inside
	// the 3x3 ground check — the feature fails.
	sendCmd(t, pw, "setblock -46 "+itoa(int32(baseY))+" -44 minecraft:stone")
	sink.take()
	sendCmd(t, pw, "place feature minecraft:mega_spruce -45 "+itoa(int32(baseY))+" -44")
	lines := collectUntil(sink, "place.feature", 10*time.Second)
	out.spruceBlocked = sawMarker(lines, "place.feature.failed") ||
		sawMarker(lines, "Failed to place feature")
	sendCmd(t, pw, "setblock -61 "+itoa(int32(baseY))+" -44 minecraft:stone")
	sink.take()
	sendCmd(t, pw, "place feature minecraft:mega_jungle_tree -60 "+itoa(int32(baseY))+" -44")
	lines = collectUntil(sink, "place.feature", 10*time.Second)
	out.jungleBlocked = sawMarker(lines, "place.feature.failed") ||
		sawMarker(lines, "Failed to place feature")

	// Pale oak's three_layers column-only y=0: a stone beside the
	// trunk at ground level doesn't block.
	sendCmd(t, pw, "setblock -73 "+itoa(int32(baseY))+" -44 minecraft:stone")
	sink.take()
	sendCmd(t, pw, "place feature minecraft:pale_oak -75 "+itoa(int32(baseY))+" -44")
	lines = collectUntil(sink, "place.feature", 10*time.Second)
	out.paleBesideOK = sawMarker(lines, "place.feature.success") ||
		sawMarker(lines, "Placed")
	waitDrained(t, pw, sink)

	// Sapling quads: spruce → 50% mega_pine/mega_spruce, jungle →
	// mega_jungle_tree, pale_oak → pale_oak_bonemeal (no moss).
	quads := []struct {
		x0, z0 int
		sap    string
	}{
		{-45, -56, "spruce_sapling"},
		{-60, -56, "jungle_sapling"},
		{-75, -56, "pale_oak_sapling"},
	}
	for _, q := range quads {
		for _, off := range [][2]int{{0, 0}, {1, 0}, {0, 1}, {1, 1}} {
			sendCmd(t, pw, "setblock "+itoa(int32(q.x0+off[0]))+" "+
				itoa(int32(baseY))+" "+itoa(int32(q.z0+off[1]))+" minecraft:"+q.sap)
		}
	}
	waitDrained(t, pw, sink)
	sendCommand(t, pw, "say MEGAWAIT")
	_ = waitForMarker(t, sink, "MEGAWAIT", 60*time.Second)

	out.names = scanNames(t, pw, sink,
		[]string{"minecraft:spruce_log", "minecraft:spruce_leaves",
			"minecraft:jungle_log", "minecraft:jungle_leaves", "minecraft:vine",
			"minecraft:pale_oak_log", "minecraft:pale_oak_leaves",
			"minecraft:pale_hanging_moss", "minecraft:podzol",
			"minecraft:dirt", "minecraft:stone", "minecraft:air",
			"minecraft:spruce_sapling", "minecraft:jungle_sapling",
			"minecraft:pale_oak_sapling"}, [][3]int{
			{4, baseY, -44},
			{5, baseY, -43},
			{4, baseY - 1, -44}, // below the pad — dirt or podzol, never grass after alter_ground+dirt
			{-45, baseY, -44},   // spruceBlocked: air
			{-46, baseY, -44},   // the stone, untouched
			{-60, baseY, -44},   // jungleBlocked: air
			{-75, baseY, -44},   // pale beside stone: log
			{-45, baseY, -56},   // spruce quad NW: spruce_log
			{-60, baseY, -56},   // jungle quad NW: jungle_log
			{-75, baseY, -56},   // pale quad NW: pale_oak_log
		})
	out.spruceQuad = strings.Contains(out.names[[3]int{-45, baseY, -56}], "spruce_log")
	out.jungleQuad = strings.Contains(out.names[[3]int{-60, baseY, -56}], "jungle_log")
	out.paleQuad = strings.Contains(out.names[[3]int{-75, baseY, -56}], "pale_oak_log")

	// Grove scans: giant trunks rise up to 31 cells with crowns and
	// branches around them; the pale oak bonemeal quad zone is
	// scanned for stray pale_hanging_moss too.
	for i, x := range xs {
		var box [][3]int
		for dx := -7; dx <= 8; dx++ {
			for dz := -7; dz <= 8; dz++ {
				for y := baseY - 1; y <= baseY+34; y++ {
					box = append(box, [3]int{x + dx, y, -44 + dz})
				}
			}
		}
		names := scanNames(t, pw, sink, []string{
			"minecraft:spruce_log", "minecraft:spruce_leaves",
			"minecraft:podzol",
		}, box)
		for _, v := range names {
			if strings.Contains(v, "spruce_log") {
				out.spruceLogs[i]++
			}
			if strings.Contains(v, "spruce_leaves") {
				out.spruceLeaves[i]++
			}
			if strings.Contains(v, "podzol") {
				out.podzol++
			}
		}
	}
	{
		var box [][3]int
		for dx := -7; dx <= 8; dx++ {
			for dz := -7; dz <= 8; dz++ {
				for y := baseY - 1; y <= baseY+34; y++ {
					box = append(box, [3]int{48 + dx, y, -44 + dz})
				}
			}
		}
		names := scanNames(t, pw, sink, []string{
			"minecraft:jungle_log", "minecraft:jungle_leaves", "minecraft:vine",
		}, box)
		for _, v := range names {
			if strings.Contains(v, "jungle_log") {
				out.jungleLogs++
			}
			if strings.Contains(v, "jungle_leaves") {
				out.jungleLeaves++
			}
			if strings.Contains(v, "vine") {
				out.vines++
			}
		}
	}
	{
		var box [][3]int
		for dx := -7; dx <= 8; dx++ {
			for dz := -7; dz <= 8; dz++ {
				for y := baseY - 1; y <= baseY+12; y++ {
					box = append(box, [3]int{62 + dx, y, -44 + dz})
				}
			}
		}
		names := scanNames(t, pw, sink, []string{
			"minecraft:pale_hanging_moss",
		}, box)
		for _, v := range names {
			if strings.Contains(v, "pale_hanging_moss") {
				out.moss++
			}
		}
	}
	{
		// pale_oak_creaking strip: heart, ground-patch moss, vegetation.
		var box [][3]int
		for x := 78; x <= 116; x++ {
			for z := -52; z <= -34; z++ {
				for y := baseY - 1; y <= baseY+12; y++ {
					box = append(box, [3]int{x, y, z})
				}
			}
		}
		names := scanNames(t, pw, sink, []string{
			"minecraft:creaking_heart", "minecraft:pale_moss_block",
			"minecraft:pale_moss_carpet", "minecraft:short_grass",
			"minecraft:tall_grass",
		}, box)
		for _, v := range names {
			if strings.Contains(v, "creaking_heart") {
				out.heart++
			}
			if strings.Contains(v, "pale_moss_block") {
				out.mossBlock++
			}
			if strings.Contains(v, "pale_moss_carpet") ||
				strings.Contains(v, "short_grass") ||
				strings.Contains(v, "tall_grass") {
				out.vegetation++
			}
		}
	}
	{
		// The bonemeal-quad zone: pale_hanging_moss must not appear —
		// pale_oak_bonemeal has no decorators.
		var box [][3]int
		for dx := -7; dx <= 8; dx++ {
			for dz := -7; dz <= 8; dz++ {
				for y := baseY - 1; y <= baseY+12; y++ {
					box = append(box, [3]int{-75 + dx, y, -56 + dz})
				}
			}
		}
		names := scanNames(t, pw, sink, []string{
			"minecraft:pale_hanging_moss",
		}, box)
		out.mossOnBonemeal = len(names) > 0
	}
	return out
}
