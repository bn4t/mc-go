package server

import "strings"

// The loom applies one banner_pattern layer to a banner, dyed by the
// dye slot. Measured on vanilla 26.2 (LoomMenu):
//
//	- slotsChanged: banner or dye empty clears the result, the pattern
//	  list, and the selection. Otherwise the selectable list rebuilds
//	  from the pattern slot — empty pattern slot means the
//	  no_item_required tag (the 32 free patterns in registry order), a
//	  pattern item means its provides_banner_patterns holder set (one
//	  namesake pattern). A one-entry list auto-selects index 0; a
//	  previously-valid selection keeps its pattern when it still
//	  appears in the new list, else resets to -1. Six existing layers
//	  force selection -1 and an empty result.
//	- clickMenuButton: a valid index selects the pattern and writes the
//	  result slot: a count-1 copy of the banner with the layer appended.
//	- onTake: banner and dye lose one each (the pattern item is NOT
//	  consumed); when both run out the selection resets to -1. The take
//	  plays ui.loom.take_result once per game tick at the block.
//	- selectedBannerPatternIndex rides Container Property 0, starting
//	  at 0 on a fresh menu (DataSlot.standalone's initial value).
//	- mayPlace: banner slot takes BannerItems only, dye slot the
//	  loom_dyes tag (all sixteen dyes carry minecraft:dye), pattern
//	  slot the loom_patterns tag with provides_banner_patterns.

// loomPatterns lists all 43 banner patterns in registry order — the
// sorted datapack file order, which is also their holder wire id
// (measured against the ominous banner's pattern ids).
var loomPatterns = [...]string{
	"base", "border", "bricks", "circle", "creeper", "cross",
	"curly_border", "diagonal_left", "diagonal_right",
	"diagonal_up_left", "diagonal_up_right", "flow",
	"flower", "globe", "gradient", "gradient_up", "guster",
	"half_horizontal",
	"half_horizontal_bottom", "half_vertical", "half_vertical_right",
	"mojang", "piglin", "rhombus", "skull", "small_stripes",
	"square_bottom_left", "square_bottom_right", "square_top_left",
	"square_top_right", "straight_cross", "stripe_bottom",
	"stripe_center", "stripe_downleft", "stripe_downright",
	"stripe_left", "stripe_middle", "stripe_right", "stripe_top",
	"triangle_bottom", "triangle_top", "triangles_bottom",
	"triangles_top",
}

// loomFreePatterns is the no_item_required tag in the tag file's
// value order — a Named HolderSet iterates in bound order, not
// registry order (measured: index 5 yields stripe_top, not the
// registry's diagonal_up_left).
var loomFreePatterns = [...]string{
	"square_bottom_left", "square_bottom_right", "square_top_left",
	"square_top_right", "stripe_bottom", "stripe_top", "stripe_left",
	"stripe_right", "stripe_center", "stripe_middle",
	"stripe_downright", "stripe_downleft", "small_stripes", "cross",
	"straight_cross", "triangle_bottom", "triangle_top",
	"triangles_bottom", "triangles_top", "diagonal_left",
	"diagonal_up_right", "diagonal_up_left", "diagonal_right",
	"circle", "rhombus", "half_vertical", "half_horizontal",
	"half_vertical_right", "half_horizontal_bottom", "border",
	"gradient", "gradient_up",
}

// loomPatternItems maps each loom_patterns-tag item to the pattern its
// provides_banner_patterns component offers — most items provide
// their namesake through a one-entry pattern_item tag; two tag
// values differ (field_masoned→bricks, bordure_indented→curly_border).
var loomPatternItems = map[string]string{
	"bordure_indented_banner_pattern": "curly_border",
	"creeper_banner_pattern":          "creeper",
	"field_masoned_banner_pattern":    "bricks",
	"flow_banner_pattern":             "flow",
	"flower_banner_pattern":           "flower",
	"globe_banner_pattern":            "globe",
	"guster_banner_pattern":           "guster",
	"mojang_banner_pattern":           "mojang",
	"piglin_banner_pattern":           "piglin",
	"skull_banner_pattern":            "skull",
}

// loomDyeNames indexes DyeColor ordinals to names — wire and canonical
// form both use it.
var loomDyeNames = [...]string{
	"white", "orange", "magenta", "light_blue", "yellow", "lime",
	"pink", "gray", "light_gray", "cyan", "purple", "blue", "brown",
	"green", "red", "black",
}

// loomPatternID is the pattern's banner_pattern registry id — the
// holder wire writes id+1.
func loomPatternID(name string) (int32, bool) {
	for i, p := range loomPatterns {
		if p == name {
			return int32(i), true
		}
	}
	return 0, false
}

// loomDyeOf is the stack's minecraft:dye component — a DyeColor
// ordinal every one of the sixteen dye items carries by default.
func loomDyeOf(it ItemStack) (int32, bool) {
	name, ok := itemNameOf(it.ID)
	if !ok {
		return 0, false
	}
	for i, d := range loomDyeNames {
		if name == d+"_dye" {
			return int32(i), true
		}
	}
	return 0, false
}

// loomIsBanner reports whether the stack is a BannerItem — the sixteen
// colored banners, the only items the banner slot takes.
func loomIsBanner(it ItemStack) bool {
	name, ok := itemNameOf(it.ID)
	if !ok {
		return false
	}
	for _, d := range loomDyeNames {
		if name == d+"_banner" {
			return true
		}
	}
	return false
}

// loomPatternProvides is the stack's provides_banner_patterns holder
// — the namesake pattern a loom_patterns-tag item offers.
func loomPatternProvides(it ItemStack) (string, bool) {
	name, ok := itemNameOf(it.ID)
	if !ok {
		return "", false
	}
	p, ok := loomPatternItems[name]
	return p, ok
}

// loomSelectable is LoomMenu.getSelectablePatterns: an empty pattern
// slot yields the no_item_required tag; a pattern item its provided
// holder set.
func loomSelectable(pattern ItemStack) []string {
	if pattern.Empty() {
		return loomFreePatterns[:]
	}
	if p, ok := loomPatternProvides(pattern); ok {
		return []string{p}
	}
	return nil
}

// loomLayerCount counts the stack's banner_patterns layers — the
// six-layer cap on loom work. The canonical form is
// "pattern:color,..." so the count is its comma-separated parts.
func loomLayerCount(it ItemStack) int {
	if it.BannerPatterns == "" {
		return 0
	}
	return len(strings.Split(it.BannerPatterns, ","))
}

// loomBuild builds the result stack for a selected pattern: a
// count-1 copy of the banner input with one layer appended,
// LoomMenu.setupResultSlot.
func loomBuild(banner ItemStack, pattern string, color int32) ItemStack {
	out := banner.withCount(1)
	layer := pattern + ":" + loomDyeNames[color]
	if out.BannerPatterns == "" {
		out.BannerPatterns = layer
	} else {
		out.BannerPatterns += "," + layer
	}
	return out
}

// loomSelMove writes the new selected index under the containers lock
// and reports whether the value changed — the caller emits the
// Container Property afterwards, outside the lock, since a DataSlot
// only broadcasts on change.
func loomSelMove(c *containerData, v int32) bool {
	if c.loomSel == v {
		return false
	}
	c.loomSel = v
	return true
}

// loomSync is LoomMenu.slotsChanged: rebuild the selectable list and
// the result slot after any container-side change.
func loomSync(s *Server, sess *Session, w *openWindow, c *containerData, touched *[]int) {
	banner := sess.menuStack(loomBanner)
	dye := sess.menuStack(loomDye)
	pattern := sess.menuStack(loomPattern)
	if banner.Empty() || dye.Empty() {
		w.dim.containers.mu.Lock()
		c.loomPatterns = nil
		changed := loomSelMove(c, -1)
		w.dim.containers.mu.Unlock()
		if changed {
			s.sendContainerProperty(sess, w.id, 0, -1)
		}
		if !sess.menuStack(loomResult).Empty() {
			sess.setMenuStack(loomResult, ItemStack{})
			*touched = append(*touched, loomResult)
		}
		return
	}
	w.dim.containers.mu.Lock()
	prevValid := c.loomSel >= 0 && int(c.loomSel) < len(c.loomPatterns)
	var prevPattern string
	if prevValid {
		prevPattern = c.loomPatterns[c.loomSel]
	}
	c.loomPatterns = loomSelectable(pattern)
	var holder string
	have := false
	sel := int32(-1)
	switch {
	case len(c.loomPatterns) == 1:
		sel, holder, have = 0, c.loomPatterns[0], true
	case !prevValid:
		sel = -1
	default:
		idx := -1
		for i, p := range c.loomPatterns {
			if p == prevPattern {
				idx = i
				break
			}
		}
		if idx >= 0 {
			sel, holder, have = int32(idx), prevPattern, true
		}
	}
	w.dim.containers.mu.Unlock()
	var out ItemStack
	if have && loomLayerCount(banner) >= 6 {
		sel, have = -1, false
	}
	w.dim.containers.mu.Lock()
	changed := loomSelMove(c, sel)
	w.dim.containers.mu.Unlock()
	if changed {
		s.sendContainerProperty(sess, w.id, 0, sel)
	}
	if have {
		if col, ok := loomDyeOf(dye); ok {
			out = loomBuild(banner, holder, col)
		}
	}
	if sess.menuStack(loomResult) != out {
		sess.setMenuStack(loomResult, out)
		*touched = append(*touched, loomResult)
	}
}

// loomSelect is LoomMenu.clickMenuButton: a valid pattern index
// selects it and writes the result slot; an out-of-range index is
// dropped without resync.
func (s *Server) loomSelect(sess *Session, w *openWindow, button int32) {
	c := w.container
	w.dim.containers.mu.Lock()
	valid := button >= 0 && int(button) < len(c.loomPatterns)
	var holder string
	if valid {
		changed := loomSelMove(c, button)
		holder = c.loomPatterns[button]
		w.dim.containers.mu.Unlock()
		if changed {
			s.sendContainerProperty(sess, w.id, 0, button)
		}
	} else {
		w.dim.containers.mu.Unlock()
		return
	}
	var out ItemStack
	banner := sess.menuStack(loomBanner)
	dye := sess.menuStack(loomDye)
	if col, ok := loomDyeOf(dye); ok && !banner.Empty() {
		out = loomBuild(banner, holder, col)
	}
	if sess.menuStack(loomResult) != out {
		sess.setMenuStack(loomResult, out)
		s.syncContainerMenuSlots(sess, w, []int{loomResult})
	}
}

// loomTake is LoomMenu$6.onTake: the banner and dye each lose one (the
// pattern item is kept); when both run out the selection resets; the
// take plays ui.loom.take_result at the block.
func (s *Server) loomTake(sess *Session, w *openWindow) {
	for _, slot := range []int{loomBanner, loomDye} {
		it := sess.menuStack(slot)
		it.Count--
		if it.Count <= 0 {
			it = ItemStack{}
		}
		sess.setMenuStack(slot, it)
	}
	c := w.container
	if sess.menuStack(loomBanner).Empty() && sess.menuStack(loomDye).Empty() {
		w.dim.containers.mu.Lock()
		changed := loomSelMove(c, -1)
		w.dim.containers.mu.Unlock()
		if changed {
			s.sendContainerProperty(sess, w.id, 0, -1)
		}
	}
	if id, ok := SoundEventID("ui.loom.take_result"); ok {
		s.playSoundIn(w.dim.name, w.pos, id)
	}
}

// loomAccepts is the per-slot mayPlace: banner items into the banner
// slot, dyes into the dye slot, pattern items into the pattern slot.
func loomAccepts(slot int, it ItemStack) bool {
	switch slot {
	case loomBanner:
		return loomIsBanner(it)
	case loomDye:
		_, ok := loomDyeOf(it)
		return ok
	case loomPattern:
		_, ok := loomPatternProvides(it)
		return ok
	}
	return false
}
