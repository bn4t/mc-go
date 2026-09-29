package server

import (
	"math/rand"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// unbreakingReduce is vanilla's item_damage enchantment pass
// (remove_binomial): every durability point is independently dropped
// with the level-based chance — armor 2l/(5l+5), everything else
// l/(l+1), as the unbreaking datapack entry defines.
func unbreakingReduce(it ItemStack, name string, amount int32) int32 {
	l := it.enchLevel("unbreaking")
	if l <= 0 || amount <= 0 {
		return amount
	}
	p := float64(l) / float64(l+1)
	if armorPoints[name] > 0 {
		p = 2 * float64(l) / (5*float64(l) + 5)
	}
	kept := amount
	for i := int32(0); i < amount; i++ {
		if rand.Float64() < p {
			kept--
		}
	}
	return kept
}

// wearHeld applies damage durability points to the held stack, the
// ItemStack.hurtAndBreak path for the main hand: only damageable
// items wear, creative/spectator players wear nothing, and when the
// damage reaches the item's max the stack breaks — shrinking to
// nothing, broadcasting the equipment-broken entity event (47 for
// the main hand), and awarding the item-broken statistic. Either way
// the slot is resynced so the client sees the new damage value or
// the empty slot.
func (s *Server) wearHeld(sess *Session, damage int32) {
	s.wearSlot(sess, invSlotForHotbar(sess.held.Load()), damage)
}

// wearSlot is wearHeld for an arbitrary inventory slot — the offhand
// use of a food-on-a-stick wears that stack instead of the main one.
func (s *Server) wearSlot(sess *Session, slot int16, damage int32) {
	if damage <= 0 || sess.mode() == GameModeCreative {
		return
	}
	sess.invMu.Lock()
	it := sess.inv[slot]
	name, ok := world.ItemName(it.ID)
	maxDmg, dmgOK := damageableItems[name]
	if !ok || !dmgOK {
		sess.invMu.Unlock()
		return
	}
	it.Damage += unbreakingReduce(it, name, damage)
	var empt bool
	if it.Damage >= maxDmg {
		sess.inv[slot] = ItemStack{}
		empt = true
	} else {
		sess.inv[slot] = it
	}
	cur := sess.inv[slot]
	sess.invState.Add(1)
	sess.invMu.Unlock()

	// Vanilla fires ITEM_DURABILITY_CHANGED inside hurtAndBreak with
	// the stack and the new damage value before setDamageValue lands —
	// the criterion's item predicate only reads the stack's identity.
	s.fireItemDurabilityChanged(sess, it)

	if empt {
		// Vanilla's onEquippedItemBroken: the entity event (players
		// hear the snap) and the stat land before the slot resync.
		s.broadcastEntityEvent(sess.entityID.Load(), breakEventForSlot(slot))
		sess.addItemStat(statBroken, it.ID, 1)
	}
	_ = s.sendContainerSlot(sess, slot, cur)
	if empt {
		s.fireInventoryChanged(sess)
	}
}

// entityEventBreakMainhand is the entity-status byte vanilla sends
// for an equipment break in the main hand
// (LivingEntity.entityEventForEquipmentBreak: mainhand=47,
// offhand=48, head=49, chest=50, feet=52, legs=51, body=65,
// saddle=68).
const entityEventBreakMainhand = 47

// breakEventForSlot is entityEventForEquipmentBreak over mc-go's
// inventory layout: any hotbar slot is the main hand, then offhand,
// then the four armor slots via armorBreakEvents. A slot outside
// the worn set still reports as main-hand — vanilla's hurtAndBreak
// only ever runs on equipped slots.
func breakEventForSlot(slot int16) byte {
	if ev, ok := armorBreakEvents[int(slot)]; ok {
		return ev
	}
	if slot == slotOffhand {
		return 48
	}
	return entityEventBreakMainhand
}

// broadcastEntityEvent sends one entity-status byte to every spawned
// session in the entity's dimension, including the owner (vanilla's
// broadcastEntityEvent reaches the breaker too).
func (s *Server) broadcastEntityEvent(eid int32, status byte) {
	dim := dimOverworld
	if e, ok := s.entities.Get(eid); ok {
		dim = e.Dim
	}
	s.broadcastEntityEventTo(nil, dim, eid, status)
}

// broadcastEntityEventTo fans an entity event out to every play-state
// session in dim except the given one.
func (s *Server) broadcastEntityEventTo(except *Session, dim string, eid int32, status byte) {
	var b protocol.Buffer
	b.Int(eid)
	b.Byte(status)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other == except || other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		if !other.tracksEntity(eid) && other.entityID.Load() != eid {
			return true
		}
		_ = other.write(other.ids().PlayEntityEvent, body)
		return true
	})
}

// mineWear wears the held tool for one broken block, the server side
// of Item.mineBlock: an item with a tool component awards the
// item-used statistic on every break and takes damage_per_block
// durability points when the block's destroy speed is nonzero.
func (s *Server) mineWear(sess *Session, info blockHardness) {
	it := sess.heldStack()
	name, ok := world.ItemName(it.ID)
	if !ok {
		return
	}
	dpb, ok := itemToolDamagePerBlock[name]
	if !ok {
		return
	}
	// Vanilla's Item.mineBlock wears the item inside the call and the
	// caller awards ITEM_USED after it returns, so a break lands its
	// broken statistic before the use that caused it.
	if info.hardness != 0 {
		s.wearHeld(sess, dpb)
	}
	sess.addItemStat(statUsed, it.ID, 1)
}

// attackWear wears the held weapon after a landed hit, the
// postHurtEnemy path: items with a weapon component award the
// item-used statistic per hit and take item_damage_per_attack
// durability points.
func (s *Server) attackWear(sess *Session) {
	it := sess.heldStack()
	name, ok := world.ItemName(it.ID)
	if !ok {
		return
	}
	dpa, ok := itemWeaponDamagePerAttack[name]
	if !ok {
		return
	}
	sess.addItemStat(statUsed, it.ID, 1)
	s.wearHeld(sess, dpa)
}

// bypassesArmor lists the damage types vanilla tags
// minecraft:bypasses_armor that this server emits; a hit from one
// never reduces through armor and never wears it.
func bypassesArmor(typeName string) bool {
	switch typeName {
	case "on_fire", "in_wall", "cramming", "drown", "fly_into_wall",
		"generic", "wither", "dragon_breath", "starve", "fall",
		"ender_pearl", "freeze", "stalagmite", "magic",
		"indirect_magic", "out_of_world", "generic_kill",
		"sonic_boom", "outside_border":
		return true
	}
	return false
}

// bypassesWolfArmor is vanilla's minecraft:bypasses_wolf_armor tag
// (bypasses_invulnerability's members plus the listed types): a hit
// of one of these types ignores wolf armor's absorb entirely and
// runs the normal armor formula instead.
func bypassesWolfArmor(typeName string) bool {
	switch typeName {
	case "out_of_world", "generic_kill", "cramming", "drown",
		"dry_out", "freeze", "in_wall", "indirect_magic", "magic",
		"outside_border", "starve", "thorns", "wither":
		return true
	}
	return false
}

// noKnockback mirrors vanilla's minecraft:no_knockback damage-type
// tag — hurts of these types skip dealDefaultKnockback entirely.
func noKnockback(typeName string) bool {
	switch typeName {
	case "explosion", "player_explosion", "bad_respawn_point",
		"in_fire", "lightning_bolt", "on_fire", "lava",
		"hot_floor", "sulfur_cube_hot", "in_wall", "cramming",
		"drown", "starve", "cactus", "fall", "ender_pearl",
		"fly_into_wall", "out_of_world", "generic", "magic",
		"wither", "dragon_breath", "dry_out", "sweet_berry_bush",
		"freeze", "stalagmite", "outside_border", "generic_kill",
		"campfire", "spear":
		return true
	}
	return false
}

// armorBreakEvents maps an armor inventory slot to its
// entity-status byte. 26.2's entityEventForEquipmentBreak switch is
// keyed by the LivingEntity$1 switchmap order — MAINHAND,OFFHAND,
// HEAD,CHEST,FEET,LEGS → 47,48,49,50,52,51 (feet and legs swap; BODY
// and SADDLE get 65/68).
var armorBreakEvents = map[int]byte{
	slotArmorStart + 3: 52, // feet
	slotArmorStart + 2: 51, // legs
	slotArmorStart + 1: 50, // chest
	slotArmorStart + 0: 49, // head
}

// armorWear is LivingEntity.doHurtEquipment for the four armor
// slots: a hit that does not bypass armor costs every damageable,
// hurt-wearable equipped item max(1, rawDamage/4) durability — the
// raw incoming amount, before armor reduces it. A piece that
// exhausts its durability empties the slot, snaps the break entity
// event, and awards the item-broken statistic.
func (s *Server) armorWear(sess *Session, src damageSource, rawDamage float32) {
	// Vanilla's doHurtEquipment walks feet, legs, chest, head, so the
	// break events leave in that order; the client's slot resyncs are
	// the inventory change broadcast, which walks slots ascending.
	s.doHurtEquipment(sess, src, rawDamage,
		[]int{slotArmorStart + 3, slotArmorStart + 2, slotArmorStart + 1, slotArmorStart})
}

// hurtHelmet is Player.hurtHelmet: doHurtEquipment scoped to HEAD,
// invoked by hurtServer for the damages_helmet damage types before
// the 0.75 absorb.
func (s *Server) hurtHelmet(sess *Session, src damageSource, rawDamage float32) {
	s.doHurtEquipment(sess, src, rawDamage, []int{slotArmorStart})
}

func (s *Server) doHurtEquipment(sess *Session, src damageSource, rawDamage float32, slots []int) {
	if sess.mode() == GameModeCreative || bypassesArmor(src.typeName) {
		return
	}
	amount := int32(rawDamage / 4)
	if amount < 1 {
		amount = 1
	}
	broke := false
	dirty := map[int]ItemStack{}
	for _, slot := range slots {
		sess.invMu.Lock()
		it := sess.inv[slot]
		name, ok := world.ItemName(it.ID)
		maxDmg, dmgOK := damageableItems[name]
		if !ok || !dmgOK || itemEquippable[name] == 0 || itemHurtWearExempt[name] != 0 {
			sess.invMu.Unlock()
			continue
		}
		it.Damage += unbreakingReduce(it, name, amount)
		var empt bool
		if it.Damage >= maxDmg {
			sess.inv[slot] = ItemStack{}
			empt = true
		} else {
			sess.inv[slot] = it
		}
		dirty[slot] = sess.inv[slot]
		sess.invState.Add(1)
		sess.invMu.Unlock()

		if empt {
			s.broadcastEntityEvent(sess.entityID.Load(), armorBreakEvents[slot])
			sess.addItemStat(statBroken, it.ID, 1)
			broke = true
		}
	}
	for slot := slotArmorStart; slot < slotArmorStart+4; slot++ {
		if cur, ok := dirty[slot]; ok {
			_ = s.sendContainerSlot(sess, int16(slot), cur)
		}
	}
	if broke {
		s.fireInventoryChanged(sess)
	}
}

// wolfArmorCrackLevel is Crackiness.WOLF_ARMOR.byDamage: the bucket on
// the remaining-durability fraction (max-damage)/max — NONE down to
// 0.95, LOW to 0.69, MEDIUM to 0.32, HIGH below that. Crossing a
// bucket edge is what plays the crack sound + scute burst in
// Wolf.actuallyHurt.
func wolfArmorCrackLevel(damage, max int32) int {
	if max <= 0 {
		return 0
	}
	switch f := float64(max-damage) / float64(max); {
	case f < 0.32:
		return 3 // HIGH
	case f < 0.69:
		return 2 // MEDIUM
	case f < 0.95:
		return 1 // LOW
	default:
		return 0 // NONE
	}
}

// Wolf armor feedback sounds (sound_event registry ids on 26.2) and
// the mob NEUTRAL SoundSource — wolf sounds ride sound_entity
// packets anchored on the wolf, source NEUTRAL.
const (
	sndWolfArmorCrack         int32 = 1792 // item.wolf_armor.crack
	sndWolfArmorDamage        int32 = 1793 // item.wolf_armor.damage
	sndWolfArmorRepair        int32 = 1794 // item.wolf_armor.repair
	sndWolfHurt               int32 = 1806 // entity.wolf.hurt
	sndCamelDash              int32 = 261  // entity.camel.dash
	sndCamelDashReady         int32 = 262  // entity.camel.dash_ready
	sndDonkeyJump             int32 = 535  // entity.donkey.jump
	sndHorseJump              int32 = 864  // entity.horse.jump
	sndMuleJump               int32 = 1026 // entity.mule.jump
	sndSkeletonHorseJumpWater int32 = 1488 // entity.skeleton_horse.jump_water
	srcNeutral                int32 = 6
)

// broadcastItemParticles emits one level_particles burst of the ITEM
// type — 26.2's ItemParticleOption carries an ItemStackTemplate:
// VarInt item id + VarInt count + empty component patch.
func (s *Server) broadcastItemParticles(dim, itemName string, x, y, z, xd, yd, zd, speed float64, count int32) {
	itemID, ok := world.ItemID(itemName)
	if !ok {
		return
	}
	pid, ok := ParticleTypeID("item")
	if !ok {
		return
	}
	var b protocol.Buffer
	b.Byte(0) // overrideLimiter
	b.Byte(0) // alwaysShow
	b.Double(x)
	b.Double(y)
	b.Double(z)
	b.Float(float32(xd))
	b.Float(float32(yd))
	b.Float(float32(zd))
	b.Float(float32(speed))
	b.Int(count)
	b.VarInt(pid)
	b.VarInt(itemID)
	b.VarInt(1) // template count
	b.VarInt(0) // component patch: added
	b.VarInt(0) // component patch: removed
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		_ = other.write(other.ids().PlayLevelParticles, body)
		return true
	})
}

// damagesHelmet mirrors vanilla's minecraft:damages_helmet tag —
// hurtServer wears the head piece and bleeds 25% of the hit through
// when one is worn and the type is in this set.
func damagesHelmet(typeName string) bool {
	switch typeName {
	case "falling_anvil", "falling_block", "falling_stalactite":
		return true
	}
	return false
}
