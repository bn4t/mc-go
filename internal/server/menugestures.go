package server

// menuGestureChanges collects one click's slot changes. Results recompute
// immediately after each input mutation, while wire updates coalesce at end.
// The adapters are also usable by item overrides that invoke Slot.safeTake.
type menuGestureChanges struct {
	slots        []int
	anvilCost    bool
	anvilDegrade bool
}

func gestureMenuSupported(w *openWindow) bool {
	if w == nil || w.chest != nil || w.ender != nil || w.furnace != nil || w.hopper != nil || w.craft != nil {
		return true
	}
	if w.container == nil {
		return false
	}
	switch w.container.spec.menu {
	case menuGeneric9x3, menuGeneric3x3, menuShulkerBox, menuBrewingStand, menuAnvil, menuSmithing, menuGrindstone, menuStonecutter, menuLoom, menuCartographyTable, menuEnchantment, menuBeacon:
		return true
	}
	return false
}

func gestureSlotStack(sess *Session, w *openWindow, slot int) ItemStack {
	if w == nil {
		return sess.stackAt(int16(slot))
	}
	if w.craft != nil {
		return sess.craftMenuStack(w.craft, slot)
	}
	return sess.menuStack(slot)
}

// Placement uses the live menu's filters; armor is a one-item equippable
// slot, crafter disabled cells reject placement, and results are take-only.
func gestureSlotMayPlace(sess *Session, w *openWindow, slot int, it ItemStack) bool {
	if it.Empty() || sess.mode() == GameModeSpectator {
		return false
	}
	if w == nil {
		if slot < slotCraftStart || slot >= slotCount {
			return false
		}
		if slot >= slotArmorStart && slot < slotArmorStart+4 {
			name, _ := itemNameOf(it.ID)
			eq, ok := dispenseEquipSlot[name]
			return ok && eq == equipHelmet-int32(slot-slotArmorStart)
		}
		return true
	}
	if slot < 0 || slot >= w.menuSize() {
		return false
	}
	if w.craft != nil {
		return slot != 0
	}
	if w.furnace != nil && slot < furnaceSlots {
		name, _ := itemNameOf(it.ID)
		return slot != furnaceOutput && (slot != furnaceFuel || isFuelItem(it.ID) || name == "bucket")
	}
	if w.container != nil {
		c := w.container
		if c.spec.crafter && slot < c.spec.slots && c.disabled&(1<<uint(slot)) != 0 {
			return false
		}
		return menuAccepts(c.spec, slot, it)
	}
	return true
}

func gestureSlotMax(w *openWindow, slot int, it ItemStack) int32 {
	if w == nil && slot >= slotArmorStart && slot < slotArmorStart+4 {
		return 1
	}
	if w != nil && w.furnace != nil && slot == furnaceFuel {
		if name, _ := itemNameOf(it.ID); name == "bucket" {
			return 1
		}
	}
	if w != nil && w.container != nil {
		return w.container.spec.slotMax(slot, it)
	}
	return maxStackOfID(it.ID)
}

func gestureSlotMayPickup(sess *Session, w *openWindow, slot int) bool {
	size := slotCount
	if w != nil {
		size = w.menuSize()
	}
	if slot < 0 || slot >= size {
		return false
	}
	if sess.mode() == GameModeSpectator {
		return false
	}
	if w == nil && slot >= slotArmorStart && slot < slotArmorStart+4 {
		return sess.mode() == GameModeCreative || gestureSlotStack(sess, w, slot).enchLevel("binding_curse") == 0
	}
	if w != nil && w.container != nil && w.container.spec.menu == menuAnvil && slot == anvilResult {
		cost := w.container.anvilCost
		return cost > 0 && (sess.mode() == GameModeCreative || sess.xpLevel >= cost)
	}
	return true
}

// AbstractContainerMenu permits collection by default. Inventory/crafting,
// smithing, stonecutting and cartography explicitly exclude their results;
// anvil, grindstone and loom do not. Measured with javap25 on 26.2 menus.
func gestureSlotMayCollect(sess *Session, w *openWindow, slot int) bool {
	if !gestureSlotMayPickup(sess, w, slot) {
		return false
	}
	if w == nil || w.craft != nil {
		return slot != 0
	}
	if w.container != nil && w.container.spec.isResult(slot) {
		switch w.container.spec.menu {
		case menuSmithing, menuStonecutter, menuCartographyTable:
			return false
		}
	}
	return true
}

func (s *Server) gestureRawSetSlot(sess *Session, w *openWindow, slot int, it ItemStack, changes *menuGestureChanges) {
	if w == nil {
		sess.setStack(int16(slot), it)
	} else if w.craft != nil {
		sess.setCraftMenuStack(w.craft, slot, it)
	} else {
		sess.setMenuStack(slot, it)
	}
	changes.slots = append(changes.slots, slot)
}

func (s *Server) gestureSetSlot(sess *Session, w *openWindow, slot int, it ItemStack, changes *menuGestureChanges) {
	s.gestureRawSetSlot(sess, w, slot, it, changes)
	s.gestureRefresh(sess, w, changes)
}

// gestureTakeSlot implements Slot.safeTake's two limits. A take-only slot
// refuses the whole operation when capacity cannot hold its entire stack,
// even when the requested amount itself would fit (Slot.tryRemove).
func (s *Server) gestureTakeSlot(sess *Session, w *openWindow, slot int, requested, capacity int32, changes *menuGestureChanges) ItemStack {
	old := gestureSlotStack(sess, w, slot)
	if old.Empty() || !gestureSlotMayPickup(sess, w, slot) {
		return ItemStack{}
	}
	if !gestureSlotMayPlace(sess, w, slot, old) && capacity < old.Count {
		return ItemStack{}
	}
	if slot == 0 && (w == nil || w.craft != nil) {
		return ItemStack{}
	} // crafting results use their existing recipe adapter
	amount := min(old.Count, min(requested, capacity))
	if amount <= 0 {
		return ItemStack{}
	}
	taken := old.withCount(amount)
	rest := old.withCount(old.Count - amount)
	if rest.Empty() {
		rest = ItemStack{}
	}
	s.gestureRawSetSlot(sess, w, slot, rest, changes)
	if w != nil && w.furnace != nil && slot == furnaceOutput {
		s.furnaceOutputTaken(sess, w, taken)
	}
	if w != nil && w.container != nil {
		c := w.container
		if c.spec.brew && slot <= brewPotionLast && taken.Potion != "" {
			s.fireBrewedPotion(sess, taken)
		}
		if c.spec.isResult(slot) {
			if c.spec.menu == menuAnvil {
				s.gestureAnvilTaken(sess, w, changes)
			} else {
				if c.spec.menu == menuCartographyTable {
					s.cartographyApplyPost(sess, &taken)
				}
				changes.slots = append(changes.slots, s.typedResultTaken(sess, w, taken)...)
			}
		}
	}
	s.gestureRefresh(sess, w, changes)
	return taken
}

func (s *Server) gestureAnvilTaken(sess *Session, w *openWindow, changes *menuGestureChanges) {
	c := w.container
	w.dim.containers.mu.Lock()
	cost, use := c.anvilCost, c.anvilUse
	for i, slot := range []int{anvilLeft, anvilRight} {
		it := c.slots[slot]
		it.Count -= use[i]
		if it.Empty() {
			it = ItemStack{}
		}
		c.slots[slot] = it
	}
	c.slots[anvilResult] = ItemStack{}
	c.anvilCost = 0
	c.anvilUse = [2]int32{}
	w.dim.containers.mu.Unlock()
	changes.slots = append(changes.slots, anvilLeft, anvilRight, anvilResult)
	changes.anvilCost = true
	s.sendAnvilLevelEvent(sess, w)
	if sess.mode() != GameModeCreative {
		sess.giveExperienceLevels(-cost)
		changes.anvilDegrade = true
	}
}

func (s *Server) gestureRefresh(sess *Session, w *openWindow, changes *menuGestureChanges) {
	if w == nil || w.craft != nil || w.container == nil {
		return
	}
	c := w.container
	switch c.spec.menu {
	case menuAnvil:
		w.dim.containers.mu.Lock()
		out, cost := recomputeAnvil(sess, w, c)
		w.dim.containers.mu.Unlock()
		if out {
			changes.slots = append(changes.slots, anvilResult)
		}
		changes.anvilCost = changes.anvilCost || cost
	case menuSmithing:
		out, _ := smithingCompute(sess.menuStack(smithingTemplate), sess.menuStack(smithingBase), sess.menuStack(smithingAddition))
		if !equipMatch(sess.menuStack(smithingResult), out) || sess.menuStack(smithingResult).Count != out.Count {
			sess.setMenuStack(smithingResult, out)
			changes.slots = append(changes.slots, smithingResult)
		}
		s.smithingMenuError(sess, w)
	case menuStonecutter:
		stonecutterSync(sess, c, w, &changes.slots)
	case menuGrindstone:
		out := grindstoneCompute(sess.menuStack(grindstoneTop), sess.menuStack(grindstoneBottom))
		if !equipMatch(sess.menuStack(grindstoneResult), out) || sess.menuStack(grindstoneResult).Count != out.Count {
			sess.setMenuStack(grindstoneResult, out)
			changes.slots = append(changes.slots, grindstoneResult)
		}
	case menuLoom:
		loomSync(s, sess, w, c, &changes.slots)
	case menuCartographyTable:
		s.cartographySync(sess, w, &changes.slots)
	}
}

func (s *Server) gestureSync(sess *Session, w *openWindow, changes *menuGestureChanges) {
	if w == nil {
		s.refreshCraftResult(sess)
		sess.invState.Add(1)
		_ = s.sendContainerContent(sess)
		_ = s.sendCarriedItem(sess, sess.carriedBuffer())
		for _, slot := range changes.slots {
			if slot >= slotArmorStart && slot < slotArmorStart+4 || slot == slotOffhand {
				s.broadcastEquipment(sess)
				break
			}
		}
		return
	}
	if w.craft != nil {
		if s.refreshTableResult(sess, w.craft) {
			changes.slots = append(changes.slots, 0)
		}
		s.syncCraftTableSlots(sess, w, w.craft, changes.slots)
		return
	}
	s.syncContainerMenuSlots(sess, w, changes.slots)
	if w.container != nil {
		switch {
		case w.container.spec.brew:
			s.updateBrewBlockState(w.dim, w.pos, brewPotionBits(w.container))
		case w.container.spec.menu == menuEnchantment:
			s.enchantSlotsChanged(sess, w)
		case w.container.spec.menu == menuAnvil:
			if changes.anvilCost {
				s.sendAnvilCost(sess, w, w.container)
			}
			if changes.anvilDegrade {
				s.anvilDegradeRoll(sess, w)
			}
		}
	}
}

// SmithingMenu's property0 marks a full, invalid recipe, not an empty grid.
func (s *Server) smithingMenuError(sess *Session, w *openWindow) {
	value := int32(0)
	if !sess.menuStack(smithingTemplate).Empty() && !sess.menuStack(smithingBase).Empty() && !sess.menuStack(smithingAddition).Empty() && sess.menuStack(smithingResult).Empty() {
		value = 1
	}
	if !w.smithingErrorSent || w.smithingError != value {
		s.sendContainerProperty(sess, w.id, 0, value)
		w.smithingError = value
		w.smithingErrorSent = true
	}
}
