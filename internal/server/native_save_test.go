package server

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"mc-go/api/player"
	"mc-go/api/worldview"
	"mc-go/internal/world"
)

func nativeSaveTestServer(t *testing.T) *Server {
	t.Helper()
	s := joinPrepTestServer(t, func(world.Pos) world.Block { return world.Air })
	for name, d := range s.dims {
		if name != dimOverworld {
			d.w.Stop()
			d.w = world.NewGen(nil, 1, func(world.Pos) world.Block { return world.Air })
		}
		d.minY, d.height, d.skylight = -16, 32, false
	}
	s.WorldDir = filepath.Join(t.TempDir(), "world")
	return s
}

func nativeSaveBarrier(t *testing.T, s *Server) (<-chan struct{}, func()) {
	t.Helper()
	started, release := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	open := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(open)
	s.nativeSaveWrite = func(snapshot *nativeWorldSaveSnapshot) error {
		once.Do(func() { close(started); <-release })
		return snapshot.write()
	}
	return started, open
}

func TestNativeSaveDetachedStateAllowsPublicMoveAndEdit(t *testing.T) {
	s := nativeSaveTestServer(t)
	java, _ := enforceSession(t, s, "NativePlayer")
	t.Cleanup(func() { s.simulationAction(func() { s.untrack(java) }) })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	core := pluginAvatarCore(s)
	avatar, err := core.Admit(ctx, player.Profile{Name: "NativeObserver", UUID: offlineUUID("NativeObserver")}, player.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := avatar.Next(ctx); err != nil {
		t.Fatal(err)
	}
	pos := world.Pos{X: 1, Y: 0, Z: 1}
	shared := nestedDataSaveStack(7)
	sign := &signText{Front: [4]string{"captured"}, frontColor: "blue", waxed: true}
	s.simulationAction(func() {
		for _, d := range s.dims {
			d.w.SetBlock(pos, world.Dirt)
		}
		java.inv[slotHotbarStart] = shared
		s.dims[dimOverworld].signs.signs = map[world.Pos]*signText{{X: 2}: sign}
		signBlock, _ := world.BlockState("oak_sign")
		s.world.SetBlock(world.Pos{X: 2}, signBlock)
		s.setDayTime(6000)
	})
	started, open := nativeSaveBarrier(t, s)
	saved := make(chan error, 1)
	go func() { saved <- s.SaveWorld() }()
	joinPrepTestWait(t, started, "native detached writer")
	if applied, err := avatar.Move(ctx, player.Movement{X: 5, Y: 1, Z: 5}); err != nil || !applied {
		t.Fatalf("move while saving=%v,%v", applied, err)
	}
	if update, err := avatar.Next(ctx); err != nil || update.Kind != player.Moved {
		t.Fatalf("move publication=%+v,%v", update, err)
	}
	if result, err := core.EditBlock(ctx, worldview.Edit{Dimension: dimOverworld, Position: worldview.Pos{X: 1, Y: 0, Z: 1}, State: blockEditKey(t, "stone")}); err != nil || !result.Applied {
		t.Fatalf("edit while saving=%+v,%v", result, err)
	}
	s.simulationAction(func() {
		shared.Contents[1].Contents[2].Count = 99
		sign.Front[0], sign.frontColor, sign.waxed = "after", "red", false
		s.setDayTime(9000)
		s.dims[dimNether].w.SetBlock(pos, world.Stone)
		s.dims[dimEnd].w.SetBlock(pos, world.Stone)
	})
	select {
	case err := <-saved:
		t.Fatalf("save returned before write completion: %v", err)
	default:
	}
	if s.TickCount() != 0 || !s.clock.isFrozen() {
		t.Fatal("save progress advanced frozen game clock")
	}
	open()
	if err := compactSaveWait(t, saved); err != nil {
		t.Fatal(err)
	}
	restarted := nativeSaveTestServer(t)
	restarted.WorldDir = s.WorldDir
	if err := restarted.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	for name, d := range restarted.dims {
		if got := d.w.GetBlock(pos); got != world.Dirt {
			t.Fatalf("%s captured block=%d want dirt", name, got)
		}
	}
	p := restarted.savedPlayers[DashUUID(offlineUUID("NativePlayer"))]
	if dataSaveInnerCount(p.Inv[slotHotbarStart]) != 7 {
		t.Fatalf("nested player state was not detached: %+v", p.Inv[slotHotbarStart])
	}
	restored := restarted.dims[dimOverworld].signs.signs[world.Pos{X: 2}]
	if restored == nil || restored.Front[0] != "captured" || restored.frontColor != "blue" || !restored.waxed {
		t.Fatalf("detached sign did not survive restart: %+v", restored)
	}
	if restarted.dayTime() != 6000 {
		t.Fatalf("captured day time=%d", restarted.dayTime())
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	latest := nativeSaveTestServer(t)
	latest.WorldDir = s.WorldDir
	if err := latest.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	if latest.world.GetBlock(pos) != world.Stone || latest.dayTime() != 9000 {
		t.Fatal("later native save did not capture committed edit/time")
	}
}

func TestNativeSaveWriteFailureReleasesAdmission(t *testing.T) {
	s := nativeSaveTestServer(t)
	want := errors.New("controlled native writer error")
	var calls atomic.Int32
	s.nativeSaveWrite = func(snapshot *nativeWorldSaveSnapshot) error {
		if calls.Add(1) == 1 {
			return want
		}
		return snapshot.write()
	}
	if err := s.SaveWorld(); !errors.Is(err, want) {
		t.Fatalf("write receipt=%v", err)
	}
	if err := s.SaveWorld(); err != nil {
		t.Fatalf("write failure retained admission: %v", err)
	}
}

func TestNativeSaveBusyCallerWaitsOutsideOwnerAndCapturesLatest(t *testing.T) {
	s := nativeSaveTestServer(t)
	pos := world.Pos{X: 3}
	s.simulationAction(func() { s.world.SetBlock(pos, world.Dirt) })
	started, open := nativeSaveBarrier(t, s)
	first := make(chan error, 1)
	go func() { first <- s.SaveWorld() }()
	joinPrepTestWait(t, started, "first native writer")
	var receipt *nativeWorldSaveJob
	s.simulationAction(func() { receipt = s.nativeSave })
	observed := make(chan bool, 1)
	if !s.queueSimulationAction(func() { observed <- s.nativeSave == receipt }) {
		t.Fatal("could not observe busy admission")
	}
	second := make(chan error, 1)
	go func() { second <- s.SaveWorld() }()
	select {
	case same := <-observed:
		if !same {
			t.Fatal("busy native caller reserved another snapshot")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("busy native caller retained owner")
	}
	s.simulationAction(func() { s.world.SetBlock(pos, world.Stone) })
	select {
	case err := <-second:
		t.Fatalf("busy caller returned old receipt: %v", err)
	default:
	}
	open()
	for _, done := range []<-chan error{first, second} {
		if err := compactSaveWait(t, done); err != nil {
			t.Fatal(err)
		}
	}
	restarted := nativeSaveTestServer(t)
	restarted.WorldDir = s.WorldDir
	if err := restarted.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	if restarted.world.GetBlock(pos) != world.Stone {
		t.Fatal("waiting native save captured before admission")
	}
}

func TestNativeSaveShutdownDrainsAcceptedAndRejectsWaiting(t *testing.T) {
	s := nativeSaveTestServer(t)
	started, open := nativeSaveBarrier(t, s)
	first := make(chan error, 1)
	go func() { first <- s.SaveWorld() }()
	joinPrepTestWait(t, started, "accepted native writer")
	waiting := make(chan error, 1)
	go func() { waiting <- s.SaveWorld() }()
	stopped := make(chan struct{})
	go func() { s.Stop(); close(stopped) }()
	deadline := time.Now().Add(5 * time.Second)
	for !s.ShuttingDown() {
		if time.Now().After(deadline) {
			t.Fatal("shutdown did not begin")
		}
		time.Sleep(time.Millisecond)
	}
	if err := s.SaveWorld(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("shutdown admitted native save: %v", err)
	}
	select {
	case <-stopped:
		t.Fatal("Stop returned before accepted native write")
	default:
	}
	open()
	if err := compactSaveWait(t, first); err != nil {
		t.Fatalf("accepted native save canceled: %v", err)
	}
	if err := compactSaveWait(t, waiting); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("waiting native save crossed shutdown: %v", err)
	}
	joinPrepTestWait(t, stopped, "native shutdown drain")
}

func TestNativeSaveOwnedCommandOrdersAfterPublicWriter(t *testing.T) {
	s := nativeSaveTestServer(t)
	pos := world.Pos{X: 3}
	s.simulationAction(func() { s.world.SetBlock(pos, world.Dirt) })
	started, open := nativeSaveBarrier(t, s)
	first := make(chan error, 1)
	go func() { first <- s.SaveWorld() }()
	joinPrepTestWait(t, started, "native public writer before command")
	s.simulationAction(func() { s.world.SetBlock(pos, world.Stone) })
	entered, command := make(chan struct{}), make(chan error, 1)
	go func() { s.simulationAction(func() { close(entered); command <- s.saveWorldOwned() }) }()
	joinPrepTestWait(t, entered, "owned native command fence")
	open()
	for _, done := range []<-chan error{first, command} {
		if err := compactSaveWait(t, done); err != nil {
			t.Fatal(err)
		}
	}
	restarted := nativeSaveTestServer(t)
	restarted.WorldDir = s.WorldDir
	if err := restarted.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	if restarted.world.GetBlock(pos) != world.Stone {
		t.Fatal("older public native snapshot overwrote owned command state")
	}
}

func TestNativeSaveDisconnectOrdersPlayerWriteAfterPublicSnapshot(t *testing.T) {
	s := nativeSaveTestServer(t)
	java, _ := enforceSession(t, s, "NativeDisconnect")
	s.simulationAction(func() { java.inv[slotHotbarStart] = nestedDataSaveStack(1) })
	started, open := nativeSaveBarrier(t, s)
	first := make(chan error, 1)
	go func() { first <- s.SaveWorld() }()
	joinPrepTestWait(t, started, "native writer before disconnect")
	s.simulationAction(func() { java.inv[slotHotbarStart].Contents[1].Contents[2].Count = 7 })
	entered, left := make(chan struct{}), make(chan struct{})
	go func() { s.simulationAction(func() { close(entered); s.untrack(java) }); close(left) }()
	joinPrepTestWait(t, entered, "native disconnect fence")
	open()
	if err := compactSaveWait(t, first); err != nil {
		t.Fatal(err)
	}
	joinPrepTestWait(t, left, "native disconnect persistence")
	restarted := nativeSaveTestServer(t)
	restarted.WorldDir = s.WorldDir
	if err := restarted.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	p := restarted.savedPlayers[DashUUID(offlineUUID("NativeDisconnect"))]
	if dataSaveInnerCount(p.Inv[slotHotbarStart]) != 7 {
		t.Fatal("older native snapshot overwrote disconnected player state")
	}
}

func TestNativeSaveCurrentIncarnationOverridesRememberedPlayer(t *testing.T) {
	s := nativeSaveTestServer(t)
	old, _ := enforceSession(t, s, "NativeOverlay")
	s.simulationAction(func() { old.inv[slotHotbarStart] = nestedDataSaveStack(1) })
	current, _ := enforceSession(t, s, "NativeOverlay")
	s.simulationAction(func() {
		current.inv[slotHotbarStart] = nestedDataSaveStack(7)
		stale := playerPersist{UUID: current.uuid, Inv: [slotCount]ItemStack{}}
		stale.Inv[slotHotbarStart] = nestedDataSaveStack(2)
		s.savedPlayersMu.Lock()
		s.savedPlayers = map[string]playerPersist{"NativeOverlay": stale, DashUUID(stale.UUID): stale}
		s.savedPlayersMu.Unlock()
	})
	if err := s.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	restarted := nativeSaveTestServer(t)
	restarted.WorldDir = s.WorldDir
	if err := restarted.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	if got := dataSaveInnerCount(restarted.savedPlayers[DashUUID(offlineUUID("NativeOverlay"))].Inv[slotHotbarStart]); got != 7 {
		t.Fatalf("remembered/retired state replaced active player: count=%d", got)
	}
}

func TestNativeSavePlayerFailuresKeepLegacyReceiptAndRecordError(t *testing.T) {
	for _, directory := range []string{"data", "stats"} {
		t.Run(directory, func(t *testing.T) {
			s := nativeSaveTestServer(t)
			java, _ := enforceSession(t, s, "NativeError")
			s.simulationAction(func() { java.inv[slotHotbarStart] = nestedDataSaveStack(7) })
			players := filepath.Join(s.WorldDir, "players")
			if err := os.MkdirAll(players, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(players, directory), []byte("blocked directory"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveWorld(); err != nil {
				t.Fatalf("player failure changed legacy world receipt: %v", err)
			}
			var recorded error
			s.simulationAction(func() { recorded = s.dataSaveErr })
			if recorded == nil {
				t.Fatal("native player failure was not recorded")
			}
			if _, err := world.ReadLevel(s.WorldDir); err != nil {
				t.Fatalf("player failure prevented world write: %v", err)
			}
		})
	}
}
