package server

import "testing"

func TestKineticWeaponNativeMetadata(t *testing.T) {
	if len(kineticWeaponMetadata) != 7 {
		t.Fatal("kinetic item count", len(kineticWeaponMetadata))
	}
	wood := kineticWeaponMetadata["wooden_spear"]
	if wood.contactCooldownTicks != 10 || wood.delayTicks != 15 || wood.damage.maxDurationTicks != 300 || wood.damage.minSpeed != 0 || wood.damage.minRelativeSpeed != 4.599999904632568 {
		t.Fatal("native wooden damage conditions", wood)
	}
	if wood.dismount.maxDurationTicks != 100 || wood.dismount.minSpeed != 14 || wood.dismount.minRelativeSpeed != 0 {
		t.Fatal("native dismount conditions", wood.dismount)
	}
	if wood.knockback.maxDurationTicks != 200 || wood.knockback.minSpeed != 5.099999904632568 || wood.damageMultiplier != .699999988079071 || wood.forwardMovement != .3799999952316284 {
		t.Fatal("promoted component floats", wood)
	}
	if wood.useSound != 1529 || wood.hitSound != 1530 {
		t.Fatal("wood sounds", wood)
	}
	for _, name := range []string{"wooden_spear", "stone_spear", "copper_spear", "iron_spear", "golden_spear", "diamond_spear", "netherite_spear"} {
		info, ok := kineticWeaponMetadata[name]
		if !ok || info.contactCooldownTicks != 10 || info.damage.minSpeed != 0 || info.damage.minRelativeSpeed != 4.599999904632568 || info.knockback.minRelativeSpeed != 0 || info.dismount.minRelativeSpeed != 0 {
			t.Fatal("component condition roles", name, info)
		}
	}
}
