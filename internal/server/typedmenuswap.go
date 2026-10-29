package server

import "mc-go/internal/data"

// typedMenuSwap handles hotbar/offhand exchanges for filtered menu slots.
// Measured on 26.2 AbstractContainerMenu SWAP: oversized incoming stacks
// leave their remainder in the source inventory slot, and an occupied target
// is returned through Inventory.add. Empty sources can take result slots.
func (s *Server) typedMenuSwap(sess *Session, w *openWindow, slot int, button byte) []int {
	inv, valid := inventorySwapSlot(button)
	if !valid {
		return nil
	}
	c := w.container
	if c == nil {
		return nil
	}
	source := menuSlotForInventory(w, inv)
	if source == slot {
		return nil
	}
	incoming, old := sess.stackAt(inv), sess.menuStack(slot)
	if incoming.Empty() && old.Empty() {
		return nil
	}
	if !incoming.Empty() && !menuAccepts(c.spec, slot, incoming) {
		return nil
	}
	touched := []int{slot}
	if source >= 0 {
		touched = append(touched, source)
	}
	if incoming.Empty() {
		taken := old
		if c.spec.isResult(slot) && c.spec.menu == menuCartographyTable {
			s.cartographyApplyPost(sess, &taken)
		}
		sess.setMenuStack(slot, ItemStack{})
		sess.setStack(inv, taken)
		if c.spec.isResult(slot) {
			touched = append(touched, s.typedResultTaken(sess, w, taken)...)
		}
		return touched
	}
	cap := c.spec.slotMax(slot, incoming)
	if incoming.Count <= cap {
		sess.setMenuStack(slot, incoming)
		sess.setStack(inv, old)
		return touched
	}
	sess.setMenuStack(slot, incoming.withCount(cap))
	sess.setStack(inv, incoming.withCount(incoming.Count-cap))
	if !old.Empty() {
		touched = append(touched, s.returnSwapDisplaced(sess, w, old)...)
	}
	return touched
}

func menuSlotForInventory(w *openWindow, slot int16) int {
	if slot >= slotMainStart && slot < slotHotbarStart {
		return w.containerSize() + int(slot-slotMainStart)
	}
	if slot >= slotHotbarStart && slot < slotOffhand {
		return w.containerSize() + 27 + int(slot-slotHotbarStart)
	}
	return -1
}

// Inventory.add prefers a matching selected hand, then offhand, then the
// hotbar/main scan; empty cells come only from the hotbar/main inventory.
func (s *Server) returnSwapDisplaced(sess *Session, w *openWindow, it ItemStack) []int {
	flat := make([]int16, 0, 36)
	for sl := int16(slotHotbarStart); sl < slotOffhand; sl++ {
		flat = append(flat, sl)
	}
	for sl := int16(slotMainStart); sl < slotHotbarStart; sl++ {
		flat = append(flat, sl)
	}
	merge := append([]int16{invSlotForHotbar(sess.held.Load()), slotOffhand}, flat...)
	var touched []int
	put := func(sl int16, value ItemStack) {
		sess.setStack(sl, value)
		if menu := menuSlotForInventory(w, sl); menu >= 0 {
			touched = append(touched, menu)
		}
	}
	seen := map[int16]bool{}
	for _, sl := range merge {
		if it.Empty() {
			break
		}
		if seen[sl] {
			continue
		}
		seen[sl] = true
		old := sess.stackAt(sl)
		if it.Damage > 0 || !old.Stackable(it) {
			continue
		}
		count := min(maxStackOfID(it.ID)-old.Count, it.Count)
		if count <= 0 {
			continue
		}
		put(sl, old.withCount(old.Count+count))
		it.Count -= count
	}
	for _, sl := range flat {
		if it.Empty() {
			break
		}
		if !sess.stackAt(sl).Empty() {
			continue
		}
		count := min(maxStackOfID(it.ID), it.Count)
		put(sl, it.withCount(count))
		it.Count -= count
	}
	if !it.Empty() && sess.mode() != GameModeCreative {
		s.dropItemLocked(sess, it)
	}
	return touched
}

// Result callbacks consume their own inputs and produce their station's
// sounds/XP/advancements. The handler's normal recomputation follows this.
func (s *Server) typedResultTaken(sess *Session, w *openWindow, taken ItemStack) []int {
	if w.container.spec.menu == menuSmithing || w.container.spec.menu == menuStonecutter {
		// These stations call ItemStack.onCraftedBy with the taken
		// stack count directly, unlike the furnace removal counter.
		sess.addItemStat(statCrafted, taken.ID, taken.Count)
	}
	switch w.container.spec.menu {
	case menuSmithing:
		_, id := smithingCompute(sess.menuStack(smithingTemplate), sess.menuStack(smithingBase), sess.menuStack(smithingAddition))
		if id != "" {
			s.awardRecipeAdvancement(sess, &data.RecipeAdv{Recipe: id})
		}
		s.smithingTake(sess)
		return []int{smithingTemplate, smithingBase, smithingAddition}
	case menuStonecutter:
		c := w.container
		if c.stoneSel >= 0 && int(c.stoneSel) < len(c.stoneRecipes) {
			id := data.Recipes[c.stoneRecipes[c.stoneSel]].ID
			s.awardRecipeAdvancement(sess, &data.RecipeAdv{Recipe: id})
			var grid craftGrid
			grid[0] = sess.menuStack(stonecutterInput)
			s.fireRecipeCrafted(sess, id, grid)
		}
		s.stonecutterTake(sess, w)
		return []int{stonecutterInput}
	case menuGrindstone:
		s.grindstoneTake(sess, w)
		return []int{grindstoneTop, grindstoneBottom}
	case menuLoom:
		s.loomTake(sess, w)
		return []int{loomBanner, loomDye}
	case menuCartographyTable:
		s.cartographyTake(sess, w)
		return []int{cartographyMap, cartographyAdditional}
	}
	return nil
}
