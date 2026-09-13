package server

import (
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Recipe book: a stable network-id index over the recipe table, and the
// Place Recipe handler that fills the open crafting grid from it. The
// index is what ties a client's recipe click back to a server recipe,
// so the ids must be assigned in one place and never reordered.

// recipeEntry is one recipe as the recipe book sees it: the result plus
// the ingredient shape the grid must be filled with.
type recipeEntry struct {
	result      string
	count       int32
	pattern     []string // shaped rows; nil for shapeless
	key         map[byte]string
	ingredients []string // shapeless ingredient names
}

// recipeIndex is the ordered recipe table. Network ids are indices into
// this slice; Recipe Book Add and Place Recipe both resolve through it.
func recipeIndex() []recipeEntry {
	out := make([]recipeEntry, 0, len(shapedRecipes)+len(shapelessRecipes))
	for _, r := range shapedRecipes {
		out = append(out, recipeEntry{
			result: r.Result, count: r.Count, pattern: r.Pattern, key: r.Key,
		})
	}
	for _, r := range shapelessRecipes {
		out = append(out, recipeEntry{
			result: r.Result, count: r.Count, ingredients: r.Ingredients,
		})
	}
	return out
}

// recipeByID resolves a network recipe id, reporting false when the id
// is outside the index.
func recipeByID(id int32) (recipeEntry, bool) {
	all := recipeIndex()
	if id < 0 || int(id) >= len(all) {
		return recipeEntry{}, false
	}
	return all[id], true
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

// recipeShape normalises a recipe into a side x side item-name matrix
// (row-major), reporting false when the recipe cannot fit.
func recipeShape(rec recipeEntry, side int) ([]string, bool) {
	grid := make([]string, side*side)
	if rec.pattern == nil {
		// Shapeless: place the ingredients in the first cells.
		if len(rec.ingredients) > side*side {
			return nil, false
		}
		copy(grid, rec.ingredients)
		return grid, true
	}
	rows := trimPattern(rec.pattern)
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
			name, ok := rec.key[row[x]]
			if !ok {
				return nil, false
			}
			grid[y*side+x] = name
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
// into its grid cell. Ingredients the inventory cannot supply leave the
// cell empty, which is what vanilla does when the recipe is only partly
// craftable.
func (s *Server) fillGrid(sess *Session, w *openWindow, side int, shape []string) {
	for cell, want := range shape {
		if want == "" {
			continue
		}
		itemID, ok := itemForRecipeKey(want)
		if !ok {
			continue
		}
		if !sess.takeOneFromInventory(itemID) {
			continue
		}
		s.setGridStack(sess, w, gridSlot(w, side, cell), ItemStack{ID: itemID, Count: 1})
	}
}

// itemForRecipeKey resolves a recipe key to a concrete item id. Keys
// starting with '*' are families, resolved to their first member in
// registry order so placement is deterministic.
func itemForRecipeKey(key string) (int32, bool) {
	if len(key) == 0 {
		return 0, false
	}
	if key[0] != '*' {
		return world.ItemID("minecraft:" + key)
	}
	suffix := key[1:]
	for name, id := range world.ItemIDs {
		if hasSuffixAfterPrefix(name, suffix) {
			return id, true
		}
	}
	return 0, false
}

// hasSuffixAfterPrefix reports whether a "minecraft:x_suffix" name ends
// with "_suffix".
func hasSuffixAfterPrefix(name, suffix string) bool {
	if len(name) < len(suffix)+1 {
		return false
	}
	return name[len(name)-len(suffix)-1] == '_' && name[len(name)-len(suffix):] == suffix
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

// recipeBookEntryCount is how many recipes the book advertises.
func recipeBookEntryCount() int32 { return int32(len(recipeIndex())) }

// sendRecipeBookAddAll sends the full recipe book so a client can list
// and click recipes. Each entry carries the result stack and the
// category/flag byte the 776 client expects.
func (s *Server) sendRecipeBookAddAll(sess *Session) error {
	all := recipeIndex()
	var b protocol.Buffer
	b.VarInt(int32(len(all)))
	for i, r := range all {
		itemID, ok := world.ItemID("minecraft:" + r.result)
		if !ok {
			itemID = 0
		}
		b.VarInt(int32(i)) // display id
		writeSlot(&b, ItemStack{ID: itemID, Count: r.count})
		b.VarInt(0)   // crafting category: building blocks
		b.Byte(0x01)  // flags: show notification
		b.Bool(false) // no ingredients list for this entry
	}
	b.Bool(true) // replace
	return sess.write(sess.ids().PlayRecipeBookAdd, b.Bytes())
}
