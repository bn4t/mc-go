package server

import (
	"testing"

	"mc-go/internal/world"
)

// randSim builds a frozen server whose overworld a test can poke
// directly: rules are invoked by name so each call is one "random tick
// landed on this block", no sampler or tick loop needed.
func randSim(t *testing.T) (*Server, *dimension) {
	t.Helper()
	s := New("127.0.0.1:0")
	t.Cleanup(func() { s.world.Stop() })
	s.SetFrozen(true)
	return s, s.dims[dimOverworld]
}

func randPut(t *testing.T, d *dimension, x, y, z int, name string, props map[string]string) {
	t.Helper()
	b, ok := world.BlockStateOf(name, props)
	if !ok {
		t.Fatalf("%s%v is not a state", name, props)
	}
	d.w.SetBlock(world.Pos{X: x, Y: y, Z: z}, b)
}

func randGet(t *testing.T, d *dimension, x, y, z int) world.Block {
	t.Helper()
	return d.w.GetBlock(world.Pos{X: x, Y: y, Z: z})
}

// randRoll drives a rule until the predicate holds or the call budget
// runs out. Rules are probabilistic, so the budget is generous.
func randRoll(s *Server, d *dimension, pos world.Pos, name string, maxCalls int, want func() bool) bool {
	rule := randRules()[name]
	for i := 0; i < maxCalls && !want(); i++ {
		rule(s, d, pos, d.w.GetBlock(pos))
	}
	return want()
}

func randAge(t *testing.T, d *dimension, x, y, z int) int {
	t.Helper()
	age, ok := intProp(randGet(t, d, x, y, z), "age")
	if !ok {
		t.Fatalf("block at %d %d %d has no age", x, y, z)
	}
	return age
}

func randName(d *dimension, x, y, z int) string {
	n, _ := world.StateOwner(d.w.GetBlock(world.Pos{X: x, Y: y, Z: z}))
	return n
}

func TestRandTickWheatGrowsOnHydratedFarmland(t *testing.T) {
	s, d := randSim(t)
	randPut(t, d, 40, -61, 30, "farmland", map[string]string{"moisture": "7"})
	randPut(t, d, 40, -60, 30, "wheat", map[string]string{"age": "0"})
	if !randRoll(s, d, world.Pos{X: 40, Y: -60, Z: 30}, "wheat", 2000, func() bool {
		return randAge(t, d, 40, -60, 30) == 7
	}) {
		t.Fatal("wheat never reached age 7")
	}
}

func TestRandTickCropNeedsLight(t *testing.T) {
	s, d := randSim(t)
	randPut(t, d, 42, -61, 30, "farmland", map[string]string{"moisture": "7"})
	randPut(t, d, 42, -60, 30, "wheat", map[string]string{"age": "0"})
	randPut(t, d, 42, -59, 30, "stone", nil)
	randRoll(s, d, world.Pos{X: 42, Y: -60, Z: 30}, "wheat", 500, func() bool {
		return randAge(t, d, 42, -60, 30) != 0
	})
	if randAge(t, d, 42, -60, 30) != 0 {
		t.Fatal("covered wheat grew")
	}
}

func TestRandTickFarmlandMoistureAndRevert(t *testing.T) {
	s, d := randSim(t)
	// Wet farmland snaps to 7 and stays. Probes sit >4 apart so their
	// water boxes don't overlap.
	randPut(t, d, 44, -61, 40, "farmland", map[string]string{"moisture": "0"})
	randPut(t, d, 45, -61, 40, "water", map[string]string{"level": "0"})
	rule := randRules()["farmland"]
	rule(s, d, world.Pos{X: 44, Y: -61, Z: 40}, randGet(t, d, 44, -61, 40))
	if m, _ := intProp(randGet(t, d, 44, -61, 40), "moisture"); m != 7 {
		t.Fatalf("moisture = %d, want 7", m)
	}
	// Dry farmland with a maintained crop drains one step at a time.
	randPut(t, d, 60, -61, 40, "farmland", map[string]string{"moisture": "7"})
	randPut(t, d, 60, -60, 40, "wheat", map[string]string{"age": "0"})
	p := world.Pos{X: 60, Y: -61, Z: 40}
	for i := 0; i < 7; i++ {
		rule(s, d, p, randGet(t, d, 60, -61, 40))
	}
	if m, _ := intProp(randGet(t, d, 60, -61, 40), "moisture"); m != 0 {
		t.Fatalf("moisture after drain = %d, want 0", m)
	}
	rule(s, d, p, randGet(t, d, 60, -61, 40))
	if randName(d, 60, -61, 40) != "farmland" {
		t.Fatal("maintained farmland reverted with a crop on it")
	}
	// Bare dry farmland turns to dirt on the next roll.
	randPut(t, d, 80, -61, 40, "farmland", map[string]string{"moisture": "1"})
	p = world.Pos{X: 80, Y: -61, Z: 40}
	rule(s, d, p, randGet(t, d, 80, -61, 40)) // -> moisture 0
	rule(s, d, p, randGet(t, d, 80, -61, 40)) // -> dirt
	if randName(d, 80, -61, 40) != "dirt" {
		t.Fatalf("bare farmland -> %s, want dirt", randName(d, 80, -61, 40))
	}
}

func TestRandTickNetherWartIgnoresLight(t *testing.T) {
	s, d := randSim(t)
	randPut(t, d, 50, -61, 30, "soul_sand", nil)
	randPut(t, d, 50, -60, 30, "nether_wart", map[string]string{"age": "0"})
	randPut(t, d, 50, -59, 30, "stone", nil)
	if !randRoll(s, d, world.Pos{X: 50, Y: -60, Z: 30}, "nether_wart", 500, func() bool {
		return randAge(t, d, 50, -60, 30) == 3
	}) {
		t.Fatal("nether wart never reached age 3 under cover")
	}
}

func TestRandTickStemFruits(t *testing.T) {
	s, d := randSim(t)
	randPut(t, d, 52, -61, 30, "farmland", map[string]string{"moisture": "7"})
	randPut(t, d, 52, -60, 30, "melon_stem", map[string]string{"age": "7"})
	// Only east can fruit: the other neighbors sit on stone.
	randPut(t, d, 51, -61, 30, "stone", nil)
	randPut(t, d, 53, -61, 30, "dirt", nil)
	randPut(t, d, 52, -61, 29, "stone", nil)
	randPut(t, d, 52, -61, 31, "stone", nil)
	if !randRoll(s, d, world.Pos{X: 52, Y: -60, Z: 30}, "melon_stem", 2000, func() bool {
		return randName(d, 53, -60, 30) == "melon"
	}) {
		t.Fatal("stem never fruited")
	}
	if randName(d, 52, -60, 30) != "attached_melon_stem" {
		t.Fatalf("stem -> %s, want attached_melon_stem", randName(d, 52, -60, 30))
	}
	if f, _ := world.StateProperty(randGet(t, d, 52, -60, 30), "facing"); f != "east" {
		t.Fatalf("attached facing = %s, want east", f)
	}
}

func TestRandTickPitcherCropDoubles(t *testing.T) {
	s, d := randSim(t)
	randPut(t, d, 54, -61, 30, "farmland", map[string]string{"moisture": "7"})
	randPut(t, d, 54, -60, 30, "pitcher_crop", map[string]string{"age": "0", "half": "lower"})
	if !randRoll(s, d, world.Pos{X: 54, Y: -60, Z: 30}, "pitcher_crop", 3000, func() bool {
		return randAge(t, d, 54, -60, 30) == 4
	}) {
		t.Fatal("pitcher never reached age 4")
	}
	up := randGet(t, d, 54, -59, 30)
	if n, _ := world.StateOwner(up); n != "pitcher_crop" {
		t.Fatalf("upper half = %s, want pitcher_crop", n)
	}
	if h, _ := world.StateProperty(up, "half"); h != "upper" {
		t.Fatalf("upper half prop = %s", h)
	}
	if a, _ := intProp(up, "age"); a != 4 {
		t.Fatalf("upper age = %d, want 4", a)
	}
}

func TestRandTickSweetBerryNeedsLightAbove(t *testing.T) {
	s, d := randSim(t)
	randPut(t, d, 56, -61, 30, "dirt", nil)
	randPut(t, d, 56, -60, 30, "sweet_berry_bush", map[string]string{"age": "0"})
	if !randRoll(s, d, world.Pos{X: 56, Y: -60, Z: 30}, "sweet_berry_bush", 1000, func() bool {
		return randAge(t, d, 56, -60, 30) == 3
	}) {
		t.Fatal("sweet berry never reached age 3")
	}
	// A solid block in the light probe cell stalls the bush.
	randPut(t, d, 57, -61, 30, "dirt", nil)
	randPut(t, d, 57, -60, 30, "sweet_berry_bush", map[string]string{"age": "0"})
	randPut(t, d, 57, -59, 30, "stone", nil)
	randRoll(s, d, world.Pos{X: 57, Y: -60, Z: 30}, "sweet_berry_bush", 500, func() bool {
		return randAge(t, d, 57, -60, 30) != 0
	})
	if randAge(t, d, 57, -60, 30) != 0 {
		t.Fatal("shaded bush grew")
	}
}

func TestRandTickGrassSpreadsAndDies(t *testing.T) {
	s, d := randSim(t)
	// A lit grass block converts a dirt neighbor eventually.
	randPut(t, d, 60, -61, 50, "grass_block", map[string]string{"snowy": "false"})
	randPut(t, d, 61, -61, 50, "dirt", nil)
	if !randRoll(s, d, world.Pos{X: 60, Y: -61, Z: 50}, "grass_block", 500, func() bool {
		return randName(d, 61, -61, 50) == "grass_block"
	}) {
		t.Fatal("grass never spread onto dirt")
	}
	// A shaded grass block reverts to dirt.
	randPut(t, d, 70, -61, 50, "grass_block", map[string]string{"snowy": "false"})
	randPut(t, d, 70, -60, 50, "stone", nil)
	randRoll(s, d, world.Pos{X: 70, Y: -61, Z: 50}, "grass_block", 1, func() bool { return false })
	if randName(d, 70, -61, 50) != "dirt" {
		t.Fatalf("shaded grass -> %s, want dirt", randName(d, 70, -61, 50))
	}
	// Submerged grass dies too.
	randPut(t, d, 72, -61, 50, "grass_block", map[string]string{"snowy": "false"})
	randPut(t, d, 72, -60, 50, "water", map[string]string{"level": "0"})
	randRoll(s, d, world.Pos{X: 72, Y: -61, Z: 50}, "grass_block", 1, func() bool { return false })
	if randName(d, 72, -61, 50) != "dirt" {
		t.Fatalf("submerged grass -> %s, want dirt", randName(d, 72, -61, 50))
	}
}

func TestRandTickSpeedGameRule(t *testing.T) {
	s, _ := randSim(t)
	s.gameRules["minecraft:random_tick_speed"] = "0"
	if s.randomTickSpeed() != 0 {
		t.Fatal("gamerule randomTickSpeed 0 did not reach the sampler")
	}
	s.gameRules["minecraft:random_tick_speed"] = "4096"
	if s.randomTickSpeed() != 4096 {
		t.Fatal("gamerule randomTickSpeed 4096 did not reach the sampler")
	}
}
