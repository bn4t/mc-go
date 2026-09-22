package server

import (
	"strings"
	"sync"

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
	mu   sync.Mutex
	seed uint64
}

func newRandState(seed uint64) *randState { return &randState{seed: seed} }

func (r *randState) next() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seed += 0x9E3779B97F4A7C15
	z := r.seed
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
	s.randMu.Lock()
	if v, ok := s.randSections[sec]; ok {
		s.randMu.Unlock()
		return v
	}
	s.randMu.Unlock()

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

// randomTickTargets collects the chunks in d that are close enough to a
// player to be simulated.
func (s *Server) randomTickTargets(d *dimension) map[[2]int]bool {
	sim := s.SimulationDistance
	if sim <= 0 {
		sim = 10
	}
	out := map[[2]int]bool{}
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay || sess.dimName() != d.name {
			return true
		}
		x, _, z, _, _, _ := sess.Pos()
		ccx, ccz := int(x)>>4, int(z)>>4
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
	buf := make([]world.Block, 16*16*16)
	for ck := range chunks {
		cx, cz := ck[0], ck[1]
		for sec := 0; sec < sections; sec++ {
			// Sections with nothing to random-tick cost one cached map
			// lookup instead of a 4096-block snapshot.
			if !s.sectionHasRandomTicks(d, randSectionKey{dim: d.name, x: cx, y: sec, z: cz}) {
				continue
			}
			// One FillBox per tickable section: a block read goes through
			// a shard round trip, so sampling each roll against the live
			// world costs microseconds per draw — seconds per tick at high
			// random_tick_speed — while a snapshot costs one round trip
			// per shard. Rules also resolve reads inside the section from
			// the snapshot (s.randView); only cells outside it hit the
			// live world. A cell's second roll in the same tick sees the
			// snapshot's state, but every rule is idempotent on rewrite,
			// which is what vanilla's live read effectively does anyway.
			min := world.Pos{X: cx * 16, Y: d.minY + sec*16, Z: cz * 16}
			max := world.Pos{X: min.X + 15, Y: min.Y + 15, Z: min.Z + 15}
			d.w.FillBox(min, max, buf)
			s.randView = &randView{min: min, buf: buf}
			for i := 0; i < speed; i++ {
				idx := s.rng.intn(len(buf))
				blk := buf[idx]
				name, ok := world.StateOwner(blk)
				if !ok {
					continue
				}
				rule, ok := randRules()[name]
				if !ok {
					continue
				}
				rule(s, d, world.Pos{
					X: min.X + (idx & 15),
					Y: min.Y + (idx >> 8),
					Z: min.Z + ((idx >> 4) & 15),
				}, blk)
			}
			s.randView = nil
		}
	}
}

// randView is the active section snapshot during a section's roll loop;
// rule reads inside its bounds resolve from it instead of the world.
type randView struct {
	min world.Pos
	buf []world.Block
}

// rtBlock reads a block for a random-tick rule: positions inside the
// section currently being rolled come from its snapshot, everything
// else goes to the live world.
func (s *Server) rtBlock(d *dimension, p world.Pos) world.Block {
	if v := s.randView; v != nil &&
		p.X >= v.min.X && p.X < v.min.X+16 &&
		p.Y >= v.min.Y && p.Y < v.min.Y+16 &&
		p.Z >= v.min.Z && p.Z < v.min.Z+16 {
		return v.buf[((p.Y-v.min.Y)*16+(p.Z-v.min.Z))*16+(p.X-v.min.X)]
	}
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
	d.w.SetBlock(pos, nb)
	s.broadcastBlockState(d, pos, nb)
	// checkPressed(): setBlock -> updateNeighbours -> click_off.
	s.explodeUpdateNeighboursAttached(d, pos, nb)
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
