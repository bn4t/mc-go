package server

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"

	"mc-go/api/event"
	"mc-go/api/frontend"
	"mc-go/api/player"
	"mc-go/api/plugin"
	"mc-go/internal/protocol"
)

func waitPlayerConnections(t *testing.T, s *Server) {
	t.Helper()
	done := make(chan struct{})
	go func() { s.connections.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Java connection did not complete teardown")
	}
}

func TestPlayerLifecycleRejectedJavaHasNoReservationOrLeave(t *testing.T) {
	for _, policy := range []string{"ban", "whitelist"} {
		t.Run(policy, func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			joins, stopJoins := s.Bus().Subscribe(event.TopicPlayerJoin, 4)
			defer stopJoins()
			leaves, stopLeaves := s.Bus().Subscribe(event.TopicPlayerLeave, 4)
			defer stopLeaves()
			if policy == "ban" {
				s.banMu.Lock()
				s.bannedNames = make(map[string]string)
				s.bannedNames["DeniedUser"] = "denied"
				s.banMu.Unlock()
			} else {
				s.whitelistOn.Store(true)
			}
			c, reader, writer := pipePair(t, s)
			if err := sendHandshake(writer, ProtocolVersion, "localhost", 25565, NextLogin); err != nil {
				t.Fatal(err)
			}
			var login protocol.Buffer
			login.String("DeniedUser")
			login.Raw(make([]byte, 16))
			if err := writer.WritePacket(IDLoginStart, login.Bytes()); err != nil {
				t.Fatal(err)
			}
			packet, err := reader.ReadPacket()
			if err != nil || packet.ID != IDLoginDisconnect {
				t.Fatalf("denied response=%#x,%v", packet.ID, err)
			}
			_ = c.Close()
			waitPlayerConnections(t, s)
			if s.OnlineCount() != 0 || len(joins) != 0 || len(leaves) != 0 || len(s.players) != 0 {
				t.Fatalf("denied identity changed lifecycle: online=%d joins=%d leaves=%d records=%d", s.OnlineCount(), len(joins), len(leaves), len(s.players))
			}
		})
	}
}

func TestPlayerLifecyclePendingJavaSharesCapacityAndReleases(t *testing.T) {
	s := New("unused")
	s.MaxPlayers = 1
	t.Cleanup(s.Stop)
	joins, stopJoins := s.Bus().Subscribe(event.TopicPlayerJoin, 4)
	defer stopJoins()
	leaves, stopLeaves := s.Bus().Subscribe(event.TopicPlayerLeave, 4)
	defer stopLeaves()
	c, _, _, _ := configurationBeforePlay(t, s, 776, "PendingUser")
	if s.OnlineCount() != 1 {
		t.Fatalf("configuring Java did not reserve shared capacity: %d", s.OnlineCount())
	}
	core := attachmentCore{frontendCore: frontendCore{s}, attachment: &frontendAttachment{ctx: context.Background()}}
	_, err := core.Admit(context.Background(), player.Profile{Name: "OtherAvatar", UUID: offlineUUID("OtherAvatar")}, player.Options{})
	if !errors.Is(err, player.ErrAdmissionRejected) {
		t.Fatalf("avatar bypassed Java reservation: %v", err)
	}
	_ = c.Close()
	waitPlayerConnections(t, s)
	if s.OnlineCount() != 0 || len(joins) != 0 || len(leaves) != 0 || len(s.players) != 0 {
		t.Fatal("configuring disconnect leaked a reservation or emitted ghost presence")
	}
}

func TestPlayerLifecycleFailedJavaJoinPreservesPreviousIncarnation(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	previous, _ := enforceSession(t, s, "SameProfile")
	previousID := previous.playerRecord.ID()
	joins, stopJoins := s.Bus().Subscribe(event.TopicPlayerJoin, 4)
	defer stopJoins()
	leaves, stopLeaves := s.Bus().Subscribe(event.TopicPlayerLeave, 4)
	defer stopLeaves()
	c, _, writer, replacement := configurationBeforePlay(t, s, 776, "SameProfile")
	if replacement == previous || s.OnlineCount() != 2 {
		t.Fatal("replacement did not obtain an independent pending reservation")
	}
	replacement.outMu.Lock()
	replacement.outByteLimit = 1
	replacement.outMu.Unlock()
	if err := writer.WritePacket(IDConfigFinishAck, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-replacement.done:
	case <-time.After(5 * time.Second):
		t.Fatal("failed Join did not close replacement")
	}
	_ = c.Close()
	waitPlayerConnections(t, s)
	if s.OnlineCount() != 1 || !previous.playerRecord.active.Load() || previous.gone() || previous.playerRecord.ID() != previousID {
		t.Fatal("failed Join retired the previous incarnation")
	}
	if len(joins) != 0 || len(leaves) != 0 {
		t.Fatal("failed replacement emitted committed lifecycle events")
	}
}

func TestPlayerLifecycleJavaReplacementAndOldTeardownAreIncarnationScoped(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	previous, _ := enforceSession(t, s, "ReplaceUser")
	previousID := previous.playerRecord.ID()
	joins, stopJoins := s.Bus().Subscribe(event.TopicPlayerJoined, 4)
	defer stopJoins()
	leaves, stopLeaves := s.Bus().Subscribe(event.TopicPlayerLeft, 4)
	defer stopLeaves()
	c, reader, writer, replacement := configurationBeforePlay(t, s, 776, "ReplaceUser")
	defer c.Close()
	if err := writer.WritePacket(IDConfigFinishAck, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := waitPacket(t, reader, replacement.ids().PlayUpdateAttributes); err != nil {
		t.Fatal(err)
	}
	s.simulationAction(func() {
		if !replacement.playerRecord.active.Load() || previous.playerRecord.active.Load() || replacement.playerRecord.ID() == previousID {
			t.Error("replacement did not commit a new exclusive incarnation")
		}
		// Network teardown follows core replacement. It must not resolve the
		// name again and remove or decrement the successor.
		s.untrack(previous)
		s.untrack(previous)
	})
	if s.OnlineCount() != 1 || s.findPlayerByIDOwned(replacement.playerRecord.ID()) == nil {
		t.Fatal("old transport teardown removed the replacement")
	}
	if len(joins) != 1 || len(leaves) != 1 {
		t.Fatalf("replacement lifecycle cardinality joins=%d leaves=%d", len(joins), len(leaves))
	}
	joined := (<-joins).Data.(event.PlayerJoined)
	left := (<-leaves).Data.(event.PlayerLeft)
	if joined.Player.ID != replacement.playerRecord.ID() || left.Player.ID != previousID {
		t.Fatal("replacement lifecycle events retargeted an incarnation")
	}
}

func awaitPlayerAction(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ready() && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if !ready() {
		t.Fatal("action did not reach its bounded ownership wait")
	}
}

func TestPlayerLifecycleAdmissionBusyAndCanceledBeforeCommit(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	core := attachmentCore{frontendCore: frontendCore{s}, attachment: &frontendAttachment{ctx: context.Background()}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.simulation.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.simulation.mu.Unlock()
		}
	}()
	result := make(chan error, 1)
	profile := player.Profile{Name: "WaitAvatar", UUID: offlineUUID("WaitAvatar")}
	go func() { _, err := core.Admit(ctx, profile, player.Options{}); result <- err }()
	awaitPlayerAction(t, core.attachment.admitting.Load)
	if _, err := core.Admit(context.Background(), profile, player.Options{}); !errors.Is(err, player.ErrBusy) {
		t.Fatalf("second admission was not rejected immediately: %v", err)
	}
	cancel()
	s.simulation.mu.Unlock()
	locked = false
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled admission=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled admission did not finish after owner released")
	}
	if s.OnlineCount() != 0 || len(s.players) != 0 {
		t.Fatal("canceled ownership wait committed an admission")
	}
}

func TestPlayerLifecycleMoveBusyCancellationAndCloseWhileWaiting(t *testing.T) {
	for _, outcome := range []string{"cancel", "close"} {
		t.Run(outcome, func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			core := attachmentCore{frontendCore: frontendCore{s}, attachment: &frontendAttachment{ctx: context.Background()}}
			connection, err := core.Admit(context.Background(), player.Profile{Name: "WaitMove", UUID: offlineUUID("WaitMove")}, player.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			avatar := connection.(*avatarConnection)
			before := avatar.record.position()
			move := before
			move.X++
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			s.simulation.mu.Lock()
			locked := true
			defer func() {
				if locked {
					s.simulation.mu.Unlock()
				}
			}()
			result := make(chan error, 1)
			go func() { _, err := connection.Move(ctx, move); result <- err }()
			awaitPlayerAction(t, avatar.moving.Load)
			if _, err := connection.Move(context.Background(), move); !errors.Is(err, player.ErrBusy) {
				t.Fatalf("second movement was not rejected immediately: %v", err)
			}
			if outcome == "close" {
				if err := connection.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			s.simulation.mu.Unlock()
			locked = false
			select {
			case err := <-result:
				want := player.ErrClosed
				if outcome == "cancel" {
					want = context.Canceled
				}
				if !errors.Is(err, want) {
					t.Fatalf("movement after %s: %v", outcome, err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("movement did not finish after owner released")
			}
			if avatar.record.position() != before {
				t.Fatal("canceled or closed movement changed canonical pose")
			}
		})
	}
}

func TestPlayerLifecycleMoveRechecksCancellationAndCloseAfterHook(t *testing.T) {
	for _, outcome := range []string{"cancel", "close"} {
		t.Run(outcome, func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			core := attachmentCore{frontendCore: frontendCore{s}, attachment: &frontendAttachment{ctx: context.Background()}}
			connection, err := core.Admit(context.Background(), player.Profile{Name: "HookMove", UUID: offlineUUID("HookMove")}, player.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			avatar := connection.(*avatarConnection)
			before := avatar.record.position()
			move := before
			move.X++
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			remove := s.hooks.OnMove(context.Background(), 0, func(context.Context, *plugin.Move) bool {
				if outcome == "close" {
					_ = connection.Close()
				} else {
					cancel()
				}
				return true
			})
			defer remove()
			applied, err := connection.Move(ctx, move)
			want := player.ErrClosed
			if outcome == "cancel" {
				want = context.Canceled
			}
			if applied || !errors.Is(err, want) || avatar.record.position() != before {
				t.Fatalf("hook %s allowed mutation: applied=%v error=%v pose=%+v", outcome, applied, err, avatar.record.position())
			}
		})
	}
}

func TestPlayerLifecycleCapturedExecuteRejectsDetachDuringHook(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	attachmentCtx, detach := context.WithCancel(context.Background())
	defer detach()
	core := attachmentCore{frontendCore: frontendCore{s}, attachment: &frontendAttachment{ctx: attachmentCtx}}
	connection, err := core.Admit(context.Background(), player.Profile{Name: "DetachHook", UUID: offlineUUID("DetachHook")}, player.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	avatar := connection.(*avatarConnection)
	before := avatar.record.position()
	move := before
	move.X++
	entered := make(chan struct{})
	release := make(chan struct{})
	remove := s.hooks.OnMove(context.Background(), 0, func(context.Context, *plugin.Move) bool {
		close(entered)
		<-release
		return true
	})
	defer remove()
	result := make(chan error, 1)
	go func() {
		_, err := core.Execute(context.Background(), frontend.Action{Kind: frontend.Move, Player: "DetachHook", Movement: move})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("Execute did not enter move hook")
	}
	detach()
	close(release)
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("detached in-flight Execute=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not return after hook release")
	}
	if avatar.record.position() != before {
		t.Fatal("Execute committed movement after attachment cancellation")
	}
}
