package server

import (
	"testing"

	"mc-go/internal/world"
)

// Support pops: a write that invalidates a neighbor's canSurvive pops
// the neighbor — the updateShape fan-out, minus the scheduled-tick
// delay, which is unobservable after the check runs.

func TestSurvivalPopsCaneOnDrySupport(t *testing.T) {
	s, d := randSim(t)
	randPut(t, d, 40, -61, 30, "dirt", nil)
	randPut(t, d, 40, -60, 30, "sugar_cane", map[string]string{"age": "3"})
	// Breaking the base pops the cane above it.
	s.setNaturalBlock(d, world.Pos{X: 40, Y: -61, Z: 30}, world.Air)
	if randName(d, 40, -60, 30) != "air" {
		t.Fatalf("cane stayed: %s", randName(d, 40, -60, 30))
	}
}

func TestSurvivalPopsCaneColumn(t *testing.T) {
	s, d := randSim(t)
	randPut(t, d, 40, -61, 30, "dirt", nil)
	randPut(t, d, 40, -60, 30, "sugar_cane", nil)
	randPut(t, d, 40, -59, 30, "sugar_cane", nil)
	randPut(t, d, 40, -58, 30, "sugar_cane", nil)
	// Removing the middle cane cascades: the top's support vanishes.
	s.setNaturalBlock(d, world.Pos{X: 40, Y: -59, Z: 30}, world.Air)
	if randName(d, 40, -58, 30) != "air" {
		t.Fatalf("top cane stayed: %s", randName(d, 40, -58, 30))
	}
}

func TestSurvivalCaneStaysWithoutWater(t *testing.T) {
	// No water beside the base — a /setblock-style placement pops the
	// cane (tick-family self-check via BlockInput.place's fold).
	s, d := randSim(t)
	randPut(t, d, 40, -61, 30, "dirt", nil)
	s.setBlockAndBroadcast("", world.Pos{X: 40, Y: -60, Z: 30},
		mustBlock(t, "sugar_cane", nil))
	if randName(d, 40, -60, 30) != "air" {
		t.Fatalf("unsupported cane stayed")
	}
	// Frosted ice beside the base supports it.
	randPut(t, d, 41, -61, 30, "frosted_ice", nil)
	s.setBlockAndBroadcast("", world.Pos{X: 40, Y: -60, Z: 30},
		mustBlock(t, "sugar_cane", nil))
	if randName(d, 40, -60, 30) != "sugar_cane" {
		t.Fatalf("supported cane popped")
	}
	// An internal write never self-checks: the same unsupported cane
	// stays until a neighbor change re-evaluates it.
	randPut(t, d, 46, -61, 30, "dirt", nil)
	s.setNaturalBlock(d, world.Pos{X: 46, Y: -60, Z: 30},
		mustBlock(t, "sugar_cane", nil))
	if randName(d, 46, -60, 30) != "sugar_cane" {
		t.Fatalf("naturally-placed cane popped")
	}
	// A neighbor write then pops it.
	s.setNaturalBlock(d, world.Pos{X: 47, Y: -60, Z: 30},
		mustBlock(t, "stone", nil))
	if randName(d, 46, -60, 30) != "air" {
		t.Fatalf("unsupported cane survived neighbor write")
	}
}

func TestSurvivalPopsCactusOnSolidNeighbor(t *testing.T) {
	s, d := randSim(t)
	randPut(t, d, 40, -61, 30, "sand", nil)
	randPut(t, d, 40, -60, 30, "cactus", nil)
	s.setNaturalBlock(d, world.Pos{X: 41, Y: -60, Z: 30},
		mustBlock(t, "stone", nil))
	if randName(d, 40, -60, 30) != "air" {
		t.Fatalf("cactus beside stone stayed")
	}
}

func TestSurvivalPopsBambooColumn(t *testing.T) {
	s, d := randSim(t)
	randPut(t, d, 40, -61, 30, "grass_block", nil)
	for y := -60; y <= -57; y++ {
		randPut(t, d, 40, y, 30, "bamboo", map[string]string{"leaves": "small"})
	}
	s.setNaturalBlock(d, world.Pos{X: 40, Y: -59, Z: 30}, world.Air)
	for _, y := range []int{-58, -57} {
		if randName(d, 40, y, 30) != "air" {
			t.Fatalf("bamboo at %d stayed: %s", y, randName(d, 40, y, 30))
		}
	}
	if randName(d, 40, -60, 30) != "bamboo" {
		t.Fatalf("stalk below the break popped: %s", randName(d, 40, -60, 30))
	}
}

func TestSurvivalPopsChorusChain(t *testing.T) {
	s, d := randSim(t)
	randPut(t, d, 40, -61, 30, "end_stone", nil)
	randPut(t, d, 40, -60, 30, "chorus_plant", nil)
	randPut(t, d, 40, -59, 30, "chorus_plant", nil)
	randPut(t, d, 40, -58, 30, "chorus_flower", map[string]string{"age": "0"})
	s.setNaturalBlock(d, world.Pos{X: 40, Y: -60, Z: 30}, world.Air)
	if randName(d, 40, -59, 30) != "air" || randName(d, 40, -58, 30) != "air" {
		t.Fatalf("chain survived: -59=%s -58=%s",
			randName(d, 40, -59, 30), randName(d, 40, -58, 30))
	}
}

func TestSurvivalPopsCropAndTorch(t *testing.T) {
	s, d := randSim(t)
	randPut(t, d, 40, -61, 30, "farmland", nil)
	randPut(t, d, 40, -60, 30, "wheat", map[string]string{"age": "0"})
	s.setNaturalBlock(d, world.Pos{X: 40, Y: -61, Z: 30}, world.Air)
	if randName(d, 40, -60, 30) != "air" {
		t.Fatalf("wheat stayed on broken farmland")
	}
	randPut(t, d, 44, -61, 30, "stone", nil)
	randPut(t, d, 44, -60, 30, "torch", nil)
	s.setNaturalBlock(d, world.Pos{X: 44, Y: -61, Z: 30}, world.Air)
	if randName(d, 44, -60, 30) != "air" {
		t.Fatalf("torch stayed on broken support")
	}
}

func TestSurvivalSilentWritesSkipCheck(t *testing.T) {
	// The chorus rewrite path is flag-2 silent: a floating plant must
	// not pop mid-write.
	s, d := randSim(t)
	s.setSilentBlock(d, world.Pos{X: 40, Y: -60, Z: 30},
		mustBlock(t, "chorus_plant", nil))
	if randName(d, 40, -60, 30) != "chorus_plant" {
		t.Fatalf("silent plant popped itself")
	}
}

func mustBlock(t *testing.T, name string, props map[string]string) world.Block {
	t.Helper()
	b, ok := world.BlockStateOf(name, props)
	if !ok {
		t.Fatalf("%s%v is not a state", name, props)
	}
	return b
}
