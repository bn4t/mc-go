package server

import (
	"math"
	"math/rand"
)

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
	"mace":              6, // 1 base + 5 ADD_VALUE (createAttributes)
	"wooden_spear":      1,
	"golden_spear":      1,
	"stone_spear":       2,
	"copper_spear":      2,
	"iron_spear":        3,
	"diamond_spear":     4,
	"netherite_spear":   5,
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

// heldAttackDelayTicks is vanilla's getCurrentItemAttackStrengthDelay:
// (1 / attackSpeed) * 20, with attackSpeed the base 4 plus the item's
// mainhand attribute modifier (a stone sword's -2.4 => 12.5 ticks).
func (sess *Session) heldAttackDelayTicks() float32 {
	speed := float64(4.0)
	if held := sess.heldStack(); !held.Empty() {
		if name, ok := itemNameOf(held.ID); ok {
			speed += itemAttackSpeedMod[name]
		}
	}
	return 20.0 / float32(speed)
}

// attackScale is vanilla's getAttackStrengthScale(0.5): the charge
// fraction that decides strong hits (>0.9) and scales the damage.
func (sess *Session) attackScale() float32 {
	scale := (float32(sess.attackTicker) + 0.5) / sess.heldAttackDelayTicks()
	if scale > 1 {
		scale = 1
	}
	return scale
}

// attackDamage applies vanilla's cooldown scale to the weapon's base
// damage: damage * (0.2 + 0.8 * scale²), where
// scale = clamp((attackTicker + 0.5) / delayTicks, 0, 1) — the 0.5 is
// the partial tick vanilla passes into getAttackStrengthScale.
func (sess *Session) attackDamage() float32 {
	scale := sess.attackScale()
	return sess.heldAttackDamage() * (0.2 + 0.8*scale*scale)
}

// Enchantment damage sensitivity — vanilla 26.2's entity_type tags
// sensitive_to_smite (#undead), sensitive_to_bane_of_arthropods
// (#arthropod) and sensitive_to_impaling (#aquatic), flattened.
var (
	smiteSensitive = map[string]bool{
		"skeleton": true, "stray": true, "wither_skeleton": true,
		"skeleton_horse": true, "bogged": true, "parched": true,
		"zombie_horse": true, "camel_husk": true, "zombie": true,
		"zombie_villager": true, "zombified_piglin": true,
		"zoglin": true, "drowned": true, "husk": true,
		"zombie_nautilus": true, "wither": true, "phantom": true,
	}
	baneSensitive = map[string]bool{
		"bee": true, "endermite": true, "silverfish": true,
		"spider": true, "cave_spider": true,
	}
	impalingSensitive = map[string]bool{
		"turtle": true, "axolotl": true, "guardian": true,
		"elder_guardian": true, "cod": true, "pufferfish": true,
		"salmon": true, "tropical_fish": true, "dolphin": true,
		"squid": true, "glow_squid": true, "tadpole": true,
		"nautilus": true, "zombie_nautilus": true,
	}
)

// mobNaturalArmor is the vanilla base ARMOR attribute per entity
// type (javap'd createAttributes): the zombie family carries 2, the
// wither 4; every other summonable type defaults to zero.
var mobNaturalArmor = map[string]float32{
	"zombie":           2,
	"zombie_villager":  2,
	"husk":             2,
	"drowned":          2,
	"zombified_piglin": 2,
	"wither":           4,
}

// mobArmor is a mob victim's armor value — the type's natural base
// plus the defense of any worn armor pieces.
func mobArmor(e *Entity) float32 {
	var total float32
	if name, ok := EntityTypeName(e.Type); ok {
		total += mobNaturalArmor[name]
	}
	for _, slot := range []int32{equipHelmet, equipChest, equipLegs, equipBoots} {
		if name, ok := itemNameOf(e.Equips[slot].ID); ok {
			total += armorPoints[name]
		}
	}
	return total
}

// enchantDamageBonus is the weapon's total minecraft:damage enchant
// effect against the victim type: sharpness adds 1+0.5·(level-1)
// unconditionally; smite/bane/impaling add 2.5·level against their
// sensitivity set. Vanilla folds this in scaled linearly by the
// attack-strength scale, after the crit multiply.
func enchantDamageBonus(held ItemStack, victimType string) float32 {
	var bonus float32
	if l := held.enchLevel("sharpness"); l > 0 {
		bonus += 1 + 0.5*float32(l-1)
	}
	if l := held.enchLevel("smite"); l > 0 && smiteSensitive[victimType] {
		bonus += 2.5 * float32(l)
	}
	if l := held.enchLevel("bane_of_arthropods"); l > 0 && baneSensitive[victimType] {
		bonus += 2.5 * float32(l)
	}
	if l := held.enchLevel("impaling"); l > 0 && impalingSensitive[victimType] {
		bonus += 2.5 * float32(l)
	}
	return bonus
}

// Damage-type tag membership for the protection enchantments' source
// requirements (vanilla 26.2 tags/damage_type).
var (
	isFireDamage       = map[string]bool{"in_fire": true, "campfire": true, "on_fire": true, "lava": true, "hot_floor": true, "sulfur_cube_hot": true, "unattributed_fireball": true, "fireball": true}
	isFallDamage       = map[string]bool{"fall": true, "ender_pearl": true, "stalagmite": true}
	isProjectileDamage = map[string]bool{"arrow": true, "trident": true, "mob_projectile": true, "unattributed_fireball": true, "fireball": true, "wither_skull": true, "thrown": true, "wind_charge": true}
	isExplosionDamage  = map[string]bool{"fireworks": true, "explosion": true, "player_explosion": true, "bad_respawn_point": true}
	burnFromStepping   = map[string]bool{"campfire": true, "hot_floor": true, "sulfur_cube_hot": true}
)

// enchantProtection returns the victim's Enchantment Protection Factor
// for this damage type — protection 1/level, the typed protections
// 2/level, feather_falling 3/level — summed over the four armor slots.
// Every protection effect requires the source not to bypass
// invulnerability (out_of_world, generic_kill).
func (sess *Session) enchantProtection(typeName string) float32 {
	if typeName == "out_of_world" || typeName == "generic_kill" {
		return 0
	}
	var epf float32
	for i := slotArmorStart; i < slotArmorStart+4; i++ {
		it := sess.stackAt(int16(i))
		if it.Empty() {
			continue
		}
		epf += float32(it.enchLevel("protection"))
		if isFireDamage[typeName] {
			epf += 2 * float32(it.enchLevel("fire_protection"))
		}
		if isFallDamage[typeName] {
			epf += 3 * float32(it.enchLevel("feather_falling"))
		}
		if isProjectileDamage[typeName] {
			epf += 2 * float32(it.enchLevel("projectile_protection"))
		}
		if isExplosionDamage[typeName] {
			epf += 2 * float32(it.enchLevel("blast_protection"))
		}
	}
	return epf
}

// enchantImmunity reports whether the victim's frost walker boots
// negate this damage type outright (damage_immunity effect,
// burn_from_stepping: campfire / hot floor / sulfur cube).
func (sess *Session) enchantImmunity(typeName string) bool {
	if !burnFromStepping[typeName] {
		return false
	}
	for i := slotArmorStart; i < slotArmorStart+4; i++ {
		if sess.stackAt(int16(i)).enchLevel("frost_walker") > 0 {
			return true
		}
	}
	return false
}

// mobKnockbackResistance is the vanilla KNOCKBACK_RESISTANCE attribute
// per entity type; absent types default to zero (take full knockback).
var mobKnockbackResistance = map[string]float64{
	"iron_golem": 1.0,
}

// knockDelta applies one vanilla LivingEntity.knockback step to a
// velocity: strength folded by knockback resistance, direction pushed
// away from the damage source, grounded victims popped upwards.
func knockDelta(vx, vy, vz, strength, resistance, dx, dz float64, grounded bool) (float64, float64, float64) {
	strength *= 1 - resistance
	if strength <= 0 {
		return vx, vy, vz
	}
	if dx*dx+dz*dz < 1e-5 {
		dx = (rand.Float64() - rand.Float64()) * 0.01
		dz = (rand.Float64() - rand.Float64()) * 0.01
	}
	l := math.Hypot(dx, dz)
	kbx, kbz := dx/l*strength, dz/l*strength
	ny := vy / 2
	if grounded {
		ny = math.Min(0.4, vy/2+strength)
	}
	return vx/2 - kbx, ny, vz/2 - kbz
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

// reduceDamage applies vanilla's armor absorb (CombatRules.
// getDamageAfterAbsorb): effective defense is armor minus
// damage/(2+toughness/4), floored at armor/5 and capped at 20; the
// surviving damage scales by 1 - defense/25. Toughness is always 0
// here — no armor on this server carries armor_toughness yet.
func reduceDamage(damage, armor float32) float32 {
	if armor <= 0 || damage <= 0 {
		return damage
	}
	defense := armor - damage/2
	if floor := armor * 0.2; defense < floor {
		defense = floor
	}
	if defense > 20 {
		defense = 20
	}
	return damage * (1 - defense/25)
}
