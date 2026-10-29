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
	if w != nil && !(w.chest != nil || w.ender != nil || w.craft != nil || w.container != nil && (w.container.spec.menu == menuGeneric9x3 || w.container.spec.menu == menuGeneric3x3 || w.container.spec.menu == menuShulkerBox)) {
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
	get := func(sl int) ItemStack {
		if w == nil {
			return sess.stackAt(int16(sl))
		}
		if w.craft != nil {
			return sess.craftMenuStack(w.craft, sl)
		}
		return sess.menuStack(sl)
	}
	put := func(sl int, it ItemStack) {
		if w == nil {
			sess.invMu.Lock()
			sess.inv[sl] = it
			sess.invMu.Unlock()
		} else if w.craft != nil {
			sess.setCraftMenuStack(w.craft, sl, it)
		} else {
			sess.setMenuStack(sl, it)
		}
	}
	cur := sess.carriedBuffer()
	if cur.Empty() || !get(int(slot)).Empty() {
		return true
	}
	limit := maxStackOfID(cur.ID)
	var touched []int
	for pass := 0; pass < 2 && cur.Count < limit; pass++ {
		for n := 0; n < size && cur.Count < limit; n++ {
			sl := n
			if button != 0 {
				sl = size - 1 - n
			}
			if (w == nil || w.craft != nil) && sl == 0 {
				continue
			}
			it := get(sl)
			if it.Empty() || !it.Stackable(cur) || pass == 0 && it.Count == maxStackOfID(it.ID) {
				continue
			}
			move := limit - cur.Count
			if move > it.Count {
				move = it.Count
			}
			cur.Count += move
			it.Count -= move
			if it.Count == 0 {
				it = ItemStack{}
			}
			put(sl, it)
			touched = append(touched, sl)
		}
	}
	if len(touched) == 0 {
		return true
	}
	s.setCarriedLocked(sess, cur)
	if w == nil {
		s.refreshCraftResult(sess)
		sess.invState.Add(1)
		_ = s.sendContainerContent(sess)
		_ = s.sendCarriedItem(sess, cur)
	} else if w.craft != nil {
		if s.refreshTableResult(sess, w.craft) {
			touched = append(touched, 0)
		}
		s.syncCraftTableSlots(sess, w, w.craft, touched)
	} else {
		s.syncContainerMenuSlots(sess, w, touched)
	}
	return true
}
