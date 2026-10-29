package server

import (
	"math"
	"testing"

	"mc-go/internal/world"
)

func TestPlayerHeldAttackAttributeComposition(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	player := s.entities.Spawn(&Entity{Type: mustEntityType("player")})
	sess := &Session{server: s}
	sess.entityID.Store(player.ID)
	cases := []struct {
		name   string
		base   float64
		weapon string
		mods   map[string]attrMod
		want   float32
	}{
		{"base", 10, "air", nil, 10},
		{"high base", 100, "air", nil, 100},
		{"weapon adds", 10, "stone_sword", nil, 14},
		{"copper sword", 10, "copper_sword", nil, 14},
		{"copper axe", 10, "copper_axe", nil, 18},
		{"zero weapon modifier", 10, "netherite_hoe", nil, 10},
		{"addition", 10, "air", map[string]attrMod{"test:add": {value: 2}}, 12},
		{"base multiplier", 10, "air", map[string]attrMod{"test:add": {value: 2}, "test:base": {value: .5, op: 1}}, 18},
		{"total multiplier", 10, "air", map[string]attrMod{"test:add": {value: 2}, "test:base": {value: .5, op: 1}, "test:total": {value: 1, op: 2}}, 36},
		{"weapon before multipliers", 10, "stone_sword", map[string]attrMod{"test:add": {value: 2}, "test:base": {value: .5, op: 1}, "test:total": {value: 1, op: 2}}, 48},
		{"minimum", -10, "air", nil, 0},
		{"maximum", 4096, "air", nil, 2048},
		{"nan minimum", math.NaN(), "air", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			player.AttrBase = map[string]float64{"attack_damage": tc.base}
			player.AttrMods = map[string]map[string]attrMod{"attack_damage": tc.mods}
			sess.inv[slotHotbarStart] = ItemStack{}
			if tc.weapon != "air" {
				id, _ := world.ItemID(tc.weapon)
				sess.inv[slotHotbarStart] = ItemStack{ID: id, Count: 1}
			}
			before := len(tc.mods)
			if got := sess.heldAttackDamage(); got != tc.want {
				t.Fatalf("damage %v, want %v", got, tc.want)
			}
			if len(tc.mods) != before {
				t.Fatal("equipment mutated stored modifier map")
			}
		})
	}
	fallback := &Session{}
	sword, _ := world.ItemID("iron_sword")
	fallback.inv[slotHotbarStart] = ItemStack{ID: sword, Count: 1}
	if got := fallback.heldAttackDamage(); got != 6 {
		t.Fatal("detached session weapon fallback", got)
	}
}
