package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestPlayerEquipmentAttributeRemovalLifecycle(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	player := s.entities.Spawn(&Entity{Type: mustEntityType("player")})
	sess := &Session{server: s}
	sess.entityID.Store(player.ID)
	sword, _ := world.ItemID("iron_sword")
	sess.inv[slotHotbarStart] = ItemStack{ID: sword, Count: 1}
	s.updatePlayerEquipmentAttributes(sess)
	if got := sess.heldAttackDamage(); got != 6 {
		t.Fatal("equipped", got)
	}
	for _, attr := range []string{"attack_damage", "attack_speed"} {
		modifier, ok := player.AttrMods[attr]["minecraft:base_"+attr]
		if !ok || modifier.permanent {
			t.Fatal("not transient", attr, modifier, ok)
		}
		delete(player.AttrMods[attr], "minecraft:base_"+attr)
	}
	s.updatePlayerEquipmentAttributes(sess)
	if got := sess.heldAttackDamage(); got != 1 {
		t.Fatal("identical stack reapplied removed modifier", got)
	}
	if got := sess.heldAttackDelayTicks(); got != 5 {
		t.Fatal("removed speed modifier still applied", got)
	}
	sess.inv[slotHotbarStart] = ItemStack{}
	s.updatePlayerEquipmentAttributes(sess)
	sess.inv[slotHotbarStart] = ItemStack{ID: sword, Count: 1}
	s.updatePlayerEquipmentAttributes(sess)
	if got := sess.heldAttackDamage(); got != 6 {
		t.Fatal("re-equipped", got)
	}
	hoe, _ := world.ItemID("netherite_hoe")
	sess.inv[slotHotbarStart] = ItemStack{ID: hoe, Count: 1}
	s.updatePlayerEquipmentAttributes(sess)
	for _, attr := range []string{"attack_damage", "attack_speed"} {
		modifier, ok := player.AttrMods[attr]["minecraft:base_"+attr]
		if !ok || modifier.value != 0 || modifier.permanent {
			t.Fatal("zero modifier missing", attr, modifier, ok)
		}
	}
}
