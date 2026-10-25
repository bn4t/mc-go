package blackbox

import (
	"fmt"
	"testing"
	"time"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// TestSaplingParity grows trees on both servers and compares the parts
// of them that are not luck: the guaranteed trunk zone, the hidden
// stage counter, and each canopy's always-leaf cells.
//
// Vanilla's own randomness rules out comparing a grown tree cell by
// cell — a birch's height is 5-7 logs and every canopy corner is a coin
// flip — so the test splits the pins: cells every birch shares go in
// the names map and compare exactly, while height and the leaf cell a
// measured height guarantees are asserted per leg.
//
// Measured on 26.2: SaplingBlock.randomTick (brightness >= 9 above,
// 1/7 roll), advanceTree (stage 0 -> 1, then growTree), TreeGrower
// (birch always picks Trees.BIRCH — no secondary roll), TreeFeature
// (StraightTrunkPlacer 5+intn(3)+intn(1) = 5-7, BlobFoliagePlacer
// radius 2 height 3 layers {1,1,2,2} with the top layer's corners
// always skipped and the rest 50/50), and two_layers_feature_size
// defaults (trunk column free everywhere, 3x3 in the top two layers).
func TestSaplingParity(t *testing.T) {
	const probe = "SaplingParity"
	// A 600-speed sprint spends most of its per-chunk budget on the
	// ice-and-snow preamble (one 1/48 roll per speed unit), so the
	// simulated radius stays small — the saplings sit two chunks from
	// spawn, well inside three.
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

	want := runSaplingScenarios(t, tg.vanilla, probe)
	got := runSaplingScenarios(t, tg.mcgo, probe)

	if !sameNames(want.names, got.names) {
		t.Errorf("trunk zone and stage cell: mc-go and vanilla disagree\n%s\nmc-go:\n%s",
			renderNameDiff(want.names, got.names), renderNames(got.names))
	}
	for i := 0; i < saplingCount; i++ {
		if want.heights[i] < 5 || want.heights[i] > 7 {
			t.Errorf("sapling %d: vanilla grew a %d-log trunk, outside the measured 5-7", i, want.heights[i])
		}
		if got.heights[i] < 5 || got.heights[i] > 7 {
			t.Errorf("sapling %d: mc-go grew a %d-log trunk, outside the measured 5-7", i, got.heights[i])
		}
		if !want.leafOK[i] {
			t.Errorf("sapling %d: vanilla has no birch_leaves east of the trunk top", i)
		}
		if !got.leafOK[i] {
			t.Errorf("sapling %d: mc-go has no birch_leaves east of the trunk top", i)
		}
	}
}

const saplingCount = 4

// saplingRun is what one server grew: the shared cells for the name
// comparison, and the per-sapling measurements the invariants need.
type saplingRun struct {
	names   map[[3]int]string
	heights [saplingCount]int
	leafOK  [saplingCount]bool
}

// runSaplingScenarios plants the row, fast-forwards the random ticks,
// and reads back both the shared pins and the per-sapling shape.
func runSaplingScenarios(t *testing.T, addr, name string) saplingRun {
	t.Helper()
	c, pr, pw := joinPlayBlackbox(t, addr, name)
	defer c.Close()
	_ = c.SetDeadline(time.Time{})
	sink := newChatSink(pr, pw)
	time.Sleep(3 * time.Second)
	sink.take()

	// Daylight open sky is the bright case the light gate needs. Every
	// setup command is paced — the throttle on either server silently
	// eats an unpaced burst, which is exactly what a missing game-time
	// reply looks like.
	sendCmd(t, pw, "time set day")
	sendCmd(t, pw, "gamerule random_tick_speed 600")
	sendCmd(t, pw, "tick freeze")
	waitDrained(t, pw, sink)

	// Four birch on the flat area (grown canopy ~4 cells wide; 4 apart
	// keeps canopies from sharing cells), and a stage-pinned oak whose
	// trunk column is blocked at +2 — vanilla clips the whole feature
	// there, so the sapling only ever reaches stage 1.
	baseY := -60
	xs := [saplingCount]int{4, 8, 12, 16}
	for _, x := range xs {
		sendCmd(t, pw, "setblock "+itoa(int32(x))+" -61 -16 minecraft:dirt")
		sendCmd(t, pw, "setblock "+itoa(int32(x))+" "+itoa(int32(baseY))+" -16 minecraft:birch_sapling")
	}
	sendCmd(t, pw, "setblock 4 -61 -20 minecraft:dirt")
	sendCmd(t, pw, "setblock 4 "+itoa(int32(baseY))+" -20 minecraft:oak_sapling")
	sendCmd(t, pw, "setblock 4 "+itoa(int32(baseY+2))+" -20 minecraft:stone")
	waitDrained(t, pw, sink)

	// 300 ticks at 600 picks per section is ~44 hits per sapling, far
	// past the ~14 the two 1/7 advances need.
	sprintTicksLoose(t, pw, sink, 300)

	out := saplingRun{names: map[[3]int]string{}}

	// The names comparison covers the cells every grown birch shares:
	// the sapling cell plus the four trunk cells above it (the shortest
	// birch is 5 logs) and the ceilinged oak — which must still be a
	// stage-1 sapling on both servers.
	candidates := []string{
		"minecraft:birch_sapling", "minecraft:birch_log", "minecraft:birch_leaves",
		"minecraft:oak_sapling[stage=0]", "minecraft:oak_sapling[stage=1]",
		"minecraft:oak_log", "minecraft:oak_leaves", "minecraft:stone", "minecraft:air",
	}
	var positions [][3]int
	for _, x := range xs {
		for y := baseY; y <= baseY+4; y++ {
			positions = append(positions, [3]int{x, y, -16})
		}
	}
	positions = append(positions, [3]int{4, baseY, -20})
	out.names = queryNames(t, pw, sink, candidates, positions)

	// Per-sapling height is the contiguous birch_log column; for the
	// measured height the cell east of the top log is a non-corner cell
	// of the top foliage layer and always a leaf.
	for i, x := range xs {
		h := 0
		for y := baseY; y <= baseY+8; y++ {
			if out.names[[3]int{x, y, -16}] == "minecraft:birch_log" {
				h++
			} else {
				break
			}
		}
		if h > 0 && h <= 5 {
			// The upper trunk cells were not all probed — query the rest
			// of the column for the real height.
			more := queryNames(t, pw, sink, candidates,
				[][3]int{{x, baseY + 5, -16}, {x, baseY + 6, -16}, {x, baseY + 7, -16}})
			for y := baseY + 5; y <= baseY+7; y++ {
				if more[[3]int{x, y, -16}] == "minecraft:birch_log" {
					h++
				} else {
					break
				}
			}
		}
		out.heights[i] = h
		if h >= 5 {
			leaf := queryNames(t, pw, sink, candidates,
				[][3]int{{x + 1, baseY + h - 1, -16}})
			out.leafOK[i] = leaf[[3]int{x + 1, baseY + h - 1, -16}] == "minecraft:birch_leaves"
		}
	}
	return out
}

// sprintTicksLoose is sprintTicks with a patient poll: mc-go's shard
// reads go through a channel per GetBlock, so a 600-speed tick costs
// ~250ms and the shared helper's ten-second gametime poll expires
// mid-sprint — the query just queues behind the synchronous run. The
// protocol is identical; only the wait is longer.
func sprintTicksLoose(t *testing.T, pw *protocol.Writer, sink *chatSink, n int) {
	t.Helper()
	before := gameTimeOf(t, pw, sink)
	sendCommand(t, pw, fmt.Sprintf("tick sprint %d", n))
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		sendCommand(t, pw, "time query gametime")
		pollEnd := time.Now().Add(30 * time.Second)
		for time.Now().Before(pollEnd) {
			for _, line := range sink.take() {
				key, args, ok := componentArgs(line)
				if !ok || key != "commands.time.query.gametime" || len(args) == 0 {
					continue
				}
				if v, ok := args[0].(int64); ok && v >= before+int64(n) {
					return
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	t.Fatalf("the server never simulated %d ticks", n)
}
