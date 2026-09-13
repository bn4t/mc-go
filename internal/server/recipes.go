package server

import (
	"strings"

	"mc-go/internal/world"
)

// Crafting: the player inventory's 2x2 grid (menu slots 1-4, result at
// 0) plus the crafting-table 3x3 when that menu lands. Recipes use the
// vanilla shape rules: patterns are trimmed to their occupied bounding
// box and may sit anywhere in the grid; shapeless recipes match any
// arrangement.

// shapedRecipe matches a normalized grid pattern. Runes in Pattern are
// keys into Key, naming an item. ' ' means empty.
type shapedRecipe struct {
	Pattern []string
	Key     map[byte]string
	Result  string
	Count   int32
}

// shapelessRecipe matches any arrangement of its ingredient names.
type shapelessRecipe struct {
	Ingredients []string
	Result      string
	Count       int32
}

var shapedRecipes = []shapedRecipe{
	// Torches: coal above a stick.
	{Pattern: []string{"C", "S"}, Key: map[byte]string{'C': "coal", 'S': "stick"}, Result: "torch", Count: 4},
	// Stick: two planks vertically (any plank kind handled below).
	{Pattern: []string{"P", "P"}, Key: map[byte]string{'P': "*planks"}, Result: "stick", Count: 4},
	// Crafting table: 2x2 planks.
	{Pattern: []string{"PP", "PP"}, Key: map[byte]string{'P': "*planks"}, Result: "crafting_table", Count: 1},
	// Furnace: eight cobblestone in a ring.
	{Pattern: []string{"CCC", "C C", "CCC"}, Key: map[byte]string{'C': "cobblestone"}, Result: "furnace", Count: 1},
	// Chest: eight planks in a ring.
	{Pattern: []string{"PPP", "P P", "PPP"}, Key: map[byte]string{'P': "*planks"}, Result: "chest", Count: 1},
}

var shapelessRecipes = []shapelessRecipe{
	{Ingredients: []string{"oak_log"}, Result: "oak_planks", Count: 4},
	{Ingredients: []string{"spruce_log"}, Result: "spruce_planks", Count: 4},
	{Ingredients: []string{"birch_log"}, Result: "birch_planks", Count: 4},
	{Ingredients: []string{"jungle_log"}, Result: "jungle_planks", Count: 4},
	{Ingredients: []string{"acacia_log"}, Result: "acacia_planks", Count: 4},
	{Ingredients: []string{"dark_oak_log"}, Result: "dark_oak_planks", Count: 4},
}

// itemMatches reports whether an item name satisfies a recipe key.
// Keys starting with '*' match a family suffix (e.g. "*planks").
func itemMatches(key, itemName string) bool {
	if strings.HasPrefix(key, "*") {
		return strings.HasSuffix(itemName, key[1:])
	}
	return key == itemName
}

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

// matchShapeless finds a shapeless recipe for the grid.
func matchShapeless(g craftGrid) (ItemStack, bool) {
	names, _, ok := gridNames(g)
	if !ok {
		return ItemStack{}, false
	}
	var present []string
	for _, n := range names {
		if n != "" {
			present = append(present, n)
		}
	}
	for _, r := range shapelessRecipes {
		if len(r.Ingredients) != len(present) {
			continue
		}
		used := make([]bool, len(present))
		matched := true
		for _, want := range r.Ingredients {
			found := false
			for i, got := range present {
				if !used[i] && itemMatches(want, got) {
					used[i] = true
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
			if id, ok := world.ItemID(r.Result); ok {
				return ItemStack{ID: id, Count: r.Count}, true
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

// matchShaped finds a shaped recipe whose normalized pattern matches
// the grid's normalized occupied box.
func matchShaped(g craftGrid) (ItemStack, bool) {
	names, filled, ok := gridNames(g)
	if !ok {
		return ItemStack{}, false
	}
	minRow, minCol := 3, 3
	maxRow, maxCol := -1, -1
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
	if maxRow < 0 {
		return ItemStack{}, false
	}
	boxH, boxW := maxRow-minRow+1, maxCol-minCol+1

	for _, r := range shapedRecipes {
		pat := trimPattern(r.Pattern)
		if len(pat) != boxH || len(pat[0]) != boxW {
			continue
		}
		matched := true
		for y := range pat {
			for x := 0; x < len(pat[y]); x++ {
				gi := (minRow+y)*3 + (minCol + x)
				if pat[y][x] == ' ' {
					if filled[gi] {
						matched = false
						break
					}
					continue
				}
				if !filled[gi] || !itemMatches(r.Key[pat[y][x]], names[gi]) {
					matched = false
					break
				}
			}
			if !matched {
				break
			}
		}
		if matched {
			if id, ok := world.ItemID(r.Result); ok {
				return ItemStack{ID: id, Count: r.Count}, true
			}
		}
	}
	return ItemStack{}, false
}

// craftResult computes the 2x2 grid's result stack.
func craftResult(g craftGrid) (ItemStack, bool) {
	if out, ok := matchShaped(g); ok {
		return out, true
	}
	return matchShapeless(g)
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
		room := maxStack - sess.carried.Count
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
