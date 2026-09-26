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

// matchRepair handles crafting_special_repairitem. canCombine
// (measured): same item, count 1 each, both carrying MAX_DAMAGE and
// DAMAGE. assemble builds a FRESH stack of the item — nothing else
// of the patch survives — sets MAX_DAMAGE to the pair's max, damage
// to max(0, i5-(rem1+rem2+i5·5%)) (remaining = max-damage), and
// updateEnchantments keeps only CURSE-tagged enchantments, merged
// at the max level of either stack.
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
	if n != 2 || a.ID != b.ID || a.Count != 1 || b.Count != 1 {
		return ItemStack{}, false
	}
	max := MaxDamageOfID(a.ID)
	if max <= 0 {
		return ItemStack{}, false
	}
	i8 := max - a.Damage + max - b.Damage + max*5/100
	dmg := max - i8
	if dmg < 0 {
		dmg = 0
	}
	curses := map[string]int{}
	for _, st := range []ItemStack{a, b} {
		for _, e := range grindstoneEnchEntries(grindstoneEnchOf(st)) {
			if grindstoneCurses[e.name] && e.level > curses[e.name] {
				curses[e.name] = e.level
			}
		}
	}
	return ItemStack{ID: a.ID, Count: 1, Damage: dmg, Ench: grindstoneWriteEnch(curses)}, true
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

// matchBookCloning handles crafting_special_bookcloning: at least two
// grid items — exactly one written_book carrying written_book_content
// whose generation sits inside the serializer's
// DEFAULT_BOOK_GENERATION_RANGES between(0,1) (used when the recipe
// omits allowed_generations) plus at least one writable_book — and
// nothing else. Assemble is createWithOriginalComponents(result,
// source, materials-1): the written_book template keeps the source's
// whole patch at one result per material stack, then
// written_book_content.craftCopy bumps the generation.
func matchBookCloning(g craftGrid, filled []bool) (ItemStack, bool) {
	var source ItemStack
	mats := int32(0)
	for i, f := range filled {
		if !f {
			continue
		}
		st := g[i]
		name, ok := itemNameOf(st.ID)
		if !ok {
			return ItemStack{}, false
		}
		switch {
		case name == "written_book" && st.BookWritten != "" && bookCanCopy(st):
			if !source.Empty() {
				return ItemStack{}, false
			}
			source = st
		case name == "writable_book":
			mats++
		default:
			return ItemStack{}, false
		}
	}
	if source.Empty() || mats == 0 {
		return ItemStack{}, false
	}
	out := source
	out.Count = mats
	out.BookWritten = bookCraftCopy(source.BookWritten)
	return out, true
}

// matchBannerDuplicate handles crafting_special_bannerduplicate:
// exactly two BannerItems of the same DyeColor — one patterned
// (1..6 layers) and one plain — refusing two patterned, two plain,
// a >6-layer stack, or mismatched colors. The result is the
// patterned stack's copy at count 1 (createWithOriginalComponents
// keeps the whole patch).
func matchBannerDuplicate(g craftGrid, filled []bool) (ItemStack, bool) {
	var stacks []ItemStack
	for i, f := range filled {
		if f {
			stacks = append(stacks, g[i])
		}
	}
	if len(stacks) != 2 {
		return ItemStack{}, false
	}
	var patterned ItemStack
	var ord0, ord1 int32 = -1, -1
	for i, st := range stacks {
		if !loomIsBanner(st) {
			return ItemStack{}, false
		}
		bname, _ := itemNameOf(st.ID)
		ord := int32(-1)
		for k, d := range loomDyeNames {
			if bname == d+"_banner" {
				ord = int32(k)
				break
			}
		}
		if i == 0 {
			ord0 = ord
		} else {
			ord1 = ord
		}
	}
	if ord0 < 0 || ord0 != ord1 {
		return ItemStack{}, false
	}
	for _, st := range stacks {
		n := bannerLayerCount(st)
		if n > 6 {
			return ItemStack{}, false
		}
		if n > 0 {
			if !patterned.Empty() {
				return ItemStack{}, false
			}
			patterned = st
		}
	}
	if patterned.Empty() {
		return ItemStack{}, false
	}
	out := patterned
	out.Count = 1
	return out, true
}

// bannerLayerCount is the size of the banner_patterns layer list —
// mc-go stores the component as "pat:color,pat:color".
func bannerLayerCount(st ItemStack) int {
	if st.BannerPatterns == "" {
		return 0
	}
	return strings.Count(st.BannerPatterns, ",") + 1
}

// matchMapExtending handles crafting_special_mapextending: a fixed
// ShapedRecipePattern ### / #x# / ### — eight paper around a center
// filled_map — then the saved-data gates from MapItemSavedData:
// getSavedData non-null (a map_id pointing at real data — exploration
// maps are never minted here so they need no check) and scale < 4.
// Assemble keeps the map's patch and sets MAP_POST_PROCESSING=SCALE;
// the post-processing itself mints the scale+1 id at take time.
func matchMapExtending(g craftGrid, filled []bool, ms *mapStore) (ItemStack, bool) {
	for i := 0; i < 9; i++ {
		if !filled[i] {
			return ItemStack{}, false
		}
		name, ok := itemNameOf(g[i].ID)
		if !ok {
			return ItemStack{}, false
		}
		if i == 4 {
			if name != "filled_map" {
				return ItemStack{}, false
			}
		} else if name != "paper" {
			return ItemStack{}, false
		}
	}
	center := g[4]
	if center.MapID == 0 || ms == nil {
		return ItemStack{}, false
	}
	md, ok := ms.get(center.MapID - 1)
	if !ok || md.scale >= 4 {
		return ItemStack{}, false
	}
	out := center
	out.Count = 1
	out.MapPost = mapPostScale
	return out, true
}

// craftResult computes the grid's result stack, iterating the recipe
// table in registry order so the first matching recipe wins like
// vanilla's CraftingManager. It also reports the winning recipe's id —
// recipe_crafted needs it at take time. ms is the dimension's map
// data store — MapExtendingRecipe reads the source map's saved data
// (MapItem.getSavedData, its null and scale>=4 gates).
func craftResult(g craftGrid, ms *mapStore) (ItemStack, string, bool) {
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
		case data.CraftingSpecialBookcloning:
			out, hit = matchBookCloning(g, filled)
		case data.CraftingSpecialMapextending:
			out, hit = matchMapExtending(g, filled, ms)
		case data.CraftingSpecialBannerduplicate:
			out, hit = matchBannerDuplicate(g, filled)
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
	want, _, ok := craftResult(grid, &sess.dimension().maps)
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
		// ResultSlot.onTake applies map_post_processing on the stack
		// taken (onCraftedPostProcess): SCALE/LOCK mint a fresh map id.
		sess.server.cartographyApplyPost(sess, &out)
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
		_, rid, ridOK := craftResult(grid, &sess.dimension().maps)
		if ridOK {
			sess.server.fireRecipeCrafted(sess, rid, grid)
		}
		rtype := ""
		if ridOK {
			if r, ok := data.RecipeByID(rid); ok {
				rtype = r.Type
			}
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
		gridStacks := [4]ItemStack{grid[0], grid[1], grid[3], grid[4]}
		for i, gid := range gridIDs {
			rem, ok := craftRemainderOf(gid)
			if !ok && rtype == data.CraftingSpecialBookcloning &&
				gridStacks[i].BookWritten != "" {
				// BookCloningRecipe.getRemainingItems: a stack with
				// written_book_content leaves a count-1 copy of
				// itself — the source book is not consumed.
				rem = gridStacks[i].withCount(1)
				ok = true
			}
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
		}, &sess.dimension().maps); ok {
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
