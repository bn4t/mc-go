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

// matchShapelessRecipe reports whether the grid's contents are exactly
// the recipe's ingredient multiset.
func matchShapelessRecipe(r *data.Recipe, names []string, filled []bool) (ItemStack, bool) {
	var present []string
	for i, n := range names {
		if filled[i] {
			present = append(present, n)
		}
	}
	if len(r.Ingredients) != len(present) {
		return ItemStack{}, false
	}
	used := make([]bool, len(present))
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
			return ItemStack{}, false
		}
	}
	return recipeStack(r.Result)
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

// matchShapedRecipe reports whether the recipe's normalized pattern
// matches the grid's normalized occupied box, trying the mirrored
// pattern too — vanilla allows horizontal flips.
func matchShapedRecipe(r *data.Recipe, names []string, filled []bool, minRow, minCol, boxH, boxW int) (ItemStack, bool) {
	if boxH <= 0 {
		return ItemStack{}, false
	}
	pat := trimPattern(r.Pattern)
	if len(pat) != boxH || len(pat[0]) != boxW {
		return ItemStack{}, false
	}
	if patternAt(pat, r, names, filled, minRow, minCol, false) ||
		patternAt(pat, r, names, filled, minRow, minCol, true) {
		return recipeStack(r.Result)
	}
	return ItemStack{}, false
}

// matchDecoratedPot handles crafting_decorated_pot: vanilla's
// DecoratedPotPattern fixes the four ingredients to the edge middles
// of the full 3x3 — (0,1), (1,0), (1,2), (2,1) — with the corners and
// center empty. Each slot matches its own ingredient (all four carry
// the same tag in the bundled recipe, but the check stays per-slot).
func matchDecoratedPot(r *data.Recipe, names []string, filled []bool, boxH, boxW int) (ItemStack, bool) {
	if boxH != 3 || boxW != 3 || len(r.Ingredients) != 4 {
		return ItemStack{}, false
	}
	// back, left, right, front in vanilla field order.
	edgeIdx := [4]int{1, 3, 5, 7}
	for i, gi := range edgeIdx {
		if !filled[gi] || !r.Ingredients[i].Accepts(names[gi]) {
			return ItemStack{}, false
		}
	}
	for gi := 0; gi < 9; gi++ {
		if filled[gi] && gi != 1 && gi != 3 && gi != 5 && gi != 7 {
			return ItemStack{}, false
		}
	}
	return recipeStack(r.Result)
}

// matchRepair handles crafting_special_repairitem: two of the same
// damageable item merge into one whose damage is the sum minus the
// max and a 5%-of-max bonus (floored at zero), exactly as vanilla's
// RepairItemRecipe computes it.
func matchRepair(g craftGrid, filled []bool) (ItemStack, bool) {
	var a, b ItemStack
	n := 0
	for i, f := range filled {
		if !f {
			continue
		}
		n++
		if n == 1 {
			a = g[i]
		} else if n == 2 {
			b = g[i]
		}
	}
	if n != 2 || a.ID != b.ID {
		return ItemStack{}, false
	}
	max := MaxDamageOfID(a.ID)
	if max <= 0 {
		return ItemStack{}, false
	}
	dmg := a.Damage + b.Damage - max - max/20
	if dmg < 0 {
		dmg = 0
	}
	return ItemStack{ID: a.ID, Count: 1, Damage: dmg}, true
}

// matchTransmute handles crafting_transmute: exactly two grid items,
// the input and the material in either order; the result is the input
// stack retyped to the result item so carried components (a shulker
// box's contents) ride along, as vanilla's TransmuteRecipe does.
func matchTransmute(r *data.Recipe, g craftGrid, filled []bool) (ItemStack, bool) {
	var stacks []ItemStack
	var names []string
	for i, f := range filled {
		if f {
			stacks = append(stacks, g[i])
			name, ok := itemNameOf(g[i].ID)
			if !ok {
				return ItemStack{}, false
			}
			names = append(names, name)
		}
	}
	if len(stacks) != 2 {
		return ItemStack{}, false
	}
	var input ItemStack
	switch {
	case r.Input.Accepts(names[0]) && r.Material.Accepts(names[1]):
		input = stacks[0]
	case r.Input.Accepts(names[1]) && r.Material.Accepts(names[0]):
		input = stacks[1]
	default:
		return ItemStack{}, false
	}
	id, ok := world.ItemID(r.Result.Item)
	if !ok {
		return ItemStack{}, false
	}
	input.ID = id
	input.Count = r.Result.Count
	return input, true
}

// matchShieldDecoration handles crafting_special_shielddecoration:
// exactly two grid items — a banner and a shield with no
// banner_patterns layers — and the result is the shield keeping its
// whole patch (TransmuteRecipe.createWithOriginalComponents) plus the
// banner's layers and dye color: BANNER_PATTERNS copied, BASE_COLOR
// set to the banner item's DyeColor.
func matchShieldDecoration(g craftGrid, filled []bool) (ItemStack, bool) {
	var stacks []ItemStack
	for i, f := range filled {
		if f {
			stacks = append(stacks, g[i])
		}
	}
	if len(stacks) != 2 {
		return ItemStack{}, false
	}
	var banner, shield ItemStack
	for _, st := range stacks {
		switch {
		case loomIsBanner(st):
			if !banner.Empty() {
				return ItemStack{}, false
			}
			banner = st
		default:
			if !shield.Empty() {
				return ItemStack{}, false
			}
			shield = st
		}
	}
	if banner.Empty() || shield.Empty() {
		return ItemStack{}, false
	}
	name, ok := itemNameOf(shield.ID)
	if !ok || name != "shield" {
		return ItemStack{}, false
	}
	// A shield already carrying layers refuses a second decoration.
	if shield.BannerPatterns != "" {
		return ItemStack{}, false
	}
	ord := int32(0)
	bname, _ := itemNameOf(banner.ID)
	for i, d := range loomDyeNames {
		if bname == d+"_banner" {
			ord = int32(i)
			break
		}
	}
	shield.Count = 1
	shield.BannerPatterns = banner.BannerPatterns
	shield.BaseColor = ord + 1
	return shield, true
}

// craftResult computes the grid's result stack, iterating the recipe
// table in registry order so the first matching recipe wins like
// vanilla's CraftingManager. It also reports the winning recipe's id —
// recipe_crafted needs it at take time.
func craftResult(g craftGrid) (ItemStack, string, bool) {
	names, filled, ok := gridNames(g)
	if !ok {
		return ItemStack{}, "", false
	}
	minRow, minCol, maxRow, maxCol := gridBox(filled)
	boxH, boxW := 0, 0
	if maxRow >= 0 {
		boxH, boxW = maxRow-minRow+1, maxCol-minCol+1
	}
	for i := range data.Recipes {
		r := &data.Recipes[i]
		var out ItemStack
		var hit bool
		switch r.Type {
		case data.CraftingShaped:
			out, hit = matchShapedRecipe(r, names, filled, minRow, minCol, boxH, boxW)
		case data.CraftingShapeless:
			out, hit = matchShapelessRecipe(r, names, filled)
		case data.CraftingSpecialRepairitem:
			out, hit = matchRepair(g, filled)
		case data.CraftingSpecialShielddecoration:
			out, hit = matchShieldDecoration(g, filled)
		case data.CraftingTransmute:
			out, hit = matchTransmute(r, g, filled)
		case data.CraftingDecoratedPot:
			out, hit = matchDecoratedPot(r, names, filled, boxH, boxW)
		}
		if hit {
			return out, r.ID, true
		}
	}
	return ItemStack{}, "", false
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
	want, _, ok := craftResult(grid)
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
func (sess *Session) takeCraftResult(n int32) (ItemStack, []int16, []ItemStack, bool) {
	sess.invMu.Lock()
	defer sess.invMu.Unlock()
	result := sess.inv[slotCraftResult]
	if result.Empty() || n <= 0 {
		return sess.carried, nil, nil, false
	}
	maxCrafts := n
	// The cursor must be empty or the same item with room.
	if !sess.carried.Empty() {
		if sess.carried.ID != result.ID {
			return sess.carried, nil, nil, false
		}
		room := maxStackOfID(sess.carried.ID) - sess.carried.Count
		if room < result.Count {
			return sess.carried, nil, nil, false
		}
		if maxCrafts > room/result.Count {
			maxCrafts = room / result.Count
		}
	}
	var touched []int16
	var leftovers []ItemStack
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
		// recipe_crafted fires on the grid as it stood when the craft
		// resolved, before the ingredients are consumed.
		grid := craftGrid{
			sess.inv[slotCraftStart],
			sess.inv[slotCraftStart+1], {},
			sess.inv[slotCraftStart+2],
			sess.inv[slotCraftStart+3], {}, {},
			{}, {},
		}
		if _, rid, ridOK := craftResult(grid); ridOK {
			sess.server.fireRecipeCrafted(sess, rid, grid)
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
		// ResultSlot.onTake's remainder tail: each input's crafting
		// remainder lands in its grid slot (post-shrink), merges when
		// the same item is still there, falls to the player inventory,
		// then drops at the feet.
		gridIDs := [4]int32{grid[0].ID, grid[1].ID, grid[3].ID, grid[4].ID}
		for i, gid := range gridIDs {
			rem, ok := craftRemainderOf(gid)
			if !ok {
				continue
			}
			s := slotCraftStart + int16(i)
			switch {
			case sess.inv[s].Empty():
				sess.inv[s] = rem
				touched = append(touched, s)
			case sess.inv[s].Stackable(rem):
				sess.inv[s].Count += rem.Count
				touched = append(touched, s)
			default:
				leftovers = append(leftovers, rem)
			}
		}
		sess.carried.Count += out.Count
		if sess.carried.Empty() {
			sess.carried = ItemStack{ID: out.ID, Count: out.Count}
		}
		sess.inv[slotCraftResult] = ItemStack{}
		sess.addItemStat(statCrafted, out.ID, out.Count)
		// Recompute for the next craft.
		if next, _, ok := craftResult(craftGrid{
			sess.inv[slotCraftStart],
			sess.inv[slotCraftStart+1], {},
			sess.inv[slotCraftStart+2],
			sess.inv[slotCraftStart+3], {}, {},
			{}, {},
		}); ok {
			sess.inv[slotCraftResult] = next
		}
	}
	return sess.carried, touched, leftovers, true
}

// craftRemainders maps a crafting input to its remainder item — the
// five Item.Properties.craftRemainder calls in the 26.2 Items static
// init (javap-verified; stews and potions use usingConvertsTo, which
// does not feed crafting). ItemStackTemplate.create() yields a plain
// count-1 stack for all of them.
var craftRemainders = map[string]string{
	"water_bucket":  "bucket",
	"lava_bucket":   "bucket",
	"milk_bucket":   "bucket",
	"dragon_breath": "glass_bottle",
	"honey_bottle":  "glass_bottle",
}

// craftRemainderOf returns an input's crafting remainder stack —
// CraftingRecipe.defaultCraftingReminder's per-slot value.
func craftRemainderOf(id int32) (ItemStack, bool) {
	name, ok := itemNameOf(id)
	if !ok {
		return ItemStack{}, false
	}
	rem, ok := craftRemainders[strings.TrimPrefix(name, "minecraft:")]
	if !ok {
		return ItemStack{}, false
	}
	rid, ok := world.ItemIDs[rem]
	if !ok {
		return ItemStack{}, false
	}
	return ItemStack{ID: rid, Count: 1}, true
}
