package server

import (
	"strings"

	"mc-go/internal/world"
)

// Armor stand equipment and the interact_at swap, measured on vanilla
// 26.2's ArmorStand.interact / getClickedSlot / swapItem.

// armorStandEquipSlot is vanilla's getEquipmentSlotForItem for the
// armor stand family: the item's equippable component picks its armor
// slot and everything else defaults to MAINHAND.
func armorStandEquipSlot(name string) int32 {
	switch {
	case name == "elytra" || strings.HasSuffix(name, "_chestplate"):
		return equipChest
	case strings.HasSuffix(name, "_leggings"):
		return equipLegs
	case strings.HasSuffix(name, "_boots"):
		return equipBoots
	case strings.HasSuffix(name, "_helmet") || strings.HasSuffix(name, "_head") ||
		strings.HasSuffix(name, "_skull") || name == "carved_pumpkin":
		return equipHelmet
	}
	return equipMainHand
}

// armorStandClickedSlot is getClickedSlot: the click height (in entity
// space) picks the occupied equipment slot, falling back to the
// offhand when the main hand is empty but the offhand holds something.
// getScale and getAgeScale are 1 for every armor stand mc-go spawns.
func armorStandClickedSlot(e *Entity, y float64) int32 {
	var hiFeet, loChest, hiChest, hiLegs float64
	if e.Small {
		hiFeet, loChest, hiChest, hiLegs = 0.8, 0.3, 1.0, 1.0
	} else {
		hiFeet, loChest, hiChest, hiLegs = 0.45, 0.0, 0.7, 0.8
	}
	slot := int32(equipMainHand)
	switch {
	case y >= 0.1 && y < 0.1+hiFeet && !e.Equips[equipBoots].Empty():
		slot = equipBoots
	case y >= 0.9+loChest && y < 0.9+hiChest && !e.Equips[equipChest].Empty():
		slot = equipChest
	case y >= 0.4 && y < 0.4+hiLegs && !e.Equips[equipLegs].Empty():
		slot = equipLegs
	case y >= 1.6 && !e.Equips[equipHelmet].Empty():
		slot = equipHelmet
	default:
		if e.Equips[equipMainHand].Empty() && !e.Equips[equipOffHand].Empty() {
			slot = equipOffHand
		}
	}
	return slot
}

// armorStandFilterID maps a wire equipment index (equipMainHand..
// equipHelmet) to the EquipmentSlot id that getFilterBit uses as its
// base — it differs from the wire ordinal for every slot except the
// main hand: {main:0, off:5, feet:1, legs:2, chest:3, head:4}.
// DisabledSlots NBT bits address this space.
func armorStandFilterID(slot int32) int32 {
	switch slot {
	case equipOffHand:
		return 5
	case equipBoots:
		return 1
	case equipLegs:
		return 2
	case equipChest:
		return 3
	case equipHelmet:
		return 4
	}
	return 0 // main hand
}

// armorStandDisabled is isDisabled: the replace bit (DisabledSlots
// bit filterID+0) or a hand slot on an armless stand.
func armorStandDisabled(e *Entity, slot int32) bool {
	if e.DisabledSlots&(1<<armorStandFilterID(slot)) != 0 {
		return true
	}
	if (slot == equipMainHand || slot == equipOffHand) && !e.ShowArms {
		return true
	}
	return false
}

// armorStandSwap is swapItem: take-disable (bit id+8) and
// place-disable (bit id+16) gates first, then creative copies one,
// stacked items split one onto an empty slot, and otherwise the whole
// stack swaps with the stand's.
func (s *Server) armorStandSwap(sess *Session, e *Entity, slot int32, stack ItemStack, hslot int16) bool {
	cur := e.Equips[slot]
	fid := armorStandFilterID(slot)
	if !cur.Empty() && e.DisabledSlots&(1<<(fid+8)) != 0 {
		return false
	}
	if cur.Empty() && e.DisabledSlots&(1<<(fid+16)) != 0 {
		return false
	}
	if sess.mode() == GameModeCreative && cur.Empty() && !stack.Empty() {
		one := stack
		one.Count = 1
		s.entities.mu.Lock()
		if e.Equips == nil {
			e.Equips = map[int32]ItemStack{}
		}
		e.Equips[slot] = one
		s.entities.mu.Unlock()
		s.armorStandBroadcastEquip(e, slot, one)
		return true
	}
	if !stack.Empty() && stack.Count > 1 {
		if !cur.Empty() {
			return false
		}
		one := stack
		one.Count = 1
		s.entities.mu.Lock()
		if e.Equips == nil {
			e.Equips = map[int32]ItemStack{}
		}
		e.Equips[slot] = one
		s.entities.mu.Unlock()
		sess.consumeSlot(hslot, 1)
		s.armorStandBroadcastEquip(e, slot, one)
		return true
	}
	s.entities.mu.Lock()
	if e.Equips == nil {
		e.Equips = map[int32]ItemStack{}
	}
	if stack.Empty() {
		delete(e.Equips, slot)
	} else {
		e.Equips[slot] = stack
	}
	s.entities.mu.Unlock()
	// setItemInHand: the stand's old stack lands in the player's hand.
	sess.invMu.Lock()
	sess.inv[hslot] = cur
	sess.invState.Add(1)
	sess.invMu.Unlock()
	_ = s.sendContainerSlot(sess, hslot, cur)
	s.armorStandBroadcastEquip(e, slot, stack)
	return true
}

// armorStandInteract is ArmorStand.interact: spectator passes through,
// an empty hand picks the clicked slot (or mainhand when that slot is
// disabled), a held stack targets its equippable slot, and the swap
// runs vanilla's gates. FAIL produces no packets.
func (s *Server) armorStandInteract(sess *Session, eid int32, hand int32, clickY float64) {
	s.entities.mu.RLock()
	e, ok := s.entities.byID[eid]
	s.entities.mu.RUnlock()
	if !ok || e.Dying || e.Type != mustEntityType("armor_stand") {
		return
	}
	if sess.mode() == GameModeSpectator {
		return
	}
	held, hslot := sess.stackForHand(hand)
	if held.Empty() {
		clicked := armorStandClickedSlot(e, clickY)
		target := clicked
		if armorStandDisabled(e, clicked) {
			target = equipMainHand // getEquipmentSlotForItem(EMPTY)
		}
		s.entities.mu.RLock()
		has := !e.Equips[target].Empty()
		s.entities.mu.RUnlock()
		if has && s.armorStandSwap(sess, e, target, held, hslot) {
			s.broadcastEntityAnimation(sess, sess.entityID.Load(), armorStandSwing(hand))
		}
		return
	}
	name, _ := itemNameOf(held.ID)
	wanted := armorStandEquipSlot(name)
	if armorStandDisabled(e, wanted) {
		return
	}
	if s.armorStandSwap(sess, e, wanted, held, hslot) {
		s.broadcastEntityAnimation(sess, sess.entityID.Load(), armorStandSwing(hand))
	}
}

// armorStandSwing is the animate-packet action player.swing sends:
// 0 for the main hand, 3 for the off hand.
func armorStandSwing(hand int32) byte {
	if hand == 1 {
		return 3
	}
	return 0
}

// armorStandBroadcastEquip sends one slot of the stand's equipment to
// every spawned session in its dimension.
func (s *Server) armorStandBroadcastEquip(e *Entity, slot int32, it ItemStack) {
	body := encodeEquipment(e.ID, []struct {
		slot  byte
		stack ItemStack
	}{{byte(slot), it}})
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != e.Dim {
			return true
		}
		_ = other.write(other.ids().PlaySetEquipment, body)
		return true
	})
}

// armorStandEquipAll returns every occupied equipment slot for the
// spawn-time equipment packet.
func armorStandEquipAll(e *Entity) []struct {
	slot  byte
	stack ItemStack
} {
	var out []struct {
		slot  byte
		stack ItemStack
	}
	for i := int32(0); i <= 5; i++ {
		if it := e.Equips[i]; !it.Empty() {
			out = append(out, struct {
				slot  byte
				stack ItemStack
			}{byte(i), it})
		}
	}
	// EquipmentSlot.BODY is wire slot 6 — horse armor, wolf armor.
	if !e.BodyItem.Empty() {
		out = append(out, struct {
			slot  byte
			stack ItemStack
		}{6, e.BodyItem})
	}
	// SADDLE is wire slot 7 — the last EquipmentSlot.
	if e.Saddle {
		if sid, ok := world.ItemID("saddle"); ok {
			out = append(out, struct {
				slot  byte
				stack ItemStack
			}{7, ItemStack{ID: sid, Count: 1}})
		}
	}
	return out
}
