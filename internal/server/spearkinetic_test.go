package server

import (
	"math"
	"testing"
)

func TestKineticConditionsNativeRoles(t *testing.T) {
	info := kineticWeaponMetadata["wooden_spear"]
	if !kineticConditionPass(info.damage, 0, 4.8, 4.8) || kineticConditionPass(info.knockback, 0, 4.8, 4.8) {
		t.Fatal("damage-only speed condition")
	}
	if !kineticConditionPass(info.damage, 0, 6, 6) || !kineticConditionPass(info.knockback, 0, 6, 6) || kineticConditionPass(info.dismount, 0, 6, 6) {
		t.Fatal("independent condition roles")
	}
	if !kineticConditionPass(info.damage, 300, 6, 6) || kineticConditionPass(info.damage, 301, 6, 6) {
		t.Fatal("condition deadline")
	}
	if kineticConditionPass(info.damage, 0, 6, 0) {
		t.Fatal("relative speed ignored")
	}
	if damage := kineticDamage(1, 10, info.damageMultiplier); damage != 7 {
		t.Fatal("promoted multiplier floor", damage)
	}
	if damage := kineticDamage(10, 6, info.damageMultiplier); damage != 14 {
		t.Fatal("canonical base", damage)
	}
}

func TestSpearNativeAirborneMotion(t *testing.T) {
	for _, tc := range []struct {
		resistance   float64
		motion, want [3]float64
	}{
		{0, [3]float64{}, [3]float64{0, 0, .4000000059604645}},
		{.5, [3]float64{}, [3]float64{0, 0, .20000000298023224}},
		{1, [3]float64{.1, .2, .3}, [3]float64{.1, .2, .3}},
		{-2, [3]float64{}, [3]float64{0, 0, 1.2000000178813934}},
		{0, [3]float64{.1, .2, 0}, [3]float64{.05, .2, .4000000059604645}},
		{0, [3]float64{0, 0, .3}, [3]float64{0, 0, .5500000059604645}},
	} {
		got := spearKnockbackMotion(tc.motion, tc.resistance, 0, 1, false)
		for axis, want := range tc.want {
			if math.IsNaN(got[axis]) || math.IsInf(got[axis], 0) || math.Abs(got[axis]-want) > 1e-12 {
				t.Fatal("spear motion", got, tc.want)
			}
		}
	}
}

func TestSpearNativeGroundedMotion(t *testing.T) {
	for _, motion := range [][3]float64{{}, {.1, .2, 0}} {
		got := spearKnockbackMotion(motion, 0, 0, 1, true)
		want := [3]float64{motion[0] / 2, .4, .4000000059604645}
		for axis := range got {
			if math.IsNaN(got[axis]) || math.Abs(got[axis]-want[axis]) > 1e-12 {
				t.Fatal(got, want)
			}
		}
	}
}
func TestSpearRawAttackBase(t *testing.T) {
	sess := &Session{}
	if got := sess.kineticAttackBase(); got != 1 {
		t.Fatal(got)
	}
	s := New("unused")
	t.Cleanup(s.Stop)
	player := s.entities.Spawn(&Entity{Type: mustEntityType("player")})
	sess.server = s
	sess.entityID.Store(player.ID)
	player.AttrBase = map[string]float64{"attack_damage": 10}
	player.AttrMods = map[string]map[string]attrMod{"attack_damage": {
		"test:add": {value: 2}, "test:base": {value: .5, op: 1}, "test:total": {value: .5, op: 2},
	}}
	if got := sess.heldAttackDamage(); got != 27 {
		t.Fatal("effective preparation", got)
	}
	if got := sess.kineticAttackBase(); got != 10 {
		t.Fatal("kinetic raw base", got)
	}
}
