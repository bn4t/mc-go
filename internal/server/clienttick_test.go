package server

import (
	"context"
	"mc-go/api/frontend"
	"mc-go/api/plugin"
	"mc-go/internal/protocol"
	"testing"
)

func TestClientTickEndAcceptedMovementAndPluginGate(t *testing.T) {
	s := simulationTestServer(t)
	sess := placeSession(t, s, "Frame", .5, 10, .5)
	s.simulationAction(func() {
		if !s.applyPlayerMoveOwned(sess, frontend.Movement{X: 1, Y: 10, Z: .5, OnGround: true}) {
			t.Fatal("move rejected")
		}
		s.handleClientTickEnd(sess, protocol.Packet{})
		if sess.lastDelta != ([3]float64{.5, 0, 0}) || sess.lastHorizSq != .25 || sess.receivedMovementThisTick {
			t.Fatal("first frame lost movement", sess.lastDelta, sess.lastHorizSq)
		}
	})
	remove := s.hooks.OnMove(context.Background(), 0, func(context.Context, *plugin.Move) bool { return false })
	s.simulationAction(func() {
		if s.applyPlayerMoveOwned(sess, frontend.Movement{X: 2, Y: 10, Z: .5}) {
			t.Fatal("plugin cancellation ignored")
		}
		if sess.receivedMovementThisTick {
			t.Fatal("denied move counted as received")
		}
		s.handleMove(sess, protocol.Packet{ID: IDPlayMovePosSB, Data: []byte{1}})
		if sess.receivedMovementThisTick {
			t.Fatal("malformed move counted as received")
		}
		s.handleClientTickEnd(sess, protocol.Packet{})
		if sess.lastDelta != ([3]float64{}) || sess.lastHorizSq != 0 {
			t.Fatal("empty frame retained stale movement")
		}
	})
	remove()
}

func TestClientTickEndAcceptedVehicleAndRejectedDriver(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Driver", .5, 10, .5)
	sess.entityID.Store(901)
	typ, ok := EntityTypeID("oak_boat")
	if !ok {
		t.Fatal("boat registry")
	}
	boat := s.entities.Spawn(&Entity{Type: typ, Dim: dimOverworld, X: .5, Y: 10, Z: .5, VehAnchor: [3]float64{.5, 10, .5}, Passengers: []int32{901}})
	sess.riding.Store(boat.ID)
	packet := func(x float64) protocol.Packet {
		var b protocol.Buffer
		b.Double(x)
		b.Double(10)
		b.Double(.5)
		b.Float(0)
		b.Float(0)
		b.Bool(true)
		return protocol.Packet{Data: b.Bytes()}
	}
	s.handleMoveVehicle(sess, packet(1))
	if sess.lastDelta != ([3]float64{.5, 0, 0}) || !sess.receivedMovementThisTick {
		t.Fatal("vehicle movement absent", sess.lastDelta)
	}
	s.handleClientTickEnd(sess, protocol.Packet{})
	s.handleMoveVehicle(sess, packet(100))
	if sess.receivedMovementThisTick {
		t.Fatal("speed rejection counted as movement")
	}
	boat.Passengers = []int32{902}
	s.handleMoveVehicle(sess, packet(1.5))
	if sess.receivedMovementThisTick {
		t.Fatal("non-driver counted as movement")
	}
	s.handleClientTickEnd(sess, protocol.Packet{})
	if sess.lastDelta != ([3]float64{}) || sess.lastHorizSq != 0 {
		t.Fatal("empty vehicle frame retained stale movement")
	}
}
