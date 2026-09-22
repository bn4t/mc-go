package server

import (
	"strconv"

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
//   - FarmlandBlock: water in the 9x2x9 box at/below pos (or rain at the
//     block above — mc-go has no weather) snaps moisture to 7; otherwise
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

// farmlandRule is FarmlandBlock.randomTick: moisture tracking and the
// dry-out revert to dirt.
func farmlandRule(s *Server, d *dimension, pos world.Pos, b world.Block) {
	moisture, _ := intProp(b, "moisture")
	if farmlandNearWater(s, d, pos) {
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
	above := world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	aboveName, _ := world.StateOwner(s.rtBlock(d, above))
	if !blockTagHas("maintains_farmland", aboveName) {
		if dirt, ok := world.BlockStateOf("dirt", nil); ok {
			s.setNaturalBlock(d, pos, dirt)
		}
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
