package server

import (
	"math"
	"testing"

	"mc-go/internal/world"
)

func TestPlayerAttackSpeedAttributeComposition(t *testing.T) {
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
		{"zero unarmed", 0, "air", nil, float32(math.Inf(1))},
		{"zero sword", 0, "iron_sword", nil, float32(math.Inf(1))},
		{"negative additive result", 4, "iron_sword", map[string]attrMod{"test:add": {value: -2}}, float32(math.Inf(1))},
		{"zero total result", 4, "iron_sword", map[string]attrMod{"test:total": {value: -1, op: 2}}, float32(math.Inf(1))},
		{"positive override", 8, "iron_sword", nil, float32(1 / (8 - 2.4000000953674316) * 20)},
		{"weapon inside all passes", 4, "iron_sword", map[string]attrMod{"test:add": {value: 2}, "test:base": {value: .5, op: 1}, "test:total": {value: 1, op: 2}}, float32(1 / 10.799999713897705 * 20)},
		{"maximum speed", 2048, "air", nil, .01953125},
		{"nan minimum", math.NaN(), "air", nil, float32(math.Inf(1))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			player.AttrBase = map[string]float64{"attack_speed": tc.base}
			player.AttrMods = map[string]map[string]attrMod{"attack_speed": tc.mods}
			sess.inv[slotHotbarStart] = ItemStack{}
			if tc.weapon != "air" {
				id, _ := world.ItemID(tc.weapon)
				sess.inv[slotHotbarStart] = ItemStack{ID: id, Count: 1}
			}
			before := len(tc.mods)
			if got := sess.heldAttackDelayTicks(); got != tc.want {
				t.Fatalf("delay %v, want %v", got, tc.want)
			}
			if len(tc.mods) != before {
				t.Fatal("weapon changed stored modifiers")
			}
		})
	}
}

func TestPlayerDefaultGeneratedWeaponDelaysUnchanged(t *testing.T) {
	sess := &Session{}
	for name, modifier := range itemAttackSpeedMod {
		id, ok := world.ItemID(name)
		if !ok {
			t.Fatal("unknown generated item", name)
		}
		sess.inv[slotHotbarStart] = ItemStack{ID: id, Count: 1}
		previous := float32(20) / float32(4+modifier)
		if got := sess.heldAttackDelayTicks(); got != previous {
			t.Fatalf("%s delay %v, previous %v", name, got, previous)
		}
	}
}
