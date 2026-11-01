package server

import (
	"math"
	"testing"
)

func TestZombieMeleeUsesMovementAttribute(t *testing.T) {
	s := New("unused")
	target, _ := enforceSession(t, s, "Target")
	target.spawned.Store(true)
	target.setMode(GameModeSurvival)
	target.setPos(0, 65, 20, 0, 0, true)
	e := s.entities.Spawn(&Entity{Type: mustEntityType("zombie"), Dim: dimOverworld, Living: true, X: 0, Y: 65, Z: 0, Health: 20, AttrBase: map[string]float64{"movement_speed": 0.1, "follow_range": 100}})
	if !s.mobMeleeStep(e) {
		t.Fatal("no target")
	}
	if math.Abs(e.WalkSpeed-0.1) > 1e-8 {
		t.Fatalf("base walk speed %v", e.WalkSpeed)
	}
	speed, _ := MobEffectID("speed")
	s.mobGiveEffect(e, speed, 1, 100, false)
	if !s.mobMeleeStep(e) {
		t.Fatal("target lost")
	}
	want := 0.1 * (1 + 0.4000000059604645)
	if math.Abs(e.WalkSpeed-want) > 1e-8 {
		t.Fatalf("effect walk speed %v want %v", e.WalkSpeed, want)
	}
}
