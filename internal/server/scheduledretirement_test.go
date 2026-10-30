package server

import (
	"errors"
	"mc-go/internal/world"
	"testing"
	"time"
)

func retirementFixture(t *testing.T) (*Server, *Session, schedKey) {
	t.Helper()
	s := nativeSaveTestServer(t)
	p := placeSession(t, s, "Retirement", 8, 0, 8)
	p.active.Store(true)
	p.spawned.Store(true)
	k := schedKey{dim: dimOverworld, pos: world.Pos{X: 1608, Y: 0, Z: 8}, kind: tickBlock, block: "stone_button"}
	button, ok := world.BlockStateOf("stone_button", map[string]string{"face": "floor", "powered": "true"})
	if !ok {
		t.Fatal("button state")
	}
	s.dims[dimOverworld].w.SetBlock(k.pos, button)
	s.ticks.schedulePrio(k, 500, tickPrioHigh)
	return s, p, k
}
func retirementAge(s *Server, now int64) { s.clock.mu.Lock(); s.clock.count = now; s.clock.mu.Unlock() }

func TestScheduledRetirementSuccessfulWritePausesAndReticketResumes(t *testing.T) {
	s, p, k := retirementFixture(t)
	retirementAge(s, 100)
	s.tickRetirementWrite = func(snapshot *scheduledRetirementSnapshot) error { return nil }
	s.scheduledRetirementStepOwned(false)
	job := s.tickRetirement
	if job == nil {
		t.Fatal("no retirement write")
	}
	<-job.done
	s.scheduledRetirementStepOwned(false)
	retirementAge(s, 200)
	saved, _ := s.ticks.snapshotDetachedChunk(scheduledChunkOf(k), 200)
	if len(saved.rows) != 1 || saved.rows[0].delay != 400 {
		t.Fatalf("cold rows %#v", saved)
	}
	p.setPos(1608, 0, 8, 0, 0, true)
	s.scheduledRetirementStepOwned(false)
	s.ticks.activateSavedChunk(scheduledChunkOf(k), 200)
	if got := s.ticks.poll(599); len(got) != 0 {
		t.Fatal("resumed early")
	}
	if got := s.ticks.poll(600); len(got) != 1 {
		t.Fatalf("resumed callbacks %v", got)
	}
}
func TestScheduledRetirementReticketDuringWriteKeepsResidentDeadline(t *testing.T) {
	s, p, k := retirementFixture(t)
	release := make(chan struct{})
	s.tickRetirementWrite = func(*scheduledRetirementSnapshot) error { <-release; return nil }
	retirementAge(s, 100)
	s.scheduledRetirementStepOwned(false)
	job := s.tickRetirement
	if job == nil {
		t.Fatal("no write")
	}
	p.setPos(1608, 0, 8, 0, 0, true)
	retirementAge(s, 200)
	s.scheduledRetirementStepOwned(false)
	if !s.ticks.isQueued(k) {
		t.Fatal("reticket did not reattach resident")
	}
	close(release)
	<-job.done
	s.scheduledRetirementStepOwned(false)
	if got := s.ticks.poll(499); len(got) != 0 {
		t.Fatal("resident early")
	}
	if got := s.ticks.poll(500); len(got) != 1 {
		t.Fatal("stale completion reset resident deadline")
	}
}
func TestScheduledRetirementFailureRetainsAbsoluteAndRetries(t *testing.T) {
	s, _, k := retirementFixture(t)
	s.tickRetirementWrite = func(*scheduledRetirementSnapshot) error { return errors.New("disk denied") }
	retirementAge(s, 100)
	s.scheduledRetirementStepOwned(false)
	job := s.tickRetirement
	if job == nil {
		t.Fatal("no write")
	}
	<-job.done
	retirementAge(s, 200)
	s.scheduledRetirementStepOwned(false)
	saved, _ := s.ticks.snapshotDetachedChunk(scheduledChunkOf(k), 200)
	if len(saved.rows) != 1 || saved.rows[0].delay != 300 {
		t.Fatal("failed write fabricated cold pause")
	}
	s.tickRetirementWrite = func(*scheduledRetirementSnapshot) error { return nil }
	s.scheduledRetirementStepOwned(false)
	if s.tickRetirement != nil {
		t.Fatal("disk failure retried without backoff")
	}
	s.tickRetirementRetryAt = time.Time{}
	s.scheduledRetirementStepOwned(false)
	retry := s.tickRetirement
	if retry == nil {
		t.Fatal("failed write not retried")
	}
	<-retry.done
	s.scheduledRetirementStepOwned(false)
	retirementAge(s, 300)
	saved, _ = s.ticks.snapshotDetachedChunk(scheduledChunkOf(k), 300)
	if saved.rows[0].delay != 300 {
		t.Fatal("successful retry did not fix saved delay")
	}
}

func TestScheduledRetirementFullSnapshotUsesCompletedColdRows(t *testing.T) {
	s, _, k := retirementFixture(t)
	s.tickRetirementWrite = func(*scheduledRetirementSnapshot) error { return nil }
	retirementAge(s, 100)
	s.scheduledRetirementStepOwned(false)
	job := s.tickRetirement
	if job == nil {
		t.Fatal("no write")
	}
	<-job.done
	retirementAge(s, 200)
	s.captureNativeWorldSaveOwned()
	rows := s.captureScheduledChunkTicksOwned()[dimOverworld][[2]int32{100, 0}].Blocks
	if len(rows) != 1 || rows[0].Delay != 400 {
		t.Fatalf("later full save aged completed cold snapshot: %#v", rows)
	}
	if !s.ticks.detached[scheduledChunkOf(k)].serialized {
		t.Fatal("full capture did not reconcile completed retirement")
	}
}
func TestScheduledRetirementOldWriteCannotDiscardSecondDetach(t *testing.T) {
	s, p, k := retirementFixture(t)
	release := make(chan struct{})
	s.tickRetirementWrite = func(*scheduledRetirementSnapshot) error { <-release; return nil }
	retirementAge(s, 100)
	s.scheduledRetirementStepOwned(false)
	old := s.tickRetirement
	if old == nil {
		t.Fatal("no write")
	}
	p.setPos(1608, 0, 8, 0, 0, true)
	s.scheduledRetirementStepOwned(false)
	extra := k
	extra.pos.X++
	s.ticks.schedule(extra, 600)
	p.setPos(8, 0, 8, 0, 0, true)
	retirementAge(s, 200)
	s.scheduledRetirementStepOwned(false)
	close(release)
	<-old.done
	s.tickRetirementWrite = func(*scheduledRetirementSnapshot) error { return nil }
	s.scheduledRetirementStepOwned(false)
	fresh := s.tickRetirement
	if fresh == nil {
		t.Fatal("no fresh generation write")
	}
	<-fresh.done
	s.scheduledRetirementStepOwned(false)
	rows, _ := s.ticks.snapshotDetachedChunk(scheduledChunkOf(k), 300)
	if len(rows.rows) != 2 || rows.rows[0].delay != 300 || rows.rows[1].delay != 400 {
		t.Fatalf("reticket edit lost or stale deadline: %#v", rows)
	}
}

func TestScheduledRetirementCompletionWithoutControllerOrDuringShutdown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "disconnect", true: "shutdown"}[shutdown], func(t *testing.T) {
			s, p, k := retirementFixture(t)
			s.tickRetirementWrite = func(*scheduledRetirementSnapshot) error { return nil }
			retirementAge(s, 100)
			s.scheduledRetirementStepOwned(false)
			job := s.tickRetirement
			if job == nil {
				t.Fatal("no write")
			}
			<-job.done
			p.active.Store(false)
			if shutdown {
				s.shuttingDown.Store(true)
				defer s.shuttingDown.Store(false)
			}
			retirementAge(s, 200)
			// The native disconnect fence must account for the submitted receipt even
			// without another player tick. Capture must not repack aged resident rows.
			s.waitNativeWorldSaveOwned()
			s.captureNativeWorldSaveOwned()
			saved, _ := s.ticks.snapshotDetachedChunk(scheduledChunkOf(k), 200)
			if len(saved.rows) != 1 || saved.rows[0].delay != 400 || s.tickRetirement != nil {
				t.Fatalf("last-controller completion lost: %#v", saved)
			}
		})
	}
}

func TestScheduledRetirementDoesNotMaterializeFutureRows(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "unmaterialized", true: "unavailable"}[missing], func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			s.WorldDir = t.TempDir()
			p := placeSession(t, s, "FutureRetirement", 8, -60, 8)
			p.active.Store(true)
			p.spawned.Store(true)
			d := s.dims[dimOverworld]
			old := d.w
			t.Cleanup(old.Stop)
			provider := &scheduledPolicyProvider{missing: missing}
			d.w = world.NewProvided(nil, 1, provider)
			s.tickRetirementWrite = func(*scheduledRetirementSnapshot) error { return nil }
			k := schedKey{dim: dimOverworld, pos: world.Pos{X: 1608, Y: -60, Z: 8}, kind: tickBlock, block: "stone_button"}
			s.ticks.schedule(k, 10000)
			s.scheduledRetirementStepOwned(false)
			if calls := provider.calls.Load(); calls != 0 {
				t.Fatalf("retirement materialized unadmitted future chunk: provider calls%d", calls)
			}
			if !s.ticks.isQueued(k) || s.tickRetirement != nil {
				t.Fatal("unmaterialized future queue changed")
			}
		})
	}
}
