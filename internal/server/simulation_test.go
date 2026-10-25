package server

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"mc-go/api/frontend"
	"mc-go/api/plugin"
	"mc-go/internal/event"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func simulationTestServer(t *testing.T) *Server {
	t.Helper()
	s := New("unused")
	// Ownership tests do not exercise lighting; avoid materializing the
	// skylight flood margin for every small fixture, especially under -race.
	s.ViewDistance = 1
	s.dims[dimOverworld].skylight = false
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	return s
}

func TestSimulationDeferredActionsAreBoundedAndRunAfterCommit(t *testing.T) {
	s := simulationTestServer(t)
	var order []int
	s.simulationAction(func() {
		order = append(order, 1)
		if !s.queueSimulationAction(func() { order = append(order, 3) }) {
			t.Fatal("hook request rejected")
		}
		order = append(order, 2)
	})
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("commit/request order = %v", order)
	}
	for i := 0; i < maxPendingSimulationActions; i++ {
		if !s.queueSimulationAction(func() {}) {
			t.Fatalf("request %d rejected before capacity", i)
		}
	}
	if s.queueSimulationAction(func() {}) {
		t.Fatal("full queue accepted an action")
	}
	s.shuttingDown.Store(true)
	s.discardSimulationActions()
	if s.queueSimulationAction(func() {}) {
		t.Fatal("shutdown accepted an action")
	}
	if len(s.simulation.pending) != 0 {
		t.Fatal("shutdown retained actions")
	}
}

func TestSimulationPlayerInitializationAndTicksShareOwner(t *testing.T) {
	s := simulationTestServer(t)
	sess := placeSession(t, s, "Owned", .5, 10, .5)
	var workers sync.WaitGroup
	for worker := 0; worker < 3; worker++ {
		workers.Add(1)
		go func(kind int) {
			defer workers.Done()
			for i := 0; i < 20; i++ {
				switch kind {
				case 0:
					s.simulationAction(sess.initSurvival)
				case 1:
					s.playerTick()
				case 2:
					s.GameTick()
				}
			}
		}(worker)
	}
	workers.Wait()
	if s.TickCount() != 20 {
		t.Fatalf("game ticks = %d, want 20", s.TickCount())
	}
}

func TestSimulationCommandStepAndSprintStopDoNotReenterOwner(t *testing.T) {
	s := simulationTestServer(t)
	s.SetFrozen(true)
	if out := s.rconRun("tick step 3"); !strings.Contains(out, "3") {
		t.Fatalf("step reply = %q", out)
	}
	if s.TickCount() != 3 {
		t.Fatalf("step completed %d ticks, want 3", s.TickCount())
	}
	ticks, unsub := s.bus.Subscribe(event.TopicTick, 8)
	defer unsub()
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.rconRun("tick sprint 1000000")
	}()
	select {
	case <-ticks:
	case <-time.After(5 * time.Second):
		t.Fatal("sprint failed to advance")
	}
	stopped := make(chan string, 1)
	go func() { stopped <- s.rconRun("tick sprint stop") }()
	select {
	case out := <-stopped:
		if strings.Contains(out, "No tick sprint") {
			t.Fatalf("sprint already finished: %s", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop command deadlocked with sprint owner")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sprint did not finish after stop")
	}
	if !s.clock.isFrozen() || s.clock.isSprinting() {
		t.Fatal("sprint stop did not restore frozen clock")
	}
}

func TestSimulationMoveHooksCommitCanonicalValidatedDestination(t *testing.T) {
	for _, tc := range []struct {
		name     string
		hook     plugin.MoveHandler
		wantX    float64
		accepted bool
	}{
		{"cancel", func(context.Context, *plugin.Move) bool { return false }, .5, false},
		{"modify", func(_ context.Context, m *plugin.Move) bool { m.To.X = 2.5; return true }, 2.5, true},
		{"invalid", func(_ context.Context, m *plugin.Move) bool { m.To.X = math.NaN(); return true }, .5, false},
		{"border", func(_ context.Context, m *plugin.Move) bool { m.To.X = 30000000; return true }, .5, false},
		{"collision", func(_ context.Context, m *plugin.Move) bool { m.To.X = 4.5; return true }, .5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := simulationTestServer(t)
			sess := placeSession(t, s, "Mover", .5, 10, .5)
			e := s.entities.Spawn(&Entity{UUID: sess.getUUID(), Type: sess.ids().EntityTypePlayer, Dim: dimOverworld, X: .5, Y: 10, Z: .5})
			sess.entityID.Store(e.ID)
			s.world.SetBlock(world.Pos{X: 4, Y: 10, Z: 0}, world.Stone)
			moves, unsub := s.bus.Subscribe(event.TopicPlayerMove, 8)
			defer unsub()
			remove := s.hooks.OnMove(context.Background(), 0, tc.hook)
			defer remove()
			var accepted bool
			s.simulationAction(func() {
				accepted = s.applyPlayerMoveOwned(sess, frontend.Movement{X: 1.5, Y: 10, Z: .5})
			})
			if accepted != tc.accepted {
				t.Fatalf("accepted = %v, want %v", accepted, tc.accepted)
			}
			x, _, _, _, _, _ := sess.Pos()
			if x != tc.wantX {
				t.Fatalf("session X = %v, want %v", x, tc.wantX)
			}
			entity, _ := s.entities.Get(e.ID)
			if entity.X != tc.wantX {
				t.Fatalf("entity X = %v, want %v", entity.X, tc.wantX)
			}
			select {
			case observed := <-moves:
				move := observed.Data.(event.PlayerMove)
				if !accepted || move.X != tc.wantX {
					t.Fatalf("observer saw noncanonical move: %+v", move)
				}
			default:
				if accepted {
					t.Fatal("accepted move not observed")
				}
			}
			// Both rejection and a changed destination correct the mover's client.
			found := false
			for len(sess.sendCh) > 0 {
				if pkt := materializeOutboundPacket(t, sess, <-sess.sendCh); pkt.id == sess.ids().PlaySyncPos {
					found = true
				}
			}
			if !found {
				t.Fatal("client received no authoritative correction")
			}
		})
	}
}

// Exercise the actual connection reader and disconnect path alongside external
// gameplay requests and the runtime tick, rather than only calling owned helpers.
func TestSimulationNetworkMovementTeleportDropAndLeaveShareOwner(t *testing.T) {
	s := simulationTestServer(t)
	s.TimeSyncEvery = -1
	stopTicks := s.startRuntimeTicks()
	t.Cleanup(stopTicks)
	client, reader, writer := joinPlay(t, s, "Concurrent")
	drainJoinBurst(t, reader)
	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			if _, err := reader.ReadPacket(); err != nil {
				return
			}
		}
	}()
	defer func() { _ = client.Close(); <-readDone }()
	sess := s.findSessionByName("Concurrent")
	if sess == nil {
		t.Fatal("joined player missing")
	}
	s.simulationAction(func() {
		sess.setStack(invSlotForHotbar(0), ItemStack{ID: world.ItemIDs["stone"], Count: 32})
	})
	left, unsubscribe := s.bus.Subscribe(event.TopicPlayerLeave, 1)
	defer unsubscribe()
	errors := make(chan error, 2)
	go func() {
		for i := 0; i < 16; i++ {
			var move protocol.Buffer
			move.Double(.5 + float64(i%2)/10)
			move.Double(10)
			move.Double(.5)
			move.Byte(0)
			if err := writer.WritePacket(IDPlayMovePosSB, move.Bytes()); err != nil {
				errors <- err
				return
			}
			drop := actionPacket(t, actionDropItem)
			if err := writer.WritePacket(drop.ID, drop.Data); err != nil {
				errors <- err
				return
			}
		}
		errors <- nil
	}()
	go func() {
		for i := 0; i < 16; i++ {
			if !s.TeleportPlayer("Concurrent", .5, 10, .5) {
				errors <- fmt.Errorf("teleport %d failed", i)
				return
			}
		}
		errors <- nil
	}()
	for i := 0; i < 2; i++ {
		select {
		case err := <-errors:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent movement/drop/teleport stalled")
		}
	}
	_ = client.Close()
	select {
	case <-left:
	case <-time.After(5 * time.Second):
		t.Fatal("disconnect failed to leave simulation")
	}
	stopTicks()
	s.simulationAction(func() {
		if s.findSessionByName("Concurrent") != nil {
			t.Error("disconnected player still tracked")
		}
		if got := sess.stackAt(invSlotForHotbar(0)).Count; got != 16 {
			t.Errorf("remaining held items = %d, want 16 after wire drops", got)
		}
	})
}

func TestSimulationMoveHookCanDeferPluginTeleport(t *testing.T) {
	s := simulationTestServer(t)
	sess := placeSession(t, s, "Hooked", .5, 10, .5)
	acceptedRequest := false
	remove := s.hooks.OnMove(context.Background(), 0, func(_ context.Context, move *plugin.Move) bool {
		acceptedRequest = (serverAPI{s}).Teleport(move.Player, 8.5, 10, .5)
		return true
	})
	defer remove()
	s.simulationAction(func() {
		if !s.applyPlayerMoveOwned(sess, frontend.Movement{X: 1.5, Y: 10, Z: .5}) {
			t.Fatal("move rejected")
		}
		x, _, _, _, _, _ := sess.Pos()
		if x != 1.5 {
			t.Fatalf("deferred teleport interrupted movement commit: X=%v", x)
		}
	})
	if !acceptedRequest {
		t.Fatal("plugin teleport request rejected")
	}
	x, _, _, _, _, _ := sess.Pos()
	if x != 8.5 {
		t.Fatalf("deferred teleport failed: X=%v", x)
	}
}

func TestSimulationShutdownInterruptsLongTickCommands(t *testing.T) {
	for _, command := range []string{"tick step 2147483647", "tick sprint 2147483647"} {
		t.Run(command, func(t *testing.T) {
			s := New("unused")
			t.Cleanup(s.Stop)
			s.SetFrozen(true)
			ticks, unsubscribe := s.bus.Subscribe(event.TopicTick, 1)
			defer unsubscribe()
			commandDone := make(chan struct{})
			go func() { defer close(commandDone); s.rconRun(command) }()
			select {
			case <-ticks:
			case <-time.After(5 * time.Second):
				t.Fatal("long command failed to begin")
			}
			stopped := make(chan struct{})
			go func() { defer close(stopped); s.Stop() }()
			select {
			case <-stopped:
			case <-time.After(5 * time.Second):
				t.Fatal("shutdown waited for huge tick count")
			}
			select {
			case <-commandDone:
			case <-time.After(5 * time.Second):
				t.Fatal("tick command kept running after shutdown")
			}
			count := s.TickCount()
			s.GameTick()
			if s.TickCount() != count {
				t.Fatal("game tick advanced after shutdown")
			}
		})
	}
}

// A second authenticated RCON connection must stop an in-flight step through
// normal command ingress, without waiting for its enormous requested count.
func TestSimulationRCONStepStopInterleavesAtTickBoundary(t *testing.T) {
	s := simulationTestServer(t)
	defer s.Stop()
	s.RconPassword = "step-test"
	s.SetFrozen(true)
	stepping, rcon := simulationRCONClient(t, s)
	stopping, _ := simulationRCONClient(t, s)
	ticks, unsubscribe := s.bus.Subscribe(event.TopicTick, 1)
	defer unsubscribe()
	done := make(chan error, 1)
	go func() {
		rcon.write(stepping, 2, rconTypeCommand, "tick step 2147483647")
		_, err := readSimulationRCON(stepping)
		done <- err
	}()
	select {
	case <-ticks:
	case <-time.After(3 * time.Second):
		t.Fatal("step did not begin")
	}
	rcon.write(stopping, 3, rconTypeCommand, "tick step stop")
	if reply, err := readSimulationRCON(stopping); err != nil || strings.Contains(reply, "No tick") {
		t.Fatalf("step stop reply = %q, error %v", reply, err)
	}
	stoppedAt := s.TickCount()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("step command did not stop at its next boundary")
	}
	if count := s.TickCount(); count != stoppedAt || count < 1 || count >= 2147483647 {
		t.Fatalf("step count after stop = %d, at stop reply = %d", count, stoppedAt)
	}
	if !s.clock.isFrozen() {
		t.Fatal("step stop unfroze the clock")
	}
}

func readSimulationRCON(conn net.Conn) (string, error) {
	var header [4]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return "", err
	}
	size := binary.LittleEndian.Uint32(header[:])
	if size < 10 || size > 8192 {
		return "", fmt.Errorf("invalid RCON response length %d", size)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(conn, body); err != nil {
		return "", err
	}
	return string(body[8 : len(body)-2]), nil
}

func simulationRCONClient(t *testing.T, s *Server) (net.Conn, *rconListener) {
	t.Helper()
	rcon := &rconListener{srv: s}
	client, conn := net.Pipe()
	done := make(chan struct{})
	go func() { defer close(done); rcon.serve(conn) }()
	t.Cleanup(func() { _ = client.Close(); <-done })
	_ = client.SetDeadline(time.Now().Add(5 * time.Second))
	rcon.write(client, 1, rconTypeAuth, s.RconPassword)
	if _, err := readSimulationRCON(client); err != nil {
		t.Fatal(err)
	}
	return client, rcon
}

func TestSimulationRCONFreezeCancelsSprintWaitingForStep(t *testing.T) {
	s := simulationTestServer(t)
	defer s.Stop()
	s.RconPassword = "mixed-drivers"
	s.SetFrozen(true)
	stepping, rcon := simulationRCONClient(t, s)
	sprinting, _ := simulationRCONClient(t, s)
	freezing, _ := simulationRCONClient(t, s)
	ticks, unsubscribe := s.bus.Subscribe(event.TopicTick, 1)
	defer unsubscribe()
	stepDone, sprintDone := make(chan error, 1), make(chan error, 1)
	go func() {
		rcon.write(stepping, 2, rconTypeCommand, "tick step 2147483647")
		_, err := readSimulationRCON(stepping)
		stepDone <- err
	}()
	select {
	case <-ticks:
	case <-time.After(3 * time.Second):
		t.Fatal("step did not begin")
	}
	go func() {
		rcon.write(sprinting, 3, rconTypeCommand, "tick sprint 2147483647")
		_, err := readSimulationRCON(sprinting)
		sprintDone <- err
	}()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for !s.clock.isSprinting() {
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatal("sprint request did not queue behind step")
		}
	}
	rcon.write(freezing, 4, rconTypeCommand, "tick freeze")
	if reply, err := readSimulationRCON(freezing); err != nil {
		t.Fatalf("freeze reply=%q, error %v", reply, err)
	}
	for name, done := range map[string]<-chan error{"step": stepDone, "sprint": sprintDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s reply: %v", name, err)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s did not stop after freeze", name)
		}
	}
	if !s.clock.isFrozen() || s.clock.isSprinting() {
		t.Fatal("freeze failed to stop both tick drivers")
	}
	count := s.TickCount()
	if count < 1 || count >= 2147483647 {
		t.Fatalf("freeze completed huge step: count=%d", count)
	}
}
