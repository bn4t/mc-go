package server

// Removing a player retires both its persistent inventory crafting grid
// and any open menu. This happens on disconnect or old-player removal at
// respawn, before the position changes; death itself leaves the menu live.
func (s *Server) removePlayerMenus(sess *Session) {
	for slot := int16(slotCraftStart); slot < slotCraftStart+4; slot++ {
		st := sess.stackAt(slot)
		sess.setStack(slot, ItemStack{})
		s.dropMenuItem(sess, st)
	}
	sess.setStack(slotCraftResult, ItemStack{})
	sess.windowMu.Lock()
	w := sess.window
	sess.window = nil
	sess.windowMu.Unlock()
	sess.quickCraft = quickCraft{}
	if w != nil {
		s.menuRemoved(sess, w, true)
	} else {
		s.dropCarried(sess)
	}
}
