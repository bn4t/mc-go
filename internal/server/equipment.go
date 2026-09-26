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

// broadcastEquipment sends one player's equipment to every other
// spawned session in their dimension.
func (s *Server) broadcastEquipment(sess *Session) {
	s.criteriaFeed(sess, "armor")
	eid := sess.entityID.Load()
	if eid == 0 {
		return
	}
	dim := sess.dimName()
	body := encodeEquipment(eid, playerEquipment(sess))
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
