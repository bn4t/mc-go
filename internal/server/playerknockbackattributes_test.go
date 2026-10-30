package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestPlayerKnockbackCanonicalAttributes(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	player := s.entities.Spawn(&Entity{Type: mustEntityType("player")})
	sess := &Session{server: s}
	sess.entityID.Store(player.ID)
	id, _ := world.ItemID("netherite_boots")
	sess.inv[slotArmorStart+3] = ItemStack{ID: id, Count: 1}
	s.updatePlayerArmorAttributes(sess)
	for _, tc := range []struct {
		name              string
		base, extra, want float64
	}{
		{"gear", 0, 0, .10000000149011612},
		{"base", .5, 0, .6000000014901161},
		{"minimum", -4, 0, -2},
		{"maximum", 2, 0, 1},
		{"modifier", 0, -1, -.8999999985098839},
		{"nan", math.NaN(), 0, -2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if player.AttrBase == nil {
				player.AttrBase = map[string]float64{}
			}
			player.AttrBase["knockback_resistance"] = tc.base
			player.AttrMods["knockback_resistance"]["test:kb"] = attrMod{value: tc.extra}
			if got := sess.armorKBResist(); got != tc.want {
				t.Fatalf("got %.17g want %.17g", got, tc.want)
			}
		})
	}
	player.AttrBase["knockback_resistance"] = 0
	delete(player.AttrMods["knockback_resistance"], "test:kb")
	delete(player.AttrMods["knockback_resistance"], "minecraft:armor.boots")
	s.updatePlayerArmorAttributes(sess)
	if got := sess.armorKBResist(); got != 0 {
		t.Fatal("unchanged boots restored removed modifier", got)
	}
}
