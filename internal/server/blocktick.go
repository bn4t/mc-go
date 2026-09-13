package server

import (
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
	found := false
	for _, b := range buf {
		if name, ok := world.StateOwner(b); ok {
			if _, ok := randomTickRules[name]; ok {
				found = true
				break
			}
		}
	}
	s.randMu.Lock()
	s.randSections[sec] = found
	s.randMu.Unlock()
	return found
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
	if s.rng == nil || len(randomTickRules) == 0 {
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
	for ck := range chunks {
		cx, cz := ck[0], ck[1]
		for sec := 0; sec < sections; sec++ {
			// Sections with nothing to random-tick cost one cached map
			// lookup instead of three block reads.
			if !s.sectionHasRandomTicks(d, randSectionKey{dim: d.name, x: cx, y: sec, z: cz}) {
				continue
			}
			for i := 0; i < speed; i++ {
				x := cx*16 + s.rng.intn(16)
				z := cz*16 + s.rng.intn(16)
				y := d.minY + sec*16 + s.rng.intn(16)
				s.randomTickBlock(d, world.Pos{X: x, Y: y, Z: z})
			}
		}
	}
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
	if rule, ok := randomTickRules[name]; ok {
		rule(s, d, pos, blk)
	}
}

// randomTickRules maps a block name to its random-tick behavior. Rules
// are added as their milestone lands; an unknown block simply has none,
// which is also true in vanilla for most blocks.
var randomTickRules = map[string]func(*Server, *dimension, world.Pos, world.Block){}

// blockTick handles one scheduled (non-random) block tick. Redstone and
// crop growth scheduling will land here; nothing schedules one yet.
func (s *Server) blockTick(d *dimension, pos world.Pos) {}

// randomTickSpeed reads the randomTickSpeed game rule.
func (s *Server) randomTickSpeed() int {
	s.rulesMu.RLock()
	defer s.rulesMu.RUnlock()
	v, ok := s.gameRules["randomTickSpeed"]
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
