package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mc-go/api/player"
	"mc-go/api/worldview"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func joinPrepTestServer(t *testing.T, gen func(world.Pos) world.Block) *Server {
	t.Helper()
	s := New("unused")
	s.MaxPlayers, s.ViewDistance = 128, 2
	s.SetFrozen(true)
	d := s.dims[dimOverworld]
	d.w.Stop()
	d.w = world.NewGen(nil, 8, gen)
	s.world = d.w
	d.minY, d.height = -16, 32
	s.setWorldSpawn(0, 1, 0)
	t.Cleanup(s.Stop)
	return s
}

func joinPrepTestSession(t *testing.T, s *Server, name string) *Session {
	t.Helper()
	sess, _ := outboundPipe(t, s, false)
	sess.playerRecord.java, sess.playerRecord.core = sess, s
	sess.setIdentity(name, offlineUUID(name))
	sess.state.Store(StateConfiguration)
	s.simulationAction(func() {
		if err := s.reservePlayerOwned(context.Background(), &sess.playerRecord); err != nil {
			t.Fatal(err)
		}
	})
	return sess
}

func joinPrepTestTarget(t *testing.T, s *Server, sess *Session) joinPreparationTarget {
	t.Helper()
	var target joinPreparationTarget
	s.simulationAction(func() {
		var ok bool
		target, ok = s.joinPreparationTargetOwned(sess)
		if !ok {
			t.Fatal("valid configuring session has no preparation target")
		}
	})
	return target
}

func joinPrepTestWait(t *testing.T, done <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatalf("%s did not finish", what)
	}
}

func TestJoinPreparationPlanGeometryAndOrder(t *testing.T) {
	for _, skylight := range []bool{true, false} {
		target := joinPreparationTarget{cx: -7, cz: 4, radius: 2, skylight: skylight}
		plan := buildJoinPreparationPlan(target)
		if len(plan.facts) != 49 {
			t.Fatalf("radius two facts=%d, want 49", len(plan.facts))
		}
		first := [][2]int{{-7, 4}, {-8, 4}, {-7, 3}, {-7, 5}, {-6, 4}}
		if !reflect.DeepEqual(plan.facts[:len(first)], first) {
			t.Fatalf("nearest-first prefix=%v, want %v", plan.facts[:len(first)], first)
		}
		seen := map[[2]int]bool{}
		for _, key := range plan.facts {
			if seen[key] || key[0] < -10 || key[0] > -4 || key[1] < 1 || key[1] > 7 {
				t.Fatalf("duplicate or outside radius-two square: %v", key)
			}
			seen[key] = true
		}
		if !skylight {
			if len(plan.light) != 0 {
				t.Fatal("skyless plan includes light generation")
			}
			continue
		}
		if len(plan.light) != 121 || plan.light[0] != [2]int{-9, 2} || plan.light[24] != [2]int{-5, 6} {
			t.Fatalf("radius-two halo count/order=%d/%v", len(plan.light), plan.light[:25])
		}
		seen = map[[2]int]bool{}
		for _, key := range plan.light {
			if seen[key] || key[0] < -12 || key[0] > -2 || key[1] < -1 || key[1] > 9 {
				t.Fatalf("duplicate or outside eleven-column halo: %v", key)
			}
			seen[key] = true
		}
	}
	for _, radius := range []int{0, 33} {
		plan := buildJoinPreparationPlan(joinPreparationTarget{radius: radius, skylight: true})
		if len(plan.facts) != 0 || len(plan.light) != 0 {
			t.Fatalf("invalid radius %d allocated a plan", radius)
		}
	}
}

func TestJoinPreparationTargetUsesFinalRestoreRulesWithoutPublishing(t *testing.T) {
	s := joinPrepTestServer(t, func(world.Pos) world.Block { return world.Air })
	sess := joinPrepTestSession(t, s, "PrepTarget")
	initial := joinPrepTestTarget(t, s, sess)
	if initial.dimension != s.dims[dimOverworld] || initial.cx != 0 || initial.cz != 0 || initial.radius != 2 {
		t.Fatalf("initial target=%+v", initial)
	}
	s.simulationAction(func() { s.setWorldSpawn(320, 1, -160) })
	changed := joinPrepTestTarget(t, s, sess)
	if changed == initial || changed.cx != 20 || changed.cz != -10 {
		t.Fatalf("changed world spawn target=%+v", changed)
	}
	s.savedPlayersMu.Lock()
	s.savedPlayers = map[string]playerPersist{
		DashUUID(sess.getUUID()): {Dim: dimNether, X: 112, Y: 65, Z: 96},
		sess.getName():           {Dim: dimEnd, X: -16.5, Y: 75, Z: -0.5},
	}
	s.savedPlayersMu.Unlock()
	// Match the existing join's int(coordinate)>>4 convention, including
	// negative fractions; this change does not alter movement geometry.
	saved := joinPrepTestTarget(t, s, sess)
	// The 26.2 end dimension type has skylight.
	if saved.dimension != s.dims[dimEnd] || saved.cx != -1 || saved.cz != 0 || !saved.skylight {
		t.Fatalf("name-keyed restore target=%+v", saved)
	}
	s.savedPlayersMu.Lock()
	delete(s.savedPlayers, sess.getName())
	s.savedPlayersMu.Unlock()
	fallback := joinPrepTestTarget(t, s, sess)
	if fallback.dimension != s.dims[dimNether] || fallback.cx != 7 || fallback.cz != 6 {
		t.Fatalf("UUID-keyed restore target=%+v", fallback)
	}
	for _, invalid := range []playerPersist{{Dim: "unknown", X: 999, Y: 75}, {Dim: dimEnd, X: 999, Y: 0}} {
		s.savedPlayersMu.Lock()
		s.savedPlayers[sess.getName()] = invalid
		s.savedPlayersMu.Unlock()
		if got := joinPrepTestTarget(t, s, sess); got != changed {
			t.Fatalf("invalid saved destination changed target: %+v", got)
		}
	}
	if sess.state.Load() != StateConfiguration || sess.entityID.Load() != 0 || sess.active.Load() || sess.admitted || sess.spawned.Load() || len(sess.sendCh) != 0 {
		t.Fatal("target resolution published session state or packets")
	}
}

func TestJoinPreparationStepHasOneColdUnitAndBoundedWarmSkips(t *testing.T) {
	s := joinPrepTestServer(t, func(world.Pos) world.Block { return world.Air })
	sess := joinPrepTestSession(t, s, "PrepStep")
	target := joinPrepTestTarget(t, s, sess)
	job := &joinPreparationJob{session: sess, plan: buildJoinPreparationPlan(target)}
	d := target.dimension
	s.simulationAction(func() {
		kind, complete, err := s.stepJoinPreparationOwned(job)
		if kind != joinPreparationFacts || complete || err != nil || job.factIndex != 1 || len(d.skyTops) != 1 || len(d.nonEmptyCnt) != 1 || len(d.skyGen) != 0 {
			t.Fatalf("first unit kind=%d complete=%v error=%v cursor=%d tops=%d counts=%d generated=%d", kind, complete, err, job.factIndex, len(d.skyTops), len(d.nonEmptyCnt), len(d.skyGen))
		}
	})
	// A whole plan that is already warm must still release ownership after
	// at most 64 checks. This pins the overlapping-join fast path's bound.
	large := target
	large.radius = 8
	job = &joinPreparationJob{session: sess, plan: buildJoinPreparationPlan(large)}
	s.simulationAction(func() {
		for _, key := range job.plan.facts {
			d.nonEmptyCnt[key] = map[int]int{}
			d.skyTops[key] = &[256]int32{}
			for sec := 0; sec < d.sections(); sec++ {
				s.randSections[randSectionKey{dim: d.name, x: key[0], y: sec, z: key[1]}] = 0
			}
		}
		d.skyGen = map[[2]int]bool{}
		for _, key := range job.plan.light {
			d.skyGen[key] = true
		}
		kind, complete, err := s.stepJoinPreparationOwned(job)
		if kind != joinPreparationNone || complete || err != nil || job.factIndex+job.lightIndex != maxJoinPreparationSkips {
			t.Fatalf("warm unit kind=%d complete=%v error=%v cursors=%d/%d", kind, complete, err, job.factIndex, job.lightIndex)
		}
		turns := 1
		for !complete {
			before := job.factIndex + job.lightIndex
			kind, complete, err = s.stepJoinPreparationOwned(job)
			if kind != joinPreparationNone || err != nil || job.factIndex+job.lightIndex-before > maxJoinPreparationSkips {
				t.Fatal("warm plan exceeded its bounded check count")
			}
			turns++
		}
		if turns < 2 {
			t.Fatal("large warm plan completed in one owner turn")
		}
	})
}

func TestJoinPreparationSchedulerFrozenBoundaryCommitsEdit(t *testing.T) {
	var s *Server
	var sess *Session
	var once sync.Once
	var armed atomic.Bool
	var job *joinPreparationJob
	observation := make(chan error, 1)
	pos := world.Pos{X: 1, Y: 0, Z: 1}
	stone, _ := world.BlockCatalog().State(world.Stone)
	s = joinPrepTestServer(t, func(world.Pos) world.Block {
		if !armed.Load() {
			return world.Air
		}
		once.Do(func() {
			if !s.queueSimulationAction(func() {
				if sess.state.Load() != StateConfiguration || sess.entityID.Load() != 0 || sess.active.Load() || sess.admitted || sess.spawned.Load() || len(sess.sendCh) != 0 {
					observation <- errors.New("first preparation boundary exposed a partial join")
					return
				}
				d := s.dims[dimOverworld]
				if len(d.skyTops) != 1 || len(d.skyGen) != 0 {
					observation <- fmt.Errorf("first boundary completed more than one unit: tops=%d generated=%d", len(d.skyTops), len(d.skyGen))
					return
				}
				select {
				case <-job.done:
					observation <- errors.New("observer action ran after preparation finished")
					return
				default:
				}
				result, err := s.editBlockOwned(context.Background(), worldview.Edit{Dimension: dimOverworld, Position: worldview.Pos{X: int32(pos.X), Y: int32(pos.Y), Z: int32(pos.Z)}, State: stone}, "test")
				if err != nil || !result.Applied {
					observation <- fmt.Errorf("edit at preparation boundary applied=%v error=%v", result.Applied, err)
					return
				}
				observation <- nil
			}) {
				observation <- errors.New("could not queue boundary edit")
			}
		})
		return world.Air
	})
	sess = joinPrepTestSession(t, s, "PrepBoundary")
	view, _ := watchTestVolume(t, s, worldview.Request{Dimension: dimOverworld, Bounds: worldview.Box{Min: worldview.Pos{X: 1, Y: 0, Z: 1}, Max: worldview.Pos{X: 2, Y: 1, Z: 2}}})
	target := joinPrepTestTarget(t, s, sess)
	p := newJoinPreparationScheduler(s, 1)
	t.Cleanup(p.close)
	var ok bool
	job, ok = p.reserve(sess)
	armed.Store(true)
	if !ok || !p.submit(job, buildJoinPreparationPlan(target)) {
		t.Fatal("could not submit preparation")
	}
	joinPrepTestWait(t, job.done, "frozen preparation")
	if job.err != nil {
		t.Fatal(job.err)
	}
	select {
	case err := <-observation:
		if err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("preparation never reached the deferred owner boundary")
	}
	if s.TickCount() != 0 || !s.clock.isFrozen() {
		t.Fatal("preparation depended on advancing the game clock")
	}
	if got := s.world.GetBlock(pos); got != world.Stone {
		t.Fatalf("boundary edit disappeared: state=%d", got)
	}
	update := nextTestWorldUpdate(t, view)
	if update.Kind != worldview.Delta || len(update.Changes) != 1 || update.Changes[0].State != world.Stone {
		t.Fatalf("boundary edit was not committed to the existing block observer: %+v", update)
	}
	s.simulationAction(func() {
		mapping, _ := world.BlockMapping(776)
		body, err := encodeChunkSnapshot(captureChunkSnapshot(target.dimension, target.cx, target.cz, nil, mapping))
		if err != nil {
			t.Fatal(err)
		}
		wire, _ := mapping.Lookup(world.Stone)
		if got := chunkSnapshotStateAt(t, body, pos.X, pos.Y, pos.Z, target.dimension.minY); got != wire {
			t.Fatalf("final captured chunk state=%d, want edited state=%d", got, wire)
		}
	})
}

func TestJoinPreparationQueuedCancellationAndRunningRetention(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	s := joinPrepTestServer(t, func(world.Pos) world.Block {
		once.Do(func() { close(started); <-release })
		return world.Air
	})
	sessions := []*Session{joinPrepTestSession(t, s, "PrepRunning"), joinPrepTestSession(t, s, "PrepQueued"), joinPrepTestSession(t, s, "PrepSpare")}
	plans := make([]joinPreparationPlan, len(sessions))
	for i, sess := range sessions {
		plans[i] = buildJoinPreparationPlan(joinPrepTestTarget(t, s, sess))
	}
	p := newJoinPreparationScheduler(s, 2)
	t.Cleanup(p.close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	first, ok := p.reserve(sessions[0])
	if !ok || !p.submit(first, plans[0]) {
		t.Fatal("running job was rejected")
	}
	joinPrepTestWait(t, started, "blocked first unit")
	var previous *joinPreparationJob
	for i := 0; i < 80; i++ {
		queued, accepted := p.reserve(sessions[1])
		if !accepted || !p.submit(queued, plans[1]) {
			t.Fatalf("queued reservation %d rejected", i)
		}
		if previous != nil {
			previous.cancel()
			p.mu.Lock()
			current := p.bySession[sessions[1]]
			p.mu.Unlock()
			if current != queued {
				t.Fatal("old receipt cancellation retired its session's successor")
			}
		}
		if _, duplicate := p.reserve(sessions[1]); duplicate || p.submit(queued, plans[1]) {
			t.Fatal("duplicate session or duplicate submission was accepted")
		}
		if _, overflow := p.reserve(sessions[2]); overflow {
			t.Fatal("scheduler exceeded running plus queued capacity")
		}
		queued.cancel()
		joinPrepTestWait(t, queued.done, "queued cancellation")
		if !errors.Is(queued.err, net.ErrClosed) {
			t.Fatalf("queued cancellation error=%v", queued.err)
		}
		p.mu.Lock()
		retained, queuedCount := len(p.jobs), len(p.queue)
		p.mu.Unlock()
		if retained != 1 || queuedCount != 0 {
			t.Fatalf("canceled plans retained: jobs=%d queue=%d", retained, queuedCount)
		}
		previous = queued
	}
	spare, ok := p.reserve(sessions[2])
	if !ok {
		t.Fatal("canceled queued job did not release capacity")
	}
	first.cancel()
	select {
	case <-first.done:
		t.Fatal("running cancellation released its slot before the unit ended")
	default:
	}
	if _, accepted := p.reserve(sessions[1]); accepted {
		t.Fatal("running canceled work did not retain its reservation")
	}
	releaseOnce.Do(func() { close(release) })
	joinPrepTestWait(t, first.done, "running cancellation retirement")
	if !errors.Is(first.err, net.ErrClosed) {
		t.Fatalf("running cancellation error=%v", first.err)
	}
	last, ok := p.reserve(sessions[1])
	if !ok {
		t.Fatal("finished running work did not release capacity")
	}
	last.cancel()
	spare.cancel()
}

func TestJoinPreparationReleasesOwnerForIndependentAvatarMove(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	s := joinPrepTestServer(t, func(world.Pos) world.Block {
		once.Do(func() { close(started); <-release })
		return world.Air
	})
	sess := joinPrepTestSession(t, s, "PrepHandoff")
	target := joinPrepTestTarget(t, s, sess)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	core := attachmentCore{frontendCore: frontendCore{s: s}, attachment: &frontendAttachment{ctx: ctx}}
	observer, err := core.Admit(ctx, player.Profile{Name: "PrepObserver", UUID: offlineUUID("PrepObserver")}, player.Options{})
	if err != nil {
		t.Fatal(err)
	}
	initial, err := observer.Next(ctx)
	if err != nil || initial.Kind != player.Snapshot {
		t.Fatalf("observer baseline kind=%d error=%v", initial.Kind, err)
	}
	p := newJoinPreparationScheduler(s, 1)
	t.Cleanup(p.close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	job, ok := p.reserve(sess)
	if !ok || !p.submit(job, buildJoinPreparationPlan(target)) {
		t.Fatal("could not submit handoff preparation")
	}
	joinPrepTestWait(t, started, "handoff first unit")
	// Stop the scheduler at its bookkeeping boundary after the first
	// gameplay action. The independent public Move must still acquire the
	// gameplay owner; no scheduling delays or fairness guesses are needed.
	p.mu.Lock()
	locked := true
	defer func() {
		if locked {
			p.mu.Unlock()
		}
	}()
	moveDone := make(chan struct{})
	var applied bool
	var moveErr error
	go func() {
		applied, moveErr = observer.Move(ctx, player.Movement{X: 5, Y: 1, Z: 5})
		close(moveDone)
	}()
	releaseOnce.Do(func() { close(release) })
	joinPrepTestWait(t, moveDone, "independent observer move during unfinished preparation")
	if moveErr != nil || !applied {
		t.Fatalf("independent move applied=%v error=%v", applied, moveErr)
	}
	select {
	case <-job.done:
		t.Fatal("independent observer moved only after preparation completed")
	default:
	}
	if sess.state.Load() != StateConfiguration || sess.entityID.Load() != 0 || sess.active.Load() || sess.admitted {
		t.Fatal("independent observer saw an unfinished joining player")
	}
	update, err := observer.Next(ctx)
	if err != nil || update.Kind != player.Moved || update.Player.ID != observer.ID() || update.Player.Pose.X != 5 || update.Player.Pose.Z != 5 {
		t.Fatalf("independent move was not published while preparation remained pending: update=%+v error=%v", update, err)
	}
	p.mu.Unlock()
	locked = false
	joinPrepTestWait(t, job.done, "handoff preparation completion")
	if job.err != nil {
		t.Fatal(job.err)
	}
}

func TestJoinPreparationKnownHostCloseAbortsBeforePublication(t *testing.T) {
	var s *Server
	var sess *Session
	var once sync.Once
	s = joinPrepTestServer(t, func(world.Pos) world.Block {
		once.Do(func() { s.queueSimulationAction(func() { sess.closeQueue() }) })
		return world.Air
	})
	sess = joinPrepTestSession(t, s, "PrepHostClose")
	if s.joinPlayPrepared(sess) {
		t.Fatal("host-closed preparation admitted a player")
	}
	s.stopJoinPreparation()
	d := s.dims[dimOverworld]
	if sess.state.Load() != StateConfiguration || sess.entityID.Load() != 0 || sess.active.Load() || sess.admitted || sess.spawned.Load() || len(sess.sendCh) != 0 || len(d.skyTops) != 1 || len(d.skyGen) != 0 {
		t.Fatalf("host cancellation published state or continued preparation: phase=%d entity=%d tops=%d generated=%d packets=%d", sess.state.Load(), sess.entityID.Load(), len(d.skyTops), len(d.skyGen), len(sess.sendCh))
	}
	s.joinPreparer.mu.Lock()
	jobs := len(s.joinPreparer.jobs)
	s.joinPreparer.mu.Unlock()
	if jobs != 0 {
		t.Fatal("host cancellation retained its preparation slot")
	}
}

func TestJoinPreparationFinalRevalidationReplansSpawnAndSavedDestination(t *testing.T) {
	for _, saved := range []bool{false, true} {
		t.Run(fmt.Sprintf("saved_%v", saved), func(t *testing.T) {
			var s *Server
			var sess *Session
			var once sync.Once
			s = joinPrepTestServer(t, func(world.Pos) world.Block {
				once.Do(func() {
					s.queueSimulationAction(func() {
						if saved {
							s.savedPlayersMu.Lock()
							s.savedPlayers = map[string]playerPersist{sess.getName(): {Dim: dimOverworld, X: 320.5, Y: 3, Z: -160.5, Health: 20, Food: 20}}
							s.savedPlayersMu.Unlock()
						} else {
							s.setWorldSpawn(320, 3, -160)
						}
					})
				})
				return world.Air
			})
			sess = joinPrepTestSession(t, s, "PrepReplan")
			if !s.joinPlayPrepared(sess) {
				t.Fatal("changed destination did not replan and join")
			}
			pose := sess.position()
			wantZ := -159.5
			if saved {
				wantZ = -160.5
			}
			if pose.X != 320.5 || pose.Y != 3 || pose.Z != wantZ || !sess.active.Load() || !sess.admitted || !sess.spawned.Load() {
				t.Fatalf("join restored stale destination: %+v", pose)
			}
			if _, warm := s.dims[dimOverworld].skyTops[[2]int{20, -10}]; !warm {
				t.Fatal("new destination was not prepared before final join")
			}
			joins, centerChunks := 0, 0
			for _, packet := range readPumpQueue(t, sess) {
				if packet.ID == sess.ids().PlayJoinGame {
					joins++
				}
				if packet.ID == sess.ids().PlayChunkData {
					r := protocol.NewPacketReader(packet.Data)
					x, _ := r.Int()
					z, _ := r.Int()
					if x == 20 && z == -10 {
						centerChunks++
					}
				}
			}
			if joins != 1 || centerChunks != 1 || len(s.entities.All()) != 1 {
				t.Fatalf("replanning duplicated publication: joins=%d center chunks=%d entities=%d", joins, centerChunks, len(s.entities.All()))
			}
		})
	}
}

func TestJoinPreparationRepeatedTargetChangesRespectReplanLimit(t *testing.T) {
	var s *Server
	var plans sync.Map
	var changes atomic.Int32
	s = joinPrepTestServer(t, func(pos world.Pos) world.Block {
		// One sample per captured chunk is enough to identify a new job;
		// avoid taking scheduler locks for every cell in the fixture.
		if pos.Y != -16 || pos.X&15 != 0 || pos.Z&15 != 0 {
			return world.Air
		}
		s.joinPreparationMu.Lock()
		p := s.joinPreparer
		s.joinPreparationMu.Unlock()
		if p != nil {
			p.mu.Lock()
			var current *joinPreparationJob
			for job := range p.jobs {
				if job.running {
					current = job
					break
				}
			}
			p.mu.Unlock()
			if current != nil {
				if _, seen := plans.LoadOrStore(current, true); !seen {
					s.queueSimulationAction(func() {
						x, y, z := s.WorldSpawn()
						s.setWorldSpawn(int(x)+1600, int(y), int(z))
						changes.Add(1)
					})
				}
			}
		}
		return world.Air
	})
	sess := joinPrepTestSession(t, s, "PrepLimit")
	if s.joinPlayPrepared(sess) {
		t.Fatal("continually changing destination bypassed the retry bound")
	}
	s.stopJoinPreparation()
	if changes.Load() != maxJoinPreparationReplans+1 || sess.state.Load() != StateConfiguration || sess.entityID.Load() != 0 || sess.active.Load() || sess.admitted || len(sess.sendCh) != 0 || !sess.gone() {
		t.Fatalf("replan exhaustion changed lifecycle: changes=%d phase=%d entity=%d packets=%d closed=%v", changes.Load(), sess.state.Load(), sess.entityID.Load(), len(sess.sendCh), sess.gone())
	}
}

func TestJoinPreparationSchedulerCloseRetiresBuildingAndQueuedWork(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	s := joinPrepTestServer(t, func(world.Pos) world.Block {
		once.Do(func() { close(started); <-release })
		return world.Air
	})
	var sessions []*Session
	var plans []joinPreparationPlan
	for i := 0; i < 3; i++ {
		sess := joinPrepTestSession(t, s, fmt.Sprintf("PrepClose%d", i))
		sessions = append(sessions, sess)
		plans = append(plans, buildJoinPreparationPlan(joinPrepTestTarget(t, s, sess)))
	}
	p := newJoinPreparationScheduler(s, 3)
	t.Cleanup(p.close)
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	first, _ := p.reserve(sessions[0])
	if !p.submit(first, plans[0]) {
		t.Fatal("could not submit running unit")
	}
	joinPrepTestWait(t, started, "running close unit")
	queued, _ := p.reserve(sessions[1])
	if !p.submit(queued, plans[1]) {
		t.Fatal("could not queue second unit")
	}
	building, _ := p.reserve(sessions[2])
	closed := make(chan struct{})
	go func() { p.close(); close(closed) }()
	joinPrepTestWait(t, queued.done, "shutdown queued job")
	joinPrepTestWait(t, building.done, "shutdown building job")
	select {
	case <-closed:
		t.Fatal("close did not join its running unit")
	default:
	}
	if p.submit(building, plans[2]) {
		t.Fatal("closed scheduler accepted a plan")
	}
	releaseOnce.Do(func() { close(release) })
	joinPrepTestWait(t, closed, "scheduler close")
	joinPrepTestWait(t, first.done, "shutdown running job")
	p.mu.Lock()
	jobs, queuedCount := len(p.jobs), len(p.queue)
	p.mu.Unlock()
	if jobs != 0 || queuedCount != 0 {
		t.Fatalf("closed scheduler retained jobs=%d queue=%d", jobs, queuedCount)
	}
}
