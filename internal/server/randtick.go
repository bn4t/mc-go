package server

import (
	"math/rand"
	"strconv"
	"sync"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// Random-tick rules for the crop family, each one measured against
// vanilla's bytecode:
//
//   - CropBlock (wheat/carrots/potatoes/beetroots/torchflower_crop):
//     rawBrightness(pos) >= 9, age < max, then a growth-speed roll —
//     nextInt((int)(25/growthSpeed)+1) == 0 — before age increments.
//   - StemBlock (melon_stem/pumpkin_stem): same gate and roll; age<7
//     increments, age==7 picks a random horizontal neighbor and, when it
//     is air over a #supports_<fruit>_stem_fruit block, places the fruit
//     and rewrites the stem as the attached variant facing it.
//   - NetherWartBlock: age<3 && nextInt(10)==0, no light check.
//   - CocoaBlock: nextInt(5)==0 && age<2, no light check.
//   - SweetBerryBushBlock: age<3 && nextInt(5)==0 and the light check
//     runs at pos.above(), not pos.
//   - PitcherCropBlock: growth-speed roll; grow bumps age by 1 (cap 4)
//     on the lower half when canGrow passes (lit, in build height, and
//     room above once the new age turns it double-tall at age>=3).
//   - FarmlandBlock: water in the 9x2x9 box at/above pos (or rain at the
//     block above) snaps moisture to 7; otherwise
//     moisture drains by 1, and at 0 with nothing in
//     #maintains_farmland above it reverts to dirt.
//
// The light gate is approximated: mc-go has no light engine, so
// "rawBrightness >= 9" reads as skylight-dimension plus an all-air
// column above the crop (vanilla's propagated sky light at any
// sky-exposed position is 15). Transparent-block and block-light edge
// cases (a torch-lit cellar crop) stay dark here.

func cropRule(maxAge int) func(*Server, *dimension, world.Pos, world.Block) {
	return func(s *Server, d *dimension, pos world.Pos, b world.Block) {
		if !s.cropLit(d, pos) {
			return
		}
		age, ok := intProp(b, "age")
		if !ok || age >= maxAge {
			return
		}
		name, _ := world.StateOwner(b)
		speed := cropGrowthSpeed(s, d, pos, name)
		if s.rng.intn(int(25.0/speed)+1) == 0 {
			if nb, ok := world.WithProperty(b, "age", strconv.Itoa(age+1)); ok {
				s.setNaturalBlock(d, pos, nb)
			}
		}
	}
}

// torchflowerRule is TorchflowerCropBlock.randomTick: an extra
// nextInt(3)==0 pre-gate over the ordinary crop path, and reaching age 2
// places the torchflower flower itself — the AGE_1 property tops out at
// 1 while getMaxAge is 2, so maturity morphs the block.
func torchflowerRule(s *Server, d *dimension, pos world.Pos, b world.Block) {
	if s.rng.intn(3) != 0 {
		return
	}
	if !s.cropLit(d, pos) {
		return
	}
	age, ok := intProp(b, "age")
	if !ok || age >= 2 {
		return
	}
	speed := cropGrowthSpeed(s, d, pos, "torchflower_crop")
	if s.rng.intn(int(25.0/speed)+1) != 0 {
		return
	}
	if age+1 >= 2 {
		if nb, ok := world.BlockStateOf("torchflower", nil); ok {
			s.setNaturalBlock(d, pos, nb)
		}
		return
	}
	if nb, ok := world.WithProperty(b, "age", strconv.Itoa(age+1)); ok {
		s.setNaturalBlock(d, pos, nb)
	}
}

// stemRule is StemBlock.randomTick: a maxed-out stem spends its
// successful growth roll on fruiting instead of aging.
func stemRule(fruit, attachedStem, supportTag string) func(*Server, *dimension, world.Pos, world.Block) {
	return func(s *Server, d *dimension, pos world.Pos, b world.Block) {
		if !s.cropLit(d, pos) {
			return
		}
		name, _ := world.StateOwner(b)
		if s.rng.intn(int(25.0/cropGrowthSpeed(s, d, pos, name))+1) != 0 {
			return
		}
		age, ok := intProp(b, "age")
		if !ok {
			return
		}
		if age < 7 {
			if nb, ok := world.WithProperty(b, "age", strconv.Itoa(age+1)); ok {
				s.setNaturalBlock(d, pos, nb)
			}
			return
		}
		dir := cardinalDirs[s.rng.intn(4)]
		fruitPos := world.Pos{X: pos.X + dir.dx, Y: pos.Y, Z: pos.Z + dir.dz}
		under := world.Pos{X: fruitPos.X, Y: fruitPos.Y - 1, Z: fruitPos.Z}
		if s.rtBlock(d, fruitPos) != world.Air {
			return
		}
		underName, _ := world.StateOwner(s.rtBlock(d, under))
		if !blockTagHas(supportTag, underName) {
			return
		}
		if fb, ok := world.BlockStateOf(fruit, nil); ok {
			s.setNaturalBlock(d, fruitPos, fb)
		}
		if ab, ok := world.BlockStateOf(attachedStem, map[string]string{"facing": dir.name}); ok {
			s.setNaturalBlock(d, pos, ab)
		}
	}
}

func wartRule(s *Server, d *dimension, pos world.Pos, b world.Block) {
	age, ok := intProp(b, "age")
	if !ok || age >= 3 {
		return
	}
	if s.rng.intn(10) == 0 {
		if nb, ok := world.WithProperty(b, "age", strconv.Itoa(age+1)); ok {
			s.setNaturalBlock(d, pos, nb)
		}
	}
}

func cocoaRule(s *Server, d *dimension, pos world.Pos, b world.Block) {
	// Vanilla rolls before reading the age.
	if s.rng.intn(5) != 0 {
		return
	}
	if age, ok := intProp(b, "age"); ok && age < 2 {
		if nb, ok := world.WithProperty(b, "age", strconv.Itoa(age+1)); ok {
			s.setNaturalBlock(d, pos, nb)
		}
	}
}

func berryRule(s *Server, d *dimension, pos world.Pos, b world.Block) {
	age, ok := intProp(b, "age")
	if !ok || age >= 3 {
		return
	}
	if s.rng.intn(5) != 0 {
		return
	}
	// The brightness probe runs at the cell above; a solid block there
	// reads dark even when the column over it is open.
	above := world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	if s.rtBlock(d, above) != world.Air || !s.cropLit(d, above) {
		return
	}
	if nb, ok := world.WithProperty(b, "age", strconv.Itoa(age+1)); ok {
		s.setNaturalBlock(d, pos, nb)
	}
}

// pitcherRule is PitcherCropBlock.randomTick + grow. Only the lower
// half random-ticks (isRandomlyTicking requires half=lower and
// age<4), and a growth roll spends one "grow by 1" step — the same
// path bonemeal takes with a larger delta.
func pitcherRule(s *Server, d *dimension, pos world.Pos, b world.Block) {
	if half, ok := world.StateProperty(b, "half"); !ok || half != "lower" {
		return
	}
	age, ok := intProp(b, "age")
	if !ok {
		return
	}
	name, _ := world.StateOwner(b)
	if s.rng.intn(int(25.0/cropGrowthSpeed(s, d, pos, name))+1) != 0 {
		return
	}
	newAge := age + 1
	if newAge > 4 {
		newAge = 4
	}
	// canGrow: not already maxed, lit, room for the upper half once the
	// new age makes the plant double-tall.
	if age >= 4 || !s.cropLit(d, pos) {
		return
	}
	above := world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	if above.Y >= d.minY+d.height {
		return
	}
	if newAge >= 3 {
		ab := s.rtBlock(d, above)
		abName, _ := world.StateOwner(ab)
		if ab != world.Air && abName != "pitcher_crop" {
			return
		}
	}
	nb, ok := world.WithProperty(b, "age", strconv.Itoa(newAge))
	if !ok {
		return
	}
	s.setNaturalBlock(d, pos, nb)
	if newAge >= 3 {
		if ub, ok := world.WithProperty(nb, "half", "upper"); ok {
			s.setNaturalBlock(d, above, ub)
		}
	}
}

// farmlandTrample is FarmlandBlock.fallOn's trample half — it runs
// BEFORE super.fallOn's damage tail: a landing LivingEntity whose
// bounding volume (bbW²·bbH) exceeds 0.512 turns the farmland to dirt
// when level.getRandom().nextFloat() < fallDistance-0.5. Non-player
// entities need the mob_griefing rule; a player always passes that
// gate. turnToDirt writes the dirt state (with neighbor updates) and
// posts GameEvent.BLOCK_CHANGE.
func (s *Server) farmlandTrample(d *dimension, pos world.Pos, x, y, z float64, isPlayer bool, vol, fallDist float64) {
	if !isPlayer && !s.gameRuleBool("minecraft:mob_griefing") {
		return
	}
	if vol <= 0.512 || rand.Float64() >= fallDist-0.5 {
		return
	}
	if dirt, ok := world.BlockStateOf("dirt", nil); ok {
		s.pushEntitiesUpFarmland(d, pos)
		s.setNaturalBlock(d, pos, dirt)
	}
	s.emitGameEvent(d, "block_change", x, y, z, nil, "")
}

// pushEntitiesUpFarmland is the pushEntitiesUp arm of
// FarmlandBlock.turnToDirt: the gained collision volume between the
// 15/16 farmland top and the full dirt top lifts every entity whose
// box intersects the slab [pos.y+0.9375, pos.y+1] onto the new surface
// via teleportRelative(0, 1+collide, 0) — the collide arm lands the
// box exactly on the slab top. Grass/mycelium reverts never reach
// here: same-shaped turns leave the ONLY_SECOND join empty.
func (s *Server) pushEntitiesUpFarmland(d *dimension, pos world.Pos) {
	slabMinY, slabTop := float64(pos.Y)+0.9375, float64(pos.Y)+1
	push := func(ex, ey, ez, half, height float64) (float64, bool) {
		if ex-half >= float64(pos.X)+1 || ex+half <= float64(pos.X) {
			return 0, false
		}
		if ez-half >= float64(pos.Z)+1 || ez+half <= float64(pos.Z) {
			return 0, false
		}
		if ey >= slabTop || ey+height <= slabMinY {
			return 0, false
		}
		dm := slabTop - (ey + 1)
		if dm > 0 {
			dm = 0
		} else if dm < -1 {
			dm = -1
		}
		return 1 + dm, true
	}
	for _, e := range s.entities.All() {
		if e.presenceOnly || e.Dying || e.Dim != d.name {
			continue
		}
		ptr, ok := s.entities.GetPtr(e.ID)
		if !ok {
			continue
		}
		bb := entityBBDims(ptr)
		if dy, ok := push(ptr.X, ptr.Y, ptr.Z, bb[0], bb[1]); ok {
			ptr.Y += dy
			ptr.PosDirty = true
			ptr.NeedsSync = true
		}
	}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dimension() != d {
			return true
		}
		x, y, z, yaw, pitch, og := sess.Pos()
		if dy, ok := push(x, y, z, 0.3, 1.8); ok {
			sess.setPos(x, y+dy, z, yaw, pitch, og)
		}
		return true
	})
}

// farmlandRule is FarmlandBlock.randomTick: moisture tracking and the
// dry-out revert to dirt.
func farmlandRule(s *Server, d *dimension, pos world.Pos, b world.Block) {
	moisture, _ := intProp(b, "moisture")
	// FarmlandBlock.randomTick hydrates from water or rain at the cell above.
	above := posAbove(pos, 1)
	rain := s.fireRainingAt(d, above) && s.precipitationAt(d, above) == "rain"
	if farmlandNearWater(s, d, pos) || rain {
		if moisture < 7 {
			if nb, ok := world.WithProperty(b, "moisture", "7"); ok {
				s.setNaturalBlock(d, pos, nb)
			}
		}
		return
	}
	if moisture > 0 {
		if nb, ok := world.WithProperty(b, "moisture", strconv.Itoa(moisture-1)); ok {
			s.setNaturalBlock(d, pos, nb)
		}
		return
	}
	aboveName, _ := world.StateOwner(s.rtBlock(d, above))
	if !blockTagHas("maintains_farmland", aboveName) {
		if dirt, ok := world.BlockStateOf("dirt", nil); ok {
			s.pushEntitiesUpFarmland(d, pos)
			s.setNaturalBlock(d, pos, dirt)
		}
		s.emitGameEvent(d, "block_change",
			float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, nil, "")
	}
}

// farmlandNearWater is FarmlandBlock.isNearWater: water inside the
// 9x2x9 box from (-4,0,-4) to (4,1,4) around the farmland. The fluid
// state check covers both water blocks and waterlogged states.
func farmlandNearWater(s *Server, d *dimension, pos world.Pos) bool {
	for dx := -4; dx <= 4; dx++ {
		for dy := 0; dy <= 1; dy++ {
			for dz := -4; dz <= 4; dz++ {
				nb := s.rtBlock(d, world.Pos{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz})
				if kind, _, ok := fluidOf(nb); ok && kind == fluidWater {
					return true
				}
				if wl, ok := world.StateProperty(nb, "waterlogged"); ok && wl == "true" {
					return true
				}
			}
		}
	}
	return false
}

// cropLit approximates hasSufficientLight: a skylight dimension whose
// column above the position holds nothing but air and light-transmitting
// plants reads as raw brightness 15. Vanilla walks real light data
// (non-occluding blocks like crops and the pitcher's own upper half let
// sky light through); mc-go substitutes the plant set for that data.
// Opaque-block and block-light edge cases (a torch-lit cellar crop)
// stay dark here — flagged in GOAL.md as a light-engine dependency.
func (s *Server) cropLit(d *dimension, pos world.Pos) bool {
	if !d.skylight {
		return false
	}
	top := d.minY + d.height
	for y := pos.Y + 1; y < top; {
		// Whole sections that hold nothing opaque skip without a read;
		// the flag comes from the same cached scan the tickable check
		// uses, so the walk is short except where buildings exist.
		sy := floorDiv(y-d.minY, 16)
		if s.sectionFlags(d, randSectionKey{
			dim: d.name,
			x:   floorDiv(pos.X, 16),
			y:   sy,
			z:   floorDiv(pos.Z, 16),
		})&secOpaque == 0 {
			y = d.minY + (sy+1)*16
			continue
		}
		end := d.minY + (sy+1)*16
		if end > top {
			end = top
		}
		for ; y < end; y++ {
			nb := s.rtBlock(d, world.Pos{X: pos.X, Y: y, Z: pos.Z})
			if nb == world.Air {
				continue
			}
			if name, _ := world.StateOwner(nb); lightPassable[name] {
				continue
			}
			return false
		}
	}
	return true
}

// turfRule is SpreadingSnowyBlock.randomTick (grass_block, mycelium):
// a shaded or submerged turf block reverts to its base (dirt); a lit
// one spends four rolls reaching into a 3x5x3 box and converts base
// blocks that can propagate. Podzol is only a SnowyBlock — it neither
// spreads nor dies — and nylium spreads only via bonemeal, so neither
// gets this rule.
func turfRule(base string) func(*Server, *dimension, world.Pos, world.Block) {
	return func(s *Server, d *dimension, pos world.Pos, b world.Block) {
		above := world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
		if !spreadSurvives(s, d, above) {
			if baseBlock, ok := world.BlockStateOf(base, nil); ok {
				s.setNaturalBlock(d, pos, baseBlock)
			}
			return
		}
		// getMaxLocalRawBrightness(above) >= 9.
		if s.rtBlock(d, above) != world.Air || !s.cropLit(d, above) {
			return
		}
		selfName, _ := world.StateOwner(b)
		for i := 0; i < 4; i++ {
			tp := world.Pos{
				X: pos.X + s.rng.intn(3) - 1,
				Y: pos.Y + s.rng.intn(5) - 3,
				Z: pos.Z + s.rng.intn(3) - 1,
			}
			tname, _ := world.StateOwner(s.rtBlock(d, tp))
			if tname != base {
				continue
			}
			// canPropagate: the target survives and its above cell isn't
			// water.
			tab := world.Pos{X: tp.X, Y: tp.Y + 1, Z: tp.Z}
			if !spreadSurvives(s, d, tab) {
				continue
			}
			if kind, _, ok := fluidOf(s.rtBlock(d, tab)); ok && kind == fluidWater {
				continue
			}
			snowy := false
			if n, _ := world.StateOwner(s.rtBlock(d, tab)); n == "snow" {
				snowy = true
			}
			if tb, ok := world.BlockStateOf(selfName, map[string]string{"snowy": strconv.FormatBool(snowy)}); ok {
				s.setNaturalBlock(d, tp, tb)
			}
		}
	}
}

// nyliumRule is NyliumBlock.randomTick: shaded nylium reverts to
// netherrack. There is no spread — vanilla nylium conversion is a
// bonemeal path.
func nyliumRule(s *Server, d *dimension, pos world.Pos, _ world.Block) {
	above := world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	if !spreadSurvives(s, d, above) {
		if base, ok := world.BlockStateOf("netherrack", nil); ok {
			s.setNaturalBlock(d, pos, base)
		}
	}
}

// spreadSurvives approximates SpreadingSnowyBlock.canStayAlive for the
// cell above a turf block: air, one-layer snow, and light-transmitting
// blocks keep it alive; full fluids and opaque blocks kill it.
func spreadSurvives(s *Server, d *dimension, above world.Pos) bool {
	nb := s.rtBlock(d, above)
	if nb == world.Air {
		return true
	}
	name, _ := world.StateOwner(nb)
	if name == "snow" {
		if l, ok := intProp(nb, "layers"); ok && l == 1 {
			return true
		}
	}
	if kind, lvl, ok := fluidOf(nb); ok && kind == fluidWater && lvl == 0 {
		return false
	}
	return lightPassable[name]
}

// lightPassable are the modeled blocks that let sky light through —
// the random-tick rule set minus the terrain blocks in it, plus the
// common transparent blocks a turf shade-check can meet.
var lightPassable = map[string]bool{
	"wheat":                 true,
	"carrots":               true,
	"potatoes":              true,
	"beetroots":             true,
	"torchflower_crop":      true,
	"melon_stem":            true,
	"pumpkin_stem":          true,
	"attached_melon_stem":   true,
	"attached_pumpkin_stem": true,
	"pitcher_crop":          true,
	"cocoa":                 true,
	"sweet_berry_bush":      true,
	"nether_wart":           true,
	"short_grass":           true,
	"tall_grass":            true,
	"fern":                  true,
	"large_fern":            true,
	"vine":                  true,
	"glow_lichen":           true,
	"torch":                 true,
	"wall_torch":            true,
	"soul_torch":            true,
	"soul_wall_torch":       true,
	"redstone_torch":        true,
	"redstone_wall_torch":   true,
	"dandelion":             true,
	"poppy":                 true,
	"torchflower":           true,
	"oak_sapling":           true,
	"spruce_sapling":        true,
	"birch_sapling":         true,
	"jungle_sapling":        true,
	"acacia_sapling":        true,
	"dark_oak_sapling":      true,
	"pale_oak_sapling":      true,
	"cherry_sapling":        true,
	"oak_leaves":            true,
	"spruce_leaves":         true,
	"birch_leaves":          true,
	"jungle_leaves":         true,
	"acacia_leaves":         true,
	"dark_oak_leaves":       true,
	"cherry_leaves":         true,
	"azalea_leaves":         true,
	"water":                 true,
	"ladder":                true,
	"lantern":               true,
	"glass":                 true,
	"white_stained_glass":   true,
	"cobweb":                true,
	"tripwire":              true,
	"iron_bars":             true,
	"glass_pane":            true,
	"brown_mushroom":        true,
	"red_mushroom":          true,
	"sugar_cane":            true,
	"cactus":                true,
	"cactus_flower":         true,
	"bamboo":                true,
	"bamboo_sapling":        true,
	"chorus_flower":         true,
	"chorus_plant":          true,
}

// cropGrowthSpeed is CropBlock.getGrowthSpeed: 1.0 plus farmland scores
// across the 3x3 one block down (moisture>0 scores 3, dry 1, off-axis
// quarters), halved when the same crop borders on both horizontal axes
// or a diagonal.
func cropGrowthSpeed(s *Server, d *dimension, pos world.Pos, cropName string) float64 {
	f := 1.0
	by := pos.Y - 1
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			g := 0.0
			nb := s.rtBlock(d, world.Pos{X: pos.X + dx, Y: by, Z: pos.Z + dz})
			nname, _ := world.StateOwner(nb)
			if blockTagHas("grows_crops", nname) {
				g = 1.0
				if m, ok := intProp(nb, "moisture"); ok && m > 0 {
					g = 3.0
				}
			}
			if dx != 0 || dz != 0 {
				g /= 4.0
			}
			f += g
		}
	}
	westEast := cropAtIs(s, d, world.Pos{X: pos.X - 1, Y: pos.Y, Z: pos.Z}, cropName) ||
		cropAtIs(s, d, world.Pos{X: pos.X + 1, Y: pos.Y, Z: pos.Z}, cropName)
	northSouth := cropAtIs(s, d, world.Pos{X: pos.X, Y: pos.Y, Z: pos.Z - 1}, cropName) ||
		cropAtIs(s, d, world.Pos{X: pos.X, Y: pos.Y, Z: pos.Z + 1}, cropName)
	if westEast && northSouth {
		f /= 2
	} else {
		diag := cropAtIs(s, d, world.Pos{X: pos.X - 1, Y: pos.Y, Z: pos.Z - 1}, cropName) ||
			cropAtIs(s, d, world.Pos{X: pos.X + 1, Y: pos.Y, Z: pos.Z - 1}, cropName) ||
			cropAtIs(s, d, world.Pos{X: pos.X + 1, Y: pos.Y, Z: pos.Z + 1}, cropName) ||
			cropAtIs(s, d, world.Pos{X: pos.X - 1, Y: pos.Y, Z: pos.Z + 1}, cropName)
		if diag {
			f /= 2
		}
	}
	return f
}

func cropAtIs(s *Server, d *dimension, pos world.Pos, name string) bool {
	n, _ := world.StateOwner(s.rtBlock(d, pos))
	return n == name
}

// Column growers, each one measured against vanilla's bytecode:
//
//   - SugarCaneBlock: air above and a column shorter than 3; the top
//     cane's age clocks to 15 and then spends a tick placing a fresh
//     cane above and resetting its own age to 0.
//   - CactusBlock: same column growth, plus an age==8 side roll that
//     places a cactus_flower on top (nextDouble < 0.25 once the column
//     is three tall, else 0.1) when a cactus could survive there.
//   - BambooStalkBlock: only stage==0 stalks tick; a nextInt(3)==0 roll,
//     air above, brightness>=9 at the cell above, and a column under 16
//     feeds growBamboo, which writes the new top with the leaf cascade
//     (top=large, demoting the stalks below) and an age/stage ramp that
//     stops the column growing.
//   - BambooSaplingBlock: nextInt(3)==0, air and brightness>=9 above,
//     then a small-leaf bamboo stalk appears above.
//   - ChorusFlowerBlock: up-growth or sideways branching with the
//     plant-block rewrite, ending in the age-5 dead flower.

// isAirCell is Level.isEmptyBlock: air and its dimension variants.
func isAirCell(b world.Block) bool {
	if b == world.Air {
		return true
	}
	name, _ := world.StateOwner(b)
	return name == "cave_air" || name == "void_air"
}

func isBlockNamed(b world.Block, name string) bool {
	n, _ := world.StateOwner(b)
	return n == name
}

func posBelow(pos world.Pos, n int) world.Pos {
	return world.Pos{X: pos.X, Y: pos.Y - n, Z: pos.Z}
}

func posAbove(pos world.Pos, n int) world.Pos {
	return world.Pos{X: pos.X, Y: pos.Y + n, Z: pos.Z}
}

// vineHeadRule is GrowingPlantHeadBlock.randomTick: at age < 25 a 10%
// roll grows the vine one cell along its growth direction into air;
// the new head carries age+1 (state.cycle) — plus an 11% berries roll
// for cave vines. The old head's conversion to its plant body comes
// from the write's neighbor pass: its updateShape sees a vine ahead.
func vineHeadRule(head, body string, growthDy int, cave bool) func(*Server, *dimension, world.Pos, world.Block) {
	prob := 0.1
	growInto := isAirCell
	if head == "kelp" {
		prob = 0.14
		growInto = func(b world.Block) bool { return isBlockNamed(b, "water") }
	}
	return func(s *Server, d *dimension, pos world.Pos, _ world.Block) {
		b := d.w.GetBlock(pos)
		age, ok := intProp(b, "age")
		if !ok || age >= 25 || s.rng.nextDouble() >= prob {
			return
		}
		ahead := world.Pos{X: pos.X, Y: pos.Y + growthDy, Z: pos.Z}
		if !growInto(d.w.GetBlock(ahead)) {
			return
		}
		nb, ok := world.WithProperty(b, "age", strconv.Itoa(age+1))
		if !ok {
			return
		}
		// CaveVinesBlock.getGrowIntoState independently assigns BERRIES
		// from a fresh 24-bit float draw (< 0.11f), including false:
		// the parent's fruit does not carry onto the new tip.
		if cave {
			berries := float32(s.rng.next()>>40)/(1<<24) < float32(0.11)
			if bb, ok := world.WithProperty(nb, "berries", strconv.FormatBool(berries)); ok {
				nb = bb
			}
		}
		s.setNaturalBlock(d, ahead, nb)
	}
}

// BuddingAmethystBlock.randomTick: 20% of the time pick a random
// face; air or a full water source there sprouts a small bud, a bud
// already facing that direction climbs a stage (small → medium →
// large → cluster). The write is setBlockAndUpdate — the bud carries
// facing=dir and waterlogged=was-water.
var amethystFacings = [6]string{"down", "up", "west", "east", "north", "south"}

func amethystBudRule(s *Server, d *dimension, pos world.Pos, _ world.Block) {
	if s.rng.intn(5) != 0 {
		return
	}
	faces := neighbourPositions(pos)
	di := s.rng.intn(6)
	np := faces[di]
	nb := d.w.GetBlock(np)
	name, _ := world.StateOwner(nb)
	face, _ := world.StateProperty(nb, "facing")
	facing := amethystFacings[di]
	var next string
	switch {
	case isAirCell(nb) || isWaterSource(nb):
		next = "small_amethyst_bud"
	case name == "small_amethyst_bud" && face == facing:
		next = "medium_amethyst_bud"
	case name == "medium_amethyst_bud" && face == facing:
		next = "large_amethyst_bud"
	case name == "large_amethyst_bud" && face == facing:
		next = "amethyst_cluster"
	default:
		return
	}
	ns, ok := world.BlockStateOf(next, map[string]string{
		"facing":      facing,
		"waterlogged": strconv.FormatBool(cellFluidIsWater(nb)),
	})
	if !ok {
		return
	}
	s.setNaturalBlock(d, np, ns)
}

// isWaterSource is state.is(WATER) && FluidState.isFull: a water
// block at level 0.
func isWaterSource(b world.Block) bool {
	k, lv, ok := fluidOf(b)
	return ok && k == fluidWater && lv == 0
}

// cellFluidIsWater is FluidState.is(WATER): any water block or a
// waterlogged block.
func cellFluidIsWater(b world.Block) bool {
	if k, _, ok := fluidOf(b); ok {
		return k == fluidWater
	}
	if wl, ok := world.StateProperty(b, "waterlogged"); ok {
		return wl == "true"
	}
	return false
}

// MushroomBlock.randomTick: 1/25 roll spreads into a random-walk
// target (dx,dz∈±1, dy = nextInt(2)-nextInt(2)); four hops, each
// moving the walk when the target is empty and survivable, then the
// spread lands if the final target survives. A 9×3×9 box holding 5+
// of the same mushroom aborts the whole thing.
func mushroomRule(s *Server, d *dimension, pos world.Pos, _ world.Block) {
	b := d.w.GetBlock(pos)
	name, ok := world.StateOwner(b)
	if !ok || s.rng.intn(25) != 0 {
		return
	}
	s.mushroomSpread(d, pos, b, name)
}

// mushroomSpread is the spread body of MushroomBlock.randomTick,
// past the 1/25 roll.
func (s *Server) mushroomSpread(d *dimension, pos world.Pos, b world.Block, name string) {
	same := 0
	for dy := -1; dy <= 1; dy++ {
		for dx := -4; dx <= 4; dx++ {
			for dz := -4; dz <= 4; dz++ {
				if isBlockNamed(d.w.GetBlock(world.Pos{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}), name) {
					same++
					if same >= 5 {
						return
					}
				}
			}
		}
	}
	// Vanilla rolls four candidates around the seed itself, not a
	// chained walk: mutable takes the last candidate that survives, so
	// the spread lands within ±1 of the seed rather than drifting.
	land := pos
	for i := 0; i < 4; i++ {
		target := mushroomOffset(s, pos)
		if isAirCell(d.w.GetBlock(target)) && mushroomCanSurvive(s, d, target) {
			land = target
		}
	}
	if isAirCell(d.w.GetBlock(land)) && mushroomCanSurvive(s, d, land) {
		s.setNaturalBlock(d, land, b)
	}
}

func mushroomOffset(s *Server, p world.Pos) world.Pos {
	return world.Pos{
		X: p.X + s.rng.intn(3) - 1,
		Y: p.Y + s.rng.intn(2) - s.rng.intn(2),
		Z: p.Z + s.rng.intn(3) - 1,
	}
}

// mushroomCanSurvive is MushroomBlock.canSurvive: an
// overrides_mushroom_light_requirement soil (mycelium, podzol,
// nylium) always allows it; otherwise the cell needs
// getRawBrightness < 13 on a solid-render floor. mc-go has no light
// engine — brightness is approximated by sky exposure alone: any
// non-translucent block overhead keeps the cell dark.
func mushroomCanSurvive(s *Server, d *dimension, pos world.Pos) bool {
	below := d.w.GetBlock(posBelow(pos, 1))
	name, _ := world.StateOwner(below)
	if blockTagHas("overrides_mushroom_light_requirement", name) {
		return true
	}
	if !d.skylight {
		return !world.NonSolid(below)
	}
	// Sky-exposed reads as full daylight (15 ≥ 13): a mushroom there
	// fails canSurvive, which is also what pops sunlit mushrooms on a
	// neighbour write. An opaque block overhead reads as darkness.
	for y := pos.Y; y < d.minY+d.height; y++ {
		if !world.NonSolid(d.w.GetBlock(world.Pos{X: pos.X, Y: y, Z: pos.Z})) {
			return !world.NonSolid(below)
		}
	}
	return false
}

func sugarCaneRule(s *Server, d *dimension, pos world.Pos, _ world.Block) {
	// Grower rules read and write back block state, so they read the
	// live world: the section snapshot can predate a same-tick write
	// and WithProperty would clobber it back.
	b := d.w.GetBlock(pos)
	above := posAbove(pos, 1)
	if !isAirCell(d.w.GetBlock(above)) {
		return
	}
	height := 1
	for isBlockNamed(d.w.GetBlock(posBelow(pos, height)), "sugar_cane") {
		height++
	}
	if height >= 3 {
		return
	}
	age, ok := intProp(b, "age")
	if !ok {
		return
	}
	if age == 15 {
		if nb, ok := world.BlockStateOf("sugar_cane", nil); ok {
			s.setNaturalBlock(d, above, nb)
		}
		if nb, ok := world.WithProperty(b, "age", "0"); ok {
			s.setNaturalBlock(d, pos, nb)
		}
		return
	}
	if nb, ok := world.WithProperty(b, "age", strconv.Itoa(age+1)); ok {
		s.setNaturalBlock(d, pos, nb)
	}
}

// cactusRule is CactusBlock.randomTick: identical column growth to
// sugar cane, plus the age-8 flower roll on top.
func cactusRule(s *Server, d *dimension, pos world.Pos, _ world.Block) {
	b := d.w.GetBlock(pos)
	above := posAbove(pos, 1)
	if !isAirCell(d.w.GetBlock(above)) {
		return
	}
	age, ok := intProp(b, "age")
	if !ok {
		return
	}
	// Vanilla's loop exits early: a column that reaches three while the
	// top is at age 15 stops ticking instead of growing.
	height := 1
	for isBlockNamed(d.w.GetBlock(posBelow(pos, height)), "cactus") {
		height++
		if height == 3 && age == 15 {
			return
		}
	}
	if age == 8 {
		chance := 0.1
		if height >= 3 {
			chance = 0.25
		}
		if cactusCanSurvive(s, d, above) && s.rng.nextDouble() < chance {
			if fb, ok := world.BlockStateOf("cactus_flower", nil); ok {
				s.setNaturalBlock(d, above, fb)
			}
		}
	} else if age == 15 && height < 3 {
		if nb, ok := world.BlockStateOf("cactus", nil); ok {
			s.setNaturalBlock(d, above, nb)
		}
		if nb, ok := world.WithProperty(b, "age", "0"); ok {
			s.setNaturalBlock(d, pos, nb)
		}
	}
	if age < 15 {
		if nb, ok := world.WithProperty(b, "age", strconv.Itoa(age+1)); ok {
			s.setNaturalBlock(d, pos, nb)
		}
	}
}

// cactusCanSurvive is CactusBlock.canSurvive for a cell above a cactus:
// no solid or lava-bearing neighbor horizontally, and the block below
// is a cactus or in #supports_cactus with a non-liquid cell above.
func cactusCanSurvive(s *Server, d *dimension, pos world.Pos) bool {
	for _, dir := range cardinalDirs {
		nb := d.w.GetBlock(world.Pos{X: pos.X + dir.dx, Y: pos.Y, Z: pos.Z + dir.dz})
		if !world.NonSolid(nb) {
			return false
		}
		if kind, _, ok := fluidOf(nb); ok && kind == fluidLava {
			return false
		}
	}
	below := d.w.GetBlock(posBelow(pos, 1))
	if !isBlockNamed(below, "cactus") {
		name, _ := world.StateOwner(below)
		if !blockTagHas("supports_cactus", name) {
			return false
		}
	}
	// The liquid-above check applies whether the support is a cactus
	// or the tag.
	if _, _, ok := fluidOf(d.w.GetBlock(posAbove(pos, 1))); ok {
		return false
	}
	return true
}

func bambooStalkRule(s *Server, d *dimension, pos world.Pos, _ world.Block) {
	b := d.w.GetBlock(pos)
	if stage, ok := intProp(b, "stage"); !ok || stage != 0 {
		return
	}
	if s.rng.intn(3) != 0 {
		return
	}
	above := posAbove(pos, 1)
	if !isAirCell(d.w.GetBlock(above)) {
		return
	}
	// getRawBrightness(above) >= 9, same column approximation as crops.
	if !s.cropLit(d, above) {
		return
	}
	// getHeightBelowUpToMax counts the bamboo run below, capped at 16.
	height := 1
	for height <= 16 && isBlockNamed(d.w.GetBlock(posBelow(pos, height)), "bamboo") {
		height++
	}
	if height >= 16 {
		return
	}
	s.growBamboo(d, pos, b, height)
}

// growBamboo is BambooStalkBlock.growBamboo: the new top's leaves are
// large when the stalk below already carried leaves (demoting it and
// the one beneath), small over anything else; age propagates the thick
// flag, and stage flips to 1 — the terminal state that stops ticking —
// always at height 15, or 25% of the time once height reaches 11.
func (s *Server) growBamboo(d *dimension, pos world.Pos, state world.Block, height int) {
	below1Pos := posBelow(pos, 1)
	below2Pos := posBelow(pos, 2)
	below1 := d.w.GetBlock(below1Pos)
	below2 := d.w.GetBlock(below2Pos)

	leaves := "none"
	if height >= 1 {
		l1, _ := world.StateProperty(below1, "leaves")
		if !isBlockNamed(below1, "bamboo") || l1 == "none" {
			leaves = "small"
		} else {
			leaves = "large"
			if isBlockNamed(below2, "bamboo") {
				if nb, ok := world.WithProperty(below1, "leaves", "small"); ok {
					s.setNaturalBlock(d, below1Pos, nb)
				}
				if nb, ok := world.WithProperty(below2, "leaves", "none"); ok {
					s.setNaturalBlock(d, below2Pos, nb)
				}
			}
		}
	}
	age, _ := intProp(state, "age")
	newAge := 0
	if age == 1 || isBlockNamed(below2, "bamboo") {
		newAge = 1
	}
	newStage := 0
	if height == 15 || (height >= 11 && s.rng.nextDouble() < 0.25) {
		newStage = 1
	}
	if nb, ok := world.BlockStateOf("bamboo", map[string]string{
		"age":    strconv.Itoa(newAge),
		"leaves": leaves,
		"stage":  strconv.Itoa(newStage),
	}); ok {
		s.setNaturalBlock(d, posAbove(pos, 1), nb)
	}
	// BambooStalkBlock.updateShape cycles a stalk's age to 1 when the
	// stalk above it is thicker — landing an age-1 top ripples down the
	// whole column through the neighbor-update chain. Reads are live,
	// not the section snapshot, so the walk sees the writes above.
	for bp := pos; ; bp = posBelow(bp, 1) {
		bb := d.w.GetBlock(bp)
		ab := d.w.GetBlock(posAbove(bp, 1))
		if !isBlockNamed(bb, "bamboo") || !isBlockNamed(ab, "bamboo") {
			break
		}
		ba, _ := intProp(bb, "age")
		aa, _ := intProp(ab, "age")
		if aa <= ba {
			break
		}
		if nb, ok := world.WithProperty(bb, "age", "1"); ok {
			s.setNaturalBlock(d, bp, nb)
		}
	}
}

// bambooSaplingRule is BambooSaplingBlock.randomTick: a 1-in-3 roll on
// a lit sapling with air above grows the first stalk. The sapling's
// own updateShape turns it into the column's base stalk when a stalk
// lands above — reproduced here as a direct write.
func bambooSaplingRule(s *Server, d *dimension, pos world.Pos, _ world.Block) {
	if s.rng.intn(3) != 0 {
		return
	}
	above := posAbove(pos, 1)
	if !isAirCell(d.w.GetBlock(above)) || !s.cropLit(d, above) {
		return
	}
	if nb, ok := world.BlockStateOf("bamboo", map[string]string{"leaves": "small"}); ok {
		s.setNaturalBlock(d, above, nb)
	}
	if nb, ok := world.BlockStateOf("bamboo", nil); ok {
		s.setNaturalBlock(d, pos, nb)
	}
}

// chorusFlowerRule is ChorusFlowerBlock.randomTick: the flower grows
// one block up when it sits on end stone, air, or a long-enough plant
// column; otherwise it scatters same-age+1 flowers sideways into air
// pockets and turns itself into a plant block — or dies at age 5 when
// nothing it tried was placeable.
func chorusFlowerRule(s *Server, d *dimension, pos world.Pos, _ world.Block) {
	b := d.w.GetBlock(pos)
	above := posAbove(pos, 1)
	if !isAirCell(d.w.GetBlock(above)) || above.Y >= d.minY+d.height {
		return
	}
	age, ok := intProp(b, "age")
	if !ok || age >= 5 {
		return
	}
	canGrowUp := false
	hasGround := false
	below := d.w.GetBlock(posBelow(pos, 1))
	belowName, _ := world.StateOwner(below)
	if blockTagHas("supports_chorus_flower", belowName) {
		canGrowUp = true
	} else if belowName == "chorus_plant" {
		// Walk down up to four plant blocks looking for the support
		// block; a run of 2+ plants gives a length-scaled chance to
		// grow up instead of branching.
		run := 1
		for i := 0; i < 4; i++ {
			bb := d.w.GetBlock(posBelow(pos, run+1))
			bbName, _ := world.StateOwner(bb)
			if bbName == "chorus_plant" {
				run++
			} else {
				if blockTagHas("supports_chorus_flower", bbName) {
					hasGround = true
				}
				break
			}
		}
		// Bytecode: run < 2 always grows up; longer runs grow only when
		// run <= nextInt(5 grounded / 4 otherwise), so run >= 5 never
		// climbs and the flower must branch or die instead.
		if run < 2 {
			canGrowUp = true
		} else {
			bound := 4
			if hasGround {
				bound = 5
			}
			if run <= s.rng.intn(bound) {
				canGrowUp = true
			}
		}
	} else if isAirCell(below) {
		canGrowUp = true
	}
	if canGrowUp && s.chorusNeighborsEmpty(d, above, -1) &&
		isAirCell(d.w.GetBlock(posAbove(pos, 2))) {
		// Vanilla writes the plant first and lets updateShape fix its
		// connections; without a neighbor-update pass the same end state
		// comes from placing the flower first.
		s.placeGrownChorus(d, above, age)
		s.setSilentBlock(d, pos, chorusPlantState(s, d, pos))
		return
	}
	if age < 4 {
		tries := s.rng.intn(4)
		if hasGround {
			tries++
		}
		grew := false
		for i := 0; i < tries; i++ {
			di := s.rng.intn(4)
			dir := cardinalDirs[di]
			t := world.Pos{X: pos.X + dir.dx, Y: pos.Y, Z: pos.Z + dir.dz}
			if !isAirCell(d.w.GetBlock(t)) || !isAirCell(d.w.GetBlock(posBelow(t, 1))) {
				continue
			}
			// cardinalDirs pairs each direction with its opposite, so the
			// direction back toward the flower is the paired index.
			if !s.chorusNeighborsEmpty(d, t, di^1) {
				continue
			}
			s.placeGrownChorus(d, t, age+1)
			grew = true
		}
		if grew {
			s.setSilentBlock(d, pos, chorusPlantState(s, d, pos))
		} else {
			s.placeDeadChorus(d, pos)
		}
		return
	}
	s.placeDeadChorus(d, pos)
}

// chorusNeighborsEmpty is allNeighborsEmpty: every horizontal neighbor
// of pos must be empty, except the direction the caller came from
// (-1 when there is none).
func (s *Server) chorusNeighborsEmpty(d *dimension, pos world.Pos, except int) bool {
	for i, dir := range cardinalDirs {
		if i == except {
			continue
		}
		if !isAirCell(d.w.GetBlock(world.Pos{X: pos.X + dir.dx, Y: pos.Y, Z: pos.Z + dir.dz})) {
			return false
		}
	}
	return true
}

// chorusPlantState is ChorusPlantBlock.getStateWithConnections: each of
// the six direction props is set when that neighbor is a chorus plant
// or flower, with `down` also connecting to #supports_chorus_plant.
// Reads are live: the caller has already written the flower this plant
// connects to, and vanilla reaches the same end state through its
// updateShape pass.
func chorusPlantState(s *Server, d *dimension, pos world.Pos) world.Block {
	connects := func(p world.Pos, down bool) bool {
		nb := d.w.GetBlock(p)
		if isBlockNamed(nb, "chorus_plant") || isBlockNamed(nb, "chorus_flower") {
			return true
		}
		if down {
			name, _ := world.StateOwner(nb)
			return blockTagHas("supports_chorus_plant", name)
		}
		return false
	}
	props := map[string]string{
		"down":  strconv.FormatBool(connects(posBelow(pos, 1), true)),
		"up":    strconv.FormatBool(connects(posAbove(pos, 1), false)),
		"north": strconv.FormatBool(connects(world.Pos{X: pos.X, Y: pos.Y, Z: pos.Z - 1}, false)),
		"south": strconv.FormatBool(connects(world.Pos{X: pos.X, Y: pos.Y, Z: pos.Z + 1}, false)),
		"east":  strconv.FormatBool(connects(world.Pos{X: pos.X + 1, Y: pos.Y, Z: pos.Z}, false)),
		"west":  strconv.FormatBool(connects(world.Pos{X: pos.X - 1, Y: pos.Y, Z: pos.Z}, false)),
	}
	b, _ := world.BlockStateOf("chorus_plant", props)
	return b
}

// placeGrownChorus is ChorusFlowerBlock.placeGrownFlower: the flower at
// the new position keeps the passed age, and level event 1033 plays the
// grow sound.
func (s *Server) placeGrownChorus(d *dimension, pos world.Pos, age int) {
	if nb, ok := world.BlockStateOf("chorus_flower", map[string]string{"age": strconv.Itoa(age)}); ok {
		s.setSilentBlock(d, pos, nb)
	}
	s.broadcastLevelEvent(d.name, 1033, pos, 0)
}

func (s *Server) placeDeadChorus(d *dimension, pos world.Pos) {
	if nb, ok := world.BlockStateOf("chorus_flower", map[string]string{"age": "5"}); ok {
		s.setSilentBlock(d, pos, nb)
	}
	s.broadcastLevelEvent(d.name, 1034, pos, 0)
}

func intProp(b world.Block, prop string) (int, bool) {
	v, ok := world.StateProperty(b, prop)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, false
	}
	return n, true
}

func blockTagHas(tag, name string) bool {
	for _, m := range data.BlockTags[tag] {
		if m == name {
			return true
		}
	}
	return false
}

type cardDir struct {
	name   string
	dx, dz int
}

// cardinalDirs are the four values Direction.Plane.HORIZONTAL can pick.
var cardinalDirs = []cardDir{
	{"north", 0, -1},
	{"south", 0, 1},
	{"west", -1, 0},
	{"east", 1, 0},
}

// copperFamilies are the fifteen WeatheringCopperCollection groups in
// 26.2, each listed as unaffected -> exposed -> weathered -> oxidized.
// Waxed variants are not weathering copper: they neither random-tick
// nor count in the neighbourhood scan.
var copperFamilies = [][4]string{
	{"copper_block", "exposed_copper", "weathered_copper", "oxidized_copper"},
	{"cut_copper", "exposed_cut_copper", "weathered_cut_copper", "oxidized_cut_copper"},
	{"chiseled_copper", "exposed_chiseled_copper", "weathered_chiseled_copper", "oxidized_chiseled_copper"},
	{"cut_copper_slab", "exposed_cut_copper_slab", "weathered_cut_copper_slab", "oxidized_cut_copper_slab"},
	{"cut_copper_stairs", "exposed_cut_copper_stairs", "weathered_cut_copper_stairs", "oxidized_cut_copper_stairs"},
	{"copper_door", "exposed_copper_door", "weathered_copper_door", "oxidized_copper_door"},
	{"copper_trapdoor", "exposed_copper_trapdoor", "weathered_copper_trapdoor", "oxidized_copper_trapdoor"},
	{"copper_bars", "exposed_copper_bars", "weathered_copper_bars", "oxidized_copper_bars"},
	{"copper_grate", "exposed_copper_grate", "weathered_copper_grate", "oxidized_copper_grate"},
	{"copper_bulb", "exposed_copper_bulb", "weathered_copper_bulb", "oxidized_copper_bulb"},
	{"copper_lantern", "exposed_copper_lantern", "weathered_copper_lantern", "oxidized_copper_lantern"},
	{"copper_chest", "exposed_copper_chest", "weathered_copper_chest", "oxidized_copper_chest"},
	{"copper_golem_statue", "exposed_copper_golem_statue", "weathered_copper_golem_statue", "oxidized_copper_golem_statue"},
	{"lightning_rod", "exposed_lightning_rod", "weathered_lightning_rod", "oxidized_lightning_rod"},
	{"copper_chain", "exposed_copper_chain", "weathered_copper_chain", "oxidized_copper_chain"},
}

var (
	copperNextOnce sync.Once
	copperNextMap  map[string]string
	copperStageMap map[string]int
)

// copperStage returns the weather-state ordinal of a weathering copper
// block (0=unaffected .. 3=oxidized) and whether the block is one.
func copperStage(name string) (int, bool) {
	copperNextOnce.Do(func() {
		copperNextMap = make(map[string]string)
		copperStageMap = make(map[string]int)
		for _, fam := range copperFamilies {
			for i, n := range fam {
				copperStageMap[n] = i
				if i+1 < len(fam) {
					copperNextMap[n] = fam[i+1]
				}
			}
		}
	})
	st, ok := copperStageMap[name]
	return st, ok
}

// copperRule is ChangeOverTimeBlock.changeOverTime, shared by every
// WeatheringCopper block class (they all answer the same WeatherState
// enum, so a weathered lantern still counts a bare copper block).
// changeOverTime gates on nextFloat() < 0.05688889 before running
// getNextState; getNextState scans positions within manhattan 4 —
// skipping the origin — and counts neighbours that are weathering
// copper too. A less-oxidized neighbour aborts the roll outright;
// otherwise ratio = (more+1)/(more+same+1), the final roll is
// nextFloat() < ratio^2 * chanceModifier with chanceModifier 0.75 on
// the unaffected stage and 1.0 past it, and a pass writes the next
// stage via setBlock(..., 2), keeping the state's shared properties
// (Block.withPropertiesOf).
func copperRule(s *Server, d *dimension, pos world.Pos, b world.Block) {
	if s.rng.nextDouble() >= 0.05688889 {
		return
	}
	name, ok := world.StateOwner(b)
	if !ok {
		return
	}
	stage, ok := copperStage(name)
	if !ok {
		return
	}
	more, same := 0, 0
	for dx := -4; dx <= 4; dx++ {
		for dy := -4; dy <= 4; dy++ {
			for dz := -4; dz <= 4; dz++ {
				dist := absi(dx) + absi(dy) + absi(dz)
				if dist == 0 || dist > 4 {
					continue
				}
				nn, ok := world.StateOwner(s.rtBlock(d, world.Pos{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}))
				if !ok {
					continue
				}
				o2, ok := copperStage(nn)
				if !ok {
					continue
				}
				if o2 < stage {
					return
				}
				if o2 > stage {
					more++
				} else {
					same++
				}
			}
		}
	}
	ratio := float64(more+1) / float64(more+same+1)
	mod := 1.0
	if stage == 0 {
		mod = 0.75
	}
	if s.rng.nextDouble() >= ratio*ratio*mod {
		return
	}
	next, ok := copperNextMap[name]
	if !ok {
		return
	}
	// withPropertiesOf: carry over the properties the next block
	// declares; everything else falls back to defaults.
	keep := map[string]bool{}
	for _, p := range world.BlockPropNames(next) {
		keep[p] = true
	}
	props := map[string]string{}
	for k, v := range world.BlockProperties(b) {
		if keep[k] {
			props[k] = v
		}
	}
	if nb, ok := world.BlockStateOf(next, props); ok {
		s.setSilentBlock(d, pos, nb)
	}
}

// snowMeltRule is SnowLayerBlock.randomTick: the stored block light
// must top 11, and the step into the snow cell costs 1 + the layer's
// own lightBlock (1 for partial layers) — so melt is emission minus
// taxicab > 12 (measured: arrival 12 holds, 13 melts) while layers=8
// is opaque and can never store enough. dropResources runs on melt
// but every pool in blocks/snow.json is entity:this-gated and the
// bare block context binds no entity — a silent air write only.
func snowMeltRule(s *Server, d *dimension, pos world.Pos, b world.Block) {
	if layers, _ := intProp(b, "layers"); layers >= 8 {
		return
	}
	if !s.blockLightAbove(d, pos, 12) {
		return
	}
	s.setNaturalBlock(d, pos, world.Air)
}

// Turtle egg sounds — entity.turtle.egg_crack / egg_hatch ids in the
// 26.2 sound registry.
const (
	sndTurtleEggCrack = 1660
	sndTurtleEggHatch = 1661
)

// turtleEggRule is TurtleEggBlock.randomTick: shouldUpdateHatchLevel
// rolls against the day timeline's turtle_egg_hatch_chance attribute
// (constant keyframes 21062 -> 1.0, 21905 -> 0.002, the attribute
// default) and onSand gates on a #sand block below. hatch < 2 plays
// the crack sound and bumps the stage; hatch == 2 plays the hatch
// sound, removes the egg, and per egg runs a 2001 break effect then
// spawns a baby turtle at x+0.3+i*0.2 / z+0.3, age -24000, home the
// egg position, yaw/pitch snapped to zero.
func turtleEggRule(s *Server, d *dimension, pos world.Pos, b world.Block) {
	chance := 0.002
	if dt := s.dayTime() % 24000; dt >= 21062 && dt < 21905 {
		chance = 1.0
	}
	if s.rng.nextDouble() >= chance {
		return
	}
	under := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	underName, _ := world.StateOwner(s.rtBlock(d, under))
	if !blockTagHas("sand", underName) {
		return
	}
	eggs, _ := intProp(b, "eggs")
	hatch, ok := intProp(b, "hatch")
	if !ok {
		return
	}
	x := float64(pos.X) + 0.5
	y := float64(pos.Y) + 0.5
	z := float64(pos.Z) + 0.5
	if hatch < 2 {
		s.playSoundSrc(d.name, x, y, z, sndTurtleEggCrack, 4, 0.7,
			float32(0.9+0.2*s.rng.nextDouble()), nil)
		if nb, ok := world.WithProperty(b, "hatch", strconv.Itoa(hatch+1)); ok {
			s.setSilentBlock(d, pos, nb)
		}
		return
	}
	s.playSoundSrc(d.name, x, y, z, sndTurtleEggHatch, 4, 0.7,
		float32(0.9+0.2*s.rng.nextDouble()), nil)
	s.setNaturalBlock(d, pos, world.Air)
	et, hasType := EntityTypeID("turtle")
	for i := 0; i < eggs; i++ {
		s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
		if !hasType {
			continue
		}
		t := s.entities.Spawn(&Entity{
			Type:       et,
			Dim:        d.name,
			X:          float64(pos.X) + 0.3 + float64(i)*0.2,
			Y:          float64(pos.Y),
			Z:          float64(pos.Z) + 0.3,
			Living:     true,
			Health:     mobHealth["turtle"],
			MaxHealth:  mobHealth["turtle"],
			Age:        -24000,
			Baby:       true,
			TurtleHome: pos,
		})
		s.broadcastEntitySpawnIn(d.name, nil, *t)
	}
}

// frogspawnHatchSound — block.frogspawn.hatch in the 26.2 sound
// registry.
const sndFrogspawnHatch = 677

// frogspawnCanSurvive is FrogspawnBlock.canSurvive → mayPlaceOn on
// the cell below: the block below must carry a SUPPORTS_FROGSPAWN
// fluid (water) or be in the (empty) #supports_frogspawn block tag,
// and the egg cell itself must be fluid-free — a frogspawn cell is a
// plain block, so the second half always holds.
func (s *Server) frogspawnCanSurvive(d *dimension, pos world.Pos) bool {
	below := posBelow(pos, 1)
	if kind, _, ok := fluidOf(d.w.GetBlock(below)); ok && kind == fluidWater {
		return true
	}
	name, _ := world.StateOwner(d.w.GetBlock(below))
	return blockTagHas("supports_frogspawn", name)
}

// frogspawnTick is FrogspawnBlock.tick: a fresh canSurvive — fail and
// the egg pops via destroyBlock (2001 + air), pass and hatchFrogspawn
// runs the same destroy plus the hatch sound and 2-5 tadpoles.
func (s *Server) frogspawnTick(d *dimension, pos world.Pos) {
	b := d.w.GetBlock(pos)
	if !isBlockNamed(b, "frogspawn") {
		return
	}
	if !s.frogspawnCanSurvive(d, pos) {
		s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
		s.setNaturalBlock(d, pos, world.Air)
		return
	}
	s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
	s.setNaturalBlock(d, pos, world.Air)
	s.playSoundSrc(d.name, float64(pos.X)+0.5, float64(pos.Y)+0.5,
		float64(pos.Z)+0.5, sndFrogspawnHatch, 4, 1.0, 1.0, nil)
	// spawnTadpoles: nextInt(2,6) tadpoles, each snapped to
	// (x+clamp(nextDouble,0.2,0.8), y-0.5, z+clamp(...)) with yaw
	// nextInt(1,361) and persistenceRequired set (spawn reason
	// BREEDING).
	et, hasType := EntityTypeID("tadpole")
	for i, n := 0, 2+s.rng.intn(4); i < n; i++ {
		if !hasType {
			break
		}
		ox := s.rng.nextDouble()
		if ox < 0.2 {
			ox = 0.2
		} else if ox > 0.8 {
			ox = 0.8
		}
		oz := s.rng.nextDouble()
		if oz < 0.2 {
			oz = 0.2
		} else if oz > 0.8 {
			oz = 0.8
		}
		tp := s.entities.Spawn(&Entity{
			Type:                et,
			Dim:                 d.name,
			X:                   float64(pos.X) + ox,
			Y:                   float64(pos.Y) - 0.5,
			Z:                   float64(pos.Z) + oz,
			Yaw:                 float32(1 + s.rng.intn(360)),
			Living:              true,
			Health:              mobHealth["tadpole"],
			MaxHealth:           mobHealth["tadpole"],
			PersistenceRequired: true,
		})
		s.broadcastEntitySpawnIn(d.name, nil, *tp)
	}
}

// sniffer egg sounds — block.sniffer_egg.crack / .hatch in the 26.2
// sound registry.
const (
	sndSnifferEggCrack = 1565
	sndSnifferEggHatch = 1566
)

// snifferEggBoost is SnifferEggBlock.hatchBoost: the block below in
// #sniffer_egg_hatch_boost (moss_block only).
func (s *Server) snifferEggBoost(d *dimension, pos world.Pos) bool {
	name, _ := world.StateOwner(d.w.GetBlock(posBelow(pos, 1)))
	return blockTagHas("sniffer_egg_hatch_boost", name)
}

// snifferEggOnPlace is SnifferEggBlock.onPlace: a boosted write
// levelEvents 3009 server-side, every write fires gameEvent
// BLOCK_PLACE, and the next crack stage arms at (boosted ? 12000 :
// 24000)/3 + nextInt(300) — the self bump writes re-arm through the
// same path.
func (s *Server) snifferEggOnPlace(d *dimension, pos world.Pos) {
	boosted := s.snifferEggBoost(d, pos)
	if boosted {
		s.broadcastLevelEvent(d.name, 3009, pos, 0)
	}
	s.emitGameEvent(d, "block_place", float64(pos.X)+0.5,
		float64(pos.Y)+0.5, float64(pos.Z)+0.5, nil, "sniffer_egg")
	period := int64(8000)
	if boosted {
		period = 4000
	}
	s.scheduleBlockTick(d, pos, period+int64(s.rng.intn(300)))
}

// snifferEggTick is SnifferEggBlock.tick: hatch < 2 plays the crack
// sound and bumps HATCH (flag-2 write); hatch == 2 plays the hatch
// sound, destroyBlocks the egg (2001 + air), and spawns one baby
// sniffer at the egg center with a wrapDegrees(nextFloat*360) yaw.
func (s *Server) snifferEggTick(d *dimension, pos world.Pos) {
	b := d.w.GetBlock(pos)
	if !isBlockNamed(b, "sniffer_egg") {
		return
	}
	hatch, ok := intProp(b, "hatch")
	if !ok {
		return
	}
	x := float64(pos.X) + 0.5
	y := float64(pos.Y) + 0.5
	z := float64(pos.Z) + 0.5
	if hatch < 2 {
		s.playSoundSrc(d.name, x, y, z, sndSnifferEggCrack, 4, 0.7,
			float32(0.9+0.2*s.rng.nextDouble()), nil)
		if nb, ok := world.WithProperty(b, "hatch", strconv.Itoa(hatch+1)); ok {
			s.setSilentBlock(d, pos, nb)
		}
		return
	}
	s.playSoundSrc(d.name, x, y, z, sndSnifferEggHatch, 4, 0.7,
		float32(0.9+0.2*s.rng.nextDouble()), nil)
	s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
	s.setNaturalBlock(d, pos, world.Air)
	et, hasType := EntityTypeID("sniffer")
	if !hasType {
		return
	}
	yaw := float32(s.rng.nextDouble() * 360.0)
	if yaw >= 180 {
		yaw -= 360
	}
	sn := s.entities.Spawn(&Entity{
		Type:      et,
		Dim:       d.name,
		X:         x,
		Y:         y,
		Z:         z,
		Yaw:       yaw,
		Living:    true,
		Health:    mobHealth["sniffer"],
		MaxHealth: mobHealth["sniffer"],
		Age:       -48000,
		Baby:      true,
	})
	s.broadcastEntitySpawnIn(d.name, nil, *sn)
}

// redstoneOreRule is RedStoneOreBlock.randomTick: a lit ore settles
// back to unlit.
func redstoneOreRule(s *Server, d *dimension, pos world.Pos, b world.Block) {
	lit, _ := world.StateProperty(b, "lit")
	if lit != "true" {
		return
	}
	if nb, ok := world.WithProperty(b, "lit", "false"); ok {
		s.setSilentBlock(d, pos, nb)
		s.connectionCascade(d, pos)
	}
}
