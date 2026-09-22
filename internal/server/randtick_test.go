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

func TestRandTickSugarCaneColumn(t *testing.T) {
	s, d := randSim(t)
	// Cane needs water (or frosted ice) beside its base block to
	// survive the neighbor-update check on writes.
	randPut(t, d, 90, -61, 50, "dirt", nil)
	randPut(t, d, 90, -61, 51, "frosted_ice", nil)
	randPut(t, d, 90, -60, 50, "sugar_cane", map[string]string{"age": "15"})
	// Age 15 spends its tick growing up and resetting.
	rule := randRules()["sugar_cane"]
	rule(s, d, world.Pos{X: 90, Y: -60, Z: 50}, randGet(t, d, 90, -60, 50))
	if randName(d, 90, -59, 50) != "sugar_cane" {
		t.Fatal("age-15 cane did not grow up")
	}
	if randAge(t, d, 90, -60, 50) != 0 {
		t.Fatal("cane age did not reset to 0 after growing")
	}
	// A capped cane never even ages: the above-air check runs first.
	randPut(t, d, 91, -61, 50, "dirt", nil)
	randPut(t, d, 91, -61, 51, "frosted_ice", nil)
	randPut(t, d, 91, -60, 50, "sugar_cane", map[string]string{"age": "0"})
	randPut(t, d, 91, -59, 50, "stone", nil)
	randRoll(s, d, world.Pos{X: 91, Y: -60, Z: 50}, "sugar_cane", 300, func() bool {
		return randAge(t, d, 91, -60, 50) != 0
	})
	if randAge(t, d, 91, -60, 50) != 0 {
		t.Fatal("capped sugar cane aged")
	}
	// A full column stops at three.
	randPut(t, d, 92, -61, 50, "dirt", nil)
	randPut(t, d, 92, -61, 51, "frosted_ice", nil)
	randPut(t, d, 92, -60, 50, "sugar_cane", nil)
	randPut(t, d, 92, -59, 50, "sugar_cane", nil)
	randPut(t, d, 92, -58, 50, "sugar_cane", map[string]string{"age": "15"})
	randRoll(s, d, world.Pos{X: 92, Y: -58, Z: 50}, "sugar_cane", 300, func() bool {
		return randName(d, 92, -57, 50) == "sugar_cane"
	})
	if randName(d, 92, -57, 50) == "sugar_cane" {
		t.Fatal("sugar cane column grew past 3")
	}
}

func TestRandTickCactusColumnAndFlower(t *testing.T) {
	s, d := randSim(t)
	randPut(t, d, 100, -61, 50, "sand", nil)
	randPut(t, d, 100, -60, 50, "cactus", map[string]string{"age": "15"})
	rule := randRules()["cactus"]
	rule(s, d, world.Pos{X: 100, Y: -60, Z: 50}, randGet(t, d, 100, -60, 50))
	if randName(d, 100, -59, 50) != "cactus" {
		t.Fatal("age-15 cactus did not grow up")
	}
	if randAge(t, d, 100, -60, 50) != 0 {
		t.Fatal("cactus age did not reset")
	}
	// A three-tall column whose top is at age 8 rolls the 0.25 flower
	// chance; keep the top pinned at 8 until it lands.
	randPut(t, d, 104, -61, 50, "sand", nil)
	for y := -60; y <= -58; y++ {
		randPut(t, d, 104, y, 50, "cactus", nil)
	}
	flowered := false
	for i := 0; i < 400 && !flowered; i++ {
		randPut(t, d, 104, -58, 50, "cactus", map[string]string{"age": "8"})
		rule(s, d, world.Pos{X: 104, Y: -58, Z: 50}, randGet(t, d, 104, -58, 50))
		flowered = randName(d, 104, -57, 50) == "cactus_flower"
	}
	if !flowered {
		t.Fatal("cactus never flowered at age 8")
	}
	// A solid side neighbor blocks the flower's canSurvive check.
	randPut(t, d, 108, -61, 50, "sand", nil)
	for y := -60; y <= -58; y++ {
		randPut(t, d, 108, y, 50, "cactus", nil)
	}
	randPut(t, d, 109, -57, 50, "stone", nil)
	for i := 0; i < 400; i++ {
		randPut(t, d, 108, -58, 50, "cactus", map[string]string{"age": "8"})
		rule(s, d, world.Pos{X: 108, Y: -58, Z: 50}, randGet(t, d, 108, -58, 50))
	}
	if randName(d, 108, -57, 50) == "cactus_flower" {
		t.Fatal("flower placed beside a solid block")
	}
}

func TestRandTickBambooCascade(t *testing.T) {
	s, d := randSim(t)
	randPut(t, d, 120, -61, 50, "dirt", nil)
	randPut(t, d, 120, -60, 50, "bamboo_sapling", nil)
	if !randRoll(s, d, world.Pos{X: 120, Y: -60, Z: 50}, "bamboo_sapling", 200, func() bool {
		return randName(d, 120, -59, 50) == "bamboo"
	}) {
		t.Fatal("sapling never grew a stalk")
	}
	if l, _ := world.StateProperty(randGet(t, d, 120, -59, 50), "leaves"); l != "small" {
		t.Fatalf("first stalk leaves = %s, want small", l)
	}
	// Roll successive tops; the leaf cascade settles to none/small/large
	// from bottom to top.
	stalk := randRules()["bamboo"]
	for i := 0; i < 6000 && randName(d, 120, -56, 50) != "bamboo"; i++ {
		for y := -59; y <= -55; y++ {
			b := randGet(t, d, 120, y, 50)
			if randName(d, 120, y, 50) == "bamboo" && isAirCell(randGet(t, d, 120, y+1, 50)) {
				if st, _ := intProp(b, "stage"); st == 0 {
					stalk(s, d, world.Pos{X: 120, Y: y, Z: 50}, b)
				}
				break
			}
		}
	}
	if randName(d, 120, -56, 50) != "bamboo" {
		t.Fatal("bamboo column never reached 4")
	}
	// The demotion only touches the two stalks below the ticked top, so
	// the tip reads ...none, small, large, large.
	l, _ := world.StateProperty(randGet(t, d, 120, -56, 50), "leaves")
	if l != "large" {
		t.Fatalf("top leaves = %s, want large", l)
	}
	l, _ = world.StateProperty(randGet(t, d, 120, -57, 50), "leaves")
	if l != "large" {
		t.Fatalf("second-from-top leaves = %s, want large", l)
	}
	l, _ = world.StateProperty(randGet(t, d, 120, -58, 50), "leaves")
	if l != "small" {
		t.Fatalf("third-from-top leaves = %s, want small", l)
	}
	l, _ = world.StateProperty(randGet(t, d, 120, -59, 50), "leaves")
	if l != "none" {
		t.Fatalf("bottom leaves = %s, want none", l)
	}
	// Covered bamboo stays put: the brightness probe is the cell above.
	randPut(t, d, 124, -61, 50, "dirt", nil)
	randPut(t, d, 124, -60, 50, "bamboo", nil)
	randPut(t, d, 124, -59, 50, "stone", nil)
	randRoll(s, d, world.Pos{X: 124, Y: -60, Z: 50}, "bamboo", 300, func() bool {
		return randName(d, 124, -59, 50) == "bamboo"
	})
	if randName(d, 124, -59, 50) == "bamboo" {
		t.Fatal("capped bamboo grew")
	}
}

func TestRandTickChorusFlower(t *testing.T) {
	s, d := randSim(t)
	rule := randRules()["chorus_flower"]
	// On end stone the flower always grows up: a plant appears at its
	// cell and the flower rematerializes one up, keeping its age.
	randPut(t, d, 130, -61, 50, "end_stone", nil)
	randPut(t, d, 130, -60, 50, "chorus_flower", map[string]string{"age": "0"})
	rule(s, d, world.Pos{X: 130, Y: -60, Z: 50}, randGet(t, d, 130, -60, 50))
	if randName(d, 130, -60, 50) != "chorus_plant" {
		t.Fatal("flower on end stone did not grow into a plant")
	}
	if randName(d, 130, -59, 50) != "chorus_flower" {
		t.Fatal("flower did not rematerialize above the plant")
	}
	p := randGet(t, d, 130, -60, 50)
	if dv, _ := world.StateProperty(p, "down"); dv != "true" {
		t.Fatal("plant does not connect down to end stone")
	}
	if uv, _ := world.StateProperty(p, "up"); uv != "true" {
		t.Fatal("plant does not connect up to the flower")
	}
	// A flower boxed in horizontally with a non-support block below
	// dies in place.
	randPut(t, d, 134, -61, 50, "stone", nil)
	randPut(t, d, 134, -60, 50, "chorus_flower", map[string]string{"age": "2"})
	for _, dir := range cardinalDirs {
		randPut(t, d, 134+dir.dx, -60, 50+dir.dz, "stone", nil)
	}
	rule(s, d, world.Pos{X: 134, Y: -60, Z: 50}, randGet(t, d, 134, -60, 50))
	b := randGet(t, d, 134, -60, 50)
	if randName(d, 134, -60, 50) != "chorus_flower" {
		t.Fatalf("boxed flower -> %s", randName(d, 134, -60, 50))
	}
	if a, _ := intProp(b, "age"); a != 5 {
		t.Fatalf("dead flower age = %d, want 5", a)
	}
	// A flower whose cell above is occupied never ticks at all.
	randPut(t, d, 138, -61, 50, "end_stone", nil)
	randPut(t, d, 138, -60, 50, "chorus_flower", map[string]string{"age": "0"})
	randPut(t, d, 138, -59, 50, "stone", nil)
	rule(s, d, world.Pos{X: 138, Y: -60, Z: 50}, randGet(t, d, 138, -60, 50))
	if a, _ := intProp(randGet(t, d, 138, -60, 50), "age"); a != 0 {
		t.Fatalf("capped flower aged to %d", a)
	}
}
