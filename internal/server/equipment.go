package server

import "mc-go/internal/protocol"

// Equipment slot ids for Set Equipment.
const (
	equipMainHand = 0
	equipOffHand  = 1
	equipBoots    = 2
	equipLegs     = 3
	equipChest    = 4
	equipHelmet   = 5
)

// playerEquipment snapshots a player's visible equipment: main hand,
// off hand, and the four armor slots.
func playerEquipment(sess *Session) []struct {
	slot  byte
	stack ItemStack
} {
	held := sess.held.Load()
	main := sess.stackAt(invSlotForHotbar(held))
	return []struct {
		slot  byte
		stack ItemStack
	}{
		{equipMainHand, main},
		{equipOffHand, sess.stackAt(slotOffhand)},
		{equipBoots, sess.stackAt(slotArmorStart + 3)},
		{equipLegs, sess.stackAt(slotArmorStart + 2)},
		{equipChest, sess.stackAt(slotArmorStart + 1)},
		{equipHelmet, sess.stackAt(slotArmorStart)},
	}
}

// encodeEquipment builds a Set Equipment body; the high bit of each
// slot byte marks that another entry follows.
func encodeEquipment(eid int32, entries []struct {
	slot  byte
	stack ItemStack
}) []byte {
	var b protocol.Buffer
	b.VarInt(eid)
	for i, e := range entries {
		slot := e.slot
		if i < len(entries)-1 {
			slot |= 0x80
		}
		b.Byte(slot)
		writeSlot(&b, e.stack)
	}
	return b.Bytes()
}

// equipMatch is ItemStack.matches: same item + same components,
// ignoring count — a stack that only changes count does not trigger
// an equipment update on vanilla either.
func equipMatch(a, b ItemStack) bool {
	if a.Empty() {
		a = ItemStack{}
	}
	if b.Empty() {
		b = ItemStack{}
	}
	a.Count, b.Count = 0, 0
	if a.Contents != nil && b.Contents != nil {
		if *a.Contents != *b.Contents {
			return false
		}
		a.Contents, b.Contents = nil, nil
	}
	if !bundleEqual(a.Bundle, b.Bundle) {
		return false
	}
	a.Bundle, b.Bundle = nil, nil
	return a == b
}

// broadcastEquipment sends one player's full equipment set to every
// other spawned session tracking them — vanilla's pairing burst — and
// refreshes the delta baseline detectEquipmentUpdates diffs against.
func (s *Server) broadcastEquipment(sess *Session) {
	s.criteriaFeed(sess, "armor")
	eid := sess.entityID.Load()
	if eid == 0 {
		return
	}
	cur := playerEquipment(sess)
	for _, c := range cur {
		sess.lastEquip[c.slot] = c.stack
	}
	sess.equipInit = true
	dim := sess.dimName()
	body := encodeEquipment(eid, cur)
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other == sess || other.state.Load() != StatePlay || !other.spawned.Load() {
			return true
		}
		if other.dimName() != dim || !other.tracksEntity(eid) {
			return true
		}
		_ = other.write(other.ids().PlaySetEquipment, body)
		return true
	})
}

// detectEquipmentUpdates is LivingEntity.detectEquipmentUpdates for a
// session player: each slot's stack is diffed against lastEquip and
// only the changed slots pack into one set_equipment. When both hands
// changed and each now matches the other's previous stack, vanilla
// emits SWAP_HANDS (entity_event 55) instead of equipment entries.
func (s *Server) detectEquipmentUpdates(sess *Session) {
	eid := sess.entityID.Load()
	if eid == 0 {
		return
	}
	cur := playerEquipment(sess)
	if !sess.equipInit {
		for _, c := range cur {
			sess.lastEquip[c.slot] = c.stack
		}
		sess.equipInit = true
		return
	}
	var changed [6]bool
	nchg := 0
	for _, c := range cur {
		if !equipMatch(sess.lastEquip[c.slot], c.stack) {
			changed[c.slot] = true
			nchg++
		}
	}
	if nchg == 0 {
		return
	}
	s.criteriaFeed(sess, "armor")
	dim := sess.dimName()
	if changed[equipMainHand] && changed[equipOffHand] &&
		equipMatch(cur[0].stack, sess.lastEquip[equipOffHand]) &&
		equipMatch(cur[1].stack, sess.lastEquip[equipMainHand]) {
		// The owner is excluded — sendToTrackingPlayers only.
		s.broadcastEntityEventTo(sess, dim, eid, 55)
		changed[equipMainHand], changed[equipOffHand] = false, false
		sess.lastEquip[equipMainHand] = cur[0].stack
		sess.lastEquip[equipOffHand] = cur[1].stack
	}
	var slots []struct {
		slot  byte
		stack ItemStack
	}
	for _, c := range cur {
		if !changed[c.slot] {
			continue
		}
		slots = append(slots, struct {
			slot  byte
			stack ItemStack
		}{c.slot, c.stack})
		sess.lastEquip[c.slot] = c.stack
	}
	if len(slots) == 0 {
		return
	}
	body := encodeEquipment(eid, slots)
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other == sess || other.state.Load() != StatePlay || !other.spawned.Load() {
			return true
		}
		if other.dimName() != dim || !other.tracksEntity(eid) {
			return true
		}
		_ = other.write(other.ids().PlaySetEquipment, body)
		return true
	})
}

// sendEquipmentTo sends every other online player's equipment to one
// joining session, limited to the joiner's dimension.
func (s *Server) sendEquipmentTo(joiner *Session) error {
	var first error
	dim := joiner.dimName()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other == joiner || other.state.Load() != StatePlay || !other.spawned.Load() {
			return true
		}
		if other.dimName() != dim {
			return true
		}
		eid := other.entityID.Load()
		if eid == 0 {
			return true
		}
		if err := joiner.write(joiner.ids().PlaySetEquipment, encodeEquipment(eid, playerEquipment(other))); err != nil && first == nil {
			first = err
		}
		return true
	})
	return first
}
