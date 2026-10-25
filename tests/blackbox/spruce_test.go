package blackbox

import (
	"testing"
	"time"

	"mc-go/internal/server"
)

// TestSpruceParity grows spruce trees on both servers and compares the
// parts that are not luck: trunk height inside the measured 5-8, the
// guaranteed non-corner leaf cells of the zigzag canopy, the dirt the
// feature roots under the trunk, and the 5x5 space check that keeps a
// crowded spruce a stage-1 sapling forever.
//
// Vanilla's own randomness rules out a cell-by-cell canopy compare —
// the spire's per-layer radius starts at nextInt(2) and its offset is
// U(0..2) — so the pins split: shared cells (trunk column, sapling
// cells, dirt, stones) go in the names map and compare exactly, while
// height and the always-leaf cells are asserted per leg.
//
// Measured on 26.2: spruce.json configured_feature —
// StraightTrunkPlacer {base 5, randA 2, randB 1} → 5-8 logs;
// SpruceFoliagePlacer {radius U(2..3), offset U(0..2), trunk_height
// U(1..2)}: foliageHeight = max(4, treeHeight - U(1..2)), rows run
// yo = offset..-foliageHeight with the zigzag radius (count up while
// under maxRadius, then reset to the running minimum as maxRadius
// grows toward the sampled leafRadius) and corners skipped whenever
// radius > 0; two_layers_feature_size {limit 2, upper 2} → trunk
// column free below y=2, the whole 5x5 at y>=2.
//
// The guaranteed-leaf cell: four steps below the attachment (y =
// posY + treeHeight - 3) every (cr0, offset, leafRadius) combination
// lands a radius >= 1 row there, and (x±1, z) are never corners.
func TestSpruceParity(t *testing.T) {
	const probe = "SpruceParity"
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

	want := runSpruceScenarios(t, tg.vanilla, probe)
	got := runSpruceScenarios(t, tg.mcgo, probe)

	if !sameNames(want.names, got.names) {
		t.Errorf("trunk zone and blocked sapling: mc-go and vanilla disagree\n%s\nmc-go:\n%s",
			renderNameDiff(want.names, got.names), renderNames(got.names))
	}
	for i := 0; i < spruceCount; i++ {
		if want.heights[i] < 5 || want.heights[i] > 8 {
			t.Errorf("spruce %d: vanilla grew a %d-log trunk, outside the measured 5-8", i, want.heights[i])
		}
		if got.heights[i] < 5 || got.heights[i] > 8 {
			t.Errorf("spruce %d: mc-go grew a %d-log trunk, outside the measured 5-8", i, got.heights[i])
		}
		if want.heights[i] >= 5 && !want.leafOK[i] {
			t.Errorf("spruce %d: vanilla has no spruce_leaves beside the canopy band", i)
		}
		if got.heights[i] >= 5 && !got.leafOK[i] {
			t.Errorf("spruce %d: mc-go has no spruce_leaves beside the canopy band", i)
		}
	}
}

const spruceCount = 4

// spruceRun is what one server grew: the shared cells for the name
// comparison, and the per-spruce measurements the invariants need.
type spruceRun struct {
	names   map[[3]int]string
	heights [spruceCount]int
	leafOK  [spruceCount]bool
}

func runSpruceScenarios(t *testing.T, addr, name string) spruceRun {
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

	// Four spruce 8 apart (the canopy reaches +-3; the 5x5 space check
	// needs +-2 empty at y>=2), plus a crowded spruce whose (x+2,y+2)
	// stone sits inside its 5x5 — it never grows, only reaches stage 1.
	baseY := -60
	xs := [spruceCount]int{4, 12, 20, 28}
	for _, x := range xs {
		sendCmd(t, pw, "setblock "+itoa(int32(x))+" -61 -40 minecraft:dirt")
		sendCmd(t, pw, "setblock "+itoa(int32(x))+" "+itoa(int32(baseY))+" -40 minecraft:spruce_sapling")
	}
	sendCmd(t, pw, "setblock 4 -61 -44 minecraft:dirt")
	sendCmd(t, pw, "setblock 4 "+itoa(int32(baseY))+" -44 minecraft:spruce_sapling")
	sendCmd(t, pw, "setblock 6 "+itoa(int32(baseY+2))+" -44 minecraft:stone")
	waitDrained(t, pw, sink)

	sprintTicksLoose(t, pw, sink, 300)

	out := spruceRun{names: map[[3]int]string{}}

	candidates := []string{
		"minecraft:spruce_sapling", "minecraft:spruce_log", "minecraft:spruce_leaves",
		"minecraft:dirt", "minecraft:stone", "minecraft:air",
	}
	var positions [][3]int
	for _, x := range xs {
		for y := baseY - 1; y <= baseY+4; y++ {
			positions = append(positions, [3]int{x, y, -40})
		}
	}
	positions = append(positions,
		[3]int{4, baseY, -44},     // the crowded spruce cell
		[3]int{4, baseY - 1, -44}, // its dirt
		[3]int{6, baseY + 2, -44}) // the blocking stone
	out.names = queryNames(t, pw, sink, candidates, positions)

	for i, x := range xs {
		h := 0
		for y := baseY; y <= baseY+8; y++ {
			cell := out.names[[3]int{x, y, -40}]
			if y > baseY+4 || cell != "minecraft:spruce_log" {
				more := queryNames(t, pw, sink, candidates,
					[][3]int{{x, y, -40}})
				if more[[3]int{x, y, -40}] != "minecraft:spruce_log" {
					break
				}
			}
			h++
		}
		out.heights[i] = h
		if h >= 5 {
			// posY + h - 3: four below the attachment — every
			// radius path lands a >=1 row here, and x+1 is a
			// non-corner cell.
			leaf := queryNames(t, pw, sink, candidates,
				[][3]int{{x + 1, baseY + h - 3, -40}})
			out.leafOK[i] = leaf[[3]int{x + 1, baseY + h - 3, -40}] == "minecraft:spruce_leaves"
		}
	}
	return out
}
