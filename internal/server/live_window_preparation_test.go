package server

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mc-go/api/player"
	"mc-go/api/worldview"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// A gate blocks one whole owned unit. Locking the scheduler's bookkeeping
// mutex before releasing that unit then leaves the owner independently usable
// while preparation is deterministically unfinished, without timing guesses.
type livePrepGate struct {
	armed             atomic.Bool
	started, release  chan struct{}
	once, releaseOnce sync.Once
}

func newLivePrepGate(t *testing.T) *livePrepGate {
	g := &livePrepGate{started: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(g.open)
	return g
}

func (g *livePrepGate) block(world.Pos) world.Block {
	if g.armed.Load() {
		g.once.Do(func() { close(g.started); <-g.release })
	}
	return world.Air
}

func (g *livePrepGate) open() { g.releaseOnce.Do(func() { close(g.release) }) }

func livePrepSession(t *testing.T, s *Server, name string) *Session {
	t.Helper()
	sess := joinPrepTestSession(t, s, name)
	s.simulationAction(func() {
		sess.setDimension(s.dims[dimOverworld])
		sess.setPos(320.5, 1, 320.5, 0, 0, true)
		sess.state.Store(StatePlay)
		sess.spawned.Store(true)
		sess.opened = true // an existing player has already received its first batch
		s.commitPlayerAdmissionOwned(&sess.playerRecord)
		s.sessions.Store(sess.id, sess)
	})
	t.Cleanup(func() { s.sessions.Delete(sess.id) })
	return sess
}

func livePrepRequest(t *testing.T, s *Server, sess *Session, cx, cz int) *joinPreparationJob {
	t.Helper()
	var job *joinPreparationJob
	s.simulationAction(func() {
		if err := s.sendWindow(sess, cx, cz); err != nil {
			t.Fatalf("stage window: %v", err)
		}
		job = sess.livePreparation
	})
	if job == nil {
		t.Fatal("live window was not staged")
	}
	return job
}

func TestLiveWindowPreparationFrozenHandoffPreservesEditAndPacing(t *testing.T) {
	g := newLivePrepGate(t)
	s := joinPrepTestServer(t, g.block)
	t.Cleanup(g.open) // release a failed fixture before joining the server
	sess := livePrepSession(t, s, "LiveHandoff")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	core := attachmentCore{frontendCore: frontendCore{s: s}, attachment: &frontendAttachment{ctx: ctx}}
	observer, err := core.Admit(ctx, player.Profile{Name: "LiveObserver", UUID: offlineUUID("LiveObserver")}, player.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if first, err := observer.Next(ctx); err != nil || first.Kind != player.Snapshot {
		t.Fatalf("observer baseline=%+v error=%v", first, err)
	}
	pos := worldview.Pos{X: 321, Y: 0, Z: 321}
	view, _ := watchTestVolume(t, s, worldview.Request{Dimension: dimOverworld,
		Bounds: worldview.Box{Min: pos, Max: worldview.Pos{X: 322, Y: 1, Z: 322}}})
	_ = readPumpQueue(t, sess) // existing observer's admission/tab-list fanout
	old, oldPending := [2]int{-1000, -1000}, [2]int{-999, -1000}
	s.simulationAction(func() {
		d := s.dims[dimOverworld]
		s.warmSectionFlags(d, oldPending[0], oldPending[1])
		d.lightMu.Lock()
		ensureSkyChunk(d, oldPending[0], oldPending[1])
		d.lightMu.Unlock()
		sess.queued = map[[2]int]bool{old: true, oldPending: true}
		sess.sent = map[[2]int]bool{old: true}
		sess.pending = [][2]int{oldPending}
	})
	g.armed.Store(true)
	job := livePrepRequest(t, s, sess, 20, 20)
	joinPrepTestWait(t, g.started, "live first unit")
	p := job.scheduler
	p.mu.Lock()
	locked := true
	defer func() {
		if locked {
			p.mu.Unlock()
		}
	}()
	g.open()

	// A real public caller must acquire ownership before this unfinished job
	// can resume. Its committed update must reach the existing observer feed.
	moveDone := make(chan struct{})
	var moved bool
	var moveErr error
	go func() {
		moved, moveErr = observer.Move(ctx, player.Movement{X: 5, Y: 1, Z: 5})
		close(moveDone)
	}()
	joinPrepTestWait(t, moveDone, "public move during live preparation")
	if !moved || moveErr != nil {
		t.Fatalf("independent move applied=%v error=%v", moved, moveErr)
	}
	if update, err := observer.Next(ctx); err != nil || update.Kind != player.Moved || update.Player.Pose.X != 5 {
		t.Fatalf("committed move update=%+v error=%v", update, err)
	}
	stone, _ := s.BlockCatalog().State(world.Stone)
	s.simulationAction(func() {
		if sess.livePreparation != job || len(sess.queued) != 2 || len(sess.pending) != 1 || len(sess.sent) != 1 {
			t.Fatal("detached preparation changed published chunk tracking")
		}
		var ack protocol.Buffer
		ack.Float(64)
		s.handleChunkBatchReceived(sess, protocol.Packet{Data: ack.Bytes()})
		s.chunkTickStep()
		if err := s.pumpChunks(sess); err != nil {
			t.Fatal(err)
		}
		if sess.burst != burstBatches || sess.cool != 0 || len(sess.pending) != 1 || len(sess.sent) != 1 || len(sess.sendCh) != 0 {
			t.Fatalf("ack/tick exposed unprepared chunks or spent pacing credit: burst=%d cool=%d pending=%d sent=%d packets=%d", sess.burst, sess.cool, len(sess.pending), len(sess.sent), len(sess.sendCh))
		}
		result, err := s.editBlockOwned(ctx, worldview.Edit{Dimension: dimOverworld, Position: pos, State: stone}, "test")
		if err != nil || !result.Applied {
			t.Fatalf("boundary edit applied=%v error=%v", result.Applied, err)
		}
	})
	if update := nextTestWorldUpdate(t, view); update.Kind != worldview.Delta || len(update.Changes) != 1 || update.Changes[0].State != world.Stone {
		t.Fatalf("live boundary edit not committed: %+v", update)
	}
	select {
	case <-job.done:
		t.Fatal("independent work ran after preparation finished")
	default:
	}
	if s.TickCount() != 0 || !s.clock.isFrozen() {
		t.Fatal("preparation handoff advanced the frozen game clock")
	}
	p.mu.Unlock()
	locked = false
	joinPrepTestWait(t, job.done, "live preparation completion")
	if job.err != nil {
		t.Fatal(job.err)
	}
	s.simulationAction(func() {
		if sess.livePreparation != nil || sess.burst != burstBatches-1 || sess.cool != 1 {
			t.Fatal("completion failed to retire current gate or spend exactly one batch")
		}
	})
	packets := readPumpQueue(t, sess)
	lastDone, forget, chunks, edited := -1, -1, 0, false
	for i, packet := range packets {
		switch packet.ID {
		case sess.ids().PlayChunkData:
			chunks++
			r := protocol.NewPacketReader(packet.Data)
			x, _ := r.Int()
			z, _ := r.Int()
			if x == 20 && z == 20 {
				mapping, _ := world.BlockMapping(776)
				wireStone, _ := mapping.Lookup(world.Stone)
				edited = chunkSnapshotStateAt(t, packet.Data, int(pos.X), int(pos.Y), int(pos.Z), -16) == wireStone
			}
		case sess.ids().PlayChunkBatchDone:
			lastDone = i
		case sess.ids().PlayForgetChunk:
			forget = i
		}
	}
	if chunks != 50 || !edited || lastDone < 0 || forget <= lastDone {
		t.Fatalf("published window/FIFO chunks=%d edited=%v done=%d forget=%d", chunks, edited, lastDone, forget)
	}
}

func TestLiveWindowPreparationFinalOwnerBoundaryAcceptsSuccessor(t *testing.T) {
	var s *Server
	var sess *Session
	var job, successor *joinPreparationJob
	var once sync.Once
	var armed atomic.Bool
	retargeted := make(chan error, 1)
	var observe func()
	observe = func() {
		if job.factIndex == len(job.plan.facts) && job.lightIndex == len(job.plan.light) {
			// The last complete light unit just ended. This action is queued
			// for the final preparation turn's owner boundary, precisely between
			// window publication and the next independent owner acquisition.
			if !s.queueSimulationAction(func() {
				err := s.sendWindow(sess, 21, 20)
				successor = sess.livePreparation
				retargeted <- err
			}) {
				retargeted <- errors.New("could not queue final-boundary successor")
			}
			return
		}
		if !s.queueSimulationAction(observe) {
			retargeted <- errors.New("could not retain final-boundary observer")
		}
	}
	s = joinPrepTestServer(t, func(world.Pos) world.Block {
		if armed.Load() {
			once.Do(func() { s.queueSimulationAction(observe) })
		}
		return world.Air
	})
	sess = livePrepSession(t, s, "LiveFinalTurn")
	armed.Store(true)
	s.simulationAction(func() {
		if err := s.sendWindow(sess, 20, 20); err != nil {
			t.Fatal(err)
		}
		job = sess.livePreparation
	})
	select {
	case err := <-retargeted:
		if err != nil {
			t.Fatalf("final-boundary successor rejected: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("preparation never reached its final owner boundary")
	}
	if successor == nil || successor == job || sess.gone() {
		t.Fatal("final boundary closed the session or reused a terminal receipt")
	}
	joinPrepTestWait(t, job.done, "old final turn")
	joinPrepTestWait(t, successor.done, "successor final turn")
	if job.err != nil || successor.err != nil {
		t.Fatalf("completion errors: old=%v successor=%v", job.err, successor.err)
	}
	// A completed old receipt may be canceled repeatedly after its replacement
	// starts; it must not remove the replacement's by-session reservation.
	job.cancel()
	s.simulationAction(func() {
		if sess.livePreparation != nil || sess.liveTarget.cx != 21 || len(sess.queued) < 49 {
			t.Fatal("old receipt changed the successor's completed window")
		}
	})
}

func TestLiveWindowPreparationSaturationClosesOnlyRecipientWithoutColdFallback(t *testing.T) {
	var scans atomic.Int64
	s := joinPrepTestServer(t, func(world.Pos) world.Block { scans.Add(1); return world.Air })
	sess := livePrepSession(t, s, "LiveFull")
	configuring := joinPrepTestSession(t, s, "JoinQuota")
	p := newJoinPreparationScheduler(s, 1)
	s.joinPreparationMu.Lock()
	s.joinPreparer = p
	s.joinPreparationMu.Unlock()
	reserved, ok := p.reserve(configuring)
	if !ok {
		t.Fatal("could not reserve configuring job")
	}
	before := scans.Load()
	var stageErr error
	s.simulationAction(func() { stageErr = s.sendWindow(sess, 20, 20) })
	if stageErr == nil || !sess.gone() || configuring.gone() || scans.Load() != before {
		t.Fatalf("saturation fallback err=%v liveclosed=%v joinclosed=%v samples=%d", stageErr, sess.gone(), configuring.gone(), scans.Load()-before)
	}
	p.mu.Lock()
	jobs := len(p.jobs)
	p.mu.Unlock()
	if jobs != 1 || sess.livePreparation != nil || len(sess.queued) != 0 || len(sess.pending) != 0 || len(sess.sendCh) != 0 {
		t.Fatal("rejected live recipient mutated tracking or reservations")
	}
	reserved.cancel()
}

func TestLiveWindowPreparationPublishedTargetNeedsNoReservationUnderSaturation(t *testing.T) {
	s := joinPrepTestServer(t, func(world.Pos) world.Block { return world.Air })
	sess := livePrepSession(t, s, "LiveWarmFull")
	job := livePrepRequest(t, s, sess, 20, 20)
	joinPrepTestWait(t, job.done, "published target preparation")
	if job.err != nil {
		t.Fatal(job.err)
	}
	p := job.scheduler
	p.mu.Lock()
	p.maxJobs = 1
	p.mu.Unlock()
	configuring := joinPrepTestSession(t, s, "JoinFiller")
	filler, ok := p.reserve(configuring)
	if !ok {
		t.Fatal("could not fill shared preparation quota")
	}
	defer filler.cancel()
	s.simulationAction(func() {
		generation := sess.liveGeneration
		// Same-column movement can immediately pump an already prepared
		// pending column even when another connection owns every job slot.
		sess.rate, sess.burst, sess.interval, sess.cool = 1, 1, 1, 0
		if err := s.sendWindow(sess, 20, 20); err != nil {
			t.Fatal(err)
		}
		if sess.gone() || sess.livePreparation != nil || sess.liveGeneration != generation || len(sess.pending) != 48 || len(sess.sent) != 1 || sess.burst != 0 {
			t.Fatal("unchanged published target reserved capacity or failed to pump prepared terrain")
		}
	})
	p.mu.Lock()
	jobs := len(p.jobs)
	p.mu.Unlock()
	if jobs != 1 {
		t.Fatal("unchanged published target changed shared quota")
	}
	assertPumpBatch(t, readPumpQueue(t, sess), 1)
}

func TestLiveWindowPreparationRetirementRetainsRunningQuotaAndIsolatesReconnect(t *testing.T) {
	var s *Server
	var sess, reconnect *Session
	var job *joinPreparationJob
	var once sync.Once
	var armed atomic.Bool
	retired := make(chan error, 1)
	s = joinPrepTestServer(t, func(world.Pos) world.Block {
		if armed.Load() {
			once.Do(func() {
				s.queueSimulationAction(func() {
					s.retirePlayerOwned(&sess.playerRecord, "test retirement")
					if sess.livePreparation != nil || !job.canceled.Load() {
						retired <- errors.New("retirement retained the current pump gate")
						return
					}
					if spare, ok := job.scheduler.reserve(reconnect); ok {
						spare.cancel()
						retired <- errors.New("running retired unit released its quota before returning")
						return
					}
					select {
					case <-job.done:
						retired <- errors.New("running retirement completed its receipt before the unit returned")
					default:
						retired <- nil
					}
				})
			})
		}
		return world.Air
	})
	sess = livePrepSession(t, s, "LiveReconnect")
	// Exact-profile Java reconnects reserve their own incarnation before the
	// old presence is retired; the scheduler keys sessions rather than names.
	reconnect = joinPrepTestSession(t, s, "LiveReconnect")
	s.simulationAction(func() {
		reconnect.setDimension(s.dims[dimOverworld])
		reconnect.state.Store(StatePlay)
		reconnect.spawned.Store(true)
		reconnect.opened = true
		s.commitPlayerAdmissionOwned(&reconnect.playerRecord)
	})
	p := newJoinPreparationScheduler(s, 1)
	s.joinPreparationMu.Lock()
	s.joinPreparer = p
	s.joinPreparationMu.Unlock()
	armed.Store(true)
	s.simulationAction(func() {
		if err := s.sendWindow(sess, 20, 20); err != nil {
			t.Fatal(err)
		}
		job = sess.livePreparation
	})
	select {
	case err := <-retired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retirement did not run at the live preparation boundary")
	}
	joinPrepTestWait(t, job.done, "retired live unit")
	if !errors.Is(job.err, net.ErrClosed) {
		t.Fatalf("retired receipt error=%v", job.err)
	}
	current := livePrepRequest(t, s, reconnect, 40, 40)
	job.cancel()
	joinPrepTestWait(t, current.done, "reconnected incarnation")
	if current.err != nil || reconnect.gone() {
		t.Fatalf("old receipt retired reconnect: err=%v closed=%v", current.err, reconnect.gone())
	}
	s.simulationAction(func() {
		if !sess.left || sess.active.Load() || reconnect.left || !reconnect.active.Load() || sess.ID() == reconnect.ID() || len(reconnect.queued) != 49 {
			t.Fatal("retirement crossed player incarnations")
		}
	})
}

func TestLiveWindowPreparationShutdownJoinsWholeRunningUnit(t *testing.T) {
	g := newLivePrepGate(t)
	s := joinPrepTestServer(t, g.block)
	t.Cleanup(g.open)
	sess := livePrepSession(t, s, "LiveShutdown")
	g.armed.Store(true)
	job := livePrepRequest(t, s, sess, 20, 20)
	joinPrepTestWait(t, g.started, "shutdown live unit")
	stopped := make(chan struct{})
	go func() { job.scheduler.close(); close(stopped) }()
	// Once cancellation is observable, shutdown still cannot return while a
	// complete facts scan or light initialization owns the world.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !job.canceled.Load() {
		select {
		case <-deadline.C:
			t.Fatal("scheduler shutdown did not cancel live work")
		default:
			runtime.Gosched()
		}
	}
	select {
	case <-stopped:
		t.Fatal("scheduler shutdown returned halfway through an owned unit")
	default:
	}
	g.open()
	joinPrepTestWait(t, stopped, "live scheduler shutdown")
	joinPrepTestWait(t, job.done, "shutdown live receipt")
	if !errors.Is(job.err, net.ErrClosed) {
		t.Fatalf("shutdown receipt error=%v", job.err)
	}
	job.scheduler.mu.Lock()
	jobs, queued := len(job.scheduler.jobs), len(job.scheduler.queue)
	job.scheduler.mu.Unlock()
	if jobs != 0 || queued != 0 || len(sess.queued) != 0 || len(sess.sendCh) != 0 {
		t.Fatal("shutdown retained plans or published an unfinished window")
	}
}

func TestLiveWindowPreparationCoalescesAndRejectsABAPlan(t *testing.T) {
	g := newLivePrepGate(t)
	s := joinPrepTestServer(t, g.block)
	t.Cleanup(g.open)
	sess := livePrepSession(t, s, "LiveABA")
	g.armed.Store(true)
	job := livePrepRequest(t, s, sess, 20, 20)
	joinPrepTestWait(t, g.started, "ABA first unit")
	p := job.scheduler
	p.mu.Lock()
	locked := true
	defer func() {
		if locked {
			p.mu.Unlock()
		}
	}()
	g.open()
	var firstGeneration uint64
	s.simulationAction(func() {
		firstGeneration = sess.liveGeneration
		for range 8 {
			if err := s.sendWindow(sess, 20, 20); err != nil {
				t.Fatal(err)
			}
		}
		if sess.livePreparation != job || sess.liveGeneration != firstGeneration {
			t.Fatal("equivalent repeated movement restarted preparation")
		}
		for _, center := range [][2]int{{60, 60}, {20, 20}} {
			if err := s.sendWindow(sess, center[0], center[1]); err != nil {
				t.Fatal(err)
			}
		}
		if sess.livePreparation != job || sess.liveGeneration <= firstGeneration || job.planGeneration == sess.liveGeneration {
			t.Fatal("A-B-A target changes reused an obsolete plan generation")
		}
	})
	if len(p.jobs) != 1 {
		t.Fatal("retargeting allocated additional reservations")
	}
	p.mu.Unlock()
	locked = false
	joinPrepTestWait(t, job.done, "ABA current target")
	if job.err != nil {
		t.Fatal(job.err)
	}
	s.simulationAction(func() {
		if sess.livePreparation != nil || len(sess.queued) != 49 || len(sess.pending) != 49 || len(sess.sent) != 0 || len(sess.sendCh) != 0 || sess.burst != 0 {
			t.Fatal("no-ack completion emitted chunks or retained an obsolete gate")
		}
		if _, preparedB := s.dims[dimOverworld].skyTops[[2]int{60, 60}]; preparedB {
			t.Fatal("superseded target was prepared after A-B-A retargeting")
		}
		for key := range sess.queued {
			if key[0] < 17 || key[0] > 23 || key[1] < 17 || key[1] > 23 {
				t.Fatalf("obsolete window published: %v", key)
			}
		}
	})
}

func TestLiveWindowPreparationResetRetainsOneReservationAndFreshGeneration(t *testing.T) {
	for _, change := range []string{"same-center-reset", "dimension", "radius-shrink"} {
		t.Run(change, func(t *testing.T) {
			g := newLivePrepGate(t)
			s := joinPrepTestServer(t, g.block)
			t.Cleanup(g.open)
			sess := livePrepSession(t, s, "LiveReset")
			if change == "radius-shrink" {
				s.ViewDistance = 3
				sess.info.ViewDistance = 3
			}
			g.armed.Store(true)
			job := livePrepRequest(t, s, sess, 20, 20)
			joinPrepTestWait(t, g.started, "reset first unit")
			p := job.scheduler
			p.mu.Lock()
			locked := true
			defer func() {
				if locked {
					p.mu.Unlock()
				}
			}()
			g.open()
			s.simulationAction(func() {
				before := sess.liveGeneration
				switch change {
				case "same-center-reset":
					sess.resetChunks()
				case "dimension":
					sess.setDimension(s.dims[dimNether])
					sess.resetChunks()
				case "radius-shrink":
					sess.infoMu.Lock()
					sess.info.ViewDistance = 2
					sess.infoMu.Unlock()
				}
				if err := s.sendWindow(sess, 20, 20); err != nil {
					t.Fatal(err)
				}
				if sess.livePreparation != job || sess.liveGeneration <= before || job.planGeneration == sess.liveGeneration || sess.gone() {
					t.Fatal("reset failed to preserve capacity and invalidate the old plan")
				}
			})
			if len(p.jobs) != 1 {
				t.Fatal("reset exceeded its one retained reservation")
			}
			p.mu.Unlock()
			locked = false
			joinPrepTestWait(t, job.done, "reset destination preparation")
			if job.err != nil {
				t.Fatal(job.err)
			}
			s.simulationAction(func() {
				if sess.livePreparation != nil || len(sess.queued) != 49 || len(sess.pending) != 49 || len(sess.sent) != 0 {
					t.Fatal("reset did not publish exactly the current destination window")
				}
				if change == "dimension" && (len(s.dims[dimNether].skyTops) != 0 || len(s.dims[dimNether].skyGen) != 0) {
					t.Fatal("skyless destination acquired skylight caches")
				}
			})
		})
	}
}
