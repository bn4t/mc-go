package server

import "mc-go/internal/protocol"

// handlePickupAllPacket collects matching stacks for double-click (mode6).
// Measured with JDK25 javap AbstractContainerMenu PICKUP_ALL: collection
// starts only on an empty cell, visits partial stacks before full stacks,
// and reverses each scan for any nonzero button. Crafting result containers
// explicitly opt out. The wire differential pins both scan directions.
func (s *Server) handlePickupAllPacket(sess *Session, p protocol.Packet) bool {
	r := protocol.NewPacketReader(p.Data)
	win, e := r.VarInt()
	if e != nil {
		return false
	}
	st, e := r.VarInt()
	if e != nil {
		return false
	}
	slot, e := r.Short()
	if e != nil {
		return false
	}
	button, e := r.Byte()
	if e != nil {
		return false
	}
	mode, e := r.VarInt()
	if e != nil || mode != 6 {
		return false
	}
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	current := int32(0)
	if w != nil {
		current = w.id
	}
	if win != current {
		return true
	}
	// Other action menus require their own on-take callbacks. Keep their
	// existing dispatch until those callbacks join this adapter.
	if !gestureMenuSupported(w) {
		return false
	}
	if _, ok := sess.readChangedSlots(r); !ok {
		return true
	}
	if _, ok := sess.readHashedSlot(r); !ok || r.Remaining() != 0 {
		return true
	}
	if w == nil {
		if st != sess.invState.Load() {
			_ = s.sendContainerContent(sess)
			return true
		}
	} else if !s.checkClickState(sess, w, st) {
		return true
	}
	size := slotCount
	if w != nil {
		size = w.menuSize()
	}
	if slot < 0 || int(slot) >= size {
		return true
	}
	get := func(sl int) ItemStack { return gestureSlotStack(sess, w, sl) }
	var changes menuGestureChanges
	cur := sess.carriedBuffer()
	if cur.Empty() || !get(int(slot)).Empty() && gestureSlotMayPickup(sess, w, int(slot)) {
		return true
	}
	limit := maxStackOfID(cur.ID)
	for pass := 0; pass < 2 && cur.Count < limit; pass++ {
		for n := 0; n < size && cur.Count < limit; n++ {
			sl := n
			if button != 0 {
				sl = size - 1 - n
			}
			if !gestureSlotMayCollect(sess, w, sl) {
				continue
			}
			it := get(sl)
			if it.Empty() || !it.Stackable(cur) || pass == 0 && it.Count == maxStackOfID(it.ID) {
				continue
			}
			taken := s.gestureTakeSlot(sess, w, sl, it.Count, limit-cur.Count, &changes)
			cur.Count += taken.Count
		}
	}
	if len(changes.slots) == 0 {
		return true
	}
	s.setCarriedLocked(sess, cur)
	s.gestureSync(sess, w, &changes)
	return true
}
