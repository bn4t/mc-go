package server

import (
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"

	"mc-go/internal/data"
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
// the non-solid decoration blocks a fluid washes away - the same set a
// heightmap looks through, so the world package owns it - except that
// a cell already holding a fluid is never the target of a spread:
// intrinsically-waterlogged blocks (kelp, seagrass, bubble columns)
// answer a full water source to getFluidState, so
// FlowingFluid.canMaybePassThrough's isSourceBlockOfThisType check
// keeps both fluids out, and explicitly waterlogged blocks hold their
// water the same way.
var intrinsicWaterSource = map[string]bool{
	"kelp":          true,
	"kelp_plant":    true,
	"seagrass":      true,
	"tall_seagrass": true,
	"bubble_column": true,
}

func replaceableByFluid(b world.Block) bool {
	if !world.NonSolid(b) {
		return false
	}
	if name, ok := world.StateOwner(b); ok && intrinsicWaterSource[name] {
		return false
	}
	if wl, ok := world.StateProperty(b, "waterlogged"); ok && wl == "true" {
		return false
	}
	return true
}

// fluidCanHold is vanilla's canHoldAnyFluid: a cell a fluid may occupy.
// A cell already holding fluid answers true — fluids do not block
// motion — which is why a lava-flowing cell below counts as a hole for
// water (water seeks lava to harden it, and lava seeks water the same
// way). Over replaceableByFluid it keeps vanilla's exclusions — the
// placeable blocks fluids are not allowed to wash into or through
// (doors, signs, ladders and sugar cane hold their ground; portals are
// untouchable).
func fluidCanHold(b world.Block) bool {
	if _, _, ok := fluidOf(b); ok {
		return true
	}
	if !replaceableByFluid(b) {
		return false
	}
	name, _ := world.StateOwner(b)
	switch name {
	case "sugar_cane", "ladder", "bubble_column", "nether_portal",
		"end_portal", "end_gateway", "structure_void":
		return false
	}
	if strings.HasSuffix(name, "_door") {
		return false
	}
	if strings.HasSuffix(name, "_sign") {
		return false
	}
	return true
}

// The three blocks a lava/water meeting can produce, resolved once on
// demand so a regenerated block table cannot shift them silently.
var (
	solidStatesOnce  sync.Once
	obsidianState    world.Block
	cobblestoneState world.Block
	stoneState       world.Block
)

func resolveSolidStates() {
	solidStatesOnce.Do(func() {
		obsidianState, _ = world.BlockStateOf("obsidian", nil)
		cobblestoneState, _ = world.BlockStateOf("cobblestone", nil)
		stoneState, _ = world.BlockStateOf("stone", nil)
	})
}

// waterContacts lists the five positions around pos that count as water
// reaching lava: the four sides and the block above. The block below is
// deliberately absent, because the real server treats it separately -
// water under lava is not what sets the lava solid.
func waterContacts(pos world.Pos) []world.Pos {
	return []world.Pos{
		{X: pos.X, Y: pos.Y + 1, Z: pos.Z},
		{X: pos.X - 1, Y: pos.Y, Z: pos.Z},
		{X: pos.X + 1, Y: pos.Y, Z: pos.Z},
		{X: pos.X, Y: pos.Y, Z: pos.Z - 1},
		{X: pos.X, Y: pos.Y, Z: pos.Z + 1},
	}
}

// touchesWater reports whether water reaches pos from any of the five
// positions that count.
func (s *Server) touchesWater(d *dimension, pos world.Pos) bool {
	for _, p := range waterContacts(pos) {
		if k, _, ok := fluidOf(d.w.GetBlock(p)); ok && k == fluidWater {
			return true
		}
	}
	return false
}

// lavaWaterContact is what lava becomes when water reaches it, measured
// against the real server rather than assumed. Over every geometry
// tried - water placed beside the lava, water falling on it, water
// flowing up to it from a distance, water arriving beside a flow three
// blocks from its source - the answer depends only on whether the lava
// is a source:
//
//   - a lava source that water reaches sets to obsidian;
//   - a lava flow that water reaches sets to cobblestone, whichever side
//     the water arrived from.
//
// Water directly under lava is not part of this: the lava ignores it and
// the water is what changes, to stone, which is handled where lava
// spreads downwards.
func (s *Server) lavaWaterContact(d *dimension, pos world.Pos, level int) (world.Block, bool) {
	if !s.touchesWater(d, pos) {
		return 0, false
	}
	resolveSolidStates()
	if level == 0 {
		return obsidianState, true
	}
	return cobblestoneState, true
}

// wakeLava sets any lava touching pos solid at once and queues the rest
// of the fluid re-evaluation around it. The real server does this in the
// block-update path rather than on the lava's own scheduled tick, so
// water placed beside lava solidifies it in the same tick it lands; a
// scheduled tick would leave the lava spreading for another 30 ticks
// while the water kept flowing.
func (s *Server) wakeLava(d *dimension, pos world.Pos) {
	for _, p := range append([]world.Pos{pos}, neighbourPositions(pos)...) {
		k, level, ok := fluidOf(d.w.GetBlock(p))
		if !ok || k != fluidLava {
			continue
		}
		if solid, ok := s.lavaWaterContact(d, p, level); ok {
			s.setNaturalBlock(d, p, solid)
			for _, q := range append([]world.Pos{p}, neighbourPositions(p)...) {
				if k2, _, ok := fluidOf(d.w.GetBlock(q)); ok {
					s.scheduleFluidTick(d, q, k2.tickDelay(d))
				}
			}
			continue
		}
		s.scheduleFluidTick(d, p, k.tickDelay(d))
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
// Vanilla reaches the same positions through neighbour updates, and sets
// lava solid in the same update rather than waiting for its tick.
func (s *Server) wakeFluids(d *dimension, pos world.Pos) {
	if s.ticks == nil || d == nil {
		return
	}
	s.wakeLava(d, pos)
	for _, p := range append([]world.Pos{pos}, neighbourPositions(pos)...) {
		if k, _, ok := fluidOf(d.w.GetBlock(p)); ok {
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
	// Water meeting lava changes the lava: obsidian at a source,
	// cobblestone at a flow, whichever side the water came from.
	if kind == fluidLava {
		if solid, ok := s.lavaWaterContact(d, pos, level); ok {
			s.setNaturalBlock(d, pos, solid)
			s.wakeFluids(d, pos)
			return
		}
	}
	var next world.Block = cur
	// Vanilla's tick gates the recompute on !isSource — a source is
	// permanent and never re-derives itself.
	if level != 0 {
		next = s.newFluidState(d, pos, kind)
	}
	if next != cur {
		s.setNaturalBlock(d, pos, next)
		if next == world.Air {
			// Dried up: neighbours lose their supply and will be
			// re-evaluated by their own ticks.
			s.wakeFluids(d, pos)
			return
		}
		// A level change re-arms the cell itself: that is what lets a
		// severed flow decay through its levels instead of dying in
		// one step or stalling on a still-equal neighbour. Vanilla
		// hands the delay to getSpreadDelay — lava's reschedule is
		// four ticks three times in four when the new state is taller.
		kind, level, ok = fluidOf(next)
		if !ok {
			return
		}
		s.scheduleFluidTick(d, pos, s.spreadDelay(d, pos, kind, cur, next))
	}
	s.spreadFluid(d, pos, kind, level)
}

// spreadDelay is vanilla's getSpreadDelay: the re-arm delay after a
// level change. Water always takes its tick delay; lava delays four
// times as long three times in four when it only got taller (slower
// level), which is why lava's receding edge moves so unevenly.
func (s *Server) spreadDelay(d *dimension, pos world.Pos, kind fluidKind, old, next world.Block) int64 {
	delay := kind.tickDelay(d)
	if kind != fluidLava {
		return delay
	}
	_, ol, _ := fluidOf(old)
	_, nl, _ := fluidOf(next)
	if ol >= 1 && ol <= fluidMaxLevel && nl >= 1 && nl <= fluidMaxLevel && nl < ol && rand.Intn(4) != 0 {
		return delay * 4
	}
	return delay
}

// canConvertToSource is vanilla's gamerule-gated source renewal: water
// converts wherever water_source_conversion allows it, lava where
// lava_source_conversion does (off by default).
func (s *Server) canConvertToSource(kind fluidKind) bool {
	switch kind {
	case fluidWater:
		return s.gameRuleBool("minecraft:water_source_conversion")
	case fluidLava:
		return s.gameRuleBool("minecraft:lava_source_conversion")
	}
	return false
}

// newFluidState derives what the fluid at pos should be, given its
// neighbours — vanilla's FlowingFluid.getNewLiquid. It is called for
// non-source cells only; the source-stays rule lives in the caller.
func (s *Server) newFluidState(d *dimension, pos world.Pos, kind fluidKind) world.Block {
	// Two or more source neighbours over solid ground (or another
	// source) merge into a new source — infinite water, and lava too
	// when its conversion gamerule is on. The neighbour levels are
	// gathered in the same pass vanilla uses for highestNeighbor.
	sources := 0
	best := -1
	for _, dir := range horizontalDirections {
		nb := world.Pos{X: pos.X + dir.X, Y: pos.Y, Z: pos.Z + dir.Z}
		k2, l2, ok := fluidOf(d.w.GetBlock(nb))
		if !ok || k2 != kind {
			continue
		}
		if l2 == 0 {
			sources++
		}
		if l2 == fluidFalling {
			l2 = 0 // a falling column counts as full strength
		}
		if best < 0 || l2 < best {
			best = l2
		}
	}
	if sources >= 2 && s.canConvertToSource(kind) {
		below := d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
		bk, bl, bok := fluidOf(below)
		if (!bok && !world.NonSolid(below)) || (bok && bk == kind && bl == 0) {
			return fluidState(kind, 0)
		}
	}
	// Fluid directly above turns this block into falling fluid.
	if above, _, ok := fluidOf(d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z})); ok && above == kind {
		return fluidState(kind, fluidFalling)
	}
	// The strongest horizontal supply wins, with equal-strength
	// neighbours still feeding: a severed cell's next state is one
	// level weaker than its best neighbour, so a ring of equals decays
	// through the levels rather than dying on the spot or living
	// forever. Vanilla has no strictness check — the decay comes for
	// free from best+dropOff being a fixed step down.
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
			switch {
			case kind == fluidLava && k2 == fluidWater:
				// Lava reaching down into water sets the water to stone.
				// This is the one place the two fluids meet from opposite
				// sides of the block: the lava stays, the water hardens,
				// and the lava is then still resting, so it goes on to
				// spread sideways below.
				resolveSolidStates()
				s.setNaturalBlock(d, below, stoneState)
				s.wakeFluids(d, below)
			default:
				columnBelow = k2 == kind
			}
		} else if fluidCanHold(under) {
			canFall = true
			// spreadTo writes the below cell's own recompute — falling
			// whenever this fluid feeds it, but a full source when two
			// more source neighbours merge there (infinite water).
			if nf := s.newFluidState(d, below, kind); nf != world.Air {
				s.fluidSpreadTo(d, below, nf, kind)
			}
		}
	}
	// A fluid with somewhere to fall goes there instead of spreading
	// sideways, and this holds for a source too: a source dropped in the
	// air sends down a one-block column and fans out at its own level
	// only once that column is under it, which is a tick later. The
	// reschedule is what gives it that next tick - the real server gets
	// it from the block update the new falling block causes.
	if canFall {
		s.scheduleFluidTick(d, pos, kind.tickDelay(d))
		return
	}
	// Once the fluid below is the same fluid, this block is standing on
	// its own column: a source at the top of one fans out, while the
	// middle of a falling column stays one block wide rather than
	// spreading at every level on the way down.
	if columnBelow && level != 0 {
		return
	}

	// Then sideways, but only while there is level left to spend.
	drop := kind.dropOff(d)
	eff := spreadLevel(level)
	if eff+drop > fluidMaxLevel {
		return
	}
	// getSpread: a fluid does not fan out in every direction — it runs
	// toward the nearest place it could fall, up to the fluid's slope
	// find distance (4 for water, 2 for lava outside the nether). Each
	// candidate direction scores the distance from the neighbour to a
	// hole — a cell over something fluid can fall into — and only the
	// directions tied for the smallest score take the flow. Flat ground
	// has no holes anywhere, so every direction scores the same and the
	// pool fans out in all four.
	lowest := fluidSlopeNoHole
	var nstates [4]world.Block
	for i, dir := range horizontalDirections {
		nb := world.Pos{X: pos.X + dir.X, Y: pos.Y, Z: pos.Z + dir.Z}
		cur := d.w.GetBlock(nb)
		k2, l2, ok := fluidOf(cur)
		if ok && k2 == kind && l2 == 0 {
			continue // canMaybePassThrough: never into a source of the same kind
		}
		if !ok && !fluidCanHold(cur) {
			continue // not traversable
		}
		dist := 0
		if !s.fluidIsHole(d, nb, kind) {
			dist = s.fluidSlopeDist(d, kind, nb, 1, oppositeDir[i])
		}
		if dist < lowest {
			lowest = dist
			nstates = [4]world.Block{}
		}
		if dist <= lowest {
			lowest = dist
			// A same-kind flow competes for the distance but is never a
			// spread target — canBeReplacedWith only lets water be
			// replaced from above by another fluid, so its level waits
			// for its own scheduled tick. Other-kind fluid is likewise
			// only traversable; lava contacts resolve through wakeLava.
			if !ok {
				nstates[i] = s.newFluidState(d, nb, kind)
			}
		}
	}
	for i, dir := range horizontalDirections {
		next := nstates[i]
		if next == 0 || next == world.Air {
			continue
		}
		nb := world.Pos{X: pos.X + dir.X, Y: pos.Y, Z: pos.Z + dir.Z}
		s.fluidSpreadTo(d, nb, next, kind)
	}
}

// fluidSlopeNoHole is getSlopeDistance's "never found a hole" answer,
// far past the deepest any search can legitimately score.
const fluidSlopeNoHole = 1000

// oppositeDir maps each horizontalDirections index to the direction
// pointing back the way it came — Direction.getOpposite for the search.
var oppositeDir = [4]int{3, 2, 1, 0}

// slopeFindDistance is how far a fluid hunts sideways for somewhere to
// fall: water searches four blocks, lava two outside the nether.
func (f fluidKind) slopeFindDistance(d *dimension) int {
	if f == fluidLava && (d == nil || !d.ultraWarm) {
		return 2
	}
	return 4
}

// fluidIsHole is vanilla's SpreadContext.isHole: the cell's below is
// enterable or already the same fluid, so fluid arriving here falls on.
func (s *Server) fluidIsHole(d *dimension, pos world.Pos, kind fluidKind) bool {
	below := d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	if k2, _, ok := fluidOf(below); ok && k2 == kind {
		return true
	}
	return fluidCanHold(below)
}

// fluidSlopeDist is vanilla's getSlopeDistance: the hop distance from
// pos to the nearest cell a fluid could fall through, walking only
// fluid-traversable cells and never back the way it came.
func (s *Server) fluidSlopeDist(d *dimension, kind fluidKind, pos world.Pos, pass, from int) int {
	lowest := fluidSlopeNoHole
	for i, dir := range horizontalDirections {
		if i == from {
			continue
		}
		tp := world.Pos{X: pos.X + dir.X, Y: pos.Y, Z: pos.Z + dir.Z}
		ts := d.w.GetBlock(tp)
		k2, l2, ok := fluidOf(ts)
		if ok && k2 == kind && l2 == 0 {
			continue // a same-kind source is not traversable
		}
		if !fluidCanHold(ts) {
			if !ok {
				continue
			}
			// A flow (of either kind) is traversable even though it can
			// never be a spread target.
		}
		if s.fluidIsHole(d, tp, kind) {
			return pass
		}
		if pass < kind.slopeFindDistance(d) {
			if v := s.fluidSlopeDist(d, kind, tp, pass+1, oppositeDir[i]); v < lowest {
				lowest = v
			}
		}
	}
	return lowest
}

// rollFluidLoot rolls a washed-away block's loot table the way
// beforeDestroyingBlock's dropResources does: no tool, no player.
func rollFluidLoot(table string, state world.Block, pos world.Pos, d *dimension) []ItemStack {
	ctx := &lootCtx{
		block: table[len("blocks/"):],
		state: state,
		pos:   pos,
		dim:   d,
		rng:   rand.New(rand.NewSource(rand.Int63())),
	}
	return rollLootDepth(ctx, table, 0)
}

// fluidSpreadTo is vanilla's spreadTo: write the neighbour's recomputed
// state, destroying what stood there first — with that block's drops
// (beforeDestroyingBlock → Block.dropResources), and arm its own tick.
func (s *Server) fluidSpreadTo(d *dimension, nb world.Pos, next world.Block, kind fluidKind) {
	cur := d.w.GetBlock(nb)
	if cur != world.Air {
		// Water washing away a plant or redstone component drops it,
		// the same loot table a player break would roll. Fluids and
		// unlootable cells leave nothing.
		if name, ok := world.StateOwner(cur); ok {
			if _, hasTable := data.LootTables()["blocks/"+name]; hasTable {
				for _, st := range rollFluidLoot("blocks/"+name, cur, nb, d) {
					s.spawnItemDrop(d.name, nb, st)
				}
			}
		}
	}
	s.setNaturalBlock(d, nb, next)
	s.scheduleFluidTick(d, nb, kind.tickDelay(d))
	if kind == fluidWater {
		// Water that has just arrived beside lava wakes it, so the
		// lava sets solid on its own next tick.
		s.wakeLava(d, nb)
	}
}

// setNaturalBlock writes a natural block change (fluid flow, random
// ticks, melt/freeze): the sharded world,
// block-entity cleanup, the Block Update fan-out, the light update
// for any section whose column top moved, fluid re-evaluation of pos
// and its neighbours, and the updateShape-style survival re-check.
// It mirrors vanilla's full setBlock flags for a natural write; writes
// that correspond to flag-2 silent setBlock (no neighbor updates) use
// setSilentBlock instead.
func (s *Server) setNaturalBlock(d *dimension, pos world.Pos, blk world.Block) {
	// Gameplay writes hand neighbourChanged the OLD state.
	s.setNaturalBlockNotify(d, pos, blk, d.w.GetBlock(pos))
}

// setNaturalBlockNotify is setNaturalBlock with an explicit notify
// block: the state vanilla hands each neighbour's neighbourChanged.
// Command writes (setblock/fill/clone) pass the NEW state — their
// updateNeighboursOnBlockSet reads the cell post-write.
func (s *Server) setNaturalBlockNotify(d *dimension, pos world.Pos, blk, notify world.Block) {
	s.setSilentBlock(d, pos, blk)
	s.wakeFluids(d, pos)
	s.neighborSurvival(d, pos, notify)
}

// fluidSurfaceHeight is the rendered fluid surface inside a cell:
// falling cells (level >= 8) read full height, flowing cells fall off
// by level, sources sit at the top (level 0 -> 9/9 = 1).
func fluidSurfaceHeight(level int) float64 {
	if level >= 8 {
		return 1.0
	}
	return float64(9-level) / 9
}

// entityFluidScan reads the fluid overlap for an entity box: the
// deepest submersion (fluid surface above the feet, clamped to the
// box), whether any cell held `kind`, and the accumulated horizontal
// flow vector of flowing cells plus their count — the tracker state
// EntityFluidInteraction.update builds for applyCurrentTo.
func entityFluidScan(w *world.World, x, y, z, half, height float64, kind fluidKind) (depth float64, in bool, accX, accZ float64, accCount int) {
	x0 := int(math.Floor(x - half))
	x1 := int(math.Floor(x + half))
	z0 := int(math.Floor(z - half))
	z1 := int(math.Floor(z + half))
	y0 := int(math.Floor(y))
	y1 := int(math.Floor(y + height))
	for cx := x0; cx <= x1; cx++ {
		for cy := y0; cy <= y1; cy++ {
			for cz := z0; cz <= z1; cz++ {
				k, lv, ok := fluidOf(w.GetBlock(world.Pos{X: cx, Y: cy, Z: cz}))
				if !ok || k != kind {
					continue
				}
				in = true
				surf := float64(cy) + fluidSurfaceHeight(lv)
				if d := surf - y; d > depth {
					depth = d
				}
				if lv >= 1 && lv <= fluidMaxLevel {
					fx, fz := fluidFlowDir(w, cx, cy, cz, kind)
					accX += fx
					accZ += fz
					accCount++
				}
			}
		}
	}
	return depth, in, accX, accZ, accCount
}

// fluidFlowDir is the horizontal flow gradient of one flowing cell:
// downhill is toward higher effective levels, with open space beside
// the flow scoring the strongest pull (a cell off the edge). Solid
// walls contribute nothing. The result is a unit vector; a still pool
// edge returns zero.
func fluidFlowDir(w *world.World, cx, cy, cz int, kind fluidKind) (float64, float64) {
	_, lv, _ := fluidOf(w.GetBlock(world.Pos{X: cx, Y: cy, Z: cz}))
	var dx, dz float64
	for _, d := range [4][2]int{{0, -1}, {0, 1}, {-1, 0}, {1, 0}} {
		nb := world.Pos{X: cx + d[0], Y: cy, Z: cz + d[1]}
		nk, nlv, ok := fluidOf(w.GetBlock(nb))
		var eff float64
		switch {
		case ok && nk == kind:
			eff = float64(nlv)
		case ok: // the other fluid — impassable like a solid wall
			continue
		default:
			b := w.GetBlock(nb)
			if b == world.Air || world.NonSolid(b) {
				eff = float64(fluidFalling)
			} else {
				continue
			}
		}
		w := eff - float64(lv)
		if w <= 0 {
			continue
		}
		dx += float64(d[0]) * w
		dz += float64(d[1]) * w
	}
	l := math.Hypot(dx, dz)
	if l == 0 {
		return 0, 0
	}
	return dx / l, dz / l
}

// applyFluidCurrent is EntityFluidInteraction.Tracker.applyCurrentTo
// for a non-player entity: the accumulated flow normalizes into a push
// scaled by the tag's factor, with a floor ramp for a nearly still
// entity in a weak current.
func (e *Entity) applyFluidCurrent(accX, accZ float64, count int, factor float64) {
	if count == 0 || accX*accX+accZ*accZ < 9.999999747378752e-6 {
		return
	}
	l := math.Hypot(accX, accZ)
	px, pz := accX/l*factor, accZ/l*factor
	pl := math.Hypot(px, pz)
	if math.Abs(e.MotionX) < 0.003 && math.Abs(e.MotionZ) < 0.003 && pl < 0.0045000000000000005 {
		px = px / pl * 0.0045000000000000005
		pz = pz / pl * 0.0045000000000000005
	}
	e.MotionX += px
	e.MotionZ += pz
}

// entityFluidTick is Entity.updateFluidInteraction for pushable
// entities (items, orbs): the water/lava current push plus the
// submersion readings the tick's gravity-vs-underwater branch and the
// needsSync arm need. The first two return values are the water depth
// and presence; the last two the lava pair.
func entityFluidTick(w *world.World, d *dimension, e *Entity, half, height float64) (waterDepth float64, inWater bool, lavaDepth float64, inLava bool) {
	waterDepth, inWater, ax, az, ac := entityFluidScan(w, e.X, e.Y, e.Z, half, height, fluidWater)
	lavaDepth, inLava, lx, lz, lc := entityFluidScan(w, e.X, e.Y, e.Z, half, height, fluidLava)
	if inWater {
		e.applyFluidCurrent(ax, az, ac, 0.014)
	}
	if inLava {
		f := 0.0023333333333333335
		if d != nil && d.ultraWarm {
			f = 0.007
		}
		e.applyFluidCurrent(lx, lz, lc, f)
	}
	return waterDepth, inWater, lavaDepth, inLava
}
