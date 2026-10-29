package server

import "testing"

func TestPlayerFallDamageAmount(t *testing.T) {
	for _, tc := range []struct{ distance, want float64 }{
		{3, 0}, {3.5, 0}, {9.5, 6}, {4, 1},
		{4 - 0.5e-6, 1}, {4 - 2e-6, 0},
	} {
		if got := playerFallDamageAmount(tc.distance); got != tc.want {
			t.Errorf("fall distance %.9f: got %v want %v", tc.distance, got, tc.want)
		}
	}
}

func TestPlayerFallDamageAttributes(t *testing.T) {
	s := New("unused")
	sess := survivalSession(t, s, "Faller")
	e := s.entities.Spawn(&Entity{})
	sess.entityID.Store(e.ID)
	e.AttrBase = map[string]float64{"safe_fall_distance": 0, "fall_damage_multiplier": 0.5}
	if got := sess.playerFallDamage(2.5); got != 1 {
		t.Fatalf("low safe distance: %v", got)
	}
	e.AttrBase["safe_fall_distance"] = 3
	id, _ := MobEffectID("jump_boost")
	sess.effects = map[int32]mobEffectInst{id: {amplifier: 1}}
	e.AttrMods = map[string]map[string]attrMod{"safe_fall_distance": {"test": {value: 1, op: 2}}}
	s.setPlayerEffectAttributes(sess, id, sess.effects[id])
	if got := sess.playerFallDamage(9.5); got != 0 {
		t.Fatalf("Jump Boost before total multiplier: %v", got)
	}
	delete(e.AttrMods["safe_fall_distance"], "test")
	delete(e.AttrMods["safe_fall_distance"], "minecraft:effect.jump_boost")
	if got := sess.playerFallDamage(9.5); got != 3 {
		t.Fatalf("explicit jump modifier removal ignored: %v", got)
	}
	e.AttrMods["safe_fall_distance"]["minecraft:effect.jump_boost"] = attrMod{value: 6, permanent: true}
	if got := sess.playerFallDamage(9.5); got != 0 {
		t.Fatalf("explicit jump modifier override ignored: %v", got)
	}
}
