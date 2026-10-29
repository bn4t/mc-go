package server

import "mc-go/internal/protocol"

func bundleMenuSupported(w *openWindow) bool { return gestureMenuSupported(w) }
func bundleMenuGet(sess *Session, w *openWindow, slot int) ItemStack {
	if w == nil {
		return sess.stackAt(int16(slot))
	}
	if w.craft != nil {
		return sess.craftMenuStack(w.craft, slot)
	}
	return sess.menuStack(slot)
}
func bundleMenuPut(sess *Session, w *openWindow, slot int, it ItemStack) {
	if w == nil {
		sess.setStack(int16(slot), it)
	} else if w.craft != nil {
		sess.setCraftMenuStack(w.craft, slot, it)
	} else {
		sess.setMenuStack(slot, it)
	}
}
func bundleMenuSlot(w *openWindow, slot int) bool {
	if w == nil {
		return slot > 0 && slot < slotCount
	}
	return slot >= 0 && slot < w.menuSize() && (w.craft == nil || slot != 0)
}
func (s *Server) selectBundleItem(sess *Session, slot, index int32) {
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if !bundleMenuSupported(w) || !bundleMenuSlot(w, int(slot)) {
		return
	}
	it := bundleMenuGet(sess, w, int(slot))
	bundleToggle(&it, index)
	bundleMenuPut(sess, w, int(slot), it)
}

// handleBundleClickPacket applies 26.2's bundle item click overrides before
// ordinary pickup. Typed slots reuse the shared menu filters, capacity,
// take callbacks and result refresh; crafting results retain their recipe path.
func (s *Server) handleBundleClickPacket(sess *Session, p protocol.Packet) bool {
	r := protocol.NewPacketReader(p.Data)
	win, err := r.VarInt()
	if err != nil {
		return false
	}
	state, err := r.VarInt()
	if err != nil {
		return false
	}
	slot, err := r.Short()
	if err != nil {
		return false
	}
	button, err := r.Byte()
	if err != nil {
		return false
	}
	mode, err := r.VarInt()
	if err != nil || mode != 0 || button > 1 {
		return false
	}
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	current := int32(0)
	if w != nil {
		current = w.id
	}
	if win != current || !bundleMenuSupported(w) || !bundleMenuSlot(w, int(slot)) {
		return false
	}
	cur := sess.carriedBuffer()
	target := bundleMenuGet(sess, w, int(slot))
	if !isBundle(cur) && !isBundle(target) {
		return false
	}
	if _, ok := sess.readChangedSlots(r); !ok {
		return true
	}
	if _, ok := sess.readHashedSlot(r); !ok || r.Remaining() != 0 {
		return true
	}
	if w == nil {
		if state != sess.invState.Load() {
			_ = s.sendContainerContent(sess)
			return true
		}
	} else if !s.checkClickState(sess, w, state) {
		return true
	}

	handled := false
	sound := ""
	var changes menuGestureChanges
	if isBundle(cur) {
		if button == 0 && !target.Empty() {
			handled = true
			probe, source := cur, target
			capacity := bundleInsert(&probe, &source)
			taken := s.gestureTakeSlot(sess, w, int(slot), target.Count, capacity, &changes)
			if bundleInsert(&cur, &taken) > 0 {
				sound = "insert"
			} else {
				sound = "insert_fail"
			}
		}
		if button == 1 && target.Empty() {
			handled = true
			removed := bundleRemove(&cur)
			remainder := removed
			if !removed.Empty() && gestureSlotMayPlace(sess, w, int(slot), removed) {
				count := min(removed.Count, gestureSlotMax(w, int(slot), removed))
				s.gestureSetSlot(sess, w, int(slot), removed.withCount(count), &changes)
				remainder.Count -= count
			}
			if !remainder.Empty() {
				bundleInsert(&cur, &remainder)
			} else if !removed.Empty() {
				sound = "remove_one"
			}
		}
	}
	if !handled && isBundle(target) {
		modifiable := gestureSlotMayPickup(sess, w, int(slot)) && gestureSlotMayPlace(sess, w, int(slot), target)
		if button == 0 && !cur.Empty() {
			handled = true
			if modifiable && bundleInsert(&target, &cur) > 0 {
				sound = "insert"
				s.gestureSetSlot(sess, w, int(slot), target, &changes)
			} else {
				sound = "insert_fail"
			}
		}
		if button == 1 && cur.Empty() {
			handled = true
			if modifiable {
				cur = bundleRemove(&target)
				if !cur.Empty() {
					sound = "remove_one"
					s.gestureSetSlot(sess, w, int(slot), target, &changes)
				}
			}
		}
		if !handled {
			bundleToggle(&target, -1)
			bundleMenuPut(sess, w, int(slot), target)
		}
	}
	if !handled {
		return false
	}
	s.setCarriedLocked(sess, cur)
	s.gestureSync(sess, w, &changes)

	if sound != "" {
		if id, ok := SoundEventID("item.bundle." + sound); ok {
			x, y, z, _, _, _ := sess.Pos()
			volume, pitch := float32(0.8), float32(0.8)+float32(s.rng.next()>>40)*(1.0/16777216.0)*0.4
			if sound == "insert_fail" {
				volume, pitch = 1, 1
			}
			s.playSoundSrc(sess.dimName(), x, y, z, id, 7, volume, pitch, sess)
		}
	}
	return true
}
