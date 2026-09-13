package server

import (
	"mc-go/internal/protocol"
)

// Anvil menu: repair-combine of two damaged tools of the same type,
// measured click by click against the real 26.2 server.
//
// The probed rules (tests/blackbox/probe_anvil*_test.go, since removed
// and pinned by TestAnvilParity):
//
//   - The menu is registry id 8 with 3 container slots (39 with the
//     player rows), title container.repair, and Container Property 0
//     carrying the current level cost (0 with no output).
//   - Both input slots take anything by hand (dirt sits in slot 0
//     without complaint); only the output computation cares what they
//     hold.
//   - Two damageables of the same item in slots 0+1 compute an output
//     in slot 2 at once, with two slot updates (input, then output)
//     and the cost property. A dirt+pick pair, a lone input, or two
//     fresh (full-durability) picks compute nothing and the cost stays
//     0. The full-durability refusal follows the public anvil rules
//     (no repair to do, no enchantments to merge, no rename pending);
//     mc-go models no enchantments, so any full-durability target
//     refuses.
//   - The output's damage is max minus (remaining target + remaining
//     sacrifice + 12% of max), floored at 0 and therefore omitted from
//     the slot bytes when fully repaired; its repair_cost is
//     max(target, sacrifice)+1. The cost is both penalties + 2 for the
//     repair (0+0+2 for a fresh pair, the only case the probes pin;
//     the penalty terms follow the public prior-work table).
//   - Taking the output plays level event 1030 at the anvil and
//     answers Container Content (inputs cleared, output on the cursor
//     or quick-moved backward into the player side) plus the zeroed
//     cost. Survival deducts the cost in levels and syncs Set
//     Experience; creative sends no XP packet. Taking with too few
//     levels is a silent full resync: nothing moves, the cost stays,
//     no chat, no sound.
//   - Clicking the empty output with something on the cursor only
//     echoes the cursor; with an empty cursor it is silent. Placing
//     into a non-empty output is silently ignored (no resync, unlike
//     the furnace).
//   - Closing returns the inputs to the player inventory forward from
//     the hotbar, each stack as Set Player Inventory plus a window-0
//     slot update, and leaves the cursor alone. The client-initiated
//     close itself is never echoed (no Container Close).
//
// Still open behind this: renaming (the item_name component id is
// unmeasured, so RenameItem only stores the pending name), unit repair
// with materials, the 12% anvil-degrade roll, costs past 39, and
// quick-move/throw/number-key handling of the output beyond what the
// parity script pins.

// anvilMaxCost is the highest level cost Survival/Adventure will pay;
// past it the operation is refused. Creative ignores the cap. Both the
// threshold and the creative exemption are the public anvil rules; the
// probes never priced past 2, so only the refusal path is pinned.
const anvilMaxCost = 39

// anvilCombine prices two inputs as a repair-combine. It reports the
// output stack and the level cost, or ok=false when the pair computes
// nothing.
func anvilCombine(target, sacrifice ItemStack) (out ItemStack, cost int32, ok bool) {
	if target.Empty() || sacrifice.Empty() || target.ID != sacrifice.ID {
		return ItemStack{}, 0, false
	}
	maxDmg := MaxDamageOfID(target.ID)
	if maxDmg <= 0 {
		return ItemStack{}, 0, false
	}
	if target.Damage <= 0 {
		// Nothing to repair and (unmodeled) nothing to merge.
		return ItemStack{}, 0, false
	}
	remT := maxDmg - target.Damage
	if remT < 0 {
		remT = 0
	}
	remS := maxDmg - sacrifice.Damage
	if remS < 0 {
		remS = 0
	}
	// The repair bonus is 12% of max durability (a 225+225 iron-pair
	// leaves damage 170: 25+25+30 remaining of 250).
	newRem := remT + remS + maxDmg*12/100
	if newRem > maxDmg {
		newRem = maxDmg
	}
	rc := target.RepairCost
	if sacrifice.RepairCost > rc {
		rc = sacrifice.RepairCost
	}
	out = ItemStack{ID: target.ID, Count: 1, Damage: maxDmg - newRem, RepairCost: rc + 1}
	cost = target.RepairCost + sacrifice.RepairCost + 2
	return out, cost, true
}

// recomputeAnvil re-derives the anvil's output slot and level cost
// from its inputs. It reports whether either changed.
func recomputeAnvil(c *containerData) (outChanged, costChanged bool) {
	out, cost, ok := anvilCombine(c.slots[anvilLeft], c.slots[anvilRight])
	if !ok {
		out = ItemStack{}
		cost = 0
	}
	if c.slots[anvilResult] != out {
		c.slots[anvilResult] = out
		outChanged = true
	}
	if c.anvilCost != cost {
		c.anvilCost = cost
		costChanged = true
	}
	return outChanged, costChanged
}

// sendAnvilCost syncs the anvil's level-cost property.
func (s *Server) sendAnvilCost(sess *Session, w *openWindow, c *containerData) {
	w.dim.containers.mu.Lock()
	cost := c.anvilCost
	w.dim.containers.mu.Unlock()
	s.sendContainerProperty(sess, w.id, 0, cost)
}

// sendAnvilLevelEvent plays the anvil-use sound at the block.
func (s *Server) sendAnvilLevelEvent(sess *Session, w *openWindow) {
	var b protocol.Buffer
	b.Int(1030) // level event: anvil use
	b.Position(int32(w.pos.X), int32(w.pos.Y), int32(w.pos.Z))
	b.Int(0)
	b.Bool(false)
	_ = sess.write(sess.ids().PlayLevelEvent, b.Bytes())
}

// sendPlayerInventorySlot syncs one player-inventory slot by its
// container index (0-8 hotbar, 9-35 main): the Set Player Inventory
// packet vanilla pairs with the window-0 update when it moves a stack
// outside a click, measured on anvil close.
func (s *Server) sendPlayerInventorySlot(sess *Session, index int32, it ItemStack) {
	var b protocol.Buffer
	b.VarInt(index)
	writeSlot(&b, it)
	_ = sess.write(sess.ids().PlaySetPlayerInventory, b.Bytes())
}

// playerContainerIndex maps a window-0 slot to the player-inventory
// container index Set Player Inventory uses, or -1 when the slot has
// none (crafting, armor, offhand). The hotbar/main halves are the
// /item registry the slot probes pinned: hotbar.N is container.N,
// inventory.N is container.9+N.
func playerContainerIndex(w0 int16) int32 {
	switch {
	case w0 >= slotHotbarStart && w0 < slotHotbarStart+9:
		return int32(w0 - slotHotbarStart)
	case w0 >= slotMainStart && w0 < slotHotbarStart:
		return int32(w0)
	}
	return -1
}

// handleAnvilClick implements click modes 0 (pickup/place/take), 1
// (quick move), 2 (hotbar swap), 3 (creative clone) and 4 (throw) for
// the anvil menu. Input slots behave like storage and recompute the
// output; the result slot takes with the measured payment rules.
func (s *Server) handleAnvilClick(sess *Session, w *openWindow, c *containerData, slot int16, button byte, mode int32) {
	if slot < 0 || int(slot) >= w.menuSize() {
		return
	}
	sess.invMu.RLock()
	cur := sess.carried
	sess.invMu.RUnlock()

	switch mode {
	case 0:
		s.anvilClick(sess, w, c, int(slot), button, cur)
	case 1:
		s.anvilQuickMove(sess, w, c, int(slot))
	case 2:
		// Hotbar swaps ignore the accept rules, like manual
		// placement; the result slot never swaps.
		swap := w.menuSize() - 9 + int(button)
		if int(slot) == anvilResult || swap < w.menuSize()-9 || swap >= w.menuSize() {
			break
		}
		a := sess.menuStack(int(slot))
		b := sess.menuStack(swap)
		sess.setMenuStack(int(slot), b)
		sess.setMenuStack(swap, a)
		s.anvilSyncInput(sess, w, c, []int{int(slot), swap})
	case 3:
		if out, ok := creativeClone(sess, sess.menuStack(int(slot)), button); ok {
			s.setCarriedLocked(sess, out)
			s.syncContainerMenuSlots(sess, w, []int{int(slot)})
			break
		}
		s.sendContainerMenuContent(sess)
	case 4:
		it := sess.menuStack(int(slot))
		if it.Empty() {
			break
		}
		drop := it
		if button == 0 {
			drop = it.withCount(1)
		}
		it.Count -= drop.Count
		if it.Count <= 0 {
			it = ItemStack{}
		}
		sess.setMenuStack(int(slot), it)
		s.dropFromMenu(sess, drop)
		s.anvilSyncInput(sess, w, c, []int{int(slot)})
	default:
		s.sendContainerMenuContent(sess)
	}
}

// anvilClick applies a mode-0 click: storage semantics on every slot
// but the result, the take rules on the result.
func (s *Server) anvilClick(sess *Session, w *openWindow, c *containerData, slot int, button byte, cur ItemStack) {
	if slot == anvilResult {
		s.takeAnvilResult(sess, w, c, cur, false)
		return
	}
	target := sess.menuStack(slot)
	if button != 0 {
		cur, target = clickRight(cur, target)
	} else {
		cur, target = clickLeft(cur, target, c.spec, slot)
	}
	sess.setMenuStack(slot, target)
	s.setCarriedLocked(sess, cur)
	s.anvilSyncInput(sess, w, c, []int{slot})
}

// anvilSyncInput resyncs after an input-side change: the touched slots
// plus a recomputed output, the cost when it moved, and the cursor
// when it is non-empty (vanilla omits the cursor packet when it
// empties).
func (s *Server) anvilSyncInput(sess *Session, w *openWindow, c *containerData, touched []int) {
	w.dim.containers.mu.Lock()
	outChanged, costChanged := recomputeAnvil(w.container)
	w.dim.containers.mu.Unlock()
	if outChanged {
		touched = append(touched, anvilResult)
	}
	sess.windowMu.Lock()
	sess.windowState++
	sess.windowMu.Unlock()
	seen := map[int]bool{}
	for _, sl := range touched {
		if seen[sl] {
			continue
		}
		seen[sl] = true
		var b protocol.Buffer
		b.VarInt(w.id)
		b.VarInt(sess.windowState)
		b.Short(int16(sl))
		writeSlot(&b, sess.menuStack(sl))
		_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
	}
	sess.invMu.RLock()
	cur := sess.carried
	sess.invMu.RUnlock()
	if !cur.Empty() {
		_ = s.sendCarriedItem(sess, cur)
	}
	if costChanged {
		s.sendAnvilCost(sess, w, w.container)
	}
}

// takeAnvilResult runs a mode-0 take (or mode-1 quick-move take when
// quick is set) of the output slot.
func (s *Server) takeAnvilResult(sess *Session, w *openWindow, c *containerData, cur ItemStack, quick bool) {
	w.dim.containers.mu.Lock()
	out := c.slots[anvilResult]
	cost := c.anvilCost
	w.dim.containers.mu.Unlock()
	if out.Empty() {
		// An empty result takes nothing: the cursor is echoed when
		// it holds something, and the click is silent otherwise.
		if !cur.Empty() {
			_ = s.sendCarriedItem(sess, cur)
		}
		return
	}
	if !quick && !cur.Empty() {
		// Manual placement into the output is silently ignored.
		return
	}
	free := sess.mode() == GameModeCreative
	if !free && (sess.xpLevel < cost || cost > anvilMaxCost) {
		// Too poor (or too expensive): nothing moves, the whole
		// window resyncs carrying the live state, with no chat, no
		// sound, and the cost left standing.
		s.sendContainerMenuContent(sess)
		s.sendAnvilCost(sess, w, c)
		return
	}
	s.sendAnvilLevelEvent(sess, w)
	w.dim.containers.mu.Lock()
	for _, sl := range []int{anvilLeft, anvilRight} {
		st := c.slots[sl]
		st.Count--
		if st.Count <= 0 {
			st = ItemStack{}
		}
		c.slots[sl] = st
	}
	c.slots[anvilResult] = ItemStack{}
	c.anvilCost = 0
	w.dim.containers.mu.Unlock()
	if quick {
		sess.setMenuStack(anvilResult, ItemStack{})
		moving := out
		var touched []int
		quickMoveIntoPlayer(sess, c, &moving, &touched)
		if !moving.Empty() {
			// Nowhere fits: park the remainder back in the
			// output, which the recompute below clears anyway.
			sess.setMenuStack(anvilResult, moving)
		}
	} else {
		s.setCarriedLocked(sess, out)
	}
	sess.windowMu.Lock()
	sess.windowState++
	sess.windowMu.Unlock()
	s.sendContainerMenuContent(sess)
	s.sendAnvilCost(sess, w, c)
	if !free {
		sess.xpLevel -= cost
		_ = sess.server.sendSetExperience(sess, sess.xpProgress, sess.xpLevel, sess.xpTotal)
	}
}

// anvilQuickMove applies a mode-1 click: the output takes with
// payment, anything else rides the generic container/player paths.
func (s *Server) anvilQuickMove(sess *Session, w *openWindow, c *containerData, slot int) {
	if slot == anvilResult {
		sess.invMu.RLock()
		cur := sess.carried
		sess.invMu.RUnlock()
		s.takeAnvilResult(sess, w, c, cur, true)
		return
	}
	moving := sess.menuStack(slot)
	if moving.Empty() {
		return
	}
	touched := []int{slot}
	if slot < c.spec.slots {
		quickMoveIntoPlayer(sess, c, &moving, &touched)
	} else {
		quickMoveIntoContainer(sess, c, &moving, &touched)
	}
	sess.setMenuStack(slot, moving)
	s.anvilSyncInput(sess, w, c, touched)
}

// closeAnvil retires an anvil menu: the anvil keeps no storage, so the
// inputs return to the player inventory forward from the hotbar, each
// as Set Player Inventory plus a window-0 update, and the cursor is
// left alone. The output slot is virtual (recompute derives it from
// the inputs), so it clears without returning: sending it back would
// duplicate the inputs. A remainder that fits nowhere parks back in
// its slot.
func (s *Server) closeAnvil(sess *Session, w *openWindow, c *containerData) {
	for _, sl := range []int{anvilLeft, anvilRight} {
		w.dim.containers.mu.Lock()
		st := c.slots[sl]
		w.dim.containers.mu.Unlock()
		if st.Empty() {
			continue
		}
		w.dim.containers.mu.Lock()
		c.slots[sl] = ItemStack{}
		c.anvilCost = 0
		w.dim.containers.mu.Unlock()
		rest, _ := sess.addStack(st)
		if !rest.Empty() {
			w.dim.containers.mu.Lock()
			c.slots[sl] = rest
			w.dim.containers.mu.Unlock()
			continue
		}
		// addStack syncs the window-0 slot itself; pair it with
		// the Set Player Inventory the real server sends. The
		// destination is the last stackable-or-empty slot addStack
		// touched: re-derive it by scan order is overkill, so find
		// the moved stack by value.
		s.sendAnvilReturnSlot(sess, st)
	}
	// The output never existed as an item, so it clears without
	// returning; the next open recomputes it from empty inputs.
	w.dim.containers.mu.Lock()
	c.slots[anvilResult] = ItemStack{}
	c.anvilCost = 0
	w.dim.containers.mu.Unlock()
}

// sendAnvilReturnSlot pairs a close-returned stack with its Set Player
// Inventory update, locating the window-0 slot addStack filled. Stacks
// merge, so the search matches by value and prefers the hotbar, the
// order addStack fills.
func (s *Server) sendAnvilReturnSlot(sess *Session, moved ItemStack) {
	sess.invMu.RLock()
	defer sess.invMu.RUnlock()
	best := int16(-1)
	for i := int16(slotHotbarStart); i <= slotOffhand; i++ {
		if sess.inv[i] == moved {
			best = i
			break
		}
	}
	if best < 0 {
		for i := int16(slotMainStart); i < slotHotbarStart; i++ {
			if sess.inv[i] == moved {
				best = i
				break
			}
		}
	}
	if best < 0 {
		return
	}
	if idx := playerContainerIndex(best); idx >= 0 {
		s.sendPlayerInventorySlot(sess, idx, moved)
	}
}

// openWindowKindAnvil reports whether an open window is an anvil menu.
func openWindowKindAnvil(w *openWindow) bool {
	return w != nil && w.container != nil && w.container.spec.menu == menuAnvil
}
