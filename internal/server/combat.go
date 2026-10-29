package server

import (
	"math"
	"math/rand"
	"strconv"
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
	// Body-slot armor (equippable slot=body): wolf armor and the
	// horse/nautilus sets. Any living mob holding one gets the armor
	// attribute — the slot is unfiltered on the real server.
	"wolf_armor":          11,
	"leather_horse_armor": 3, "copper_horse_armor": 4, "iron_horse_armor": 5,
	"golden_horse_armor": 7, "diamond_horse_armor": 11, "netherite_horse_armor": 19,
	"copper_nautilus_armor": 4, "iron_nautilus_armor": 5,
	"golden_nautilus_armor": 7, "diamond_nautilus_armor": 11, "netherite_nautilus_armor": 19,
}

// heldAttackDamage combines the player's attack_damage base/modifiers
// with the main-hand weapon's additive modifier before cooldown scaling.
func (sess *Session) heldAttackDamage() float32 {
	base := playerAttrBases["attack_damage"]
	mods := map[string]attrMod{}
	if sess.server != nil && sess.server.entities != nil {
		if player, ok := sess.server.entities.Get(sess.entityID.Load()); ok {
			if value, present := player.AttrBase["attack_damage"]; present {
				base = value
			}
			for id, modifier := range player.AttrMods["attack_damage"] {
				mods[id] = modifier
			}
		}
	}
	if held := sess.heldStack(); !held.Empty() {
		if name, ok := itemNameOf(held.ID); ok {
			if modifier, weapon := itemAttackDamageMod[name]; weapon {
				mods["minecraft:base_attack_damage"] = attrMod{value: modifier}
			}
		}
	}
	value := attrValue(base, mods)
	// Attributes.ATTACK_DAMAGE is ranged 0..2048; RangedAttribute
	// sanitizes NaN to the minimum (26.2 factual constants).
	if math.IsNaN(value) {
		return 0
	}
	return float32(max(0, min(2048, value)))
}

// heldAttackDelayTicks applies the mainhand modifier inside the player's
// attack_speed passes, then evaluates (1 / speed) * 20 in double precision
// before the final float32 conversion (Player, 26.2 factual arithmetic).
func (sess *Session) heldAttackDelayTicks() float32 {
	base := playerAttrBases["attack_speed"]
	mods := map[string]attrMod{}
	if sess.server != nil && sess.server.entities != nil {
		if player, ok := sess.server.entities.Get(sess.entityID.Load()); ok {
			if value, present := player.AttrBase["attack_speed"]; present {
				base = value
			}
			for id, modifier := range player.AttrMods["attack_speed"] {
				mods[id] = modifier
			}
		}
	}
	if held := sess.heldStack(); !held.Empty() {
		if name, ok := itemNameOf(held.ID); ok {
			if modifier, present := itemAttackSpeedMod[name]; present {
				mods["minecraft:base_attack_speed"] = attrMod{value: modifier}
			}
		}
	}
	speed := attrValue(base, mods)
	if math.IsNaN(speed) {
		speed = 0
	}
	speed = max(0, min(1024, speed))
	return float32(1 / speed * 20)
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

// attrRange clamps an attribute's computed value to its registered
// min/max — armor and armor_toughness are RangedAttributes in vanilla
// (0..30 and 0..20 respectively).
func attrRange(name string, v float64) float64 {
	switch name {
	case "armor":
		return max(0, min(30, v))
	case "armor_toughness":
		return max(0, min(20, v))
	}
	return v
}

// equipAttrMods merges an entity's AttrMods for an attribute with the
// worn pieces' per-item values as synthetic add_value modifiers —
// vanilla registers each equipped armor piece as an attribute modifier,
// which is why /attribute base set and equipment compose.
func equipAttrMods(e *Entity, attr string, table map[string]float32, worn []string) map[string]attrMod {
	mods := make(map[string]attrMod, len(e.AttrMods[attr])+len(worn))
	for k, m := range e.AttrMods[attr] {
		mods[k] = m
	}
	for i, iname := range worn {
		if v := table[iname]; v != 0 {
			mods["__equip"+strconv.Itoa(i)] = attrMod{value: float64(v)}
		}
	}
	return mods
}

// wornItemNames lists the item names in an entity's four armor slots
// plus the body slot.
func wornItemNames(e *Entity) []string {
	out := make([]string, 0, 5)
	for _, slot := range []int32{equipHelmet, equipChest, equipLegs, equipBoots} {
		name, _ := itemNameOf(e.Equips[slot].ID)
		out = append(out, name)
	}
	name, _ := itemNameOf(e.BodyItem.ID)
	out = append(out, name)
	return out
}

// mobAttrBase resolves an attribute's registered default for a mob
// type: AttrBase overrides win, then the per-type natural table.
func mobAttrBase(e *Entity, attr string) float64 {
	if v, ok := e.AttrBase[attr]; ok {
		return v
	}
	name, ok := EntityTypeName(e.Type)
	if !ok {
		return 0
	}
	switch attr {
	case "armor":
		return float64(mobNaturalArmor[name])
	case "max_health":
		if v, ok := mobAttrBases[name]["max_health"]; ok {
			return v
		}
		return float64(mobHealth[name])
	}
	return 0
}

// mobArmor is a mob victim's armor value — vanilla's
// LivingEntity.getArmorValue: the armor attribute (natural base or a
// /attribute override) with the worn pieces folded in as modifiers.
func mobArmor(e *Entity) float32 {
	v := attrValue(mobAttrBase(e, "armor"), equipAttrMods(e, "armor", armorPoints, wornItemNames(e)))
	return float32(attrRange("armor", v))
}

// mobToughness is the mob victim's armor_toughness attribute — the
// worn pieces' contributions folded over the (usually zero) base.
func mobToughness(e *Entity) float32 {
	v := attrValue(mobAttrBase(e, "armor_toughness"), equipAttrMods(e, "armor_toughness", armorToughness, wornItemNames(e)))
	return float32(attrRange("armor_toughness", v))
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

// mobHurtKnockback is LivingEntity.dealDefaultKnockback for a mob
// victim: the 0.4 push away from the damage source's position, folded
// by the type's knockback_resistance and composed into the entity's
// delta, which the tracker then ships as set_entity_motion.
func (s *Server) mobHurtKnockback(e *Entity, srcCause int32) {
	var sx, sz float64
	if cs := s.sessionByEntityID(srcCause); cs != nil {
		sx, _, sz, _, _, _ = cs.Pos()
	} else {
		se, ok := s.entities.Get(srcCause)
		if !ok {
			return
		}
		sx, sz = se.X, se.Z
	}
	res := float64(0)
	if name, ok := EntityTypeName(e.Type); ok {
		res = mobKnockbackResistance[name]
	}
	vx, vy, vz := knockDelta(e.MotionX, e.MotionY, e.MotionZ, 0.4, res,
		sx-e.X, sz-e.Z, e.Grounded)
	s.entities.SetMotion(e.ID, vx, vy, vz)
	s.broadcastEntityMotion(e.Dim, e.ID, vx, vy, vz)
}

// sessArmorNames lists the item names in the session's four armor
// slots plus the body slot.
func (sess *Session) sessArmorNames() []string {
	out := make([]string, 0, 5)
	for i := slotArmorStart; i < slotArmorStart+4; i++ {
		name, _ := itemNameOf(sess.stackAt(int16(i)).ID)
		out = append(out, name)
	}
	name, _ := itemNameOf(sess.bodyArmor.ID)
	out = append(out, name)
	return out
}

// sessAttr computes a player attribute with the worn armor pieces
// folded in as add_value modifiers — vanilla registers each piece as
// an attribute modifier, so /attribute base set and equipment compose.
func (sess *Session) sessAttr(attr string, table map[string]float32) float64 {
	base := playerAttrBases[attr]
	mods := map[string]attrMod{}
	if e, ok := sess.server.entities.Get(sess.entityID.Load()); ok {
		if v, is := e.AttrBase[attr]; is {
			base = v
		}
		for k, m := range e.AttrMods[attr] {
			mods[k] = m
		}
	}
	for i, iname := range sess.sessArmorNames() {
		if v := table[iname]; v != 0 {
			mods["__equip"+strconv.Itoa(i)] = attrMod{value: float64(v)}
		}
	}
	return attrRange(attr, attrValue(base, mods))
}

// armorDefense is the player's armor attribute value: base (or the
// /attribute override) plus the worn pieces' defense.
func (sess *Session) armorDefense() float32 {
	return float32(sess.sessAttr("armor", armorPoints))
}

// armorToughness is the armor_toughness attribute each worn piece
// contributes — only diamond (2) and netherite (3) carry any, per
// 26.2's ArmorMaterials constants.
var armorToughness = map[string]float32{
	"diamond_helmet": 2, "diamond_chestplate": 2,
	"diamond_leggings": 2, "diamond_boots": 2,
	"netherite_helmet": 3, "netherite_chestplate": 3,
	"netherite_leggings": 3, "netherite_boots": 3,
	"diamond_horse_armor": 2, "netherite_horse_armor": 3,
	"diamond_nautilus_armor": 2, "netherite_nautilus_armor": 3,
}

// armorKBResist is the knockback_resistance attribute per piece —
// netherite's 0.1 each (a full set resists 40% of knockback).
var armorKBResist = map[string]float64{
	"netherite_helmet": 0.1, "netherite_chestplate": 0.1,
	"netherite_leggings": 0.1, "netherite_boots": 0.1,
}

// armorToughness reads the player's armor_toughness attribute the
// same way — worn pieces as add_value modifiers over the base.
func (sess *Session) armorToughness() float32 {
	return float32(sess.sessAttr("armor_toughness", armorToughness))
}

// armorKBResistOf sums knockback_resistance over the worn pieces.
func (sess *Session) armorKBResist() float64 {
	total := float64(0)
	for i := slotArmorStart; i < slotArmorStart+4; i++ {
		it := sess.stackAt(int16(i))
		if it.Empty() {
			continue
		}
		if name, ok := itemNameOf(it.ID); ok {
			total += armorKBResist[name]
		}
	}
	return total
}

// reduceDamage applies vanilla's armor absorb (CombatRules.
// getDamageAfterAbsorb): effective defense is armor minus
// damage/(2+toughness/4), floored at armor/5 and capped at 20; the
// surviving fraction is 1 - defense/25, which the attacker's
// armor_effectiveness enchantment effects (breach: -0.15/level) then
// lower, clamped to [0,1] — modifyArmorEffectiveness on the damage
// source's weapon item.
func reduceDamage(damage, armor, toughness, breachLevel float32) float32 {
	if armor <= 0 || damage <= 0 {
		return damage
	}
	defense := armor - damage/(2+toughness/4)
	if floor := armor * 0.2; defense < floor {
		defense = floor
	}
	if defense > 20 {
		defense = 20
	}
	eff := defense/25 - 0.15*breachLevel
	if eff < 0 {
		eff = 0
	}
	if eff > 1 {
		eff = 1
	}
	return damage * (1 - eff)
}
