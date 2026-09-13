package server

import (
	"strconv"
	"sync"

	"mc-go/internal/world"
)

// Fluid simulation: water and lava spreading, clean-room from the
// publicly documented behavior of the "flowing fluids" system.
//
// Level model
//
// Both fluids carry a `level` property:
//
//	0      a source block; permanent, never dries up
//	1..7   flowing, with the surface height falling as the level rises
//	8      falling: the state a fluid takes when the same fluid sits
//	       directly above it, drawn as a full block
//
// A fluid spreads sideways with a fixed drop-off added to its level per
// block travelled, and dries up once the level would exceed 7. Water's
// drop-off is 1, so it reaches 7 blocks; lava's is 2 outside the nether,
// so it reaches 3. The nether is ultra-warm, which drops lava's
// drop-off back to 1 and makes it spread like water, and also cuts
// lava's tick delay from 30 ticks to 10.

// Fluid tick delays in game ticks.
const (
	waterTickDelay         = 5
	lavaTickDelayOverworld = 30
	lavaTickDelayNether    = 10
)

// Level boundaries.
const (
	fluidMaxLevel = 7 // highest flowing level; beyond it a fluid dries up
	fluidFalling  = 8 // the falling state's level
)

// fluidKind identifies a simulated fluid.
type fluidKind uint8

const (
	fluidNone fluidKind = iota
	fluidWater
	fluidLava
)

func (f fluidKind) name() string {
	switch f {
	case fluidWater:
		return "water"
	case fluidLava:
		return "lava"
	}
	return ""
}

// tickDelay is how long after being disturbed a fluid re-evaluates.
func (f fluidKind) tickDelay(d *dimension) int64 {
	if f == fluidWater {
		return waterTickDelay
	}
	if d != nil && d.ultraWarm {
		return lavaTickDelayNether
	}
	return lavaTickDelayOverworld
}

// dropOff is how much the level rises per block of horizontal spread.
func (f fluidKind) dropOff(d *dimension) int {
	if f == fluidLava && (d == nil || !d.ultraWarm) {
		return 2
	}
	return 1
}

// fluidOf reads a block state as a fluid: which fluid, and its level.
func fluidOf(b world.Block) (fluidKind, int, bool) {
	name, ok := world.StateOwner(b)
	if !ok {
		return fluidNone, 0, false
	}
	var k fluidKind
	switch name {
	case "water":
		k = fluidWater
	case "lava":
		k = fluidLava
	default:
		return fluidNone, 0, false
	}
	lv, ok := world.StateProperty(b, "level")
	if !ok {
		return fluidNone, 0, false
	}
	n, err := strconv.Atoi(lv)
	if err != nil {
		return fluidNone, 0, false
	}
	return k, n, true
}

// The fluid state ids are resolved once, lazily: the generated
// block-state table is decoded in its own init, and package init order
// is not ours to rely on.
var (
	fluidStateOnce sync.Once
	fluidStateTab  [3][16]world.Block
)

func buildFluidStates() {
	for _, k := range []fluidKind{fluidWater, fluidLava} {
		for lvl := 0; lvl <= 15; lvl++ {
			b, ok := world.BlockStateOf(k.name(), map[string]string{"level": strconv.Itoa(lvl)})
			if ok {
				fluidStateTab[k][lvl] = b
			}
		}
	}
}

// fluidState is the block state for a fluid at a level.
func fluidState(k fluidKind, level int) world.Block {
	fluidStateOnce.Do(buildFluidStates)
	if level < 0 || level > 15 {
		return world.Air
	}
	return fluidStateTab[k][level]
}

// replaceableByFluid reports whether a fluid may flow into this block.
// Vanilla's rule is "the block can be replaced", which covers air and
// the non-solid decoration blocks a fluid washes away.
func replaceableByFluid(b world.Block) bool {
	if b == world.Air {
		return true
	}
	name, ok := world.StateOwner(b)
	if !ok {
		return false
	}
	return replaceableFluidTargets[name]
}

// replaceableFluidTargets are the blocks a fluid flows into and
// destroys. Waterlogged blocks are handled separately (they keep their
// block and just take the fluid); this is the "washed away" set.
var replaceableFluidTargets = map[string]bool{
	"air": true, "cave_air": true, "void_air": true,
	"short_grass": true, "tall_grass": true, "fern": true, "large_fern": true,
	"dead_bush": true, "dandelion": true, "poppy": true, "cornflower": true,
	"azure_bluet": true, "oxeye_daisy": true, "allium": true, "blue_orchid": true,
	"lily_of_the_valley": true, "sunflower": true, "lilac": true, "rose_bush": true,
	"peony": true, "red_tulip": true, "orange_tulip": true, "white_tulip": true,
	"pink_tulip": true, "torchflower": true, "pitcher_plant": true,
	"wheat": true, "carrots": true, "potatoes": true, "beetroots": true,
	"oak_sapling": true, "birch_sapling": true, "spruce_sapling": true,
	"jungle_sapling": true, "acacia_sapling": true, "dark_oak_sapling": true,
	"cherry_sapling": true, "pale_oak_sapling": true, "mangrove_propagule": true,
	"sugar_cane": true, "kelp": true, "kelp_plant": true, "seagrass": true,
	"tall_seagrass": true, "snow": true, "fire": true, "soul_fire": true,
	"nether_sprouts": true, "crimson_roots": true, "warped_roots": true,
	"crimson_fungus": true, "warped_fungus": true, "brown_mushroom": true,
	"red_mushroom": true, "sweet_berry_bush": true, "glow_lichen": true,
	"sculk_vein": true, "vine": true, "hanging_roots": true, "big_dripleaf": true,
	"small_dripleaf": true, "azalea": true, "flowering_azalea": true,
	"moss_carpet": true, "pink_petals": true, "spore_blossom": true,
	"cave_vines": true, "cave_vines_plant": true, "weeping_vines": true,
	"weeping_vines_plant": true, "twisting_vines": true, "twisting_vines_plant": true,
	"nether_wart": true, "torch": true, "soul_torch": true, "redstone_torch": true,
	"redstone_wire": true, "lever": true, "tripwire": true, "tripwire_hook": true,
	"string": true, "rail": true, "powered_rail": true, "detector_rail": true,
	"activator_rail": true, "red_mushroom_block": false,
}

// The two blocks lava turns into when water reaches it, resolved once
// on demand so a regenerated block table cannot shift them silently.
var (
	solidStatesOnce  sync.Once
	obsidianState    world.Block
	cobblestoneState world.Block
)

func resolveSolidStates() {
	solidStatesOnce.Do(func() {
		obsidianState, _ = world.BlockStateOf("obsidian", nil)
		cobblestoneState, _ = world.BlockStateOf("cobblestone", nil)
	})
}

// touchesWater reports whether any of the six blocks around pos holds
// water.
func (s *Server) touchesWater(d *dimension, pos world.Pos) bool {
	for _, p := range neighbourPositions(pos) {
		if k, _, ok := fluidOf(d.w.GetBlock(p)); ok && k == fluidWater {
			return true
		}
	}
	return false
}

// wakeLava schedules any lava touching pos, so water arriving beside it
// makes it tick and set solid instead of waiting for a tick it may never
// be given.
func (s *Server) wakeLava(d *dimension, pos world.Pos) {
	for _, p := range append([]world.Pos{pos}, neighbourPositions(pos)...) {
		if k, _, ok := fluidOf(d.w.GetBlock(p)); ok && k == fluidLava {
			s.scheduleFluidTick(d, p, k.tickDelay(d))
		}
	}
}

// scheduleFluidTick queues a fluid re-evaluation for pos.
func (s *Server) scheduleFluidTick(d *dimension, pos world.Pos, delay int64) {
	if s.ticks == nil || d == nil {
		return
	}
	if delay < 1 {
		delay = 1
	}
	s.ticks.schedule(schedKey{dim: d.name, pos: pos, kind: tickFluid}, s.TickCount()+delay)
}

// wakeFluids schedules a fluid tick for pos and each of its neighbours,
// which is what makes a placed or broken block start or stop a flow.
// Vanilla reaches the same positions through neighbour updates.
func (s *Server) wakeFluids(d *dimension, pos world.Pos) {
	if s.ticks == nil || d == nil {
		return
	}
	for _, p := range append([]world.Pos{pos}, neighbourPositions(pos)...) {
		if k, lvl, ok := fluidOf(d.w.GetBlock(p)); ok {
			_ = lvl
			s.scheduleFluidTick(d, p, k.tickDelay(d))
		}
	}
}

// neighbourPositions lists the six positions touching pos.
func neighbourPositions(pos world.Pos) []world.Pos {
	return []world.Pos{
		{X: pos.X, Y: pos.Y - 1, Z: pos.Z},
		{X: pos.X, Y: pos.Y + 1, Z: pos.Z},
		{X: pos.X - 1, Y: pos.Y, Z: pos.Z},
		{X: pos.X + 1, Y: pos.Y, Z: pos.Z},
		{X: pos.X, Y: pos.Y, Z: pos.Z - 1},
		{X: pos.X, Y: pos.Y, Z: pos.Z + 1},
	}
}

// horizontalDirections are the four sideways offsets, in a fixed order
// so a stepped run is reproducible.
var horizontalDirections = [4]world.Pos{
	{X: 0, Y: 0, Z: -1},
	{X: -1, Y: 0, Z: 0},
	{X: 1, Y: 0, Z: 0},
	{X: 0, Y: 0, Z: 1},
}

// scheduledTickStep runs everything the tick queue has due: fluid
// re-evaluations and (later) scheduled block behaviour.
func (s *Server) scheduledTickStep() {
	if s.ticks == nil {
		return
	}
	due := s.ticks.poll(s.TickCount())
	for _, k := range due {
		d := s.dims[k.dim]
		if d == nil {
			continue
		}
		switch k.kind {
		case tickFluid:
			s.fluidTick(d, k.pos)
		case tickBlock:
			s.blockTick(d, k.pos)
		}
	}
}

// fluidTick advances the fluid at pos by one step: first re-deriving its
// own level from its surroundings, then spreading outward.
func (s *Server) fluidTick(d *dimension, pos world.Pos) {
	cur := d.w.GetBlock(pos)
	kind, level, ok := fluidOf(cur)
	if !ok {
		return
	}
	// Water meeting lava sets it solid: a lava source becomes obsidian,
	// a lava flow becomes cobblestone. The water is what does the
	// setting, so the lava is the block that changes.
	if kind == fluidLava && s.touchesWater(d, pos) {
		solid := obsidianState
		if level != 0 {
			solid = cobblestoneState
		}
		s.setFluidBlock(d, pos, solid)
		s.wakeFluids(d, pos)
		return
	}
	next := s.newFluidState(d, pos, kind, level)
	if next != cur {
		s.setFluidBlock(d, pos, next)
		if next == world.Air {
			// Dried up: neighbours lose their supply and will be
			// re-evaluated by their own ticks.
			s.wakeFluids(d, pos)
			return
		}
		kind, level, ok = fluidOf(next)
		if !ok {
			return
		}
	}
	s.spreadFluid(d, pos, kind, level)
}

// newFluidState derives what the fluid at pos should be, given its
// neighbours. Sources are permanent; everything else follows supply.
func (s *Server) newFluidState(d *dimension, pos world.Pos, kind fluidKind, level int) world.Block {
	if level == 0 {
		return fluidState(kind, 0) // a source stays a source
	}
	// Fluid directly above turns this block into falling fluid.
	if above, _, ok := fluidOf(d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z})); ok && above == kind {
		return fluidState(kind, fluidFalling)
	}
	// Otherwise take a supply from a horizontal neighbour that is
	// strictly stronger — a lower level, with falling counting as level
	// zero. Requiring strictness is what makes flow directional: a
	// block's own level must come from something further upstream, so
	// when a source is removed the flow drains outward-in rather than
	// rearranging itself into a ring that feeds its own middle.
	best := -1
	for _, dir := range horizontalDirections {
		nb := world.Pos{X: pos.X + dir.X, Y: pos.Y, Z: pos.Z + dir.Z}
		k2, l2, ok := fluidOf(d.w.GetBlock(nb))
		if !ok || k2 != kind {
			continue
		}
		if l2 == fluidFalling {
			l2 = 0
		}
		if l2 >= level {
			continue // not a supplier: same strength or weaker
		}
		if best < 0 || l2 < best {
			best = l2
		}
	}
	if best < 0 {
		return world.Air // nothing feeds it any more
	}
	nl := best + kind.dropOff(d)
	if nl > fluidMaxLevel {
		return world.Air
	}
	return fluidState(kind, nl)
}

// spreadLevel is the level a fluid spreads from: sources and falling
// columns spread as if they were level 0, so both fan out seven blocks.
func spreadLevel(level int) int {
	if level == fluidFalling {
		return 0
	}
	return level
}

// spreadFluid pushes a fluid into the block below and then sideways.
//
// The asymmetry between those two is the whole shape of a waterfall,
// and it was read off the real server rather than assumed. Pouring a
// source into the air, looking down the x/z axes (the centre column is
// the source and what it drops):
//
//	y=-55    1    S    1     the source feeds its four neighbours too
//	y=-56    .    F    .     each of those falls; the column stays one
//	y=-57    .    F    .     block wide on the way down
//	y=-58    .    F    .
//	y=-59    .    F    .
//	y=-60    1    F    1     the foot has come to rest, and spreads out
//	y=-60    2    1    2     to level 7 at distance 8
//
// So: a source spreads in every direction, a non-source fluid that can
// still fall does not spread sideways at all, and one that has come to
// rest spreads with its own level plus the drop-off. That last part is
// why a falling column reaches one block further than a resting source
// does (level 7 at distance 8 rather than 7).
func (s *Server) spreadFluid(d *dimension, pos world.Pos, kind fluidKind, level int) {
	if s.ticks == nil {
		return
	}

	// Straight down first: a fluid always falls if it can.
	canFall := false
	// columnBelow is true when this block is part of a falling column
	// rather than its foot: the fluid below is the same fluid.
	columnBelow := false
	below := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	if d.minY <= below.Y && below.Y < d.minY+d.height {
		under := d.w.GetBlock(below)
		if k2, _, isFluid := fluidOf(under); isFluid {
			columnBelow = k2 == kind
		} else if replaceableByFluid(under) {
			canFall = true
			s.setFluidBlock(d, below, fluidState(kind, fluidFalling))
			s.scheduleFluidTick(d, below, kind.tickDelay(d))
		}
	}
	// A source feeds every direction. Everything else spreads sideways
	// only once it has come to rest: a fluid with somewhere to fall goes
	// there instead, and the middle of a falling column stays one block
	// wide instead of fanning out at every level on the way down.
	if level != 0 && (canFall || columnBelow) {
		return
	}

	// Then sideways, but only while there is level left to spend.
	drop := kind.dropOff(d)
	eff := spreadLevel(level)
	if eff+drop > fluidMaxLevel {
		return
	}
	next := fluidState(kind, eff+drop)
	for _, dir := range horizontalDirections {
		nb := world.Pos{X: pos.X + dir.X, Y: pos.Y, Z: pos.Z + dir.Z}
		cur := d.w.GetBlock(nb)
		if k2, l2, ok := fluidOf(cur); ok {
			if k2 != kind {
				continue
			}
			// Never weaken a flow that is already stronger. A falling
			// block is a full-height column, so it counts as the
			// strongest supply there is, not as its literal level 8 —
			// comparing the raw number would let a thin flow overwrite a
			// falling block, and the two would then flip back and forth
			// for the rest of the world's life.
			if l2 == fluidFalling || l2 <= eff+drop {
				continue
			}
		} else if !replaceableByFluid(cur) {
			continue
		}
		s.setFluidBlock(d, nb, next)
		s.scheduleFluidTick(d, nb, kind.tickDelay(d))
		if kind == fluidWater {
			// Water that has just arrived beside lava wakes it, so the
			// lava sets solid on its own next tick.
			s.wakeLava(d, nb)
		}
	}
}

// setFluidBlock writes a fluid-driven block change: the sharded world,
// block-entity cleanup, the Block Update fan-out, and the light update
// for any section whose column top moved. It mirrors the /setblock path
// without the player-facing event, since no player caused this.
func (s *Server) setFluidBlock(d *dimension, pos world.Pos, blk world.Block) {
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
