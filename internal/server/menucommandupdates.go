package server

import "mc-go/internal/world"

// Command slot writes are observed by the menu on the player's next tick,
// including while world simulation is frozen. Keep the pre-write value so
// repeated writes in one tick coalesce and a net no-op stays silent.
type menuCommandUpdates struct {
	slots               map[int]ItemStack
	furnaceCookTotal    int32
	hasFurnaceCookTotal bool
}

func menuRemoteStackEqual(a, b ItemStack) bool {
	// Bundle selection is client interaction state, not BundleContents equality.
	a.BundleSelected, b.BundleSelected = 0, 0
	return a.Count == b.Count && equipMatch(a, b)
}

func (s *Server) queueMenuCommandSlot(d *dimension, pos world.Pos, slot int, old ItemStack, cookTotal int32) {
	s.queueMenuBlockSlot(d, pos, slot, old, cookTotal, nil)
}

func (s *Server) queueMenuBlockSlot(d *dimension, pos world.Pos, slot int, old ItemStack, cookTotal int32, exclude *Session) {
	s.sessions.Range(func(_, value any) bool {
		sess := value.(*Session)
		if sess == exclude {
			return true
		}
		sess.windowMu.Lock()
		w := sess.window
		if w != nil && w.dim == d && w.ender == nil {
			mapped, matches := w.blockMenuSlot(pos, slot)
			if !matches || mapped >= w.containerSize() {
				sess.windowMu.Unlock()
				return true
			}
			slot := mapped
			if w.commandUpdates == nil {
				w.commandUpdates = &menuCommandUpdates{slots: make(map[int]ItemStack)}
			}
			pending := w.commandUpdates
			if _, ok := pending.slots[slot]; !ok {
				pending.slots[slot] = old
			}
			if w.furnace != nil && !pending.hasFurnaceCookTotal {
				pending.hasFurnaceCookTotal = true
				pending.furnaceCookTotal = cookTotal
			}
		}
		sess.windowMu.Unlock()
		return true
	})
}

func (s *Server) flushMenuCommandUpdates(sess *Session) {
	sess.windowMu.Lock()
	w := sess.window
	if w == nil || w.commandUpdates == nil {
		sess.windowMu.Unlock()
		return
	}
	pending := w.commandUpdates
	w.commandUpdates = nil
	sess.windowMu.Unlock()
	var changed []int
	// Menu broadcasts visit slots in their stable menu order.
	for slot := 0; slot < w.containerSize(); slot++ {
		old, ok := pending.slots[slot]
		if ok && !menuRemoteStackEqual(old, sess.menuStack(slot)) {
			changed = append(changed, slot)
		}
	}
	if len(changed) > 0 {
		s.syncContainerMenuSlots(sess, w, changed)
	}
	if pending.hasFurnaceCookTotal {
		w.dim.furnaces.mu.Lock()
		total := w.furnace.cookTotal
		w.dim.furnaces.mu.Unlock()
		if total != pending.furnaceCookTotal {
			s.sendContainerProperty(sess, w.id, 3, total)
		}
	}
}
