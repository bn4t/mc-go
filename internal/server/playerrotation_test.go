package server

import (
	"context"
	"math"
	"mc-go/api/plugin"
	"testing"
)

func TestJavaPlayerRotation(t *testing.T) {
	for _, row := range [][4]float32{{450, 100, 90, 90}, {-450, -100, -90, -90}, {180, 180, -180, -90}, {-540, -270, -180, 90}, {720.5, 91, .5, 90}, {math.MaxFloat32, -math.MaxFloat32, 0, 0}, {16777216, -16777216, 136, -90}} {
		y, p := javaPlayerRotation(row[0], row[1])
		if y != row[2] || p != row[3] {
			t.Fatalf("%v => %g,%g", row, y, p)
		}
	}
}

func TestJavaPlayerRotationBeforeHookKeepsReplacement(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "RotationHook", .5, 10, .5)
	called := false
	remove := s.hooks.OnMove(context.Background(), 0, func(_ context.Context, m *plugin.Move) bool {
		called = true
		if m.To.Yaw != 90 || m.To.Pitch != 90 {
			t.Errorf("hook proposal %g,%g", m.To.Yaw, m.To.Pitch)
		}
		m.To.Yaw, m.To.Pitch = 720, 120
		return true
	})
	defer remove()
	s.handleMove(sess, playerNumbersPacketForUnit(sess, 2, [3]float64{}, 450, 100))
	_, _, _, yaw, pitch, _ := sess.Pos()
	if !called || yaw != 720 || pitch != 120 {
		t.Fatalf("hook result called%v yaw%g pitch%g", called, yaw, pitch)
	}
}
