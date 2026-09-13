package server

// weaponDamage is the attack damage of the common weapons (vanilla
// values, main hand only).
var weaponDamage = map[string]float32{
	"wooden_sword":      4,
	"golden_sword":      4,
	"stone_sword":       5,
	"iron_sword":        6,
	"diamond_sword":     7,
	"netherite_sword":   8,
	"wooden_axe":        7,
	"stone_axe":         9,
	"iron_axe":          9,
	"diamond_axe":       9,
	"netherite_axe":     10,
	"golden_axe":        7,
	"trident":           9,
	"wooden_pickaxe":    2,
	"stone_pickaxe":     3,
	"iron_pickaxe":      4,
	"diamond_pickaxe":   5,
	"netherite_pickaxe": 6,
	"wooden_shovel":     2.5,
	"stone_shovel":      3.5,
	"iron_shovel":       4.5,
	"diamond_shovel":    5.5,
	"netherite_shovel":  6.5,
}

// armorPoints is the defense value of the common armor pieces.
var armorPoints = map[string]float32{
	"leather_boots": 1, "leather_leggings": 2, "leather_chestplate": 3, "leather_helmet": 1,
	"golden_boots": 1, "golden_leggings": 3, "golden_chestplate": 5, "golden_helmet": 2,
	"chainmail_boots": 1, "chainmail_leggings": 4, "chainmail_chestplate": 5, "chainmail_helmet": 2,
	"iron_boots": 2, "iron_leggings": 5, "iron_chestplate": 6, "iron_helmet": 2,
	"diamond_boots": 3, "diamond_leggings": 6, "diamond_chestplate": 8, "diamond_helmet": 3,
	"netherite_boots": 3, "netherite_leggings": 6, "netherite_chestplate": 8, "netherite_helmet": 3,
	"turtle_helmet": 2,
}

// heldAttackDamage resolves the main-hand attack damage.
func (sess *Session) heldAttackDamage() float32 {
	held := sess.heldStack()
	if held.Empty() {
		return 1
	}
	name, ok := itemNameOf(held.ID)
	if !ok {
		return 1
	}
	if dmg, ok := weaponDamage[name]; ok {
		return dmg
	}
	return 1
}

// armorDefense sums the armor points of a player's four armor slots.
func (sess *Session) armorDefense() float32 {
	total := float32(0)
	for i := slotArmorStart; i < slotArmorStart+4; i++ {
		it := sess.stackAt(int16(i))
		if it.Empty() {
			continue
		}
		if name, ok := itemNameOf(it.ID); ok {
			total += armorPoints[name]
		}
	}
	return total
}

// reduceDamage applies vanilla's armor formula: damage scaled by
// 1 - min(20, armor)/25.
func reduceDamage(damage, armor float32) float32 {
	if armor <= 0 {
		return damage
	}
	if armor > 20 {
		armor = 20
	}
	return damage * (1 - armor/25)
}
