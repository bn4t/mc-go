package server

import (
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

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
	if damage <= 0 || sess.mode() != GameModeSurvival {
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
	it.Damage += damage
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
		s.broadcastEntityEvent(sess.entityID.Load(), entityEventBreakMainhand)
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
// offhand=48, feet=49, legs=50, chest=52, head=51).
const entityEventBreakMainhand = 47

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
	case "generic", "generic_kill", "fall", "starve", "out_of_world":
		return true
	}
	return false
}

// armorBreakEvents maps an armor inventory slot to its
// entity-status byte (LivingEntity.entityEventForEquipmentBreak on
// 26.2's tableswitch: head=49, chest=50, legs=51, feet=52).
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
	if sess.mode() != GameModeSurvival || bypassesArmor(src.typeName) {
		return
	}
	amount := int32(rawDamage / 4)
	if amount < 1 {
		amount = 1
	}
	broke := false
	dirty := map[int]ItemStack{}
	// Vanilla's doHurtEquipment walks feet, legs, chest, head, so the
	// break events leave in that order; the client's slot resyncs are
	// the inventory change broadcast, which walks slots ascending.
	for _, slot := range []int{slotArmorStart + 3, slotArmorStart + 2, slotArmorStart + 1, slotArmorStart} {
		sess.invMu.Lock()
		it := sess.inv[slot]
		name, ok := world.ItemName(it.ID)
		maxDmg, dmgOK := damageableItems[name]
		if !ok || !dmgOK || itemEquippable[name] == 0 || itemHurtWearExempt[name] != 0 {
			sess.invMu.Unlock()
			continue
		}
		it.Damage += amount
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
		s.broadcastEquipment(sess)
	}
}
