package server

import (
	"sync"

	"mc-go/internal/world"
)

// Neighbor-update survival. Vanilla's updateShape re-checks a block's
// canSurvive whenever a face neighbor changes (updateNeighbourShapes
// walks the six neighbors only — the written cell itself is not
// self-checked by setBlock). Blocks fail in one of two ways:
//
//   - tick family (sugar_cane, cactus, bamboo stalk, chorus plant and
//     flower): updateShape schedules a one-tick block tick whose tick()
//     calls destroyBlock.
//   - return-air family (crops, bamboo sapling, nether wart, snow,
//     torch): updateShape returns air, replacing the block instantly.
//
// /setblock and /fill place through BlockInput.place, which folds the
// requested state through updateFromNeighbourShapes first — but an
// air result is discarded in favor of the requested state, so the
// return-air family NEVER pops on placement, while the tick family's
// side-effect scheduleTick still pops them one tick later. mc-go
// mirrors this: writes check only the six neighbors, and command
// placements additionally self-check the tick family (popOnPlace).
// The pop runs synchronously instead of on a 1-tick schedule —
// observable-identical once the client sees the result.
//
// Writes that vanilla makes with flag 2 (client update only, no
// neighbor updates — e.g. chorus flower's internal plant/flower
// rewrite) must NOT trigger these checks; those use setSilentBlock.

var (
	surviveOnce sync.Once
	surviveMap  map[string]func(*Server, *dimension, world.Pos, world.Block) bool
)

func surviveFns() map[string]func(*Server, *dimension, world.Pos, world.Block) bool {
	surviveOnce.Do(func() {
		surviveMap = map[string]func(*Server, *dimension, world.Pos, world.Block) bool{
			"sugar_cane":     sugarCaneCanSurvive,
			"cactus":         func(s *Server, d *dimension, pos world.Pos, _ world.Block) bool { return cactusCanSurvive(s, d, pos) },
			"bamboo":         belowIsTag("supports_bamboo"),
			"bamboo_sapling": belowIsTag("supports_bamboo"),
			"chorus_flower":  chorusFlowerCanSurvive,
			"chorus_plant":   chorusPlantCanSurvive,
			"wheat":          cropCanSurvive,
			"carrots":        cropCanSurvive,
			"potatoes":       cropCanSurvive,
			"beetroots":      cropCanSurvive,
			"nether_wart":    netherWartCanSurvive,
			"torch":          torchCanSurvive,
			"snow":           snowLayerCanSurvive,
		}
	})
	return surviveMap
}

// popOnPlace is the tick family: blocks whose updateShape side-effects
// scheduleTick on failure, which pops them even when the failed check
// ran against their own placement (BlockInput.place's neighbour-shapes
// fold). The return-air family is absent — its air fold is discarded
// and the requested state placed.
var popOnPlace = map[string]bool{
	"sugar_cane":    true,
	"cactus":        true,
	"bamboo":        true,
	"chorus_plant":  true,
	"chorus_flower": true,
}

// selfCheckSurvival applies BlockInput.place's side-effect: a placed
// tick-family block that fails canSurvive schedules its pop tick.
func (s *Server) selfCheckSurvival(d *dimension, pos world.Pos, blk world.Block) {
	if name, ok := world.StateOwner(blk); ok && popOnPlace[name] {
		s.checkSurvival(d, pos, pos)
	}
}

// checkSurvival re-evaluates one cell; a failed check pops the block.
// changed is the cell whose write triggered the check (== pos for a
// placement self-check).
func (s *Server) checkSurvival(d *dimension, pos, changed world.Pos) {
	b := d.w.GetBlock(pos)
	name, ok := world.StateOwner(b)
	if !ok {
		return
	}
	// ChorusFlowerBlock.updateShape skips the check when the change
	// arrives from above it (direction == UP).
	if name == "chorus_flower" && changed.Y > pos.Y {
		return
	}
	fn, ok := surviveFns()[name]
	if !ok || fn(s, d, pos, b) {
		return
	}
	s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
	s.setNaturalBlock(d, pos, world.Air)
}

// neighborSurvival is the updateShape fan-out: a write at pos re-checks
// its six face neighbors. The written cell is not self-checked here —
// that belongs to the command-placement path (selfCheckSurvival).
func (s *Server) neighborSurvival(d *dimension, pos world.Pos) {
	s.checkSurvival(d, posAbove(pos, 1), pos)
	s.checkSurvival(d, posBelow(pos, 1), pos)
	for _, dir := range cardinalDirs {
		s.checkSurvival(d, world.Pos{X: pos.X + dir.dx, Y: pos.Y, Z: pos.Z + dir.dz}, pos)
	}
}

// setSilentBlock is a flag-2 write: world update plus the usual
// fan-out, but no neighbor survival pass.
func (s *Server) setSilentBlock(d *dimension, pos world.Pos, blk world.Block) {
	oldTop := columnTop(d, pos.X, pos.Z)
	d.w.SetBlock(pos, blk)
	newTop := columnTop(d, pos.X, pos.Z)
	s.broadcastBlockState(d, pos, blk)
	s.syncBlockEntities(d, pos, blk)
	s.invalidateRandomTickSection(d, pos)
	if affected := affectedSections(d, oldTop, newTop); len(affected) > 0 {
		s.sendLightUpdate(d, pos.X>>4, pos.Z>>4, affected)
	}
}

func belowIsTag(tag string) func(*Server, *dimension, world.Pos, world.Block) bool {
	return func(_ *Server, d *dimension, pos world.Pos, _ world.Block) bool {
		name, _ := world.StateOwner(d.w.GetBlock(posBelow(pos, 1)))
		return blockTagHas(tag, name)
	}
}

// sugarCaneCanSurvive is SugarCaneBlock.canSurvive: a cane column hangs
// on cane, or on a #supports_sugar_cane base that has water (or
// #supports_sugar_cane_adjacently, e.g. frosted_ice) beside it.
func sugarCaneCanSurvive(_ *Server, d *dimension, pos world.Pos, _ world.Block) bool {
	belowPos := posBelow(pos, 1)
	below := d.w.GetBlock(belowPos)
	if isBlockNamed(below, "sugar_cane") {
		return true
	}
	belowName, _ := world.StateOwner(below)
	if !blockTagHas("supports_sugar_cane", belowName) {
		return false
	}
	for _, dir := range cardinalDirs {
		nb := d.w.GetBlock(world.Pos{X: belowPos.X + dir.dx, Y: belowPos.Y, Z: belowPos.Z + dir.dz})
		if kind, _, ok := fluidOf(nb); ok && kind == fluidWater {
			return true
		}
		if n, _ := world.StateOwner(nb); blockTagHas("supports_sugar_cane_adjacently", n) {
			return true
		}
	}
	return false
}

// chorusFlowerCanSurvive is ChorusFlowerBlock.canSurvive: on a plant or
// end stone it always lives; floating on air it needs exactly one
// horizontal plant neighbor with all other sides empty.
func chorusFlowerCanSurvive(_ *Server, d *dimension, pos world.Pos, _ world.Block) bool {
	below := d.w.GetBlock(posBelow(pos, 1))
	if isBlockNamed(below, "chorus_plant") {
		return true
	}
	belowName, _ := world.StateOwner(below)
	if blockTagHas("supports_chorus_flower", belowName) {
		return true
	}
	if !isAirCell(below) {
		return false
	}
	foundPlant := false
	for _, dir := range cardinalDirs {
		n := d.w.GetBlock(world.Pos{X: pos.X + dir.dx, Y: pos.Y, Z: pos.Z + dir.dz})
		if isBlockNamed(n, "chorus_plant") {
			if foundPlant {
				return false
			}
			foundPlant = true
		} else if !isAirCell(n) {
			return false
		}
	}
	return foundPlant
}

// chorusPlantCanSurvive is ChorusPlantBlock.canSurvive: a plant is
// grounded through its own below (plant or end stone), or sideways
// through a plant whose own below is grounded — but a plant pinched
// between two non-air vertical neighbors cannot hang on a side link.
func chorusPlantCanSurvive(_ *Server, d *dimension, pos world.Pos, _ world.Block) bool {
	below := d.w.GetBlock(posBelow(pos, 1))
	above := d.w.GetBlock(posAbove(pos, 1))
	pinched := !isAirCell(below) && !isAirCell(above)
	for _, dir := range cardinalDirs {
		np := world.Pos{X: pos.X + dir.dx, Y: pos.Y, Z: pos.Z + dir.dz}
		if !isBlockNamed(d.w.GetBlock(np), "chorus_plant") {
			continue
		}
		if pinched {
			return false
		}
		nb := d.w.GetBlock(posBelow(np, 1))
		nbName, _ := world.StateOwner(nb)
		if isBlockNamed(nb, "chorus_plant") || blockTagHas("supports_chorus_plant", nbName) {
			return true
		}
	}
	belowName, _ := world.StateOwner(below)
	return isBlockNamed(below, "chorus_plant") || blockTagHas("supports_chorus_plant", belowName)
}

// cropCanSurvive is CropBlock.canSurvive's structural half — the
// #supports_crops tag (farmland only). Vanilla also requires
// hasSufficientLight (raw brightness >= 8); mc-go has no lighting
// engine yet, so dark-grown crops surviving is a known gap.
func cropCanSurvive(_ *Server, d *dimension, pos world.Pos, _ world.Block) bool {
	name, _ := world.StateOwner(d.w.GetBlock(posBelow(pos, 1)))
	return blockTagHas("supports_crops", name)
}

func netherWartCanSurvive(_ *Server, d *dimension, pos world.Pos, _ world.Block) bool {
	return isBlockNamed(d.w.GetBlock(posBelow(pos, 1)), "soul_sand")
}

// torchCanSurvive is BaseTorchBlock.canSurvive: the block below needs a
// sturdy center top face — approximated as solidity, so slabs and
// other partial blocks are a known gap.
func torchCanSurvive(_ *Server, d *dimension, pos world.Pos, _ world.Block) bool {
	return !world.NonSolid(d.w.GetBlock(posBelow(pos, 1)))
}

// snowLayerCanSurvive is SnowLayerBlock.canSurvive: a full-height
// support below, or a full eight-layer snow block.
func snowLayerCanSurvive(_ *Server, d *dimension, pos world.Pos, _ world.Block) bool {
	below := d.w.GetBlock(posBelow(pos, 1))
	if isBlockNamed(below, "snow") {
		layers, _ := intProp(below, "layers")
		return layers == 8
	}
	return !world.NonSolid(below)
}
