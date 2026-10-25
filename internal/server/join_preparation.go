package server

import (
	"net"
	"runtime"
	"sync"
	"sync/atomic"

	"mc-go/api/player"
)

const (
	maxJoinPreparationJobs    = 32
	maxJoinPreparationRadius  = 32 // parseClientInfo's admitted maximum
	maxJoinPreparationSkips   = 64
	maxJoinPreparationReplans = 3
)

// The target contains detached routing scalars and immutable identities. Its
// dimension pointer identifies the owner of caches; only owned steps dereference
// it. Resolving a target neither restores session state nor creates an entity.
type joinPreparationTarget struct {
	dimension   *dimension
	cx, cz      int
	radius      int
	skylight    bool
	incarnation player.ID
}

type joinPreparationPlan struct {
	target       joinPreparationTarget
	facts, light [][2]int
}

func (s *Server) joinPreparationTargetOwned(sess *Session) (joinPreparationTarget, bool) {
	if s.ShuttingDown() || sess.gone() || sess.left || !sess.counted || sess.state.Load() != StateConfiguration {
		return joinPreparationTarget{}, false
	}
	d := s.dims[dimOverworld]
	x, _, z := s.WorldSpawn()
	if saved, ok := s.savedPlayerForSession(sess); ok && saved.Y != 0 {
		if restored := s.dimensionByName(saved.Dim); restored != nil {
			d, x, z = restored, saved.X, saved.Z
		}
	}
	radius := sess.viewRadius(s.ViewDistance)
	if d == nil || radius < 1 || radius > maxJoinPreparationRadius {
		return joinPreparationTarget{}, false
	}
	return joinPreparationTarget{dimension: d, cx: int(x) >> 4, cz: int(z) >> 4,
		radius: radius, skylight: d.skylight, incarnation: sess.ID()}, true
}

// buildJoinPreparationPlan is pure. It preserves sendWindow's ordered fresh
// columns and the first occurrence of each light column in its +/-2 traversal.
// A reservation precedes these allocations. At radius 32 the two coordinate
// capacities total 9,530 entries (152,480 bytes on a 64-bit host); the temporary
// deduplication grid is 5,041 bytes. Thirty-two reservations bound this storage,
// excluding ordinary slice headers, allocator overhead and garbage collection.
func buildJoinPreparationPlan(target joinPreparationTarget) joinPreparationPlan {
	plan := joinPreparationPlan{target: target}
	if target.radius < 1 || target.radius > maxJoinPreparationRadius {
		return plan
	}
	limit := target.radius + 1
	width := limit*2 + 1
	plan.facts = make([][2]int, 0, width*width)
	for dx := -limit; dx <= limit; dx++ {
		for dz := -limit; dz <= limit; dz++ {
			if chunkInWindow(target.radius, dx, dz) {
				plan.facts = append(plan.facts, [2]int{target.cx + dx, target.cz + dz})
			}
		}
	}
	queueOrder(plan.facts, target.cx, target.cz)
	if !target.skylight {
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

type joinPreparationStep uint8

const (
	joinPreparationNone joinPreparationStep = iota
	joinPreparationFacts
	joinPreparationLight
)

type joinPreparationJob struct {
	scheduler                    *joinPreparationScheduler
	session                      *Session
	plan                         joinPreparationPlan
	factIndex, lightIndex        int // one scheduler, or direct owned test steps
	canceled                     atomic.Bool
	done                         chan struct{}
	err                          error // published by closing done
	submitted, running, finished bool  // scheduler.mu
}

// A single runtime goroutine schedules cache preparation, never accesses live
// gameplay state outside simulationAction, and never holds mu while entering
// ownership. Reserved/building jobs count against the same limit as running jobs.
type joinPreparationScheduler struct {
	server    *Server
	mu        sync.Mutex
	ready     *sync.Cond
	stopped   bool
	maxJobs   int
	jobs      map[*joinPreparationJob]struct{}
	bySession map[*Session]*joinPreparationJob
	queue     []*joinPreparationJob
	wg        sync.WaitGroup
}

func newJoinPreparationScheduler(s *Server, maxJobs int) *joinPreparationScheduler {
	p := &joinPreparationScheduler{server: s, maxJobs: maxJobs,
		jobs: make(map[*joinPreparationJob]struct{}), bySession: make(map[*Session]*joinPreparationJob)}
	p.ready = sync.NewCond(&p.mu)
	p.wg.Add(1)
	go p.work()
	return p
}

func (s *Server) joinPreparationScheduler() *joinPreparationScheduler {
	s.joinPreparationMu.Lock()
	defer s.joinPreparationMu.Unlock()
	if s.ShuttingDown() {
		return nil
	}
	if s.joinPreparer == nil {
		s.joinPreparer = newJoinPreparationScheduler(s, maxJoinPreparationJobs)
	}
	return s.joinPreparer
}

func (p *joinPreparationScheduler) reserve(sess *Session) (*joinPreparationJob, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || p.server.ShuttingDown() || len(p.jobs) >= p.maxJobs || p.bySession[sess] != nil {
		return nil, false
	}
	job := &joinPreparationJob{scheduler: p, session: sess, done: make(chan struct{})}
	p.jobs[job] = struct{}{}
	p.bySession[sess] = job
	return job, true
}

func (p *joinPreparationScheduler) submit(job *joinPreparationJob, plan joinPreparationPlan) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped || job.scheduler != p || job.submitted || job.finished || job.canceled.Load() {
		return false
	}
	// A reservation is submitted exactly once by its connection goroutine.
	job.plan, job.submitted = plan, true
	p.queue = append(p.queue, job)
	p.ready.Signal()
	return true
}

func (p *joinPreparationScheduler) finishLocked(job *joinPreparationJob, err error) {
	if job.finished {
		return
	}
	job.finished, job.err = true, err
	for i, queued := range p.queue {
		if queued == job {
			copy(p.queue[i:], p.queue[i+1:])
			p.queue[len(p.queue)-1] = nil
			p.queue = p.queue[:len(p.queue)-1]
			break
		}
	}
	delete(p.jobs, job)
	delete(p.bySession, job.session)
	// Completion receipts retain no coordinate arrays while their connection
	// waits to reenter the owner for final admission.
	job.plan = joinPreparationPlan{}
	close(job.done)
}

func (job *joinPreparationJob) cancel() {
	job.canceled.Store(true)
	p := job.scheduler
	if p == nil {
		return
	}
	p.mu.Lock()
	if !job.running {
		p.finishLocked(job, net.ErrClosed)
	}
	p.mu.Unlock()
}

func (p *joinPreparationScheduler) work() {
	defer p.wg.Done()
	for {
		p.mu.Lock()
		for len(p.queue) == 0 && !p.stopped {
			p.ready.Wait()
		}
		if p.stopped {
			p.mu.Unlock()
			return
		}
		job := p.queue[0]
		copy(p.queue, p.queue[1:])
		p.queue[len(p.queue)-1] = nil
		p.queue = p.queue[:len(p.queue)-1]
		if job.finished {
			p.mu.Unlock()
			continue
		}
		job.running = true
		p.mu.Unlock()
		var complete bool
		var err error
		p.server.simulationAction(func() { _, complete, err = p.server.stepJoinPreparationOwned(job) })
		p.mu.Lock()
		job.running = false
		if p.stopped || job.canceled.Load() {
			err = net.ErrClosed
		}
		if complete || err != nil {
			p.finishLocked(job, err)
		} else {
			p.queue = append(p.queue, job)
		}
		p.mu.Unlock()
		// Let runnable gameplay callers contend between complete owner turns.
		runtime.Gosched()
	}
}

func (p *joinPreparationScheduler) close() {
	p.mu.Lock()
	p.stopped = true
	for job := range p.jobs {
		job.canceled.Store(true)
		if !job.running {
			p.finishLocked(job, net.ErrClosed)
		}
	}
	p.queue = nil
	p.ready.Broadcast()
	p.mu.Unlock()
	p.wg.Wait()
}

func (s *Server) stopJoinPreparation() {
	s.joinPreparationMu.Lock()
	p := s.joinPreparer
	s.joinPreparationMu.Unlock()
	if p != nil {
		p.close()
	}
}

func (s *Server) joinColumnFactsWarmOwned(d *dimension, key [2]int) bool {
	if d.skylight {
		d.lightMu.Lock()
		_, counts := d.nonEmptyCnt[key]
		_, tops := d.skyTops[key]
		d.lightMu.Unlock()
		if !counts || !tops {
			return false
		}
	}
	if s.randSections == nil {
		return true
	}
	s.randMu.Lock()
	defer s.randMu.Unlock()
	for sec := 0; sec < d.sections(); sec++ {
		if _, warm := s.randSections[randSectionKey{dim: d.name, x: key[0], y: sec, z: key[1]}]; !warm {
			return false
		}
	}
	return true
}

// Each turn completes at most one cold facts column or light initialization,
// including its entire flood. Even already-warm overlap scans yield after a
// fixed number of checks. A whole light initialization can exceed a tick budget;
// this is an ownership boundary, not a wall-clock deadline or parallel lighting.
func (s *Server) stepJoinPreparationOwned(job *joinPreparationJob) (joinPreparationStep, bool, error) {
	sess, target := job.session, job.plan.target
	if job.canceled.Load() || s.ShuttingDown() || sess.gone() || sess.left || !sess.counted ||
		sess.state.Load() != StateConfiguration || sess.ID() != target.incarnation ||
		target.dimension == nil || s.dims[target.dimension.name] != target.dimension {
		return joinPreparationNone, true, net.ErrClosed
	}
	d := target.dimension
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
		return joinPreparationNone, true, nil
	}
	return joinPreparationNone, false, nil
}

// Initial joins wait outside ownership while their destination caches warm.
// Every packet, entity and admission side effect remains in joinPlayOwned.
// Known host/session/runtime cancellation stops between complete steps. As in
// the original join, peer EOF is not detected until the packet reader resumes.
func (s *Server) joinPlayPrepared(sess *Session) bool {
	for replan := 0; replan <= maxJoinPreparationReplans; replan++ {
		p := s.joinPreparationScheduler()
		if p == nil {
			break
		}
		job, ok := p.reserve(sess)
		if !ok {
			break
		}
		var target joinPreparationTarget
		s.simulationAction(func() { target, ok = s.joinPreparationTargetOwned(sess) })
		if !ok {
			job.cancel()
			break
		}
		plan := buildJoinPreparationPlan(target)
		if !p.submit(job, plan) {
			job.cancel()
			break
		}
		select {
		case <-job.done:
		case <-sess.done:
			job.cancel()
		case <-s.runtimeCtx.Done():
			job.cancel()
		}
		// Cancellation of a running step retains its reservation until that
		// step completes. The connection itself need not wait for the flood.
		if job.canceled.Load() || sess.gone() || s.ShuttingDown() {
			break
		}
		if job.err != nil {
			break
		}
		joined, changed := false, false
		s.simulationAction(func() {
			current, valid := s.joinPreparationTargetOwned(sess)
			if !valid {
				return
			}
			if current != target {
				changed = true
				return
			}
			joined = s.joinPlayOwned(sess)
		})
		if joined {
			return true
		}
		if !changed {
			break
		}
	}
	// Finish Configuration was already acknowledged. Close without inventing
	// a configuration or pre-Join play packet for resource/cancellation failure.
	sess.closeQueue()
	return false
}
