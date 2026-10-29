package server

// pickItem selects an existing identical plain stack, moves a main-inventory
// stack to a suitable hotbar slot, or creates it for creative players. Measured
// on 26.2 ServerGamePacketListenerImpl.tryPickItem and Inventory's pick helpers:
// selection searches hotbar before main inventory, empty hotbar slots wrap from
// the current selection, then unenchanted slots are preferred. The cursor is
// independent of this operation.
func (sess *Session) pickItem(itemID int32) {
	if itemID == 0 {
		return
	}
	target := ItemStack{ID: itemID, Count: 1}
	sess.invMu.Lock()
	selected := sess.held.Load()
	found := int16(-1)
	for i := int16(0); i < 36; i++ {
		slot := i
		if i < 9 {
			slot = slotHotbarStart + i
		}
		if target.Stackable(sess.inv[slot]) {
			found = slot
			break
		}
	}
	changed := map[int16]ItemStack{}
	if found >= slotHotbarStart {
		selected = int32(found - slotHotbarStart)
	} else if found >= 0 || sess.mode() == GameModeCreative {
		suitable := selected
		emptyFound := false
		for i := int32(0); i < 9; i++ {
			h := (selected + i) % 9
			if sess.inv[slotHotbarStart+h].Empty() {
				suitable = h
				emptyFound = true
				break
			}
		}
		if !emptyFound {
			for i := int32(0); i < 9; i++ {
				h := (selected + i) % 9
				if sess.inv[slotHotbarStart+h].Ench == "" {
					suitable = h
					break
				}
			}
		}
		selected = suitable
		dest := int16(slotHotbarStart + selected)
		old := sess.inv[dest]
		if found >= 0 {
			sess.inv[dest], sess.inv[found] = sess.inv[found], old
			changed[found] = old
		} else {
			if !old.Empty() {
				for i := int16(slotMainStart); i < slotHotbarStart; i++ {
					if sess.inv[i].Empty() {
						sess.inv[i] = old
						changed[i] = old
						break
					}
				}
			}
			sess.inv[dest] = target
		}
		changed[dest] = sess.inv[dest]
	}
	sess.invMu.Unlock()
	sess.selectHotbar(selected)
	// Stable player-window order keeps the updates deterministic.
	for slot := int16(slotMainStart); slot < slotOffhand; slot++ {
		if it, ok := changed[slot]; ok {
			_ = sess.server.sendContainerSlot(sess, slot, it)
		}
	}
}
