package server

import (
	"testing"

	"mc-go/internal/world"
)

// fluidSim builds a server with no sessions and a flat world, which is
// enough to drive the fluid engine deterministically: GameTick advances
// the scheduled ticks, and the world can be read back directly.
func fluidSim(t *testing.T) *Server {
	t.Helper()
	s := New("127.0.0.1:0")
	t.Cleanup(func() { s.world.Stop() })
	s.SetFrozen(true)
	return s
}

// set places a block and lets the fluid engine notice it.
func (s *Server) set(d *dimension, p world.Pos, b world.Block) {
	s.setNaturalBlock(d, p, b)
	s.wakeFluids(d, p)
}

// water builds a water state at a level.
func water(t *testing.T, level int) world.Block {
	t.Helper()
	b, ok := world.BlockStateOf("water", map[string]string{"level": itoa(level)})
	if !ok {
		t.Fatalf("water[level=%d] is not a state", level)
	}
	return b
}

func lava(t *testing.T, level int) world.Block {
	t.Helper()
	b, ok := world.BlockStateOf("lava", map[string]string{"level": itoa(level)})
	if !ok {
		t.Fatalf("lava[level=%d] is not a state", level)
	}
	return b
}

func itoa(n int) string {
	return string(rune('0' + n))
}

// iabs is an integer absolute value; the package's abs is float-only.
func iabs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// levelAt reads a fluid's level, or -1 when the block is not that fluid.
func levelAt(d *dimension, kind fluidKind, p world.Pos) int {
	k, lvl, ok := fluidOf(d.w.GetBlock(p))
	if !ok || k != kind {
		return -1
	}
	return lvl
}

// TestWaterSpreadOnFlatGround pins the shape a lone source makes when it
// has nowhere to fall: a diamond whose level is the Manhattan distance,
// stopping at seven.
//
// The expected numbers are the ones the real server produced in
// TestFluidParity, not a guess.
func TestWaterSpreadOnFlatGround(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	origin := world.Pos{X: 0, Y: -60, Z: 0}
	s.set(d, origin, water(t, 0))
	for i := 0; i < 400; i++ {
		s.GameTick()
	}

	for dx := -9; dx <= 9; dx++ {
		for dz := -9; dz <= 9; dz++ {
			p := world.Pos{X: origin.X + dx, Y: origin.Y, Z: origin.Z + dz}
			dist := iabs(dx) + iabs(dz)
			got := levelAt(d, fluidWater, p)
			want := -1
			switch {
			case dist == 0:
				want = 0
			case dist <= fluidMaxLevel:
				want = dist
			}
			if got != want {
				t.Fatalf("(%d,%d): level %d, want %d", dx, dz, got, want)
			}
		}
	}
}

// TestLavaReachesThreeBlocks: lava spends two levels per block of
// spread, so it stops one third of the way out from water.
func TestLavaReachesThreeBlocks(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	origin := world.Pos{X: 0, Y: -60, Z: 0}
	s.set(d, origin, lava(t, 0))
	for i := 0; i < 4000; i++ {
		s.GameTick()
	}
	want := map[int]int{-3: 6, -2: 4, -1: 2, 0: 0, 1: 2, 2: 4, 3: 6}
	for dx, wl := range want {
		p := world.Pos{X: origin.X + dx, Y: origin.Y, Z: origin.Z}
		if got := levelAt(d, fluidLava, p); got != wl {
			t.Fatalf("lava at dx=%d is level %d, want %d", dx, got, wl)
		}
	}
	for _, dx := range []int{-4, 4} {
		p := world.Pos{X: origin.X + dx, Y: origin.Y, Z: origin.Z}
		if got := levelAt(d, fluidLava, p); got != -1 {
			t.Fatalf("lava reached dx=%d (level %d), it should stop at 3", dx, got)
		}
	}
}

// TestLavaSpreadsLikeWaterInTheNether: ultra-warm dimensions cut lava's
// drop-off back to one, so the nether gets a seven-block diamond.
func TestLavaSpreadsLikeWaterInTheNether(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimNether]
	if !d.ultraWarm {
		t.Fatal("the nether should be ultra-warm")
	}
	origin := world.Pos{X: 0, Y: 33, Z: 0}
	s.set(d, origin, lava(t, 0))
	for i := 0; i < 4000; i++ {
		s.GameTick()
	}
	for dx := -7; dx <= 7; dx++ {
		p := world.Pos{X: origin.X + dx, Y: origin.Y, Z: origin.Z}
		want := iabs(dx)
		if got := levelAt(d, fluidLava, p); got != want {
			t.Fatalf("nether lava at dx=%d is level %d, want %d", dx, got, want)
		}
	}
}

// TestFallingColumnShape pins the waterfall: the source feeds its four
// neighbours, each of those falls as a full-height column, and the
// column's foot spreads one block further than a resting source would.
func TestFallingColumnShape(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	top := world.Pos{X: 0, Y: -55, Z: 0}
	s.set(d, top, water(t, 0))
	for i := 0; i < 600; i++ {
		s.GameTick()
	}
	// The source spreads sideways at its own level.
	for _, o := range horizontalDirections {
		p := world.Pos{X: top.X + o.X, Y: top.Y, Z: top.Z + o.Z}
		if got := levelAt(d, fluidWater, p); got != 1 {
			t.Fatalf("beside the source: level %d, want 1", got)
		}
	}
	// The column is one block wide and falling the whole way down, with
	// the neighbours of the source falling alongside it.
	for y := -60; y <= -56; y++ {
		p := world.Pos{X: 0, Y: y, Z: 0}
		if got := levelAt(d, fluidWater, p); got != fluidFalling {
			t.Fatalf("column y=%d: level %d, want falling", y, got)
		}
		for _, o := range horizontalDirections {
			q := world.Pos{X: p.X + o.X, Y: y, Z: p.Z + o.Z}
			want := 1
			if y <= -56 {
				want = fluidFalling // the source's neighbours fall too
			}
			if got := levelAt(d, fluidWater, q); got != want {
				t.Fatalf("beside the column at y=%d: level %d, want %d", y, got, want)
			}
		}
	}
	// The foot is falling, and the pool it feeds reaches eight blocks
	// (level 7 at distance 8), one further than a resting source.
	if got := levelAt(d, fluidWater, world.Pos{X: 0, Y: -60, Z: 0}); got != fluidFalling {
		t.Fatalf("the foot of the column is level %d, want falling", got)
	}
	for dx := 2; dx <= 8; dx++ {
		p := world.Pos{X: dx, Y: -60, Z: 0}
		if got := levelAt(d, fluidWater, p); got != dx-1 {
			t.Fatalf("pool at dx=%d is level %d, want %d", dx, got, dx-1)
		}
	}
	if got := levelAt(d, fluidWater, world.Pos{X: 9, Y: -60, Z: 0}); got != -1 {
		t.Fatalf("the pool reached dx=9 (level %d), it should stop at 8", got)
	}
}

// TestDrainedWaterDisappears: removing the supply dries the flow up.
func TestDrainedWaterDisappears(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	origin := world.Pos{X: 0, Y: -60, Z: 0}
	s.set(d, origin, water(t, 0))
	for i := 0; i < 200; i++ {
		s.GameTick()
	}
	if got := levelAt(d, fluidWater, world.Pos{X: 3, Y: -60, Z: 0}); got != 3 {
		t.Fatalf("before draining, dx=3 is level %d, want 3", got)
	}
	// Remove the source: everything it fed has to go.
	s.set(d, origin, world.Air)
	for i := 0; i < 400; i++ {
		s.GameTick()
	}
	for dx := 0; dx <= 7; dx++ {
		p := world.Pos{X: dx, Y: -60, Z: 0}
		if d.w.GetBlock(p) != world.Air {
			name, props, _ := world.DescribeState(d.w.GetBlock(p))
			t.Fatalf("dx=%d still holds %s%v after the source was removed", dx, name, props)
		}
	}
}

// TestFluidDoesNotOscillate is the regression guard for the bug that
// made a falling pool flip between full-height and a thin flow forever:
// once a flow has settled, no further tick may change it.
func TestFluidDoesNotOscillate(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	s.set(d, world.Pos{X: 0, Y: -55, Z: 0}, water(t, 0))
	for i := 0; i < 600; i++ {
		s.GameTick()
	}
	before := d.w.Snapshot()
	for i := 0; i < 200; i++ {
		s.GameTick()
	}
	after := d.w.Snapshot()
	if len(before) != len(after) {
		t.Fatalf("settled world changed size: %d -> %d blocks", len(before), len(after))
	}
	for p, b := range before {
		if after[p] != b {
			name, props, _ := world.DescribeState(after[p])
			t.Fatalf("settled block at %v changed to %s%v", p, name, props)
		}
	}
	// And nothing is left waiting to run.
	if n := s.ticks.pending(); n != 0 {
		t.Fatalf("%d fluid ticks still queued after settling", n)
	}
}

// TestUltraWarmChangesLavaDelay covers the tick-delay half of the
// ultra-warm rule.
func TestUltraWarmChangesLavaDelay(t *testing.T) {
	s := fluidSim(t)
	over := s.dims[dimOverworld]
	nether := s.dims[dimNether]
	if got := fluidLava.tickDelay(over); got != lavaTickDelayOverworld {
		t.Fatalf("overworld lava delay = %d, want %d", got, lavaTickDelayOverworld)
	}
	if got := fluidLava.tickDelay(nether); got != lavaTickDelayNether {
		t.Fatalf("nether lava delay = %d, want %d", got, lavaTickDelayNether)
	}
	if got := fluidWater.tickDelay(nether); got != waterTickDelay {
		t.Fatalf("water delay in the nether = %d, want %d", got, waterTickDelay)
	}
}

// TestFluidPredicatesMatchAnyLevel checks the /execute-style predicate
// helper the block queries rely on.
func TestFluidPredicatesMatchAnyLevel(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	p := world.Pos{X: 0, Y: -60, Z: 0}
	s.set(d, p, water(t, 3))
	anyWater, anyProps, _ := parseBlockSpec("minecraft:water")
	exactWater, exactProps, _ := parseBlockSpec("minecraft:water[level=3]")
	wrongLevel, wrongProps, _ := parseBlockSpec("minecraft:water[level=4]")
	if !s.matchesBlockPredicate(d, p, anyWater, anyProps) {
		t.Fatal("a bare water predicate should match any water level")
	}
	if !s.matchesBlockPredicate(d, p, exactWater, exactProps) {
		t.Fatal("water[level=3] should match a level 3 block")
	}
	if s.matchesBlockPredicate(d, p, wrongLevel, wrongProps) {
		t.Fatal("water[level=4] should not match a level 3 block")
	}
}

// TestWaterSetsLavaSolid: water arriving beside a lava source sets the
// source to obsidian, and the flow the water later reaches sets to
// cobblestone. Both halves were measured against the real server (the
// fluid parity scenarios pour water at lava from every side), so this
// pins the shapes the parity run covers. The two solid lookups are
// lazy, so this also pins that they are resolved before they are
// written: a zero block id would leave air behind, and the water would
// flow into the hole.
func TestWaterSetsLavaSolid(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	src := world.Pos{X: 96, Y: -60, Z: 96}
	waterPos := world.Pos{X: 97, Y: -60, Z: 96}

	s.set(d, src, lava(t, 0))
	for i := 0; i < 40; i++ {
		s.GameTick()
	}
	// The source has spread lava in each direction; this diagonal
	// neighbour is a flow the water has to travel to.
	flow := world.Pos{X: 96, Y: -60, Z: 97}
	if got := ownerName(d, flow); got != "lava" {
		t.Fatalf("the neighbour of the source is %s, want a lava flow", got)
	}

	s.set(d, waterPos, water(t, 0))
	for i := 0; i < 200; i++ {
		s.GameTick()
	}

	if got := ownerName(d, src); got != "obsidian" {
		t.Fatalf("the lava source became %s, want obsidian", got)
	}
	if got := ownerName(d, flow); got != "cobblestone" {
		t.Fatalf("the lava flow the water reached became %s, want cobblestone", got)
	}
	if got := ownerName(d, waterPos); got != "water" {
		t.Fatalf("the water source became %s, want water", got)
	}
	// The flows past that lost their supply too: once the source is gone
	// they drain, and nothing leaves lava behind.
	for _, dz := range []int{-6, -5, -4, 4, 5, 6} {
		p := world.Pos{X: 96, Y: -60, Z: 96 + dz}
		if got := ownerName(d, p); got == "lava" {
			t.Fatalf("lava still flows at dz=%d after its source set solid", dz)
		}
	}
}

// TestWaterFallingOnLavaFlowMakesCobblestone: the cobblestone generator
// case. Water above a lava flow sets that flow to cobblestone, while the
// same water above a lava source sets the source to obsidian.
func TestWaterFallingOnLavaFlowMakesCobblestone(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	src := world.Pos{X: 0, Y: -60, Z: 0}
	s.set(d, src, lava(t, 0))
	for i := 0; i < 40; i++ {
		s.GameTick()
	}
	flow := world.Pos{X: 1, Y: -60, Z: 0}
	if got := ownerName(d, flow); got != "lava" {
		t.Fatalf("the neighbour of the source is %s, want a lava flow", got)
	}
	s.set(d, world.Pos{X: 1, Y: -59, Z: 0}, water(t, 0))
	for i := 0; i < 200; i++ {
		s.GameTick()
	}
	if got := ownerName(d, flow); got != "cobblestone" {
		t.Fatalf("water falling on a lava flow left %s, want cobblestone", got)
	}
	if got := ownerName(d, src); got != "obsidian" {
		t.Fatalf("the lava source became %s, want obsidian", got)
	}
}

// ownerName is the block state's owner name at pos.
func ownerName(d *dimension, p world.Pos) string {
	n, _ := world.StateOwner(d.w.GetBlock(p))
	return n
}

// A waterloggable block is not washed away: water flows into it and the
// cell holds a source, from which it can feed neighbours again.
func TestWaterloggableFillsAndFeeds(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	fencePos := world.Pos{X: 1, Y: -60, Z: 0}
	fence, ok := world.BlockStateOf("oak_fence", map[string]string{
		"east": "false", "north": "false", "south": "false", "west": "false", "waterlogged": "false",
	})
	if !ok {
		t.Fatal("oak_fence state missing")
	}
	s.set(d, fencePos, fence)
	s.set(d, world.Pos{X: 0, Y: -60, Z: 0}, water(t, 0))
	for i := 0; i < 40; i++ {
		s.GameTick()
	}
	got := d.w.GetBlock(fencePos)
	if name, _ := world.StateOwner(got); name != "oak_fence" {
		t.Fatalf("the fence is %s, want it to survive the water", name)
	}
	if wl, _ := world.StateProperty(got, "waterlogged"); wl != "true" {
		t.Fatalf("the fence is waterlogged=%s, want waterlogged=true", wl)
	}
	// The waterlogged cell reads as a source: it can feed a neighbour
	// even after the original source is cut.
	s.set(d, world.Pos{X: 0, Y: -60, Z: 0}, airBlock(t))
	for i := 0; i < 40; i++ {
		s.GameTick()
	}
	if name := ownerName(d, world.Pos{X: 2, Y: -60, Z: 0}); name != "water" {
		t.Fatalf("beyond the waterlogged fence is %s, want the fence to feed water out", name)
	}
	if wl, _ := world.StateProperty(d.w.GetBlock(fencePos), "waterlogged"); wl != "true" {
		t.Fatalf("the fence drained to waterlogged=%s, want a permanent source", wl)
	}
}

// Lava has no way into a waterloggable block: it flows around.
func TestLavaCannotEnterWaterloggable(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	fencePos := world.Pos{X: 1, Y: -60, Z: 0}
	fence, _ := world.BlockStateOf("oak_fence", map[string]string{
		"east": "false", "north": "false", "south": "false", "west": "false", "waterlogged": "false",
	})
	s.set(d, fencePos, fence)
	s.set(d, world.Pos{X: 0, Y: -60, Z: 0}, lava(t, 0))
	for i := 0; i < 200; i++ {
		s.GameTick()
	}
	got := d.w.GetBlock(fencePos)
	if name, _ := world.StateOwner(got); name != "oak_fence" {
		t.Fatalf("lava entered the fence: %s", name)
	}
	if wl, _ := world.StateProperty(got, "waterlogged"); wl != "false" {
		t.Fatalf("the fence is waterlogged=%s, want dry", wl)
	}
}

func airBlock(t *testing.T) world.Block {
	t.Helper()
	b, ok := world.BlockStateOf("air", nil)
	if !ok {
		t.Fatal("air state missing")
	}
	return b
}
