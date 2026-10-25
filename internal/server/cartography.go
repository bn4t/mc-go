package server

// The cartography table is menu 23: three container slots —
// 0 map, 1 additional, 2 result — plus the 36 player slots. Measured
// on the 26.2 jar's CartographyTableMenu (and $3/$4/$5 inner slots):
//
//   - map slot mayPlace = stack.has(MAP_ID): only a filled_map
//     carrying a map_id goes in; the additional slot takes paper,
//     the empty map item, or glass_pane; the result slot refuses.
//   - slotsChanged fires on input changes only: an empty map or
//     additional clears the result; both present recomputes it.
//   - setupResultSlot looks up the map's saved data first — a map_id
//     with no data yields no result — then: paper + unlocked +
//     scale<4 → a count-1 copy stamped MAP_POST_PROCESSING=SCALE;
//     glass_pane + unlocked → a count-1 copy stamped =LOCK; the
//     empty map item → a count-2 copy of the same id (a clone);
//     anything else → no result. The result writes only when it
//     differs from the slot's content.
//   - the result's onTake removes one of each input, calls
//     onCraftedBy (which resolves map_post_processing into a fresh
//     MapId — a scaled copy for SCALE, a locked copy for LOCK), and
//     plays ui.cartography_table.take_result once per game tick at
//     the block (lastSoundTime dedup).
//   - removed returns the inputs to the player via clearContainer.

// MapPost's internal encodings: the wire enum is lock 0, scale 1, and
// the stack stores wire+1 so 0 reads as absent.
const (
	mapPostLock  = 1
	mapPostScale = 2
)

// cartographyAccepts is the per-slot mayPlace.
func cartographyAccepts(slot int, it ItemStack) bool {
	switch slot {
	case cartographyMap:
		return it.MapID > 0
	case cartographyAdditional:
		return itemNamed(it, "paper") || itemNamed(it, "map") ||
			itemNamed(it, "glass_pane")
	}
	return true
}

// cartographySync mirrors slotsChanged: clear the result when an
// input empties, otherwise recompute it when it differs.
func (s *Server) cartographySync(sess *Session, w *openWindow, touched *[]int) {
	mapSt := sess.menuStack(cartographyMap)
	addSt := sess.menuStack(cartographyAdditional)
	resSt := sess.menuStack(cartographyResult)
	if !resSt.Empty() && (mapSt.Empty() || addSt.Empty()) {
		sess.setMenuStack(cartographyResult, ItemStack{})
		*touched = append(*touched, cartographyResult)
		return
	}
	if mapSt.Empty() || addSt.Empty() {
		return
	}
	md, ok := w.dim.maps.get(mapSt.MapID - 1)
	if !ok {
		return
	}
	var res ItemStack
	switch {
	case itemNamed(addSt, "paper") && !md.locked && md.scale < 4:
		res = mapSt.withCount(1)
		res.MapPost = mapPostScale
	case itemNamed(addSt, "glass_pane") && !md.locked:
		res = mapSt.withCount(1)
		res.MapPost = mapPostLock
	case itemNamed(addSt, "map"):
		res = mapSt.withCount(2)
	default:
		res = ItemStack{}
	}
	if resSt != res {
		sess.setMenuStack(cartographyResult, res)
		*touched = append(*touched, cartographyResult)
	}
}

// cartographyTake is the result slot's onTake: one of each input and
// the take_result sound at most once per game tick. Post-processing
// resolves on the taken stack itself — cartographyApplyPost — called
// by the click paths before the stack settles.
func (s *Server) cartographyTake(sess *Session, w *openWindow) {
	for _, slot := range []int{cartographyMap, cartographyAdditional} {
		it := sess.menuStack(slot)
		it.Count--
		if it.Count <= 0 {
			it = ItemStack{}
		}
		sess.setMenuStack(slot, it)
	}
	c := w.container
	w.dim.containers.mu.Lock()
	fire := s.gameTime() != c.cartSoundTick
	c.cartSoundTick = s.gameTime()
	w.dim.containers.mu.Unlock()
	if fire {
		if id, ok := SoundEventID("ui.cartography_table.take_result"); ok {
			s.playSoundIn(w.dim.name, w.pos, id)
		}
	}
}

// syncInventorySlot stores one window-0 slot and emits it where the
// client looks: on the open menu window when one is up (the open
// menu's broadcastChanges carries inventory changes as menu slots),
// otherwise the plain player-inventory packet.
func (s *Server) syncInventorySlot(sess *Session, slot int16, it ItemStack) {
	sess.setStack(slot, it)
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w != nil && w.container != nil {
		s.syncContainerMenuSlots(sess, w, []int{w.container.spec.slots + int(slot-9)})
		return
	}
	_ = s.sendContainerSlot(sess, slot, it)
}

// addStackMenu is addStack emitting on the open menu window: merge
// hotbar-first then main inventory, then the first empty slot — the
// same order Inventory.add uses. base is the window's player-slot
// offset (containerSize covers every menu kind).
func (s *Server) addStackMenu(sess *Session, w *openWindow, it ItemStack) {
	base := w.containerSize()
	var touched []int
	for i := base + 27; i < base+36 && !it.Empty(); i++ {
		other := sess.menuStack(i)
		if !other.Stackable(it) {
			continue
		}
		room := maxStackOfID(other.ID) - other.Count
		move := it.Count
		if move > room {
			move = room
		}
		other.Count += move
		it.Count -= move
		sess.setMenuStack(i, other)
		touched = append(touched, i)
	}
	for i := base; i < base+27 && !it.Empty(); i++ {
		other := sess.menuStack(i)
		if !other.Stackable(it) {
			continue
		}
		room := maxStackOfID(other.ID) - other.Count
		move := it.Count
		if move > room {
			move = room
		}
		other.Count += move
		it.Count -= move
		sess.setMenuStack(i, other)
		touched = append(touched, i)
	}
	for i := base + 27; i < base+36 && !it.Empty(); i++ {
		if !sess.menuStack(i).Empty() {
			continue
		}
		put := it.Count
		if limit := maxStackOfID(it.ID); put > limit {
			put = limit
		}
		sess.setMenuStack(i, it.withCount(put))
		it.Count -= put
		if it.Count <= 0 {
			it = ItemStack{}
		}
		touched = append(touched, i)
	}
	for i := base; i < base+27 && !it.Empty(); i++ {
		if !sess.menuStack(i).Empty() {
			continue
		}
		put := it.Count
		if limit := maxStackOfID(it.ID); put > limit {
			put = limit
		}
		sess.setMenuStack(i, it.withCount(put))
		it.Count -= put
		if it.Count <= 0 {
			it = ItemStack{}
		}
		touched = append(touched, i)
	}
	s.syncContainerMenuSlots(sess, w, touched)
	if !it.Empty() {
		s.dropItemLocked(sess, it)
	}
}

// cartographyApplyPost resolves a taken result's map_post_processing:
// the component drops and the stack's map_id rewrites to a fresh id
// holding the scaled or locked data (MapItem.onCraftedPostProcess →
// scaleMap/lockMap). A stack with no post, or whose map_id has no
// saved data, keeps its id — only the component clears.
func (s *Server) cartographyApplyPost(sess *Session, it *ItemStack) {
	if it.MapPost <= 0 {
		return
	}
	post := it.MapPost
	it.MapPost = 0
	if nid := sess.dimension().maps.derive(it.MapID-1, post); nid >= 0 {
		it.MapID = nid + 1
	}
}
