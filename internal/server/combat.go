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
