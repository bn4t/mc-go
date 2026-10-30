package server

import "testing"

func TestMobKnockbackEffectiveAttributes(t *testing.T) {
	if mobHurtKnockbackStrength != .4000000059604645 {
		t.Fatal("lost measured float32 strength", mobHurtKnockbackStrength)
	}
	s := New("unused")
	t.Cleanup(s.Stop)
	for _, tc := range []struct {
		name string
		want float64
	}{
		{"pig", 0}, {"iron_golem", 1}, {"hoglin", .6000000238418579}, {"ravager", .75}, {"warden", 1}, {"nautilus", .30000001192092896},
	} {
		e := &Entity{Type: mustEntityType(tc.name)}
		if got := s.mobKnockbackResistance(e); got != tc.want {
			t.Fatalf("%s got%g want%g", tc.name, got, tc.want)
		}
	}
	e := &Entity{Type: mustEntityType("pig"), AttrBase: map[string]float64{"knockback_resistance": .5}, AttrMods: map[string]map[string]attrMod{"knockback_resistance": {"test:total": {value: 1, op: 2}}}}
	if got := s.mobKnockbackResistance(e); got != 1 {
		t.Fatal("modifier composition", got)
	}
	e.AttrBase["knockback_resistance"] = -4
	if got := s.mobKnockbackResistance(e); got != -2 {
		t.Fatal("lower bound", got)
	}
}
