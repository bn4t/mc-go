package server

// AbstractContainerMenu's SWAP accepts hotbar indices 0..8 or inventory
// index 40 (offhand). Button 9 is not the offhand. Measured on 26.2's
// AbstractContainerMenu.doClick and pinned against its wire behavior.
func inventorySwapSlot(button byte) (int16, bool) {
	if button < 9 {
		return slotHotbarStart + int16(button), true
	}
	if button == 40 {
		return slotOffhand, true
	}
	return 0, false
}

// Offhand is absent from ordinary storage menus' visible slot array.
// Vanilla broadcasts only the touched menu slot; the offhand is visible in
// the next player-inventory synchronization, rather than a window-0 update.
func (s *Server) swapMenuOffhand(sess *Session, slot int) {
	old := sess.menuStack(slot)
	off := sess.stackAt(slotOffhand)
	sess.setMenuStack(slot, off)
	sess.setStack(slotOffhand, old)
}
