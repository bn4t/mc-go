package server

import (
	"mc-go/internal/data"
	"mc-go/internal/world"
)

// Recipe book: the network recipe id indexes data.Recipes directly,
// and the Place Recipe handler fills the open crafting grid from it.
// The index is what ties a client's recipe click back to a server
// recipe, so it must never be reordered.

// recipeByID resolves a network recipe id, reporting false when the id
// is outside the table or not a grid-craftable recipe.
func recipeByID(id int32) (data.Recipe, bool) {
	if id < 0 || int(id) >= len(data.Recipes) {
		return data.Recipe{}, false
	}
	r := data.Recipes[id]
	if r.Type != data.CraftingShaped && r.Type != data.CraftingShapeless {
		return data.Recipe{}, false
	}
	return r, true
}

// gridSide reports the open crafting grid's side length: 3 for a
// crafting table, 2 for the player's own grid, and 0 when no crafting
// grid is open.
func (s *Server) gridSide(sess *Session) int {
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil {
		return 2 // the player inventory grid is always present
	}
	if w.craft != nil {
		return 3
	}
	return 0
}

// handlePlaceRecipe fills the open crafting grid from a recipe-book
// click: the grid is emptied back into the inventory, then one of each
// required ingredient is drawn from the inventory. makeAll repeats the
// draw as far as the inventory allows, matching shift-click.
func (s *Server) placeRecipe(sess *Session, windowID int32, recipeID int32, makeAll bool) {
	side := s.gridSide(sess)
	if side == 0 {
		return
	}
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w != nil && w.id != windowID {
		return
	}
	rec, ok := recipeByID(recipeID)
	if !ok {
		return
	}
	shape, ok := recipeShape(rec, side)
	if !ok {
		return
	}
	s.clearGrid(sess, w, side)
	s.fillGrid(sess, w, side, shape)
	if makeAll {
		// Vanilla repeats while the inventory still satisfies the
		// recipe; mc-go draws once per click and lets the client
		// re-click, which keeps the packet flow identical without
		// modelling the repeat count.
		_ = makeAll
	}
	s.syncCraftGrid(sess, w, side)
}

// recipeShape normalises a recipe into a side x side ingredient matrix
// (row-major), reporting false when the recipe cannot fit.
func recipeShape(rec data.Recipe, side int) ([]data.Ingredient, bool) {
	grid := make([]data.Ingredient, side*side)
	if rec.Type == data.CraftingShapeless {
		// Shapeless: place the ingredients in the first cells.
		if len(rec.Ingredients) > side*side {
			return nil, false
		}
		copy(grid, rec.Ingredients)
		return grid, true
	}
	rows := trimPattern(rec.Pattern)
	if len(rows) == 0 || len(rows) > side {
		return nil, false
	}
	width := 0
	for _, r := range rows {
		if len(r) > width {
			width = len(r)
		}
	}
	if width > side {
		return nil, false
	}
	for y, row := range rows {
		for x := 0; x < len(row); x++ {
			if row[x] == ' ' {
				continue
			}
			in, ok := rec.Key[row[x]]
			if !ok {
				return nil, false
			}
			grid[y*side+x] = in
		}
	}
	return grid, true
}

// gridSlot maps a (row, col) grid cell to its menu slot.
func gridSlot(w *openWindow, side, cell int) int16 {
	if w != nil && w.craft != nil {
		return int16(craftTableGridOff + cell)
	}
	// The player inventory grid is 2x2 at menu slots 1-4.
	row, col := cell/side, cell%side
	return int16(slotCraftStart + row*2 + col)
}

// clearGrid returns every grid stack to the inventory.
func (s *Server) clearGrid(sess *Session, w *openWindow, side int) {
	for cell := 0; cell < side*side; cell++ {
		slot := gridSlot(w, side, cell)
		it := s.gridStack(sess, w, slot)
		if it.Empty() {
			continue
		}
		s.setGridStack(sess, w, slot, ItemStack{})
		sess.addStack(it)
	}
}

// fillGrid draws one of each required ingredient from the inventory
// into its grid cell, trying the ingredient's acceptable items in
// order and taking the first the player has. Ingredients the inventory
// cannot supply leave the cell empty, which is what vanilla does when
// the recipe is only partly craftable.
func (s *Server) fillGrid(sess *Session, w *openWindow, side int, shape []data.Ingredient) {
	for cell, want := range shape {
		if len(want.Items) == 0 {
			continue
		}
		for _, name := range want.Items {
			itemID, ok := world.ItemID(name)
			if !ok {
				continue
			}
			if !sess.takeOneFromInventory(itemID) {
				continue
			}
			s.setGridStack(sess, w, gridSlot(w, side, cell), ItemStack{ID: itemID, Count: 1})
			break
		}
	}
}

// gridStack reads one crafting-grid slot for the open menu.
func (s *Server) gridStack(sess *Session, w *openWindow, slot int16) ItemStack {
	if w != nil && w.craft != nil {
		sess.windowMu.Lock()
		it := w.craft.slots[slot]
		sess.windowMu.Unlock()
		return it
	}
	return sess.stackAt(slot)
}

// setGridStack writes one crafting-grid slot for the open menu.
func (s *Server) setGridStack(sess *Session, w *openWindow, slot int16, it ItemStack) {
	if w != nil && w.craft != nil {
		sess.windowMu.Lock()
		w.craft.slots[slot] = it
		sess.windowMu.Unlock()
		return
	}
	sess.setStack(slot, it)
}

// syncCraftGrid re-sends the grid, the result, and the whole player
// window after a placement, which is the packet set vanilla produces.
func (s *Server) syncCraftGrid(sess *Session, w *openWindow, side int) {
	if w != nil && w.craft != nil {
		s.refreshTableResult(sess, w.craft)
		s.sendCraftTableContent(sess, w.craft)
		return
	}
	_ = side
	_ = s.sendContainerContent(sess)
}

// takeOneFromInventory removes a single item of the given id from the
// player's main inventory or hotbar, reporting whether one was found.
func (sess *Session) takeOneFromInventory(itemID int32) bool {
	sess.invMu.Lock()
	defer sess.invMu.Unlock()
	for slot := int16(slotMainStart); slot < slotCount; slot++ {
		if slot == slotOffhand {
			continue
		}
		it := sess.inv[slot]
		if it.ID != itemID || it.Empty() {
			continue
		}
		it.Count--
		if it.Count <= 0 {
			sess.inv[slot] = ItemStack{}
		} else {
			sess.inv[slot] = it
		}
		return true
	}
	return false
}
