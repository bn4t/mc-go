package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestScheduledSaveRelativeMetadataAndDetachedQueues(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	s.clock.mu.Lock()
	s.clock.count = 100
	s.clock.mu.Unlock()
	block := schedKey{dim: dimOverworld, pos: world.Pos{X: -17, Y: -60, Z: 16}, kind: tickBlock, block: "stone_button"}
	fluid := schedKey{dim: dimOverworld, pos: world.Pos{X: 17, Y: -60}, kind: tickFluid, fluid: "flowing_water"}
	past := schedKey{dim: dimNether, pos: world.Pos{Y: 4}, kind: tickBlock, block: "stone_button"}
	narrow := schedKey{dim: dimOverworld, pos: world.Pos{X: 18, Y: -60}, kind: tickBlock, block: "repeater"}
	s.ticks.schedulePrio(block, 113, tickPrioVeryHigh)
	s.ticks.schedule(fluid, 105)
	s.ticks.schedule(past, 90)
	s.ticks.schedulePrio(narrow, 100+(int64(1)<<32)+7, tickPrioHigh)
	captured := s.captureScheduledChunkTicksOwned()
	s.ticks.clearBlockArea(dimOverworld, world.Pos{X: -100, Y: -100, Z: -100}, world.Pos{X: 100, Y: 100, Z: 100})
	// Later requests and mutable queue metadata cannot alter the detached result.
	s.ticks.schedulePrio(block, 250, tickPrioExtremelyHigh)
	got := captured[dimOverworld][[2]int32{-2, 1}].Blocks
	if len(got) != 1 || got[0].Type != "minecraft:stone_button" || got[0].Delay != 13 || got[0].Priority != int32(tickPrioVeryHigh) {
		t.Fatalf("block metadata%#v", got)
	}
	got = captured[dimOverworld][[2]int32{1, 0}].Fluids
	if len(got) != 1 || got[0].Type != "minecraft:flowing_water" || got[0].Delay != 5 {
		t.Fatalf("fluid metadata%#v", got)
	}
	got = captured[dimNether][[2]int32{}].Blocks
	if len(got) != 1 || got[0].Delay != -10 {
		t.Fatalf("negative delay%#v", got)
	}
	got = captured[dimOverworld][[2]int32{1, 0}].Blocks
	if len(got) != 1 || got[0].Delay != 7 || got[0].Priority != int32(tickPrioHigh) {
		t.Fatalf("native int narrowing%#v", got)
	}
}

func TestScheduledSaveDoesNotPersistCollectedCallbacks(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	a := schedKey{dim: dimOverworld, pos: world.Pos{X: 1}, kind: tickBlock, block: "stone_button"}
	b := a
	b.pos.X = 2
	s.ticks.schedule(a, 0)
	s.ticks.schedule(b, 10)
	s.ticks.poll(0)
	captured := s.captureScheduledChunkTicksOwned()[dimOverworld][[2]int32{}].Blocks
	if len(captured) != 1 || captured[0].Pos.X != 2 || captured[0].Delay != 10 {
		t.Fatalf("collected callback saved again: %#v", captured)
	}
}
