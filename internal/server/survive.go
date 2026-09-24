package server

import (
	"strconv"
	"strings"
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
	"sugar_cane":           true,
	"cactus":               true,
	"bamboo":               true,
	"chorus_plant":         true,
	"chorus_flower":        true,
	"weeping_vines":        true,
	"weeping_vines_plant":  true,
	"twisting_vines":       true,
	"twisting_vines_plant": true,
	"cave_vines":           true,
	"cave_vines_plant":     true,
	"kelp":                 true,
	"kelp_plant":           true,
}

// selfCheckSurvival applies BlockInput.place's side-effect: a placed
// tick-family block that fails canSurvive schedules its pop tick, and
// a placed leaf schedules its distance recompute.
func (s *Server) selfCheckSurvival(d *dimension, pos world.Pos, blk world.Block) {
	name, ok := world.StateOwner(blk)
	if !ok {
		return
	}
	if popOnPlace[name] || isLeafBlock(name) {
		s.checkSurvival(d, pos, pos, blk)
	}
}

// budAttachOffset maps a bud's facing prop to the offset of the cell
// it hangs/sits on — the direction facing.getOpposite() points.
func budAttachOffset(face string) (world.Pos, bool) {
	switch face {
	case "up":
		return world.Pos{X: 0, Y: -1, Z: 0}, true
	case "down":
		return world.Pos{X: 0, Y: 1, Z: 0}, true
	case "north":
		return world.Pos{X: 0, Y: 0, Z: 1}, true
	case "south":
		return world.Pos{X: 0, Y: 0, Z: -1}, true
	case "west":
		return world.Pos{X: 1, Y: 0, Z: 0}, true
	case "east":
		return world.Pos{X: -1, Y: 0, Z: 0}, true
	}
	return world.Pos{}, false
}

// isAmethystBud covers the AmethystClusterBlock family.
func isAmethystBud(name string) bool {
	switch name {
	case "small_amethyst_bud", "medium_amethyst_bud", "large_amethyst_bud", "amethyst_cluster":
		return true
	}
	return false
}

// budUpdate is AmethystClusterBlock.updateShape: the return-air
// family — the bud folds to air only when the write lands on the
// attach side and the cell there isn't sturdy.
func (s *Server) budUpdate(d *dimension, pos, changed world.Pos, b world.Block) {
	face, ok := world.StateProperty(b, "facing")
	if !ok {
		return
	}
	off, ok := budAttachOffset(face)
	if !ok {
		return
	}
	attachPos := world.Pos{X: pos.X + off.X, Y: pos.Y + off.Y, Z: pos.Z + off.Z}
	if changed != attachPos {
		return
	}
	attach := d.w.GetBlock(attachPos)
	_, _, behindFluid := fluidOf(attach)
	if behindFluid || world.NonSolid(attach) {
		s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
		s.setNaturalBlock(d, pos, world.Air)
	}
}

// checkSurvival re-evaluates one cell; a failed check pops the block.
// changed is the cell whose write triggered the check (== pos for a
// placement self-check). notify is the block vanilla passes the
// neighbourChanged callback — the old state for gameplay writes
// (Level.setBlock), the new one for command writes
// (updateNeighboursOnBlockSet).
func (s *Server) checkSurvival(d *dimension, pos, changed world.Pos, notify world.Block) {
	if pos != changed {
		// updateShape runs before neighbourChanged: the connection
		// family (fences, panes, bars) rewrites its prop toward the
		// changed cell first.
		s.connectionUpdate(d, pos, changed)
	}
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
	if _, _, _, ok := vineSpec(name); ok {
		s.vineUpdate(d, pos, changed, b, name)
		return
	}
	if isLeafBlock(name) {
		s.leafShape(d, pos, changed)
		return
	}
	if isAmethystBud(name) {
		s.budUpdate(d, pos, changed, b)
		return
	}
	if name == "vine" {
		s.vinePrune(d, pos, changed, b)
		return
	}
	if name == "frosted_ice" && changed != pos {
		s.frostedNeighborChange(d, pos, notify)
		return
	}
	if (name == "fire" || name == "soul_fire") && changed != pos {
		s.fireNeighborChanged(d, pos, b)
		return
	}
	if (name == "pointed_dripstone" || name == "sulfur_spike") && changed != pos {
		s.speleoNeighborChange(d, pos, changed)
		return
	}
	// TntBlock.neighborChanged: re-check hasNeighborSignal and prime.
	if name == "tnt" && changed != pos {
		s.tntNeighborChanged(d, pos)
		return
	}
	if (name == "water" || name == "bubble_column") && changed != pos {
		// LiquidBlock.neighbourChanged/updateShape and
		// BubbleColumnBlock.updateShape — bubble-column scheduling.
		s.bubbleNeighborChanged(d, pos, changed)
		return
	}
	if name == "brown_mushroom" || name == "red_mushroom" {
		// VegetationBlock.updateShape: return-air family — canSurvive
		// is re-checked on any neighbour change.
		if !mushroomCanSurvive(s, d, pos) {
			s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
			s.setNaturalBlock(d, pos, world.Air)
		}
		return
	}
	fn, ok := surviveFns()[name]
	if !ok || fn(s, d, pos, b) {
		return
	}
	s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
	s.setNaturalBlock(d, pos, world.Air)
}

// vineSpec returns a vine block's head name and growth sign (-1 grows
// down — weeping and cave vines; +1 grows up — twisting vines). Heads
// and bodies share the same spec.
func vineSpec(name string) (head string, growthDy int, isHead, ok bool) {
	switch name {
	case "weeping_vines":
		return "weeping_vines", -1, true, true
	case "weeping_vines_plant":
		return "weeping_vines", -1, false, true
	case "twisting_vines":
		return "twisting_vines", 1, true, true
	case "twisting_vines_plant":
		return "twisting_vines", 1, false, true
	case "cave_vines":
		return "cave_vines", -1, true, true
	case "cave_vines_plant":
		return "cave_vines", -1, false, true
	case "kelp":
		return "kelp", 1, true, true
	case "kelp_plant":
		return "kelp", 1, false, true
	}
	return "", 0, false, false
}

// vineUpdate is the GrowingPlant updateShape. Survival is re-checked
// only when the attach-side cell changed (the fold's direction set
// covers it for a placement self-check, which arrives as changed ==
// pos). Vine blocks also rewrite in place: a head seeing the same vine
// ahead becomes its plant body, and a body whose growth-side neighbor
// stopped being the same vine becomes a fresh-aged head.
func (s *Server) vineUpdate(d *dimension, pos, changed world.Pos, b world.Block, name string) {
	head, growthDy, isHead, _ := vineSpec(name)
	body := head + "_plant"
	cave := head == "cave_vines"
	attach := world.Pos{X: pos.X, Y: pos.Y - growthDy, Z: pos.Z}
	ahead := world.Pos{X: pos.X, Y: pos.Y + growthDy, Z: pos.Z}
	if changed == attach || changed == pos {
		// canSurvive: the attach cell is this vine's head or body, or a
		// sturdy face — NonSolid marks the fluid-washable decoration
		// set, and a fluid cell itself has no sturdy face either (a
		// kelp tip whose column popped to water must keep falling) —
		// kelp's canAttachTo additionally rejects #cannot_support_kelp
		// (magma_block).
		behind := d.w.GetBlock(attach)
		canAttach := true
		if head == "kelp" {
			behindName, _ := world.StateOwner(behind)
			canAttach = !blockTagHas("cannot_support_kelp", behindName)
		}
		_, _, behindFluid := fluidOf(behind)
		sturdy := !behindFluid && !world.NonSolid(behind)
		if !canAttach || (!isBlockNamed(behind, head) && !isBlockNamed(behind, body) && !sturdy) {
			s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
			// destroyBlock leaves the cell's fluid: kelp pops back to a
			// water source, everything else to air.
			if head == "kelp" {
				if w, ok := world.BlockStateOf("water", nil); ok {
					s.setNaturalBlock(d, pos, w)
					return
				}
			}
			s.setNaturalBlock(d, pos, world.Air)
			return
		}
	}
	if isHead {
		nb := d.w.GetBlock(ahead)
		if !isBlockNamed(nb, head) && !isBlockNamed(nb, body) {
			return
		}
		nb2, ok := world.BlockStateOf(body, nil)
		if !ok {
			return
		}
		if cave {
			if berries, ok := world.StateProperty(b, "berries"); ok {
				nb2, _ = world.WithProperty(nb2, "berries", berries)
			}
		}
		s.setNaturalBlock(d, pos, nb2)
		return
	}
	if changed != ahead {
		return
	}
	nb := d.w.GetBlock(ahead)
	if isBlockNamed(nb, head) || isBlockNamed(nb, body) {
		return
	}
	nb2, ok := world.BlockStateOf(head, nil)
	if !ok {
		return
	}
	// getStateForPlacement: a fresh random age 0..24.
	nb2, _ = world.WithProperty(nb2, "age", strconv.Itoa(s.rng.intn(25)))
	if cave {
		if berries, ok := world.StateProperty(b, "berries"); ok {
			nb2, _ = world.WithProperty(nb2, "berries", berries)
		}
	}
	s.setNaturalBlock(d, pos, nb2)
}

// neighborSurvival is the updateShape fan-out: a write at pos re-checks
// its six face neighbors. The written cell is not self-checked here —
// that belongs to the command-placement path (selfCheckSurvival).
// notify is what vanilla hands each neighbourChanged: the old block on
// gameplay writes, the new block on command writes.
func (s *Server) neighborSurvival(d *dimension, pos world.Pos, notify world.Block) {
	s.checkSurvival(d, posAbove(pos, 1), pos, notify)
	s.checkSurvival(d, posBelow(pos, 1), pos, notify)
	for _, dir := range cardinalDirs {
		s.checkSurvival(d, world.Pos{X: pos.X + dir.dx, Y: pos.Y, Z: pos.Z + dir.dz}, pos, notify)
	}
}

// writeOnPlace is the onPlace hook vanilla fires inside
// LevelChunk.setBlockState on every write: scheduled-tick blocks arm
// their first tick here (frosted ice, fire, ...).
func (s *Server) writeOnPlace(d *dimension, pos world.Pos, blk, old world.Block) {
	s.frostedIceOnPlace(d, pos, blk)
	s.fireOnPlace(d, pos, blk, old)
	// ScaffoldingBlock.onPlace: the 1-tick distance refold arms on
	// every write (same as a neighbour updateShape).
	if name, ok := world.StateOwner(blk); ok && strings.TrimPrefix(name, "minecraft:") == "scaffolding" {
		s.scheduleBlockTick(d, pos, 1)
	}
	// CoralBlock.getStateForPlacement / updateShape: a coral with no
	// water in contact schedules the die tick at 60+rng(40).
	if name, ok := world.StateOwner(blk); ok {
		short := strings.TrimPrefix(name, "minecraft:")
		oldName, _ := world.StateOwner(old)
		oldShort := strings.TrimPrefix(oldName, "minecraft:")
		if coralLive(short) && !s.coralWaterScan(d, pos) {
			s.scheduleBlockTick(d, pos, int64(60+s.rng.intn(40)))
		}
		// TripWireBlock.onPlace / affectNeighborsAfterRemoval: a wire
		// write or removal reruns calculateState from the run's
		// south/west-side hook (updateSource). A hook write does not —
		// hooks attach via getStateForPlacement/setPlacedBy, which are
		// item-placement paths only.
		if short == "tripwire" != (oldShort == "tripwire") {
			s.tripwireUpdateSource(d, pos, blk)
		}
		// RedstoneTorchBlock.onPlace / affectNeighborsAfterRemoval:
		// a torch write notifies the neighbours OF each neighbour —
		// the ring its emitted signal actually reaches.
		if short == "redstone_torch" || short == "redstone_wall_torch" ||
			oldShort == "redstone_torch" || oldShort == "redstone_wall_torch" {
			s.rsTorchNotify(d, pos)
		}
	}
	// TntBlock.onPlace: prime + self-remove when a neighbour carries a
	// signal — callers re-read the cell after this hook.
	s.tntOnPlace(d, pos, blk)
	// CarvedPumpkinBlock.onPlace / WitherSkullBlock.checkSpawn: a
	// pumpkin or wither skull completing a golem pattern spawns the
	// golem and airs the cells — callers re-read after this hook too.
	s.golemOnPlace(d, pos, blk)
	// LiquidBlock.onPlace: a fresh water source over a column base
	// arms the 20-tick conversion check itself.
	if name, _ := world.StateOwner(blk); name == "water" {
		if k, level, ok := fluidOf(blk); ok && k == fluidWater && level == 0 {
			below, _ := world.StateOwner(d.w.GetBlock(posBelow(pos, 1)))
			if below == "soul_sand" || below == "magma_block" {
				s.scheduleBlockTick(d, pos, 20)
			}
		}
	}
}

// setSilentBlock is a flag-2 write: world update plus the usual
// fan-out, but no neighbor survival pass.
func (s *Server) setSilentBlock(d *dimension, pos world.Pos, blk world.Block) {
	oldTop := columnTop(d, pos.X, pos.Z)
	old := d.w.GetBlock(pos)
	d.w.SetBlock(pos, blk)
	s.writeOnPlace(d, pos, blk, old)
	// onPlace may have rewritten the cell (TNT primed on a signal
	// self-removes) — everything downstream reads the final state.
	blk = d.w.GetBlock(pos)
	// The second scan is skippable in the common cases: anything below
	// the top never moves it, a solid write above it sets the top to
	// pos.Y outright, and an air write above the top writes a cell that
	// was already air. Only removing the top cell rescans down.
	var newTop int32
	switch {
	case int32(pos.Y) < oldTop || (blk != world.Air && int32(pos.Y) == oldTop):
		newTop = oldTop
	case int32(pos.Y) > oldTop:
		if blk != world.Air {
			newTop = int32(pos.Y)
		} else {
			newTop = oldTop
		}
	default:
		newTop = columnTop(d, pos.X, pos.Z)
	}
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
