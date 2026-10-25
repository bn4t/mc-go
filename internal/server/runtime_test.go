package server

import (
	"context"
	"errors"
	"math"
	"net"
	"sync"
	"testing"
	"time"

	"mc-go/api/frontend"
	"mc-go/internal/event"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

type observingFrontend struct{ core chan frontend.Core }

func (f *observingFrontend) Name() string { return "observer" }
func (f *observingFrontend) Serve(ctx context.Context, core frontend.Core, ln net.Listener) error {
	f.core <- core
	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		conn.Close()
	}
}

func runtimeListener(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	return ln
}
func awaitAttachmentCount(t *testing.T, s *Server, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.listenersMu.Lock()
		count := len(s.listeners)
		s.listenersMu.Unlock()
		if count == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("did not reach %d attached listeners", n)
}
func awaitServeExit(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("serve exit=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("frontend did not exit")
	}
}

func TestRuntimeMultipleListenersShareClockAndSurviveDetach(t *testing.T) {
	s := New("unused")
	s.StartFrozen = true
	t.Cleanup(s.Stop)
	first, second := runtimeListener(t), runtimeListener(t)
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { firstDone <- s.Serve(first) }()
	go func() { secondDone <- NewJavaFrontend(s).Serve(second) }() // direct frontend shares lifecycle too
	awaitAttachmentCount(t, s, 2)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	events, unsubscribe := s.Bus().Subscribe(event.TopicTick, 16)
	defer unsubscribe()
	s.StepTicks(2)
	if got := s.TickCount(); got != 2 {
		t.Fatalf("ticks=%d,want 2", got)
	}
	// Only the authoritative game clock publishes ticks, once for all dimensions.
	for tick := int64(1); tick <= 2; tick++ {
		select {
		case got := <-events:
			if got.Data != tick {
				t.Fatalf("tick event=%v,want %d", got, tick)
			}
		case <-time.After(time.Second):
			t.Fatal("missing authoritative tick")
		}
	}
	select {
	case extra := <-events:
		t.Fatalf("duplicate tick event %v", extra)
	default:
	}
	first.Close()
	awaitServeExit(t, firstDone)
	awaitAttachmentCount(t, s, 1)
	// Closing one listener must not retire the world's shard workers.
	pos := world.Pos{X: 42, Y: 70, Z: 8}
	s.World().SetBlock(pos, world.Stone)
	if got := s.World().GetBlock(pos); got != world.Stone {
		t.Fatalf("world stopped on detach: block=%v", got)
	}
	s.StepTicks(1)
	select {
	case got := <-events:
		if got.Data != int64(3) {
			t.Fatalf("post-detach tick=%v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("clock stopped with first listener")
	}
	// The second protocol listener continues accepting actual status requests.
	conn, err := net.DialTimeout("tcp", second.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	writer, reader := protocol.NewWriter(conn), protocol.NewReader(conn)
	if err = sendHandshake(writer, ProtocolVersion, "localhost", 25565, NextStatus); err != nil {
		t.Fatal(err)
	}
	if err = writer.WritePacket(IDStatusRequest, nil); err != nil {
		t.Fatal(err)
	}
	if packet, err := reader.ReadPacket(); err != nil || packet.ID != IDStatusResponse {
		t.Fatalf("surviving frontend status=%v,%v", packet.ID, err)
	}
	conn.Close()
	s.Stop()
	awaitServeExit(t, secondDone)
	stopped := s.TickCount()
	time.Sleep(70 * time.Millisecond)
	if got := s.TickCount(); got != stopped {
		t.Fatalf("tick after Stop=%d,want %d", got, stopped)
	}
	if err := s.Start(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("restart=%v", err)
	}
}

// joinRuntimeJava uses a real Java listener. Registry encoding under -race can
// exceed the small pipe helper's watchdog, so this integration test owns a
// generous transport deadline and always joins its read goroutine.
func joinRuntimeJava(t *testing.T, ln net.Listener, name string) (net.Conn, *protocol.Reader, *protocol.Writer) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(90 * time.Second))
	reader, writer := protocol.NewReader(conn), protocol.NewWriter(conn)
	if err = sendHandshake(writer, ProtocolVersion, "localhost", 25565, NextLogin); err != nil {
		t.Fatal(err)
	}
	var login protocol.Buffer
	login.String(name)
	login.Raw(make([]byte, 16))
	if err = writer.WritePacket(IDLoginStart, login.Bytes()); err != nil {
		t.Fatal(err)
	}
	compression, err := reader.ReadPacket()
	if err != nil || compression.ID != IDLoginSetCompression {
		t.Fatalf("compression: %v %v", compression.ID, err)
	}
	threshold, err := protocol.NewPacketReader(compression.Data).VarInt()
	if err != nil {
		t.Fatal(err)
	}
	reader.SetThreshold(threshold)
	writer.SetThreshold(threshold)
	if _, err = reader.ReadPacket(); err != nil {
		t.Fatal(err)
	}
	if err = writer.WritePacket(IDLoginAcknowledged, nil); err != nil {
		t.Fatal(err)
	}
	var packs protocol.Buffer
	writeKnownPacks(&packs, []packEntry{{"minecraft", "core", "99.9-test"}})
	if err = writer.WritePacket(IDConfigKnownPacks, packs.Bytes()); err != nil {
		t.Fatal(err)
	}
	for {
		packet, err := reader.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		if packet.ID == IDConfigFinish {
			break
		}
	}
	if err = writer.WritePacket(IDConfigFinishAck, nil); err != nil {
		t.Fatal(err)
	}
	if err = writer.WritePacket(IDPlayPlayerLoadedSB, nil); err != nil {
		t.Fatal(err)
	}
	for {
		packet, err := reader.ReadPacket()
		if err != nil {
			t.Fatal(err)
		}
		switch packet.ID {
		case IDPlaySyncPos:
			id, err := protocol.NewPacketReader(packet.Data).VarInt()
			if err != nil {
				t.Fatal(err)
			}
			var confirmation protocol.Buffer
			confirmation.VarInt(id)
			if err := writer.WritePacket(IDPlayTeleportConfirmSB, confirmation.Bytes()); err != nil {
				t.Fatal(err)
			}
		case IDPlayChunkBatchDone:
			var acknowledgement protocol.Buffer
			acknowledgement.Float(64)
			if err := writer.WritePacket(IDPlayChunkBatchRecvSB, acknowledgement.Bytes()); err != nil {
				t.Fatal(err)
			}
		case IDPlayKeepAliveCB:
			if err := writer.WritePacket(IDPlayKeepAliveSB, packet.Data); err != nil {
				t.Fatal(err)
			}
		case IDPlayUpdateAttributes:
			return conn, reader, writer
		}
	}
}

func TestPublicFrontendSharesJavaPlayerActions(t *testing.T) {
	s := New("unused")
	s.StartFrozen = true
	t.Cleanup(s.Stop)
	ln := runtimeListener(t)
	endpoint := &observingFrontend{core: make(chan frontend.Core, 1)}
	done := make(chan error, 1)
	go func() { done <- s.Attach(endpoint, ln) }()
	var core frontend.Core
	select {
	case core = <-endpoint.core:
	case <-time.After(3 * time.Second):
		t.Fatal("frontend was not attached")
	}
	javaListener := runtimeListener(t)
	javaDone := make(chan error, 1)
	go func() { javaDone <- s.Serve(javaListener) }()
	awaitAttachmentCount(t, s, 2)
	conn, reader, writer := joinRuntimeJava(t, javaListener, "Shared")
	defer func() { javaListener.Close(); awaitServeExit(t, javaDone) }()
	// Drain the real Java codec's outbound stream and retain teleport observations.
	teleports := make(chan [3]float64, 16)
	drainDone := make(chan struct{})
	go func() {
		defer close(drainDone)
		for {
			packet, err := reader.ReadPacket()
			if err != nil {
				return
			}
			if packet.ID != IDPlaySyncPos {
				continue
			}
			r := protocol.NewPacketReader(packet.Data)
			if _, err = r.VarInt(); err != nil {
				continue
			}
			x, ex := r.Double()
			y, ey := r.Double()
			z, ez := r.Double()
			if ex == nil && ey == nil && ez == nil {
				select {
				case teleports <- [3]float64{x, y, z}:
				default:
				}
			}
		}
	}()
	defer func() { conn.Close(); <-drainDone }()
	// Java's wire movement enters the same action seam observed by the new frontend.
	var movement protocol.Buffer
	movement.Double(2.5)
	movement.Double(-60)
	movement.Double(0.5)
	movement.Byte(1)
	if err := writer.WritePacket(IDPlayMovePosSB, movement.Bytes()); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		snapshot, err := core.Snapshot(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(snapshot.Players) == 1 && snapshot.Players[0].X == 2.5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("public frontend did not observe Java movement: %+v", snapshot)
		}
		time.Sleep(time.Millisecond)
	}
	result, err := core.Execute(context.Background(), frontend.Action{Kind: frontend.Teleport, Player: "Shared", Movement: frontend.Movement{X: 8.5, Y: 70, Z: 4.5}})
	if err != nil || !result.Applied {
		t.Fatalf("neutral teleport=%+v,%v", result, err)
	}
	for {
		select {
		case position := <-teleports:
			if position == [3]float64{8.5, 70, 4.5} {
				goto received
			}
		case <-time.After(3 * time.Second):
			t.Fatal("Java client did not receive neutral frontend teleport")
		}
	}
received:
	result, err = core.Execute(context.Background(), frontend.Action{Kind: frontend.Move, Player: "Shared", Movement: frontend.Movement{X: 9.5, Y: 70, Z: 4.5, Yaw: 90, OnGround: false}})
	if err != nil || !result.Applied {
		t.Fatalf("neutral move=%+v,%v", result, err)
	}
	snapshot, err := core.Snapshot(context.Background())
	if err != nil || len(snapshot.Players) != 1 || snapshot.Players[0].X != 9.5 || snapshot.Players[0].Yaw != 90 {
		t.Fatalf("shared movement snapshot=%+v,%v", snapshot, err)
	}
	snapshot.Players[0].Name = "mutated copy"
	again, _ := core.Snapshot(context.Background())
	if again.Players[0].Name != "Shared" {
		t.Fatal("snapshot aliases live state")
	}
	result, err = core.Execute(context.Background(), frontend.Action{Kind: frontend.Move, Player: "Shared", Movement: frontend.Movement{X: math.NaN(), Y: 70, Z: 4.5}})
	if err != nil || result.Applied {
		t.Fatalf("invalid neutral move=%+v,%v", result, err)
	}
	ln.Close()
	awaitServeExit(t, done)
	// Attachment capabilities are revoked at detach, including a retained
	// privileged core handle. The independent Java player/runtime continues.
	if _, err = core.Snapshot(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("detached snapshot did not reject: %v", err)
	}
	if _, err = core.Execute(context.Background(), frontend.Action{Kind: frontend.Move, Player: "Shared", Movement: frontend.Movement{X: 10.5, Y: 70, Z: 4.5}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("detached action did not reject: %v", err)
	}
	if live, err := (frontendCore{s}).Snapshot(context.Background()); err != nil || len(live.Players) != 1 || live.Players[0].X != 9.5 {
		t.Fatalf("detaching observer changed Java runtime: %+v,%v", live, err)
	}
}

func TestRuntimeStartFailureAndConcurrentStop(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	occupied := runtimeListener(t)
	s.RconAddr = occupied.Addr().String()
	s.RconPassword = "secret"
	if err := s.Start(); err == nil {
		t.Fatal("Start succeeded with occupied RCON port")
	}
	if s.runtimeStarted || s.ticking.Load() {
		t.Fatal("failed startup left live runtime")
	}
	s.RconPassword = ""
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	var callers sync.WaitGroup
	for range 8 {
		callers.Add(1)
		go func() { defer callers.Done(); s.Stop() }()
	}
	callers.Wait()
	if s.ticking.Load() {
		t.Fatal("Stop returned before tick loop exited")
	}
}

func TestRuntimeStartupRollsBackServicesOnQueryFailure(t *testing.T) {
	s := New("unused")
	s.StartFrozen = true
	t.Cleanup(s.Stop)
	reserved := runtimeListener(t)
	rconAddr := reserved.Addr().String()
	reserved.Close()
	occupied, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	s.RconAddr, s.RconPassword = rconAddr, "secret"
	s.EnableQuery, s.QueryAddr = true, occupied.LocalAddr().String()
	if err = s.Start(); err == nil {
		t.Fatal("Start succeeded with occupied query port")
	}
	probe, err := net.Listen("tcp", rconAddr)
	if err != nil {
		t.Fatalf("failed startup retained RCON listener: %v", err)
	}
	probe.Close()
	if s.runtimeStarted || s.ticking.Load() {
		t.Fatal("partial startup left a runtime")
	}
	s.EnableQuery = false
	if err = s.Start(); err != nil {
		t.Fatalf("retry after rollback: %v", err)
	}
}

func TestRuntimeStopWinsBlockedStartup(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	s.runtimeMu.Lock()
	started, stopped := make(chan error, 1), make(chan struct{})
	go func() { started <- s.Start() }()
	go func() { s.Stop(); close(stopped) }()
	deadline := time.Now().Add(time.Second)
	for !s.ShuttingDown() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	s.runtimeMu.Unlock()
	if err := <-started; !errors.Is(err, net.ErrClosed) {
		t.Fatalf("start during stop = %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop blocked")
	}
	if s.runtimeStarted || s.ticking.Load() {
		t.Fatal("Stop allowed startup")
	}
}

func TestFrontendActionCancellationAfterWaitingForOwner(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	core := frontendCore{s}
	ctx, cancel := context.WithCancel(context.Background())
	s.simulation.mu.Lock()
	done := make(chan error, 1)
	go func() {
		_, err := core.Execute(ctx, frontend.Action{Kind: frontend.Command, Command: "time set 123"})
		done <- err
	}()
	cancel()
	s.simulation.mu.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled action=%v", err)
	}
	if got := s.dayTime(); got != 0 {
		t.Fatalf("canceled command changed time=%d", got)
	}
}
