package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestPlayerArmorAttributeLifecycle(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	player := s.entities.Spawn(&Entity{Type: mustEntityType("player")})
	sess := &Session{server: s}
	sess.entityID.Store(player.ID)
	stack := func(name string) ItemStack {
		id, ok := world.ItemID(name)
		if !ok {
			t.Fatal(name)
		}
		return ItemStack{ID: id, Count: 1}
	}
	sess.inv[slotArmorStart] = stack("copper_helmet")
	s.updatePlayerArmorAttributes(sess)
	if sess.armorDefense() != 2 || sess.armorToughness() != 0 {
		t.Fatal("copper attributes", sess.armorDefense(), sess.armorToughness())
	}
	zero, ok := player.AttrMods["armor_toughness"]["minecraft:armor.helmet"]
	if !ok || zero.value != 0 || zero.permanent {
		t.Fatal("zero transient toughness missing", zero, ok)
	}
	sess.inv[slotArmorStart] = stack("diamond_boots")
	sess.inv[slotArmorStart+3] = stack("netherite_boots")
	s.updatePlayerArmorAttributes(sess)
	if sess.armorDefense() != 3 || sess.armorToughness() != 3 {
		t.Fatal("wrong-slot item applied or canonical armor counted twice", sess.armorDefense(), sess.armorToughness())
	}
	if got := player.AttrMods["knockback_resistance"]["minecraft:armor.boots"].value; got != .10000000149011612 {
		t.Fatal("netherite float fact lost", got)
	}
	sess.inv[slotArmorStart+1] = stack("iron_chestplate")
	s.updatePlayerArmorAttributes(sess)
	if sess.armorDefense() != 9 {
		t.Fatal("chest/boots composition", sess.armorDefense())
	}
	delete(player.AttrMods["armor"], "minecraft:armor.chestplate")
	delete(player.AttrMods["armor_toughness"], "minecraft:armor.chestplate")
	s.updatePlayerArmorAttributes(sess)
	if sess.armorDefense() != 3 {
		t.Fatal("unchanged stack reinserted removed armor", sess.armorDefense())
	}
	sess.inv[slotArmorStart] = stack("copper_helmet")
	s.updatePlayerArmorAttributes(sess)
	if sess.armorDefense() != 5 {
		t.Fatal("another slot reinserted removed chest", sess.armorDefense())
	}
	player.playerArmorAttrsInit = false
	s.updatePlayerArmorAttributes(sess)
	if sess.armorDefense() != 11 {
		t.Fatal("fresh life did not restore equipment", sess.armorDefense())
	}
	sess.bodyArmor = stack("wolf_armor")
	s.updatePlayerArmorAttributes(sess)
	if sess.armorDefense() != 22 {
		t.Fatal("body armor lost", sess.armorDefense())
	}
}
