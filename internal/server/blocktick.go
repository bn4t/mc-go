package server

import (
	"strings"
	"sync"
	"sync/atomic"

	"mc-go/internal/world"
)

// Random block ticks and scheduled block ticks.
//
// Vanilla gives every loaded chunk a number of random-tick attempts per
// tick (the randomTickSpeed game rule, 3 by default) per 16-block
// section, and each attempt asks the block it lands on to "random
// tick": grass spreads, crops grow, leaves decay, ice and snow form and
// melt. That is what makes an untouched world change over time.
//
// mc-go reproduces the mechanism and the per-block rules. The draws are
// NOT bit-identical to vanilla's: vanilla's sequence depends on its own
// RNG state, which no clean-room implementation can reproduce. What is
// reproduced is the rate (attempts per section per tick) and every rule
// those attempts feed, so statistically and behaviorally the world
// evolves the same way and deterministically reaches the same states
// for the rules that converge.

// randState is a splitmix64 generator. It is used where a draw is
// needed but matching vanilla's exact sequence is not required, and it
// is seeded explicitly so a test can replay a run.
type randState struct {
	seed atomic.Uint64
}

func newRandState(seed uint64) *randState {
	r := &randState{}
	r.seed.Store(seed)
	return r
}

// next is one SplitMix64 step. The seed advances with a single atomic
// add, so concurrent callers each get a distinct step without a lock —
// the random-tick pass draws millions per tick at high tick speeds.
func (r *randState) next() uint64 {
	z := r.seed.Add(0x9E3779B97F4A7C15)
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// intn returns a uniform value in [0, n).
func (r *randState) intn(n int) int {
	if n <= 0 {
		return 0
	}
	return int(r.next() % uint64(n))
}

// nextDouble returns a uniform value in [0, 1) — Java's 53-bit
// RandomSource.nextDouble.
func (r *randState) nextDouble() float64 {
	return float64(r.next()>>11) * (1.0 / 9007199254740992.0)
}

// chance reports a 1-in-n draw.
func (r *randState) chance(n int) bool {
	if n <= 1 {
		return true
	}
	return r.next()%uint64(n) == 0
}

// randSectionKey identifies one 16^3 section of one dimension.
type randSectionKey struct {
	dim     string
	x, y, z int
}

// Section cache flag bits.
const (
	secTickable uint8 = 1 << iota // holds a block with a random-tick rule
	secOpaque                     // holds a block that is neither air nor light-passable
)

// sectionFlags returns the cached flags for a section, scanning it once
// on first use. The scan doubles as the cropLit shortcut: a section with
// no opaque blocks can never shade a crop column, so cropLit skips it
// without a single block read.
func (s *Server) sectionFlags(d *dimension, sec randSectionKey) uint8 {
	s.randMu.RLock()
	if v, ok := s.randSections[sec]; ok {
		s.randMu.RUnlock()
		return v
	}
	s.randMu.RUnlock()

	min := world.Pos{X: sec.x * 16, Y: d.minY + sec.y*16, Z: sec.z * 16}
	max := world.Pos{X: min.X + 15, Y: min.Y + 15, Z: min.Z + 15}
	buf := make([]world.Block, 16*16*16)
	d.w.FillBox(min, max, buf)
	var flags uint8
	for _, b := range buf {
		if b == world.Air {
			continue
		}
		name, ok := world.StateOwner(b)
		if !ok {
			continue
		}
		if _, ok := randRules()[name]; ok {
			flags |= secTickable
		}
		if !lightPassable[name] {
			flags |= secOpaque
		}
		if flags == secTickable|secOpaque {
			break
		}
	}
	s.randMu.Lock()
	s.randSections[sec] = flags
	s.randMu.Unlock()
	return flags
}

// sectionHasRandomTicks reports whether a section holds any block with a
// random-tick rule, caching the answer.
//
// This is the difference between a live world and a stalled one: without
// it, every tick samples three positions in every one of a chunk's 24
// sections for all 289 chunks a player keeps loaded — twenty thousand
// block reads a tick, nearly all of them air. Vanilla keeps the same
// fact per section (`LevelChunkSection.isRandomlyTicking`); the cache is
// invalidated whenever a block in the section changes.
func (s *Server) sectionHasRandomTicks(d *dimension, sec randSectionKey) bool {
	return s.sectionFlags(d, sec)&secTickable != 0
}

// sectionFlagsWarm reads the cached flags without ever scanning. The
// random-tick pass uses it to keep a cold join cheap: building a flag
// costs a 4096-block FillBox, and a freshly streamed window holds on
// the order of ten thousand cold sections — scanning them all in one
// tick stalls the whole world for several hundred milliseconds. The
// pass therefore warms only randScanBudget cold sections per call and
// treats the rest as untickable for that tick; they join on later ticks.
func (s *Server) sectionFlagsWarm(sec randSectionKey) (uint8, bool) {
	s.randMu.RLock()
	v, ok := s.randSections[sec]
	s.randMu.RUnlock()
	return v, ok
}

// randScanBudget caps how many cold section-flag scans one tick pays
// for. Sixty-four scans ≈ 2ms — the whole fresh-window warmup drains
// over a few seconds of normal ticking instead of one stalled tick.
const randScanBudget = 64

// warmSectionFlags builds the cached random-tick/light flags for every
// section of column (cx, cz) that lacks them. sendWindow calls it under
// simulation ownership so the tick pass finds its targets already warm.
// Cold skylight columns share one detached scan with light preparation;
// later flag invalidation still pays only for the affected sections.
func (s *Server) warmSectionFlags(d *dimension, cx, cz int) {
	if s.randSections == nil {
		return
	}
	if d.skylight {
		s.prepareColumnFactsOwned(d, cx, cz)
	}
	for sec := 0; sec < d.height/16; sec++ {
		s.sectionFlags(d, randSectionKey{dim: d.name, x: cx, y: sec, z: cz})
	}
}

// invalidateRandomTickSection drops the cached answer for the section a
// block write touched.
func (s *Server) invalidateRandomTickSection(d *dimension, pos world.Pos) {
	if s.randSections == nil {
		return
	}
	key := randSectionKey{
		dim: d.name,
		x:   floorDiv(pos.X, 16),
		y:   floorDiv(pos.Y-d.minY, 16),
		z:   floorDiv(pos.Z, 16),
	}
	s.randMu.Lock()
	delete(s.randSections, key)
	s.randMu.Unlock()
}

// opaqueTopKey names one block column of one dimension.
type opaqueTopKey struct {
	dim  string
	x, z int
}

// opaqueTopEntry is one cached column answer and the column's write
// version it was computed at.
type opaqueTopEntry struct {
	top int
	ver uint32
}

// opaqueTop is the highest Y in column (x, z) holding a block that is
// neither air nor light-passable, or minY-1 when there is none — what
// cropLit's upward walk is looking for. The answer is cached per column
// and trusted only while the world reports no write to that column since
// (every write path bumps the column's version), so raw SetBlock callers
// need no hook.
func (s *Server) opaqueTop(d *dimension, x, z int) int {
	key := opaqueTopKey{dim: d.name, x: x, z: z}
	ver := d.w.ColumnVersion(x, z)
	s.randMu.RLock()
	e, ok := s.opaqueTops[key]
	s.randMu.RUnlock()
	if ok && e.ver == ver {
		return e.top
	}
	// A miss reads the whole column in one shard pass rather than
	// trusting per-section flags, which raw world writes do not refresh.
	t := d.minY - 1
	col := make([]world.Block, d.height)
	d.w.FillBox(world.Pos{X: x, Y: d.minY, Z: z}, world.Pos{X: x, Y: d.minY + d.height - 1, Z: z}, col)
	for i := len(col) - 1; i >= 0; i-- {
		if col[i] == world.Air {
			continue
		}
		if name, _ := world.StateOwner(col[i]); lightPassable[name] {
			continue
		}
		t = d.minY + i
		break
	}
	if s.randSections != nil {
		s.randMu.Lock()
		if s.opaqueTops == nil {
			s.opaqueTops = map[opaqueTopKey]opaqueTopEntry{}
		}
		s.opaqueTops[key] = opaqueTopEntry{top: t, ver: ver}
		s.randMu.Unlock()
	}
	return t
}

func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// randomTickStep runs one tick of random block ticks across the chunks
// that have players near them, inside the simulation distance.
func (s *Server) randomTickStep() {
	if s.rng == nil || len(randRules()) == 0 {
		// Nothing in the registry has random-tick behavior, so there is
		// nothing to sample for. This is not just an optimization: it is
		// the same short-circuit vanilla gets from a section whose blocks
		// all report isRandomlyTicking() == false.
		return
	}
	speed := s.randomTickSpeed()
	if speed <= 0 {
		return
	}
	for _, d := range s.dims {
		s.randomTickDimension(d, speed)
	}
}

// randomTickTargets collects the chunks in d that run block ticks: the
// rings of simulation distance + 1 around players' ticket chunks.
func (s *Server) randomTickTargets(d *dimension) map[[2]int]bool {
	sim := s.simulationDistance() + 1
	out := map[[2]int]bool{}
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay || sess.dimName() != d.name {
			return true
		}
		ccx, ccz := sess.ticketChunk()
		for dx := -sim; dx <= sim; dx++ {
			for dz := -sim; dz <= sim; dz++ {
				out[[2]int{ccx + dx, ccz + dz}] = true
			}
		}
		return true
	})
	return out
}

func (s *Server) randomTickDimension(d *dimension, speed int) {
	chunks := s.randomTickTargets(d)
	if len(chunks) == 0 {
		return
	}
	sections := d.height / 16
	scans := randScanBudget
	for ck := range chunks {
		cx, cz := ck[0], ck[1]
		// A column whose terrain is still being generated has not
		// loaded yet: it does not tick.
		if !d.terrainReady(cx, cz) {
			continue
		}
		// tickChunk's iceandsnow pass: one 1/48 roll per speed unit
		// picks a random column for freeze/snow-accumulate.
		for i := 0; i < speed; i++ {
			if s.rng.intn(48) == 0 {
				s.tickPrecipitation(d, cx, cz)
			}
		}
		for sec := 0; sec < sections; sec++ {
			// Sections with nothing to random-tick cost one cached map
			// lookup instead of a 4096-block snapshot. A cold section
			// costs a scan of that snapshot, so only a bounded number
			// warm per tick (see randScanBudget); the rest are skipped
			// as if untickable and join on later ticks.
			key := randSectionKey{dim: d.name, x: cx, y: sec, z: cz}
			flags, warm := s.sectionFlagsWarm(key)
			if !warm {
				if scans <= 0 {
					continue
				}
				scans--
				flags = s.sectionFlags(d, key)
			}
			if flags&secTickable == 0 {
				continue
			}
			// Each roll is one uniform position inside the section read
			// live, exactly what vanilla's chunkSource.tickChunk does —
			// speed point reads against the world per section per tick.
			// (Snapshotting the whole section just to roll three cells
			// made the tick do ~4096 shard reads per chunk and starved
			// the loop under load.)
			min := world.Pos{X: cx * 16, Y: d.minY + sec*16, Z: cz * 16}
			for i := 0; i < speed; i++ {
				p := world.Pos{
					X: min.X + s.rng.intn(16),
					Y: min.Y + s.rng.intn(16),
					Z: min.Z + s.rng.intn(16),
				}
				blk := d.w.GetBlock(p)
				name, ok := world.StateOwner(blk)
				if !ok {
					continue
				}
				rule, ok := randRules()[name]
				if !ok {
					continue
				}
				rule(s, d, p, blk)
			}
		}
	}
}

// rtBlock reads a block for a random-tick rule against the live world.
func (s *Server) rtBlock(d *dimension, p world.Pos) world.Block {
	return d.w.GetBlock(p)
}

// randomTickBlock applies a block's random-tick rule, if it has one.
// Rules are keyed by block name so a regenerated block table cannot
// silently shift them.
func (s *Server) randomTickBlock(d *dimension, pos world.Pos) {
	blk := d.w.GetBlock(pos)
	name, ok := world.StateOwner(blk)
	if !ok {
		return
	}
	if rule, ok := randRules()[name]; ok {
		rule(s, d, pos, blk)
	}
}

// randRules maps a block name to its random-tick behavior. Rules
// are added as their milestone lands; an unknown block simply has none,
// which is also true in vanilla for most blocks. The map builds lazily:
// a package-level initializer would form an init cycle through the rule
// closures back into sectionFlags, which reads this same map.
var (
	randRulesOnce sync.Once
	randRulesMap  map[string]func(*Server, *dimension, world.Pos, world.Block)
)

func randRules() map[string]func(*Server, *dimension, world.Pos, world.Block) {
	randRulesOnce.Do(func() {
		randRulesMap = map[string]func(*Server, *dimension, world.Pos, world.Block){
			"wheat":                   cropRule(7),
			"carrots":                 cropRule(7),
			"potatoes":                cropRule(7),
			"beetroots":               cropRule(3),
			"torchflower_crop":        torchflowerRule,
			"melon_stem":              stemRule("melon", "attached_melon_stem", "supports_melon_stem_fruit"),
			"pumpkin_stem":            stemRule("pumpkin", "attached_pumpkin_stem", "supports_pumpkin_stem_fruit"),
			"nether_wart":             wartRule,
			"cocoa":                   cocoaRule,
			"sweet_berry_bush":        berryRule,
			"pitcher_crop":            pitcherRule,
			"farmland":                farmlandRule,
			"grass_block":             turfRule("dirt"),
			"mycelium":                turfRule("dirt"),
			"crimson_nylium":          nyliumRule,
			"warped_nylium":           nyliumRule,
			"sugar_cane":              sugarCaneRule,
			"cactus":                  cactusRule,
			"bamboo":                  bambooStalkRule,
			"bamboo_sapling":          bambooSaplingRule,
			"chorus_flower":           chorusFlowerRule,
			"weeping_vines":           vineHeadRule("weeping_vines", "weeping_vines_plant", -1, false),
			"twisting_vines":          vineHeadRule("twisting_vines", "twisting_vines_plant", 1, false),
			"cave_vines":              vineHeadRule("cave_vines", "cave_vines_plant", -1, true),
			"kelp":                    vineHeadRule("kelp", "kelp_plant", 1, false),
			"oak_leaves":              leafDecayRule,
			"spruce_leaves":           leafDecayRule,
			"birch_leaves":            leafDecayRule,
			"jungle_leaves":           leafDecayRule,
			"acacia_leaves":           leafDecayRule,
			"dark_oak_leaves":         leafDecayRule,
			"mangrove_leaves":         leafDecayRule,
			"cherry_leaves":           leafDecayRule,
			"pale_oak_leaves":         leafDecayRule,
			"azalea_leaves":           leafDecayRule,
			"flowering_azalea_leaves": leafDecayRule,
			"budding_amethyst":        amethystBudRule,
			"brown_mushroom":          mushroomRule,
			"red_mushroom":            mushroomRule,
			"vine":                    vineRule,
			"ice":                     iceMeltRule,
			"snow":                    snowMeltRule,
			"turtle_egg":              turtleEggRule,
			"redstone_ore":            redstoneOreRule,
			"deepslate_redstone_ore":  redstoneOreRule,
			"oak_sapling":             saplingRule,
			"spruce_sapling":          saplingRule,
			"birch_sapling":           saplingRule,
			"jungle_sapling":          saplingRule,
			"acacia_sapling":          saplingRule,
			"dark_oak_sapling":        saplingRule,
			"mangrove_propagule":      propaguleRule,
			"cherry_sapling":          saplingRule,
			"pale_oak_sapling":        saplingRule,
		}
		// WeatheringCopper.isRandomlyTicking = getNext().isPresent() —
		// the oxidized stage is terminal and stays out of the table.
		randRulesMap["pointed_dripstone"] = speleoRule
		randRulesMap["sulfur_spike"] = speleoRule
		for _, fam := range copperFamilies {
			for _, n := range fam[:3] {
				randRulesMap[n] = copperRule
			}
		}
	})
	return randRulesMap
}

// blockTick handles one scheduled (non-random) block tick. Redstone
// and crop growth scheduling will land here; the only current client
// is the button release a wind-charge burst schedules.
func (s *Server) blockTick(d *dimension, pos world.Pos) {
	blk := d.w.GetBlock(pos)
	name, ok := world.StateOwner(blk)
	if !ok {
		return
	}
	short := strings.TrimPrefix(name, "minecraft:")
	if isLeafBlock(short) {
		s.leafDistanceTick(d, pos)
		return
	}
	if short == "frosted_ice" {
		s.frostedTick(d, pos, blk)
		return
	}
	if short == "fire" {
		s.fireTick(d, pos, blk)
		return
	}
	if short == "water" {
		s.bubbleLiquidTick(d, pos)
		return
	}
	if short == "bubble_column" {
		s.bubbleColumnTick(d, pos)
		return
	}
	if short == "scaffolding" {
		s.scaffoldTick(d, pos, blk)
		return
	}
	if coralLive(short) {
		s.coralTick(d, pos, blk)
		return
	}
	if short == "pointed_dripstone" || short == "sulfur_spike" {
		s.speleoTick(d, pos, blk)
		return
	}
	if short == "cauldron" || short == "water_cauldron" {
		s.cauldronDripTick(d, pos)
		return
	}
	if short == "tripwire" {
		s.tripwireTick(d, pos, blk)
		return
	}
	if isSculkSensor(short) {
		s.sculkSensorTick(d, pos, blk)
		return
	}
	if short == "frogspawn" {
		s.frogspawnTick(d, pos)
		return
	}
	if short == "sniffer_egg" {
		s.snifferEggTick(d, pos)
		return
	}
	if short == "repeater" {
		s.rsDiodeTick(d, pos, blk, short)
		return
	}
	if short == "comparator" {
		// ComparatorBlock.tick = refreshOutputState.
		s.rsCompRefresh(d, pos, blk)
		return
	}
	if short == "lectern" {
		// LecternBlock.tick: the page-turn pulse's 2-tick release.
		s.lecternTick(d, pos)
		return
	}
	if short == "redstone_lamp" {
		// RedstoneLampBlock.tick: the unlatch lands 4 ticks after the
		// signal dropped — but only if it's still dropped.
		lit, _ := world.StateProperty(blk, "lit")
		if lit != "true" || rsHasNeighborSignal(d, pos) {
			return
		}
		if nb, ok := world.WithProperty(blk, "lit", "false"); ok {
			s.rsWriteBlock(d, pos, nb, nb, nil)
		}
		return
	}
	if short == "dispenser" || short == "dropper" {
		s.dispenseFrom(d, pos, blk)
		return
	}
	if short == "observer" {
		// ObserverBlock.tick: a powered observer unpowers; an
		// unpowered one powers and re-arms the un-power 2 ticks out —
		// the 2-tick pulse. Both write flag 2 then
		// updateNeighborsInFront.
		powered := rsPropsOf(blk)("powered") == "true"
		if nb, ok := world.WithProperty(blk, "powered", boolStr(!powered)); ok {
			s.rsWriteBlockFlag2(d, pos, nb)
		}
		if !powered {
			s.ticks.schedule(schedKey{dim: d.name, pos: pos, kind: tickBlock},
				s.TickCount()+2)
		}
		s.rsDiodeUpdateInFront(d, pos, blk)
		return
	}
	if short == "redstone_torch" || short == "redstone_wall_torch" {
		// RedstoneTorchBlock.tick: prune toggles older than
		// RECENT_TOGGLE_TIMER(60), then flip if lit still agrees with
		// the mount's signal after the 2-tick delay. Going dark records
		// a toggle — the 8th inside the window fires the burnout
		// (levelEvent 1502) and re-arms a 160-tick restart; relights
		// are refused while the count stays at the cap.
		lit, _ := world.StateProperty(blk, "lit")
		if lit == "" {
			return
		}
		s.rsTorchPruneToggles(d)
		sig := rsTorchSeesSignal(d, pos, blk)
		if lit == "true" != sig {
			return
		}
		if lit == "true" {
			if nb, ok := world.WithProperty(blk, "lit", "false"); ok {
				s.rsWriteBlock(d, pos, nb, nb, nil)
			}
			if s.rsTorchTooOften(d, pos, true) {
				s.broadcastLevelEvent(d.name, 1502, pos, 0)
				s.ticks.schedule(schedKey{dim: d.name, pos: pos, kind: tickBlock},
					s.TickCount()+160)
			}
			return
		}
		if s.rsTorchTooOften(d, pos, false) {
			return
		}
		if nb, ok := world.WithProperty(blk, "lit", "true"); ok {
			s.rsWriteBlock(d, pos, nb, nb, nil)
		}
		return
	}
	if !strings.HasSuffix(short, "_button") {
		return
	}
	// ButtonBlock.tick: an unpowered button ignores its tick; a powered
	// one releases with the click-off sound.
	if p, _ := world.StateProperty(blk, "powered"); p != "true" {
		return
	}
	nb, ok := world.WithProperty(blk, "powered", "false")
	if !ok {
		return
	}
	// checkPressed(): setBlock -> updateNeighbours -> click_off.
	s.rsWriteBlock(d, pos, nb, nb, func() {
		s.explodeUpdateNeighboursAttached(d, pos, nb)
	})
	s.explodeBlockSound(d, pos, windSoundFamily(short)+"_button.click_off", 1, 1)
}

// randomTickSpeed reads the randomTickSpeed game rule.
func (s *Server) randomTickSpeed() int {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	v, ok := s.gameRules["minecraft:random_tick_speed"]
	if !ok {
		return 3
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return 3
		}
		n = n*10 + int(c-'0')
	}
	if n < 0 {
		return 0
	}
	return n
}
