package server

import "mc-go/internal/protocol"

// quickCraft tracks a single menu's drag gesture. The simulation owner
// handles click packets; a menu change or any intervening click retires it.
// Measured with javap25 on AbstractContainerMenu's quick-craft operations.
type quickCraft struct {
	menu   int32
	kind   byte
	active bool
	slots  []int
}

// handleQuickCraftPacket intercepts drag gestures before ordinary click
// handlers reject the outside-slot start/end markers. Storage and crafting
// menus share distribution; other action menus retain their own click paths.
func (s *Server) handleQuickCraftPacket(sess *Session, p protocol.Packet) bool {
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
	if e != nil {
		return false
	}
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	current := int32(0)
	if w != nil {
		current = w.id
	}
	q := &sess.quickCraft
	if q.active && q.menu != current {
		*q = quickCraft{}
	}
	if mode != 5 {
		if q.active {
			*q = quickCraft{}
			return true
		}
		return false
	}
	if win != current {
		return true
	}
	if _, ok := sess.readChangedSlots(r); !ok {
		return true
	}
	if _, ok := sess.readHashedSlot(r); !ok || r.Remaining() != 0 {
		return true
	}
	if win == 0 {
		if st != sess.invState.Load() {
			_ = s.sendContainerContent(sess)
			return true
		}
	} else if !s.checkClickState(sess, w, st) {
		return true
	}
	// Storage and crafting adapters share placement; other action/equipment
	// menus need their own callbacks before joining this path.
	if w != nil && !(w.chest != nil || w.ender != nil || w.craft != nil || w.furnace != nil || w.hopper != nil || w.container != nil && (w.container.spec.menu == menuGeneric9x3 || w.container.spec.menu == menuGeneric3x3 || w.container.spec.menu == menuShulkerBox || w.container.spec.brew)) {
		*q = quickCraft{}
		return true
	}
	cur := sess.carriedBuffer()
	if cur.Empty() {
		*q = quickCraft{}
		return true
	}
	phase := button & 3
	if phase == 0 {
		if q.active {
			*q = quickCraft{}
			return true
		}
		kind := (button >> 2) & 3
		if kind > 2 || kind == 2 && sess.mode() != 1 {
			*q = quickCraft{}
			return true
		}
		*q = quickCraft{menu: win, kind: kind, active: true}
		return true
	}
	if !q.active {
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
	accepts := func(sl int) bool {
		if w == nil {
			return sl >= int(slotCraftStart) && sl < int(slotCraftStart)+4 ||
				sl >= int(slotMainStart) && sl < slotCount
		} // grid, main, hotbar, offhand
		if sl < 0 || sl >= w.menuSize() {
			return false
		}
		if w.craft != nil {
			return sl != 0
		}
		if w.furnace != nil && sl < furnaceSlots {
			// Furnace input is an ordinary slot; fuel alone is filtered.
			name, _ := itemNameOf(cur.ID)
			return sl != furnaceOutput && (sl != furnaceFuel || isFuelItem(cur.ID) || name == "bucket")
		}
		return w.container == nil || menuAccepts(w.container.spec, sl, cur)
	}
	limit := func(sl int) int32 {
		if w != nil && w.furnace != nil && sl == furnaceFuel {
			if name, _ := itemNameOf(cur.ID); name == "bucket" {
				return 1
			}
		}
		if w != nil && w.container != nil {
			return w.container.spec.slotMax(sl, cur)
		}
		return maxStackOfID(cur.ID)
	}
	eligible := func(sl int) bool {
		if !accepts(sl) {
			return false
		}
		it := get(sl)
		return it.Empty() || it.Stackable(cur) && it.Count <= maxStackOfID(cur.ID)
	}
	if phase == 1 {
		sl := int(slot)
		if !eligible(sl) || q.kind != 2 && cur.Count <= int32(len(q.slots)) {
			return true
		}
		for _, old := range q.slots {
			if old == sl {
				return true
			}
		}
		q.slots = append(q.slots, sl)
		return true
	}
	if phase != 2 {
		*q = quickCraft{}
		return true
	}
	slots, kind := q.slots, q.kind
	*q = quickCraft{}
	var touched []int
	share := cur.Count
	if len(slots) > 1 && kind == 0 {
		share = cur.Count / int32(len(slots))
	}
	if kind == 1 {
		share = 1
	}
	if kind == 2 {
		share = maxStackOfID(cur.ID)
	}
	// Vanilla delegates a single selected cell to ordinary pickup. Its
	// creative middle-button pickup is inert, even in creative mode.
	if len(slots) == 1 && kind == 2 {
		return true
	}
	remaining := cur.Count
	for _, sl := range slots {
		if !eligible(sl) || kind != 2 && cur.Count < int32(len(slots)) {
			continue
		}
		old := get(sl)
		count := old.Count + share
		if cap := limit(sl); count > cap {
			count = cap
		}
		added := count - old.Count
		if added <= 0 {
			continue
		}
		put(sl, cur.withCount(count))
		remaining -= added
		touched = append(touched, sl)
	}
	if len(touched) == 0 {
		return true
	}
	if remaining <= 0 {
		cur = ItemStack{}
	} else {
		cur.Count = remaining
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
		if w.container != nil && w.container.spec.brew {
			s.updateBrewBlockState(w.dim, w.pos, brewPotionBits(w.container))
		}
	}
	return true
}
