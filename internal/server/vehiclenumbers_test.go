package server

import (
	"math"
	"mc-go/internal/protocol"
	"reflect"
	"testing"
)

func vehicleNumbersPacketForUnit(position [3]float64, yaw, pitch float32) protocol.Packet {
	var b protocol.Buffer
	for _, value := range position {
		b.Double(value)
	}
	b.Float(yaw)
	b.Float(pitch)
	b.Bool(true)
	return protocol.Packet{Data: b.Bytes()}
}
func TestVehicleNumbersRejectBeforeAuthorityAndMutation(t *testing.T) {
	invalid := []struct {
		label      string
		pos        [3]float64
		yaw, pitch float32
	}{
		{label: "x", pos: [3]float64{math.NaN(), 10, .5}}, {label: "y", pos: [3]float64{.5, math.NaN(), .5}}, {label: "z", pos: [3]float64{.5, 10, math.NaN()}},
		{label: "yaw_nan", pos: [3]float64{.5, 10, .5}, yaw: float32(math.NaN())}, {label: "pitch_nan", pos: [3]float64{.5, 10, .5}, pitch: float32(math.NaN())},
		{label: "yaw_inf", pos: [3]float64{.5, 10, .5}, yaw: float32(math.Inf(1))}, {label: "pitch_inf", pos: [3]float64{.5, 10, .5}, pitch: float32(math.Inf(-1))},
	}
	for _, mode := range []string{"unmounted", "driver", "non_driver"} {
		for _, tc := range invalid {
			t.Run(mode+"/"+tc.label, func(t *testing.T) {
				s := New("unused")
				s.OpsRequired = true
				// This validation precedes world/authority reads; keep the
				// mutation fixture independent of chunk streaming.
				sess := &Session{server: s, sendCh: make(chan sendPkt, 64), done: make(chan struct{}), ver: LatestVersion()}
				sess.state.Store(StatePlay)
				sess.loaded.Store(true)
				sess.setPos(.5, 10, .5, 0, 0, true)
				sess.entityID.Store(901)
				typ, _ := EntityTypeID("oak_boat")
				passenger := int32(901)
				if mode == "non_driver" {
					passenger = 902
				}
				boat := s.entities.Spawn(&Entity{Type: typ, Dim: dimOverworld, X: .5, Y: 10, Z: .5, VehAnchor: [3]float64{.5, 10, .5}, Passengers: []int32{passenger}})
				if mode != "unmounted" {
					sess.riding.Store(boat.ID)
				}
				sess.setKnownMovement([3]float64{7, 8, 9})
				s.handleClientTickEnd(sess, protocol.Packet{})
				before := []float64{boat.X, boat.Y, boat.Z, float64(boat.Yaw), float64(boat.Pitch), boat.MotionX, boat.MotionY, boat.MotionZ, boat.VehAnchor[0], boat.VehAnchor[1], boat.VehAnchor[2]}
				dirty, sync := boat.PosDirty, boat.NeedsSync
				s.handleMoveVehicle(sess, vehicleNumbersPacketForUnit(tc.pos, tc.yaw, tc.pitch))
				after := []float64{boat.X, boat.Y, boat.Z, float64(boat.Yaw), float64(boat.Pitch), boat.MotionX, boat.MotionY, boat.MotionZ, boat.VehAnchor[0], boat.VehAnchor[1], boat.VehAnchor[2]}
				if !sess.closing.Load() {
					t.Fatal("invalid numeric packet did not close session")
				}
				if !reflect.DeepEqual(after, before) || boat.PosDirty != dirty || boat.NeedsSync != sync {
					t.Fatalf("invalid input mutated vehicle%v ->%v", before, after)
				}
				if sess.lastDelta != ([3]float64{7, 8, 9}) || sess.lastHorizSq != 130 || sess.receivedMovementThisTick {
					t.Fatal("invalid input mutated known movement")
				}
			})
		}
	}
}
func TestVehicleNumbersInfinityAndFiniteMovement(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "FiniteDriver", .5, 10, .5)
	sess.entityID.Store(901)
	typ, _ := EntityTypeID("oak_boat")
	boat := s.entities.Spawn(&Entity{Type: typ, Dim: dimOverworld, X: .5, Y: 10, Z: .5, VehAnchor: [3]float64{.5, 10, .5}, Passengers: []int32{901}})
	sess.riding.Store(boat.ID)
	for _, position := range [][3]float64{{math.Inf(1), 10, .5}, {.5, math.Inf(-1), .5}, {.5, 10, math.Inf(1)}, {math.MaxFloat64, 10, .5}} {
		s.handleMoveVehicle(sess, vehicleNumbersPacketForUnit(position, 0, 0))
		if sess.closing.Load() || boat.X != .5 || boat.Y != 10 || boat.Z != .5 || sess.receivedMovementThisTick {
			t.Fatal("position infinity/large finite lost clamp-and-reject behavior")
		}
	}
	s.handleMoveVehicle(sess, vehicleNumbersPacketForUnit([3]float64{1, 10, .5}, math.MaxFloat32, -math.MaxFloat32))
	if sess.closing.Load() || boat.X != 1 || sess.lastDelta != ([3]float64{.5, 0, 0}) || !sess.receivedMovementThisTick || math.IsNaN(float64(boat.Yaw)) || math.IsNaN(float64(boat.Pitch)) {
		t.Fatal("finite accepted movement regressed")
	}
}
