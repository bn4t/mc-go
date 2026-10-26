package server

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mc-go/api/player"
	"mc-go/api/worldview"
	"mc-go/internal/world"
)

func compactSaveTestServer(t *testing.T) *Server {
	t.Helper()
	s := joinPrepTestServer(t, func(world.Pos) world.Block { return world.Air })
	s.dims[dimOverworld].skylight = false
	s.WorldPath = filepath.Join(t.TempDir(), "world.json")
	return s
}

func compactSavedBlock(t *testing.T, path string, pos world.Pos) world.Block {
	t.Helper()
	w := world.NewGen(nil, 1, func(world.Pos) world.Block { return world.Air })
	defer w.Stop()
	if err := w.Load(path); err != nil {
		t.Fatal(err)
	}
	return w.GetBlock(pos)
}

func compactSaveWait(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("compact save did not complete")
		return nil
	}
}

func TestCompactSaveDetachedDimensionsAllowPublicMoveAndEdit(t *testing.T) {
	s := compactSaveTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	core := pluginAvatarCore(s)
	avatar, err := core.Admit(ctx, player.Profile{Name: "SaveObserver", UUID: offlineUUID("SaveObserver")}, player.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if baseline, err := avatar.Next(ctx); err != nil || baseline.Kind != player.Snapshot {
		t.Fatalf("avatar baseline=%+v err=%v", baseline, err)
	}
	pos := world.Pos{X: 1, Y: 0, Z: 1}
	s.simulationAction(func() {
		s.dims[dimOverworld].w.SetBlock(pos, world.Dirt)
		s.dims[dimNether].w.SetBlock(pos, world.Stone)
		s.dims[dimEnd].w.SetBlock(pos, world.Air)
	})
	view, _ := watchTestVolume(t, s, worldview.Request{Dimension: dimOverworld,
		Bounds: worldview.Box{Min: worldview.Pos{X: 1, Y: 0, Z: 1}, Max: worldview.Pos{X: 2, Y: 1, Z: 2}}})
	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)
	s.compactSaveWrite = func(path string, snapshot *world.SaveSnapshot) error {
		once.Do(func() { close(started); <-release })
		return snapshot.Write(path)
	}
	saved := make(chan error, 1)
	go func() { saved <- s.SaveWorld() }()
	joinPrepTestWait(t, started, "detached save writer")
	if applied, err := avatar.Move(ctx, player.Movement{X: 5, Y: 1, Z: 5}); err != nil || !applied {
		t.Fatalf("public movement during pending save applied=%v err=%v", applied, err)
	}
	if update, err := avatar.Next(ctx); err != nil || update.Kind != player.Moved || update.Player.Pose.X != 5 {
		t.Fatalf("movement was not committed while saving: %+v, %v", update, err)
	}
	result, err := core.EditBlock(ctx, worldview.Edit{Dimension: dimOverworld, Position: worldview.Pos{X: 1, Y: 0, Z: 1}, State: blockEditKey(t, "stone")})
	if err != nil || !result.Applied {
		t.Fatalf("public edit during pending save applied=%v err=%v", result.Applied, err)
	}
	if update := nextTestWorldUpdate(t, view); update.Kind != worldview.Delta || len(update.Changes) != 1 || update.Changes[0].State != world.Stone {
		t.Fatalf("edit was not committed while saving: %+v", update)
	}
	s.simulationAction(func() {
		s.dims[dimNether].w.SetBlock(pos, world.Dirt)
		s.dims[dimEnd].w.SetBlock(pos, world.Stone)
	})
	select {
	case err := <-saved:
		t.Fatalf("SaveWorld returned before its write completed: %v", err)
	default:
	}
	if s.TickCount() != 0 || !s.clock.isFrozen() {
		t.Fatal("save progress required advancing the frozen clock")
	}
	open()
	if err := compactSaveWait(t, saved); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		path string
		want world.Block
	}{{s.WorldPath, world.Dirt}, {s.WorldPath + ".the_nether", world.Stone}, {s.WorldPath + ".the_end", world.Air}} {
		if got := compactSavedBlock(t, row.path, pos); got != row.want {
			t.Fatalf("post-capture dimension mutation leaked into %s: %d want %d", row.path, got, row.want)
		}
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	if got := compactSavedBlock(t, s.WorldPath, pos); got != world.Stone {
		t.Fatal("later save did not capture the committed edit")
	}
}

func TestCompactSaveOwnedCommandOrdersAfterPendingPublicWrite(t *testing.T) {
	s := compactSaveTestServer(t)
	pos := world.Pos{X: 3, Y: 0, Z: 3}
	s.simulationAction(func() { s.world.SetBlock(pos, world.Dirt) })
	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)
	var writing, maximum atomic.Int32
	s.compactSaveWrite = func(path string, snapshot *world.SaveSnapshot) error {
		n := writing.Add(1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		defer writing.Add(-1)
		once.Do(func() { close(started); <-release })
		return snapshot.Write(path)
	}
	first := make(chan error, 1)
	go func() { first <- s.SaveWorld() }()
	joinPrepTestWait(t, started, "first compact write")
	s.simulationAction(func() { s.world.SetBlock(pos, world.Stone) })
	commandEntered, commandDone := make(chan struct{}), make(chan error, 1)
	go func() {
		s.simulationAction(func() {
			close(commandEntered)
			// This is the owned persistence fence used by /save-all. It may
			// wait for a submitted writer, which must never need this owner.
			commandDone <- s.saveWorldOwned()
		})
	}()
	joinPrepTestWait(t, commandEntered, "owned save fence")
	second := make(chan error, 1)
	go func() { second <- s.SaveWorld() }()
	open()
	for _, done := range []<-chan error{first, commandDone, second} {
		if err := compactSaveWait(t, done); err != nil {
			t.Fatal(err)
		}
	}
	if maximum.Load() != 1 || compactSavedBlock(t, s.WorldPath, pos) != world.Stone {
		t.Fatal("ordered saves overlapped or an older snapshot overwrote the newer state")
	}
}

func TestCompactSaveBusyPublicCallerWaitsOutsideOwnerAndCapturesLatest(t *testing.T) {
	s := compactSaveTestServer(t)
	pos := world.Pos{X: 3, Y: 0, Z: 3}
	s.simulationAction(func() { s.world.SetBlock(pos, world.Dirt) })
	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)
	s.compactSaveWrite = func(path string, snapshot *world.SaveSnapshot) error {
		once.Do(func() { close(started); <-release })
		return snapshot.Write(path)
	}
	first := make(chan error, 1)
	go func() { first <- s.SaveWorld() }()
	joinPrepTestWait(t, started, "busy public save writer")
	var firstReceipt *compactWorldSaveJob
	s.simulationAction(func() { firstReceipt = s.compactSave })
	observed := make(chan bool, 1)
	// With frozen ticks and no other callers, this deferred observation runs
	// at the second public save's owner boundary, after its admission attempt.
	if !s.queueSimulationAction(func() { observed <- s.compactSave == firstReceipt }) {
		t.Fatal("could not observe the waiting public caller")
	}
	second := make(chan error, 1)
	go func() { second <- s.SaveWorld() }()
	select {
	case sameReceipt := <-observed:
		if !sameReceipt {
			t.Fatal("a busy save captured a second outstanding snapshot")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("busy save waited while retaining the simulation owner")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := pluginAvatarCore(s).EditBlock(ctx, worldview.Edit{Dimension: dimOverworld,
		Position: worldview.Pos{X: 3, Y: 0, Z: 3}, State: blockEditKey(t, "stone")})
	if err != nil || !result.Applied {
		t.Fatalf("edit behind a busy save applied=%v err=%v", result.Applied, err)
	}
	select {
	case err := <-second:
		t.Fatalf("busy caller reported the older save's receipt as its own: %v", err)
	default:
	}
	open()
	for _, done := range []<-chan error{first, second} {
		if err := compactSaveWait(t, done); err != nil {
			t.Fatal(err)
		}
	}
	if compactSavedBlock(t, s.WorldPath, pos) != world.Stone {
		t.Fatal("waiting caller captured before admission instead of after the edit")
	}
}

func TestCompactSaveWriteFailureReleasesAdmission(t *testing.T) {
	s := compactSaveTestServer(t)
	want := errors.New("controlled compact write failure")
	var calls atomic.Int32
	s.compactSaveWrite = func(path string, snapshot *world.SaveSnapshot) error {
		if calls.Add(1) == 1 {
			return want
		}
		return snapshot.Write(path)
	}
	if err := s.SaveWorld(); !errors.Is(err, want) {
		t.Fatalf("write failure receipt=%v", err)
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatalf("failed write retained its admission: %v", err)
	}
}

func TestCompactSaveCanonicalFailureReceiptAndRecovery(t *testing.T) {
	s := compactSaveTestServer(t)
	s.simulationAction(func() { s.world.SetBlock(world.Pos{X: 1}, world.Block(^uint32(0))) })
	if err := s.SaveWorld(); err == nil {
		t.Fatal("canonical serialization failure was reported as a successful save")
	}
	s.simulationAction(func() { s.world.Restore(map[world.Pos]world.Block{{X: 1}: world.Stone}) })
	if err := s.SaveWorld(); err != nil {
		t.Fatalf("canonical failure retained its snapshot slot: %v", err)
	}
	if compactSavedBlock(t, s.WorldPath, world.Pos{X: 1}) != world.Stone {
		t.Fatal("recovered save did not capture the corrected canonical state")
	}
}

func TestCompactSaveOwnedWaitRejectsNewCaptureAfterShutdown(t *testing.T) {
	s := compactSaveTestServer(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)
	var writes atomic.Int32
	s.compactSaveWrite = func(path string, snapshot *world.SaveSnapshot) error {
		writes.Add(1)
		once.Do(func() { close(started); <-release })
		return snapshot.Write(path)
	}
	first := make(chan error, 1)
	go func() { first <- s.SaveWorld() }()
	joinPrepTestWait(t, started, "accepted write before owned wait")
	entered, commandDone := make(chan struct{}), make(chan error, 1)
	go func() {
		s.simulationAction(func() {
			close(entered)
			commandDone <- s.saveWorldOwned()
		})
	}()
	joinPrepTestWait(t, entered, "owned save waiting for older write")
	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !s.ShuttingDown() {
		select {
		case <-deadline.C:
			t.Fatal("shutdown did not become observable during the owned wait")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	open()
	if err := compactSaveWait(t, first); err != nil {
		t.Fatalf("accepted older write was canceled: %v", err)
	}
	if err := compactSaveWait(t, commandDone); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("owned waiter admitted a new save after shutdown: %v", err)
	}
	joinPrepTestWait(t, stopped, "shutdown behind owned save wait")
	if writes.Load() != int32(len(s.dims)) {
		t.Fatal("owned waiter captured or wrote another snapshot after shutdown")
	}
}

func TestCompactSaveShutdownDrainsAcceptedAndRejectsWaiting(t *testing.T) {
	s := compactSaveTestServer(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)
	var writes atomic.Int32
	s.compactSaveWrite = func(path string, snapshot *world.SaveSnapshot) error {
		writes.Add(1)
		once.Do(func() { close(started); <-release })
		return snapshot.Write(path)
	}
	first := make(chan error, 1)
	go func() { first <- s.SaveWorld() }()
	joinPrepTestWait(t, started, "accepted shutdown save")
	waiting := make(chan error, 1)
	go func() { waiting <- s.SaveWorld() }()
	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for !s.ShuttingDown() {
		select {
		case <-deadline.C:
			t.Fatal("shutdown did not start")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if err := s.SaveWorld(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("save admitted after shutdown: %v", err)
	}
	select {
	case <-stopped:
		t.Fatal("Stop returned before an accepted save finished")
	default:
	}
	open()
	if err := compactSaveWait(t, first); err != nil {
		t.Fatalf("shutdown canceled an accepted save: %v", err)
	}
	if err := compactSaveWait(t, waiting); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("waiting save crossed shutdown admission: %v", err)
	}
	joinPrepTestWait(t, stopped, "shutdown save fence")
	if writes.Load() != int32(len(s.dims)) {
		t.Fatal("shutdown started an unadmitted save or omitted an accepted dimension")
	}
}
