package server

import (
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// Crafting: the player inventory's 2x2 grid (menu slots 1-4, result at
// 0) plus the crafting-table 3x3 when that menu lands. Recipes come
// from the generated vanilla recipe table (data.Recipes); matching
// follows the vanilla shape rules: patterns are trimmed to their
// occupied bounding box and may sit anywhere in the grid, a shaped
// recipe also matches horizontally mirrored, and shapeless recipes
// match any arrangement.

// craftGrid is a 3x3 crafting grid in row-major order. The player
// inventory uses the top-left 2x2 subset of it.
type craftGrid [9]ItemStack

// gridNames resolves the grid's items, or reports false when a grid
// slot is empty-but-counted or an unknown item id appears.
func gridNames(g craftGrid) ([]string, []bool, bool) {
	names := make([]string, 9)
	filled := make([]bool, 9)
	for i, it := range g {
		if it.Empty() {
			continue
		}
		name, ok := itemNameOf(it.ID)
		if !ok {
			return nil, nil, false
		}
		names[i] = name
		filled[i] = true
	}
	return names, filled, true
}

// gridBox returns the occupied bounding box of the grid as min/max row
// and column; maxRow is -1 when the grid is empty.
func gridBox(filled []bool) (minRow, minCol, maxRow, maxCol int) {
	minRow, minCol, maxRow, maxCol = 3, 3, -1, -1
	for i, f := range filled {
		if !f {
			continue
		}
		row, col := i/3, i%3
		if row < minRow {
			minRow = row
		}
		if row > maxRow {
			maxRow = row
		}
		if col < minCol {
			minCol = col
		}
		if col > maxCol {
			maxCol = col
		}
	}
	return
}

// recipeStack converts a data.ItemStack into a wire ItemStack.
func recipeStack(out data.ItemStack) (ItemStack, bool) {
	id, ok := world.ItemID(out.Item)
	if !ok {
		return ItemStack{}, false
	}
	return ItemStack{ID: id, Count: out.Count}, true
}

// matchShapeless finds the first shapeless recipe whose ingredient
// multiset is exactly the grid's contents.
func matchShapeless(names []string, filled []bool) (ItemStack, bool) {
	var present []string
	for i, n := range names {
		if filled[i] {
			present = append(present, n)
		}
	}
	for i := range data.Recipes {
		r := &data.Recipes[i]
		if r.Type != data.CraftingShapeless || len(r.Ingredients) != len(present) {
			continue
		}
		used := make([]bool, len(present))
		matched := true
		for _, in := range r.Ingredients {
			found := false
			for j, got := range present {
				if !used[j] && in.Accepts(got) {
					used[j] = true
					found = true
					break
				}
			}
			if !found {
				matched = false
				break
			}
		}
		if matched {
			if st, ok := recipeStack(r.Result); ok {
				return st, true
			}
		}
	}
	return ItemStack{}, false
}

// trimPattern removes empty rows and columns from a pattern.
func trimPattern(rows []string) []string {
	var kept []string
	for _, row := range rows {
		if strings.TrimSpace(row) != "" {
			kept = append(kept, row)
		}
	}
	if len(kept) == 0 {
		return nil
	}
	width := len(kept[0])
	left, right := width, 0
	for _, row := range kept {
		for i := 0; i < width && i < len(row); i++ {
			if row[i] != ' ' {
				if i < left {
					left = i
				}
				if i > right {
					right = i
				}
			}
		}
	}
	out := make([]string, 0, len(kept))
	for _, row := range kept {
		out = append(out, row[left:right+1])
	}
	return out
}

// patternAt reports whether the trimmed pattern, optionally mirrored
// horizontally the way vanilla shaped matching allows, occupies the
// grid's bounding box cell-for-cell.
func patternAt(pat []string, r *data.Recipe, names []string, filled []bool, minRow, minCol int, mirror bool) bool {
	for y := range pat {
		for x := 0; x < len(pat[y]); x++ {
			px := x
			if mirror {
				px = len(pat[y]) - 1 - x
			}
			gi := (minRow+y)*3 + (minCol + x)
			if pat[y][px] == ' ' {
				if filled[gi] {
					return false
				}
				continue
			}
			if !filled[gi] || !r.Key[pat[y][px]].Accepts(names[gi]) {
				return false
			}
		}
	}
	return true
}

// matchShaped finds the first shaped recipe whose normalized pattern
// matches the grid's normalized occupied box, trying the mirrored
// pattern too — vanilla allows horizontal flips.
func matchShaped(names []string, filled []bool) (ItemStack, bool) {
	minRow, minCol, maxRow, maxCol := gridBox(filled)
	if maxRow < 0 {
		return ItemStack{}, false
	}
	boxH, boxW := maxRow-minRow+1, maxCol-minCol+1

	for i := range data.Recipes {
		r := &data.Recipes[i]
		if r.Type != data.CraftingShaped {
			continue
		}
		pat := trimPattern(r.Pattern)
		if len(pat) != boxH || len(pat[0]) != boxW {
			continue
		}
		if patternAt(pat, r, names, filled, minRow, minCol, false) ||
			patternAt(pat, r, names, filled, minRow, minCol, true) {
			if st, ok := recipeStack(r.Result); ok {
				return st, true
			}
		}
	}
	return ItemStack{}, false
}

// craftResult computes the grid's result stack. Shaped recipes win
// over shapeless ones, matching the order a vanilla table resolves
// them.
func craftResult(g craftGrid) (ItemStack, bool) {
	names, filled, ok := gridNames(g)
	if !ok {
		return ItemStack{}, false
	}
	if out, ok := matchShaped(names, filled); ok {
		return out, true
	}
	return matchShapeless(names, filled)
}

// playerCraftGrid snapshots the session's 2x2 grid inside a 3x3 view.
func (sess *Session) playerCraftGrid() craftGrid {
	sess.invMu.RLock()
	defer sess.invMu.RUnlock()
	return craftGrid{
		sess.inv[slotCraftStart],
		sess.inv[slotCraftStart+1], {},
		sess.inv[slotCraftStart+2],
		sess.inv[slotCraftStart+3], {}, {},
		{}, {},
	}
}

// refreshCraftResult recomputes the result slot and syncs it when it
// changed.
func (s *Server) refreshCraftResult(sess *Session) {
	grid := sess.playerCraftGrid()
	want, ok := craftResult(grid)
	if !ok {
		want = ItemStack{}
	}
	sess.invMu.Lock()
	prev := sess.inv[slotCraftResult]
	sess.inv[slotCraftResult] = want
	sess.invMu.Unlock()
	if prev != want {
		_ = s.sendContainerSlot(sess, slotCraftResult, want)
	}
}

// consumeCraftGrid removes one item from each grid slot used by the
// current recipe.
func (sess *Session) consumeCraftGrid() {
	sess.invMu.Lock()
	defer sess.invMu.Unlock()
	for i := int16(slotCraftStart); i < slotCraftStart+4; i++ {
		if sess.inv[i].Empty() {
			continue
		}
		sess.inv[i].Count--
		if sess.inv[i].Count <= 0 {
			sess.inv[i] = ItemStack{}
		}
	}
}

// takeCraftResult grants up to n crafts to the cursor and consumes the
// grid, returning the new cursor and the touched grid slots.
func (sess *Session) takeCraftResult(n int32) (ItemStack, []int16, bool) {
	sess.invMu.Lock()
	defer sess.invMu.Unlock()
	result := sess.inv[slotCraftResult]
	if result.Empty() || n <= 0 {
		return sess.carried, nil, false
	}
	maxCrafts := n
	// The cursor must be empty or the same item with room.
	if !sess.carried.Empty() {
		if sess.carried.ID != result.ID {
			return sess.carried, nil, false
		}
		room := maxStackOfID(sess.carried.ID) - sess.carried.Count
		if room < result.Count {
			return sess.carried, nil, false
		}
		if maxCrafts > room/result.Count {
			maxCrafts = room / result.Count
		}
	}
	var touched []int16
	for c := int32(0); c < maxCrafts; c++ {
		if sess.inv[slotCraftResult].Empty() {
			break
		}
		out := sess.inv[slotCraftResult]
		// Consume one from every non-empty grid slot.
		ok := false
		for i := int16(slotCraftStart); i < slotCraftStart+4; i++ {
			if sess.inv[i].Empty() {
				continue
			}
			ok = true
			break
		}
		if !ok {
			break
		}
		for i := int16(slotCraftStart); i < slotCraftStart+4; i++ {
			if sess.inv[i].Empty() {
				continue
			}
			sess.inv[i].Count--
			if sess.inv[i].Count <= 0 {
				sess.inv[i] = ItemStack{}
			}
			touched = append(touched, i)
		}
		sess.carried.Count += out.Count
		if sess.carried.Empty() {
			sess.carried = ItemStack{ID: out.ID, Count: out.Count}
		}
		sess.inv[slotCraftResult] = ItemStack{}
		// Recompute for the next craft.
		if next, ok := craftResult(craftGrid{
			sess.inv[slotCraftStart],
			sess.inv[slotCraftStart+1], {},
			sess.inv[slotCraftStart+2],
			sess.inv[slotCraftStart+3], {}, {},
			{}, {},
		}); ok {
			sess.inv[slotCraftResult] = next
		}
	}
	return sess.carried, touched, true
}
