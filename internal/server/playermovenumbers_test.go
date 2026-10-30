package server

import (
	"context"
	"math"
	"mc-go/api/plugin"
	"mc-go/internal/protocol"
	"testing"
)

func playerNumbersPacketForUnit(sess *Session, kind int, position [3]float64, yaw, pitch float32) protocol.Packet {
	var b protocol.Buffer
	id := sess.ids().PlayMovePosRotSB
	if kind == 1 {
		id = sess.ids().PlayMovePosSB
	} else if kind == 2 {
		id = sess.ids().PlayMoveRotSB
	} else if kind == 3 {
		id = sess.ids().PlayMoveStatusSB
	}
	if kind == 0 || kind == 1 {
		for _, value := range position {
			b.Double(value)
		}
	}
	if kind == 0 || kind == 2 {
		b.Float(yaw)
		b.Float(pitch)
	}
	b.Byte(1)
	return protocol.Packet{ID: id, Data: b.Bytes()}
}
func TestPlayerNumbersRawValidationPrecedesHooksAndAuthority(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		kind                   int
		position               [3]float64
		yaw, pitch             float32
		loaded, passenger, ack bool
	}{
		{name: "unready_posrot", position: [3]float64{math.NaN(), 10, .5}},
		{name: "passenger_rotation", kind: 2, pitch: float32(math.Inf(-1)), loaded: true, passenger: true},
		{name: "acknowledged_position", kind: 1, position: [3]float64{.5, 10, math.NaN()}, loaded: true, ack: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := New("unused")
			s.OpsRequired = true
			sess := placeSession(t, s, "RawNumeric", .5, 10, .5)
			sess.loaded.Store(tc.loaded)
			sess.setMode(GameModeSpectator)
			if tc.passenger {
				sess.riding.Store(999)
			}
			if tc.ack {
				sess.teleportAck.Store(sess.teleportNext.Load())
			}
			entity := s.entities.Spawn(&Entity{UUID: sess.getUUID(), Type: sess.ids().EntityTypePlayer, Dim: dimOverworld, X: .5, Y: 10, Z: .5})
			sess.entityID.Store(entity.ID)
			calls := 0
			remove := s.hooks.OnMove(context.Background(), 0, func(context.Context, *plugin.Move) bool { calls++; return true })
			defer remove()
			sess.setKnownMovement([3]float64{7, 8, 9})
			s.handleClientTickEnd(sess, protocol.Packet{})
			teleports := sess.teleportNext.Load()
			s.handleMove(sess, playerNumbersPacketForUnit(sess, tc.kind, tc.position, tc.yaw, tc.pitch))
			x, y, z, _, _, _ := sess.Pos()
			if !sess.closing.Load() || calls != 0 {
				t.Fatalf("invalidrawinput closing%v hookcalls%d", sess.closing.Load(), calls)
			}
			if x != .5 || y != 10 || z != .5 || entity.X != .5 || entity.Y != 10 || entity.Z != .5 || sess.teleportNext.Load() != teleports {
				t.Fatal("invalidrawinput changed player/entity/correction state")
			}
			if sess.lastDelta != ([3]float64{7, 8, 9}) || sess.lastHorizSq != 130 || sess.receivedMovementThisTick {
				t.Fatal("invalidrawinput changed known movement")
			}
		})
	}
}
func TestPlayerNumbersKeepSharedHookResultValidation(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "HookNumeric", .5, 10, .5)
	entity := s.entities.Spawn(&Entity{UUID: sess.getUUID(), Type: sess.ids().EntityTypePlayer, Dim: dimOverworld, X: .5, Y: 10, Z: .5})
	sess.entityID.Store(entity.ID)
	calls := 0
	inject := true
	remove := s.hooks.OnMove(context.Background(), 0, func(_ context.Context, move *plugin.Move) bool {
		calls++
		if inject {
			move.To.X = math.NaN()
		} else if math.IsInf(move.To.X, 0) {
			move.To.X = 1.5
		}
		return true
	})
	defer remove()
	s.handleMove(sess, playerNumbersPacketForUnit(sess, 0, [3]float64{1, 10, .5}, 0, 0))
	x, _, _, _, _, _ := sess.Pos()
	if sess.closing.Load() || calls != 1 || x != .5 || entity.X != .5 {
		t.Fatal("plugin-produced invalid destination changed existing correction policy")
	}
	inject = false
	s.handleMove(sess, playerNumbersPacketForUnit(sess, 1, [3]float64{math.Inf(1), 10, .5}, 0, 0))
	x, _, _, _, _, _ = sess.Pos()
	if sess.closing.Load() || calls != 2 || x != 1.5 || entity.X != 1.5 {
		t.Fatal("coordinate infinity/plugin retargeting no longer follows shared validation")
	}
}
func TestPlayerNumbersOmittedFieldsUseZero(t *testing.T) {
	if invalidPlayerPacketNumbers(false, false, math.NaN(), math.NaN(), math.NaN(), float32(math.Inf(1)), float32(math.NaN())) {
		t.Fatal("absent packet fields used retained pose for numeric validation")
	}
	if invalidPlayerPacketNumbers(true, false, 1, 2, 3, float32(math.NaN()), float32(math.Inf(1))) {
		t.Fatal("position-only packet validated omitted rotation")
	}
	if invalidPlayerPacketNumbers(false, true, math.NaN(), 0, 0, 0, 0) {
		t.Fatal("rotation-only packet validated omitted position")
	}
}
