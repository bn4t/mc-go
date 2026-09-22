package server

import (
	"strconv"
	"strings"

	"mc-go/internal/world"
)

// plainFire / soulFireBlock are the default states getState picks
// between.
var (
	plainFire, _     = world.BlockState("fire")
	soulFireBlock, _ = world.BlockState("soul_fire")
)

// fireFaceDir maps a face direction to the fire state property that
// faces it — vanilla's PROPERTY_BY_DIRECTION (down is absent).
type fireFaceDir struct {
	prop string
	dx   int
	dy   int
	dz   int
}

func (f fireFaceDir) from(pos world.Pos) world.Pos {
	return world.Pos{X: pos.X + f.dx, Y: pos.Y + f.dy, Z: pos.Z + f.dz}
}

var fireFaceDirs = [5]fireFaceDir{
	{prop: "up", dy: 1},
	{prop: "north", dz: -1},
	{prop: "east", dx: 1},
	{prop: "south", dz: 1},
	{prop: "west", dx: -1},
}

// Fire spread and decay — FireBlock's scheduled tick. Vanilla runs this
// on a 30-40 tick cadence (getFireTickDelay = 30 + nextInt(10)) armed by
// onPlace on every fire write and re-armed at the top of each tick. The
// tick order is verbatim:
//
//  1. reschedule (always — even when the gates below stop the body)
//  2. ServerLevel.canSpreadFireAround — the fire_spread_radius_around_player
//     game rule (-1 = always; otherwise a player within N blocks)
//  3. !canSurvive -> removeBlock(pos, false), then continue
//  4. infiniburn = below is in the dimension's infiniburn tag
//  5. !infiniburn && isRaining && isNearRain && nextFloat() < 0.2+0.03*age
//     -> removeBlock
//  6. newAge = min(15, age + nextInt(3)/2); write flag 260 when changed
//  7. !infiniburn && !isValidFireLocation -> removeBlock unless the below
//     face is sturdy and age <= 3, then return (no burnout, no spread)
//  8. age == 15 && nextInt(4) == 0 && !canBurn(below) -> removeBlock
//  9. checkBurnOut on each of 6 neighbors (E,W below,above N,S order:
//     horizontal and vertical base 300, below/above 250, -50 under the
//     nether's INCREASED_FIRE_BURNOUT environment attribute)
// 10. the spread box x[-1,1] z[-1,1] y[-1,4], origin skipped
//
// Direction-dependent details mc-go models or stubs: the environment
// attribute is overworld-false so the -50 bonus never applies; TNT
// priming is unmodeled (mc-go has no primed TNT).

// fireTickDelay mirrors vanilla getFireTickDelay: 30 + nextInt(10).
func (s *Server) fireTickDelay() int { return 30 + s.rng.intn(10) }

// fireOnPlace is BaseFireBlock.onPlace + FireBlock's scheduleTick
// tail: a fresh fire (old cell wasn't the same block) first tries to
// light a portal — inPortalDimension plus an empty shape closes the
// frame and fills it, replacing the fire outright — and otherwise pops
// the fire when it can't survive. FireBlock then schedules the first
// tick 30-40 out; soul fire never schedules (its tick is
// BlockBehaviour's no-op).
func (s *Server) fireOnPlace(d *dimension, pos world.Pos, blk, old world.Block) {
	name, ok := world.StateOwner(blk)
	if !ok {
		return
	}
	short := strings.TrimPrefix(name, "minecraft:")
	switch short {
	case "fire", "soul_fire":
		if !isBlockNamed(old, short) {
			if firePortalDim(d) {
				if p := s.findEmptyPortalShape(d, pos, true); p != nil {
					s.createPortalBlocks(d, p)
				}
			}
			if d.w.GetBlock(pos) == blk && !fireCanSurvive(d, pos, blk) {
				s.setNaturalBlock(d, pos, world.Air)
			}
		}
		if short == "fire" {
			s.scheduleBlockTick(d, pos, int64(s.fireTickDelay()))
		}
	}
}

// fireIgniteOdds is FireBlock.getIgniteOdds(state): waterlogged cells
// can't ignite and everything else reads the bootStrap table.
func fireIgniteOdds(b world.Block) int {
	if v, ok := world.StateProperty(b, "waterlogged"); ok && v == "true" {
		return 0
	}
	name, ok := world.StateOwner(b)
	if !ok {
		return 0
	}
	return fireOdds[strings.TrimPrefix(name, "minecraft:")][0]
}

// fireBurnOdds is FireBlock.getBurnOdds(state) — same table, second
// column.
func fireBurnOdds(b world.Block) int {
	if v, ok := world.StateProperty(b, "waterlogged"); ok && v == "true" {
		return 0
	}
	name, ok := world.StateOwner(b)
	if !ok {
		return 0
	}
	return fireOdds[strings.TrimPrefix(name, "minecraft:")][1]
}

// fireCanBurn is FireBlock.canBurn: ignite odds > 0.
func fireCanBurn(b world.Block) bool { return fireIgniteOdds(b) > 0 }

// fireIgniteOddsAt is getIgniteOdds(LevelReader, pos) — the spread
// target's odds: zero unless the cell itself is empty, then the max
// ignite odds across its six neighbors.
func fireIgniteOddsAt(d *dimension, pos world.Pos) int {
	if !isAirCell(d.w.GetBlock(pos)) {
		return 0
	}
	best := 0
	for _, np := range neighbourPositions(pos) {
		if o := fireIgniteOdds(d.w.GetBlock(np)); o > best {
			best = o
		}
	}
	return best
}

// fireValidLocation is isValidFireLocation: at least one of the six
// neighbors can burn.
func fireValidLocation(d *dimension, pos world.Pos) bool {
	for _, np := range neighbourPositions(pos) {
		if fireCanBurn(d.w.GetBlock(np)) {
			return true
		}
	}
	return false
}

// fireSturdyBelow approximates below.isFaceSturdy(UP) the way the rest
// of mc-go's survival checks do: a non-fluid, solid block.
func fireSturdyBelow(d *dimension, pos world.Pos) bool {
	below := d.w.GetBlock(posBelow(pos, 1))
	if _, _, isFluid := fluidOf(below); isFluid {
		return false
	}
	return !world.NonSolid(below)
}

// fireCanSurvive is FireBlock.canSurvive: sturdy floor or a burnable
// neighbor. Soul fire survives only on its base blocks.
func fireCanSurvive(d *dimension, pos world.Pos, blk world.Block) bool {
	name, _ := world.StateOwner(blk)
	if strings.TrimPrefix(name, "minecraft:") == "soul_fire" {
		return soulFireBase(d.w.GetBlock(posBelow(pos, 1)))
	}
	return fireSturdyBelow(d, pos) || fireValidLocation(d, pos)
}

// soulFireBase is SoulFireBlock.canSurviveOnBlock — the
// soul_fire_base_blocks tag, {soul_sand, soul_soil}.
func soulFireBase(b world.Block) bool {
	name, ok := world.StateOwner(b)
	if !ok {
		return false
	}
	switch strings.TrimPrefix(name, "minecraft:") {
	case "soul_sand", "soul_soil":
		return true
	}
	return false
}

// fireInfiniburn is the dimension's infiniburn tag read on the block
// below: netherrack+magma_block in the overworld and nether, plus
// bedrock in the end.
func fireInfiniburn(d *dimension, pos world.Pos) bool {
	name, ok := world.StateOwner(d.w.GetBlock(posBelow(pos, 1)))
	if !ok {
		return false
	}
	switch strings.TrimPrefix(name, "minecraft:") {
	case "netherrack", "magma_block":
		return true
	case "bedrock":
		return d.name == "minecraft:the_end" || d.name == "the_end"
	}
	return false
}

// fireNearRain is isNearRain: raining at the cell or at any of the four
// horizontal neighbors. isRainingAt needs rain plus sky access; biome
// precipitation is unmodeled.
func (s *Server) fireNearRain(d *dimension, pos world.Pos) bool {
	if s.fireRainingAt(d, pos) {
		return true
	}
	for _, dir := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
		np := world.Pos{X: pos.X + dir[0], Y: pos.Y, Z: pos.Z + dir[1]}
		if s.fireRainingAt(d, np) {
			return true
		}
	}
	return false
}

// fireRainingAt is Level.isRainingAt via precipitationAt: raining,
// sky-visible, and a biome that actually precipitates — the_void, the
// nether and the end never do. (The motion-blocking heightmap check
// is covered by cropLit's upward walk.)
func (s *Server) fireRainingAt(d *dimension, pos world.Pos) bool {
	return s.weatherRaining.Load() && biomePrecipitates(d.biome) && s.cropLit(d, pos)
}

// biomePrecipitates reports whether the dimension's single biome can
// rain — vanilla's Biome.getPrecipitationAt is NONE for the_void and
// every nether/end biome.
func biomePrecipitates(biome string) bool {
	switch strings.TrimPrefix(biome, "minecraft:") {
	case "the_void",
		"nether_wastes", "soul_sand_valley", "crimson_forest",
		"warped_forest", "basalt_deltas",
		"the_end", "end_highlands", "end_midlands",
		"small_end_islands", "end_barrens":
		return false
	}
	return true
}

// fireStateForPlacement is FireBlock.getStateForPlacement(BlockGetter,
// pos): when the floor burns or is sturdy the state is plain fire;
// otherwise each of the five face properties (down has no property)
// folds to canBurn of that neighbor.
func fireStateForPlacement(d *dimension, pos world.Pos) world.Block {
	below := d.w.GetBlock(posBelow(pos, 1))
	if fireCanBurn(below) || fireSturdyBelow(d, pos) {
		return plainFire
	}
	st := plainFire
	for _, dir := range fireFaceDirs {
		if fireCanBurn(d.w.GetBlock(dir.from(pos))) {
			if nb, ok := world.WithProperty(st, dir.prop, "true"); ok {
				st = nb
			}
		}
	}
	return st
}

// fireStateWithAge is getStateWithAge: getState picks soul_fire on a
// soul base, otherwise the face-folded placement state; only plain
// fire takes the AGE write.
func fireStateWithAge(d *dimension, pos world.Pos, age int) world.Block {
	if soulFireBase(d.w.GetBlock(posBelow(pos, 1))) {
		return soulFireBlock
	}
	st := fireStateForPlacement(d, pos)
	if nb, ok := world.WithProperty(st, "age", strconv.Itoa(age)); ok {
		st = nb
	}
	return st
}

// fireNeighborChanged is the updateShape half of the neighbor fan-out:
// fire keeps its age, refolds its faces, and pops to air when it can no
// longer survive. Runs on every 6-neighbor write with either notify
// arg — updateShape reads the live world either way.
func (s *Server) fireNeighborChanged(d *dimension, pos world.Pos, blk world.Block) {
	if !fireCanSurvive(d, pos, blk) {
		s.setNaturalBlock(d, pos, world.Air)
		return
	}
	name, _ := world.StateOwner(blk)
	if strings.TrimPrefix(name, "minecraft:") == "soul_fire" {
		return
	}
	age := fireAge(blk)
	nb := fireStateWithAge(d, pos, age)
	if nb != blk {
		s.setSilentBlock(d, pos, nb)
	}
}

// fireTick is FireBlock.tick — see the file header for the order.
func (s *Server) fireTick(d *dimension, pos world.Pos, blk world.Block) {
	s.scheduleBlockTick(d, pos, int64(s.fireTickDelay()))
	if !s.fireSpreadAround(d, pos) {
		return
	}
	if !fireCanSurvive(d, pos, blk) {
		s.setNaturalBlock(d, pos, world.Air)
		// Vanilla has no early-out here: the removed fire still runs
		// the rest of the tick — the age step can even write it back
		// (onPlace re-pops it) and spread still rolls.
	}
	infiniburn := fireInfiniburn(d, pos)
	age := fireAge(blk)
	if !infiniburn && s.weatherRaining.Load() && s.fireNearRain(d, pos) &&
		s.rng.nextDouble() < 0.2+0.03*float64(age) {
		s.setNaturalBlock(d, pos, world.Air)
		return
	}
	newAge := min(15, age+s.rng.intn(3)/2)
	if newAge != age {
		if nb, ok := world.WithProperty(blk, "age", strconv.Itoa(newAge)); ok {
			s.setSilentBlock(d, pos, nb)
		}
	}
	if !infiniburn {
		if !fireValidLocation(d, pos) {
			if !fireSturdyBelow(d, pos) || age > 3 {
				s.setNaturalBlock(d, pos, world.Air)
			}
			return
		}
		if age == 15 && s.rng.intn(4) == 0 && !fireCanBurn(d.w.GetBlock(posBelow(pos, 1))) {
			s.setNaturalBlock(d, pos, world.Air)
			return
		}
	}
	// checkBurnOut, vanilla call order: E W below above N S. The
	// environment-attribute bonus is -50 in the nether; overworld 0.
	s.fireCheckBurnOut(d, world.Pos{X: pos.X + 1, Y: pos.Y, Z: pos.Z}, 300, age)
	s.fireCheckBurnOut(d, world.Pos{X: pos.X - 1, Y: pos.Y, Z: pos.Z}, 300, age)
	s.fireCheckBurnOut(d, posBelow(pos, 1), 250, age)
	s.fireCheckBurnOut(d, posAbove(pos, 1), 250, age)
	s.fireCheckBurnOut(d, world.Pos{X: pos.X, Y: pos.Y, Z: pos.Z - 1}, 300, age)
	s.fireCheckBurnOut(d, world.Pos{X: pos.X, Y: pos.Y, Z: pos.Z + 1}, 300, age)
	// The spread box walks x,z outer and y inner.
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			for dy := -1; dy <= 4; dy++ {
				if dx == 0 && dz == 0 && dy == 0 {
					continue
				}
				maxT := 100
				if dy > 1 {
					maxT += (dy - 1) * 100
				}
				tp := world.Pos{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}
				odds := fireIgniteOddsAt(d, tp)
				if odds <= 0 {
					continue
				}
				spread := (odds + 40 + int(s.difficulty)*7) / (age + 30)
				if spread <= 0 {
					continue
				}
				if s.rng.intn(maxT) > spread {
					continue
				}
				if s.weatherRaining.Load() && s.fireNearRain(d, tp) {
					continue
				}
				targetAge := min(15, age+s.rng.intn(5)/4)
				s.setNaturalBlock(d, tp, fireStateWithAge(d, tp, targetAge))
			}
		}
	}
}

// fireCheckBurnOut is checkBurnOut: a burn-odds roll at the cell; on
// hit the cell relights as fire (age+10 roll <5 and dry) or is removed,
// and a TNT cell primes afterwards either way.
func (s *Server) fireCheckBurnOut(d *dimension, pos world.Pos, chance, age int) {
	if s.rng.intn(chance) >= fireBurnOdds(d.w.GetBlock(pos)) {
		return
	}
	if s.rng.intn(age+10) < 5 && !s.fireRainingAt(d, pos) {
		a := min(15, age+s.rng.intn(5)/4)
		s.setNaturalBlock(d, pos, fireStateWithAge(d, pos, a))
	} else {
		s.setNaturalBlock(d, pos, world.Air)
	}
	if isBlockNamed(d.w.GetBlock(pos), "tnt") {
		s.primeTntBlock(d, pos, tntFuseTicks)
	}
}

// fireSpreadAround is ServerLevel.canSpreadFireAround gated on the
// fire_spread_radius_around_player game rule: -1 always spreads,
// otherwise a player must be within the radius.
func (s *Server) fireSpreadAround(d *dimension, pos world.Pos) bool {
	s.rulesMu.RLock()
	radius, err := strconv.Atoi(s.gameRules["minecraft:fire_spread_radius_around_player"])
	s.rulesMu.RUnlock()
	if err != nil {
		// Unset or unparsable is vanilla's -1 default: always spreads.
		return true
	}
	if radius < 0 {
		return true
	}
	r2 := float64(radius) * float64(radius)
	found := false
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay || sess.dimName() != d.name {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		dx, dy, dz := px-float64(pos.X), py-float64(pos.Y), pz-float64(pos.Z)
		if dx*dx+dy*dy+dz*dz <= r2 {
			found = true
			return false
		}
		return true
	})
	return found
}

// fireAge reads the AGE property (0 when absent).
func fireAge(b world.Block) int {
	v, ok := world.StateProperty(b, "age")
	if !ok {
		return 0
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
