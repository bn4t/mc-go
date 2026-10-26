package server

import (
	"fmt"
	"net"
)

// A generation distinguishes A -> B -> A, resets in the same dimension, and
// retirement even when the detached routing scalars happen to be identical.
// All target changes and preparation steps run on the simulation owner.
type livePreparationTarget struct {
	joinPreparationTarget
	generation uint64
}

// resetChunks invalidates a plan but retains its reservation until the next
// owner turn. Respawn/dimension transactions immediately stage a replacement in
// the same slot, including when the scheduler considers that slot running.
func (sess *Session) invalidateLivePreparationOwned() {
	sess.liveGeneration++
	sess.liveTargetSet = false
	if job := sess.livePreparation; job != nil {
		job.desired = livePreparationTarget{}
	}
}

func (sess *Session) cancelLivePreparationOwned() {
	sess.invalidateLivePreparationOwned()
	sess.sentMu.Lock()
	job := sess.livePreparation
	sess.livePreparation = nil
	sess.sentMu.Unlock()
	if job != nil {
		job.cancel()
	}
}

// stageLiveWindowOwned never releases ownership inside a movement, teleport or
// respawn transaction. Only its latest scalar target is retained while an old
// plan runs; no staged column enters queued/pending/sent until final publication.
func (s *Server) stageLiveWindowOwned(sess *Session, cx, cz, radius int) error {
	d := sess.dimension()
	if s.ShuttingDown() || sess.gone() || sess.left || d == nil || radius < 1 || radius > maxJoinPreparationRadius {
		sess.closeQueue()
		return net.ErrClosed
	}
	target := joinPreparationTarget{dimension: d, cx: cx, cz: cz, radius: radius,
		skylight: d.skylight, incarnation: sess.ID()}
	if sess.livePreparation == nil && sess.liveTargetSet && sess.liveTarget == target {
		// Movement calls sendWindow even inside one column. An already
		// published identical window needs no plan or scheduler reservation.
		return s.finishWindowOwned(sess, cx, cz, radius, nil)
	}
	if !sess.liveTargetSet || sess.liveTarget != target {
		sess.liveGeneration++
		sess.liveTarget, sess.liveTargetSet = target, true
	}
	desired := livePreparationTarget{joinPreparationTarget: target, generation: sess.liveGeneration}
	if job := sess.livePreparation; job != nil {
		job.desired = desired
		return nil
	}
	p := s.joinPreparationScheduler()
	if p == nil {
		sess.closeQueue()
		return net.ErrClosed
	}
	job, ok := p.reserve(sess)
	if !ok {
		// Shared joins and live windows have a finite runtime budget. Overflow
		// closes only this recipient; it never performs a cold synchronous plan.
		sess.closeQueue()
		return fmt.Errorf("chunk preparation resource limit")
	}
	job.live, job.desired = true, desired
	sess.sentMu.Lock()
	sess.livePreparation = job
	sess.sentMu.Unlock()
	// Building the detached coordinate plan happens in its first owner turn,
	// after the caller's complete transaction and after reserving its quota.
	if !p.submit(job, joinPreparationPlan{}) {
		sess.cancelLivePreparationOwned()
		sess.closeQueue()
		return net.ErrClosed
	}
	return nil
}

func buildLivePreparationPlan(sess *Session, target joinPreparationTarget) joinPreparationPlan {
	plan := joinPreparationPlan{target: target}
	limit := target.radius + 1
	width := limit*2 + 1
	plan.facts = make([][2]int, 0, width*width)
	sess.sentMu.Lock()
	for dx := -limit; dx <= limit; dx++ {
		for dz := -limit; dz <= limit; dz++ {
			key := [2]int{target.cx + dx, target.cz + dz}
			if chunkInWindow(target.radius, dx, dz) && !sess.queued[key] {
				plan.facts = append(plan.facts, key)
			}
		}
	}
	sess.sentMu.Unlock()
	queueOrder(plan.facts, target.cx, target.cz)
	if !target.skylight || len(plan.facts) == 0 {
		return plan
	}
	lightLimit := limit + 2
	lightWidth := lightLimit*2 + 1
	seen := make([]bool, lightWidth*lightWidth)
	plan.light = make([][2]int, 0, len(seen))
	for _, key := range plan.facts {
		for dx := -2; dx <= 2; dx++ {
			for dz := -2; dz <= 2; dz++ {
				column := [2]int{key[0] + dx, key[1] + dz}
				index := (column[0]-target.cx+lightLimit)*lightWidth + column[1] - target.cz + lightLimit
				if !seen[index] {
					seen[index] = true
					plan.light = append(plan.light, column)
				}
			}
		}
	}
	return plan
}

func (s *Server) livePreparationCurrentOwned(job *joinPreparationJob) bool {
	sess, target := job.session, job.desired
	return !job.canceled.Load() && !s.ShuttingDown() && !sess.gone() && !sess.left &&
		sess.state.Load() == StatePlay && sess.spawned.Load() && sess.livePreparation == job &&
		sess.liveTargetSet && sess.liveGeneration == target.generation && sess.liveTarget == target.joinPreparationTarget &&
		sess.ID() == target.incarnation && sess.dimension() == target.dimension &&
		target.dimension != nil && s.dims[target.dimension.name] == target.dimension
}

// Ordinary turns check at most 64 warm entries and perform at most one whole
// cold facts-column scan or light initialization/flood. The final metadata-only
// scan is bounded by the admitted radius, rather than the 64-entry skip limit:
// edits can invalidate columns examined on earlier turns. It refills at most one
// such column and yields again. No wall-clock deadline or parallel lighting is
// implied, and continual invalidation can delay completion.
func (s *Server) stepLivePreparationOwned(job *joinPreparationJob) (joinPreparationStep, bool, error) {
	sess := job.session
	if !s.livePreparationCurrentOwned(job) {
		// A stale completion must never clear a successor's pump gate.
		sess.sentMu.Lock()
		if sess.livePreparation == job {
			sess.livePreparation = nil
		}
		sess.sentMu.Unlock()
		return joinPreparationNone, true, net.ErrClosed
	}
	if job.planGeneration != job.desired.generation {
		// Release obsolete arrays before allocating their replacement. The job
		// keeps its existing global reservation across arbitrarily many moves.
		job.plan = joinPreparationPlan{}
		job.plan = buildLivePreparationPlan(sess, job.desired.joinPreparationTarget)
		job.planGeneration = job.desired.generation
		job.factIndex, job.lightIndex = 0, 0
	}
	d := job.desired.dimension
	for range maxJoinPreparationSkips {
		if job.factIndex < len(job.plan.facts) {
			key := job.plan.facts[job.factIndex]
			job.factIndex++
			if s.joinColumnFactsWarmOwned(d, key) {
				continue
			}
			s.warmSectionFlags(d, key[0], key[1])
			return joinPreparationFacts, false, nil
		}
		if job.lightIndex < len(job.plan.light) {
			key := job.plan.light[job.lightIndex]
			job.lightIndex++
			d.lightMu.Lock()
			warm := d.skyGen[key]
			if !warm {
				ensureSkyChunk(d, key[0], key[1])
			}
			d.lightMu.Unlock()
			if warm {
				continue
			}
			return joinPreparationLight, false, nil
		}
		// Under the same owner turn as publication, catch any section flags
		// invalidated by edits while the plan yielded on other columns.
		for _, key := range job.plan.facts {
			if !s.joinColumnFactsWarmOwned(d, key) {
				s.warmSectionFlags(d, key[0], key[1])
				return joinPreparationFacts, false, nil
			}
		}
		target := job.desired
		sess.sentMu.Lock()
		sess.livePreparation = nil
		sess.sentMu.Unlock()
		err := s.finishWindowOwned(sess, target.cx, target.cz, target.radius, job.plan.facts)
		if err != nil {
			sess.closeQueue()
		}
		return joinPreparationNone, true, err
	}
	return joinPreparationNone, false, nil
}
