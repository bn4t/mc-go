package server

import "mc-go/internal/world"

func trappedChestPower(d *dimension, pos world.Pos) int {
	d.chests.mu.Lock()
	defer d.chests.mu.Unlock()
	if chest := d.chests.chests[pos]; chest != nil {
		return min(15, max(0, chest.openers))
	}
	return 0
}

func (s *Server) chestViewOpen(sess *Session, w *openWindow) {
	if w.chest == nil || sess.mode() == GameModeSpectator {
		return
	}
	name, _ := world.StateOwner(w.dim.w.GetBlock(w.pos))
	if name == "trapped_chest" {
		sess.addCustomStat("trigger_trapped_chest", 1)
	} else {
		sess.addCustomStat("open_chest", 1)
	}
	w.chestOpenCounted = true
	s.chestViewOpeners(w, 1)
}

func (s *Server) chestViewClose(w *openWindow) {
	if !w.chestOpenCounted {
		return
	}
	w.chestOpenCounted = false
	s.chestViewOpeners(w, -1)
}

func (s *Server) chestViewOpeners(w *openWindow, delta int) {
	var positions []world.Pos
	w.dim.chests.mu.Lock()
	w.chest.openers = max(0, w.chest.openers+delta)
	first := w.pos
	if w.chestOther != nil {
		first = w.chestFirstPos
		w.chestOther.openers = max(0, w.chestOther.openers+delta)
	}
	positions = append(positions, first)
	if w.chestOther != nil {
		positions = append(positions, w.chestOtherPos)
	}
	w.dim.chests.mu.Unlock()
	for _, pos := range positions {
		name, _ := world.StateOwner(w.dim.w.GetBlock(pos))
		if name != "trapped_chest" {
			continue
		}
		// TrappedChestBlockEntity notifies its own neighbors and the block
		// below when the opener count changes. Strong power points downward.
		s.rsNeighborFanOut(w.dim, pos)
		s.rsNeighborFanOut(w.dim, world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z})
	}
}
