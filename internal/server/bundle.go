package server

import (
	"math"
	"math/rand"
	"strings"

	"mc-go/internal/world"
)

// compBundleContents is minecraft:bundle_contents, registered 51st in
// the data_component_type registry (id 50 in both measured profiles).
// BundleContents.STREAM_CODEC is a VarInt-counted list of
// ItemStackTemplate (item id, count, component patch) — unlike the
// container component there is no per-entry presence flag.
const compBundleContents = 50

// BundleContents is the minecraft:bundle_contents component: the
// stacks a bundle holds, newest first. It is shared by pointer the way
// a shulker box's Contents is, and treated as immutable once a stack
// carries it: every change builds a new value, so copies of a stack
// never see each other's edits (vanilla's component is an immutable
// record too).
type BundleContents struct {
	Items []ItemStack `json:"items"`
	// Selected is the selected entry's index +1, 0 for none — what
	// Select Bundle Item writes and a later removal reads. It belongs to
	// the component value but never leaves the server: the stream and
	// persistent codecs carry only the list (decoding always yields no
	// selection), and component equality ignores it.
	Selected int32 `json:"-"`
}

// Bundle weights are kept in sixty-fourths of a bundle. Vanilla uses
// exact fractions — 1/maxStackSize per item, 1/16 on top of a nested
// bundle's own contents, a full bundle for a hive holding bees — and the
// measured stack sizes are only 1, 16 and 64, so every weight it can
// compute is a whole number of sixty-fourths.
const (
	bundleCapacity     = 64
	bundleNestedWeight = 4
)

// isBundleItemName reports the seventeen BundleItem ids (the
// minecraft:bundles tag): bundle and its sixteen dyed variants.
func isBundleItemName(name string) bool {
	if name == "bundle" {
		return true
	}
	color, ok := strings.CutSuffix(name, "_bundle")
	if !ok {
		return false
	}
	for _, d := range loomDyeNames {
		if d == color {
			return true
		}
	}
	return false
}

func isBundleItem(id int32) bool {
	name, ok := world.ItemName(id)
	return ok && isBundleItemName(name)
}

// bundleOf reports a stack's bundle_contents. A bundle item always has
// the component — its default is the empty list, which a patch never
// stores, so a nil Bundle on a bundle item reads as empty. Any other
// item has one only when a patch added it.
func bundleOf(it ItemStack) (*BundleContents, bool) {
	if it.Bundle != nil {
		return it.Bundle, true
	}
	if it.Empty() || !isBundleItem(it.ID) {
		return nil, false
	}
	return &BundleContents{}, true
}

// bundleCanHold is canItemBeInBundle: anything but an empty stack or a
// shulker box (the one item whose canFitInsideContainerItems is false).
func bundleCanHold(it ItemStack) bool {
	if it.Empty() {
		return false
	}
	name, ok := world.ItemName(it.ID)
	return !ok || (name != "shulker_box" && !strings.HasSuffix(name, "_shulker_box"))
}

// bundleItemWeight is one item's weight: a nested bundle_contents
// weighs its own contents plus 1/16, a hive with bees fills a bundle,
// anything else is 1/maxStackSize.
func bundleItemWeight(it ItemStack) int64 {
	if b, ok := bundleOf(it); ok {
		return bundleWeight(b.Items) + bundleNestedWeight
	}
	if it.Bees > 0 {
		return bundleCapacity
	}
	return bundleCapacity / int64(maxStackOfID(it.ID))
}

// bundleWeight sums weight*count over a contents list.
func bundleWeight(items []ItemStack) int64 {
	var w int64
	for _, it := range items {
		w += bundleItemWeight(it) * int64(it.Count)
	}
	return w
}

// bundleMut is BundleContents' working copy while a click or a use
// edits it. Vanilla falls back to an empty working copy when the stored
// weight overflows its int fractions; the bounded decoders here cannot
// produce such contents, so that branch has no counterpart.
type bundleMut struct {
	items  []ItemStack
	weight int64
	sel    int32 // -1 for none
}

func newBundleMut(b *BundleContents) bundleMut {
	items := make([]ItemStack, len(b.Items))
	copy(items, b.Items)
	return bundleMut{items: items, weight: bundleWeight(items), sel: b.Selected - 1}
}

// maxAdd is how many items of weight w still fit: the free weight over
// w, truncated toward zero and floored at zero (an overfull bundle from
// a command takes nothing).
func (m *bundleMut) maxAdd(w int64) int32 {
	n := (bundleCapacity - m.weight) / w
	if n < 0 {
		return 0
	}
	return int32(n)
}

// mergeIndex is the entry st merges into: none for an item that does
// not stack (a stack size of 1 — damageable items measure 1 too), else
// the first entry with the same item and components.
func (m *bundleMut) mergeIndex(st ItemStack) int {
	if maxStackOfID(st.ID) <= 1 {
		return -1
	}
	for i, e := range m.items {
		if sameItemSameComponents(e, st) {
			return i
		}
	}
	return -1
}

// tryInsert moves as many of *st as fit into the bundle and reports how
// many moved. The merged or new entry always lands at the front: a
// merge removes the old entry and re-adds the grown copy at index 0.
func (m *bundleMut) tryInsert(st *ItemStack) int32 {
	if !bundleCanHold(*st) {
		return 0
	}
	w := bundleItemWeight(*st)
	n := min(st.Count, m.maxAdd(w))
	if n == 0 {
		return 0
	}
	m.weight += w * int64(n)
	var entry ItemStack
	if i := m.mergeIndex(*st); i >= 0 {
		entry = m.items[i].withCount(m.items[i].Count + n)
		m.items = append(m.items[:i], m.items[i+1:]...)
	} else {
		entry = st.withCount(n)
	}
	m.items = append([]ItemStack{entry}, m.items...)
	st.Count -= n
	if st.Count <= 0 {
		*st = ItemStack{}
	}
	return n
}

// toggle is toggleSelectedItem: selecting the current entry again, or
// anything outside the list, clears the selection.
func (m *bundleMut) toggle(i int32) {
	if m.sel == i || i < 0 || int(i) >= len(m.items) {
		m.sel = -1
		return
	}
	m.sel = i
}

// removeOne takes the selected entry, or the front one when nothing
// valid is selected, and clears the selection.
func (m *bundleMut) removeOne() (ItemStack, bool) {
	if len(m.items) == 0 {
		return ItemStack{}, false
	}
	i := 0
	if m.sel >= 0 && int(m.sel) < len(m.items) {
		i = int(m.sel)
	}
	out := m.items[i]
	m.items = append(m.items[:i:i], m.items[i+1:]...)
	m.weight -= bundleItemWeight(out) * int64(out.Count)
	m.toggle(-1)
	return out, true
}

// store writes the working copy back as a fresh component value. A
// bundle item left empty drops the component from its patch — the
// empty list is its default — exactly as vanilla's patched component
// map removes a value equal to the prototype's.
func (m *bundleMut) store(it *ItemStack) {
	if len(m.items) == 0 && isBundleItem(it.ID) {
		it.Bundle = nil
		return
	}
	it.Bundle = &BundleContents{Items: m.items, Selected: m.sel + 1}
}

// sameItemSameComponents is ItemStack.isSameItemSameComponents: same
// item and an equal component patch, counts ignored. Nested contents
// compare by value rather than by pointer.
func sameItemSameComponents(a, b ItemStack) bool {
	a.Count, b.Count = 1, 1
	return itemStacksEqual(a, b)
}

// itemStacksEqual compares two stacks by value, following the
// container and bundle contents instead of comparing their pointers.
// A bundle's selection is not part of its value.
func itemStacksEqual(a, b ItemStack) bool {
	if a.Empty() || b.Empty() {
		return a.Empty() && b.Empty()
	}
	if (a.Contents == nil) != (b.Contents == nil) || !bundleEqual(a.Bundle, b.Bundle) {
		return false
	}
	if a.Contents != nil && a.Contents != b.Contents {
		for i := range a.Contents {
			if !itemStacksEqual(a.Contents[i], b.Contents[i]) {
				return false
			}
		}
	}
	a.Contents, b.Contents = nil, nil
	a.Bundle, b.Bundle = nil, nil
	return a == b
}

// bundleEqual is BundleContents.equals: the item lists compare, the
// selection does not.
func bundleEqual(a, b *BundleContents) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a == b {
		return true
	}
	if len(a.Items) != len(b.Items) {
		return false
	}
	for i := range a.Items {
		if !itemStacksEqual(a.Items[i], b.Items[i]) {
			return false
		}
	}
	return true
}

// bundleSlotView is the clicked menu slot as BundleItem's click
// overrides see it — vanilla's Slot: the stack it holds plus the rules
// safeTake, safeInsert and allowModification consult. Callers build one
// from their own slot storage and write item back afterwards.
type bundleSlotView struct {
	item      ItemStack
	mayPlace  func(ItemStack) bool  // nil: the slot takes any stack
	mayPickup bool                  // false: a locked slot
	max       func(ItemStack) int32 // nil: the stack's own size
}

func (v *bundleSlotView) placeable(it ItemStack) bool {
	return v.mayPlace == nil || v.mayPlace(it)
}

func (v *bundleSlotView) slotMax(it ItemStack) int32 {
	if v.max != nil {
		return v.max(it)
	}
	return maxStackOfID(it.ID)
}

// allowModification: the slot gives up its stack and would take it back.
func (v *bundleSlotView) allowModification() bool {
	return v.mayPickup && v.placeable(v.item)
}

// safeTake is Slot.safeTake(count, max): a slot that does not allow
// modification gives all or nothing.
func (v *bundleSlotView) safeTake(count, max int32) ItemStack {
	if !v.mayPickup || (!v.allowModification() && max < v.item.Count) {
		return ItemStack{}
	}
	n := min(count, max, v.item.Count)
	if n <= 0 || v.item.Empty() {
		return ItemStack{}
	}
	out := v.item.withCount(n)
	if v.item.Count -= n; v.item.Count <= 0 {
		v.item = ItemStack{}
	}
	return out
}

// safeInsert is Slot.safeInsert: as much of st as the slot accepts,
// returning what is left.
func (v *bundleSlotView) safeInsert(st ItemStack) ItemStack {
	if st.Empty() || !v.placeable(st) {
		return st
	}
	n := min(st.Count, v.slotMax(st)-v.item.Count)
	if n <= 0 {
		return st
	}
	switch {
	case v.item.Empty():
		v.item = st.withCount(n)
	case sameItemSameComponents(v.item, st):
		v.item.Count += n
	default:
		return st
	}
	if st.Count -= n; st.Count <= 0 {
		return ItemStack{}
	}
	return st
}

// bundleClick runs BundleItem's PICKUP-click overrides the way
// AbstractContainerMenu.doClick offers them: first to the carried
// stack (overrideStackedOnOther), then to the clicked slot's stack
// (overrideOtherStackedOnMe). It reports whether a bundle consumed the
// click; when it did not, the caller's ordinary pickup runs on the
// (possibly selection-cleared) slot stack. right is the secondary
// button. Either way the caller writes slot.item and *carried back.
func (s *Server) bundleClick(sess *Session, right bool, slot *bundleSlotView, carried *ItemStack) bool {
	if b, ok := bundleOf(*carried); ok && isBundleItem(carried.ID) {
		m := newBundleMut(b)
		switch {
		case !right && !slot.item.Empty():
			moved := int32(0)
			if bundleCanHold(slot.item) {
				taken := slot.safeTake(slot.item.Count, m.maxAdd(bundleItemWeight(slot.item)))
				moved = m.tryInsert(&taken)
			}
			s.bundleInsertSound(sess, moved > 0)
			m.store(carried)
			return true
		case right && slot.item.Empty():
			if out, ok := m.removeOne(); ok {
				if rest := slot.safeInsert(out); !rest.Empty() {
					m.tryInsert(&rest) // back in at the front, silently
				} else {
					s.bundlePlayerSound(sess, "item.bundle.remove_one")
				}
			}
			m.store(carried)
			return true
		}
	}
	b, ok := bundleOf(slot.item)
	if !ok || !isBundleItem(slot.item.ID) {
		return false
	}
	m := newBundleMut(b)
	switch {
	case !right && !carried.Empty():
		s.bundleInsertSound(sess, slot.allowModification() && m.tryInsert(carried) > 0)
	case right && carried.Empty():
		if slot.allowModification() {
			if out, ok := m.removeOne(); ok {
				s.bundlePlayerSound(sess, "item.bundle.remove_one")
				*carried = out
			}
		}
	default:
		// A plain left-click pickup, or a right-click with something
		// carried: the selection clears and the ordinary click runs.
		m.toggle(-1)
		m.store(&slot.item)
		return false
	}
	m.store(&slot.item)
	return true
}

// bundleInsertSound plays item.bundle.insert or, when nothing moved,
// item.bundle.insert_fail at fixed volume and pitch.
func (s *Server) bundleInsertSound(sess *Session, ok bool) {
	if ok {
		s.bundlePlayerSound(sess, "item.bundle.insert")
		return
	}
	if snd, found := soundEventIDs["item.bundle.insert_fail"]; found {
		x, y, z, _, _, _ := sess.Pos()
		s.playSoundSrc(sess.dimName(), x, y, z, snd, srcPlayers, 1, 1, sess)
	}
}

// bundlePlayerSound is Player.playSound for the insert/remove family:
// volume 0.8, pitch 0.8 + rand*0.4, PLAYERS source at the player's
// feet, everyone nearby but the player (whose client plays it itself).
func (s *Server) bundlePlayerSound(sess *Session, name string) {
	snd, ok := soundEventIDs[name]
	if !ok {
		return
	}
	x, y, z, _, _, _ := sess.Pos()
	s.playSoundSrc(sess.dimName(), x, y, z, snd, srcPlayers, 0.8, 0.8+rand.Float32()*0.4, sess)
}

// window0BundleSlot views one player-inventory menu slot for a bundle
// click. The armor slots are vanilla's ArmorSlot: they hold one piece
// of their own kind, and Curse of Binding locks a worn piece in place
// outside creative. Every other slot takes anything.
func window0BundleSlot(sess *Session, slot int16) bundleSlotView {
	v := bundleSlotView{item: sess.inv[slot], mayPickup: true}
	if slot >= slotArmorStart && slot < slotArmorStart+4 {
		want := [4]int32{equipHelmet, equipChest, equipLegs, equipBoots}[slot-slotArmorStart]
		v.mayPlace = func(it ItemStack) bool {
			name, _ := world.ItemName(it.ID)
			return armorStandEquipSlot(name) == want
		}
		v.max = func(ItemStack) int32 { return 1 }
		if v.item.enchLevel("binding_curse") > 0 && sess.mode() != GameModeCreative {
			v.mayPickup = false
		}
	}
	return v
}

// bundleUseTicks is BundleItem.getUseDuration.
const bundleUseTicks = 200

// useBundle is BundleItem.use: startUsingItem, always a success, even
// for an empty bundle — the drops happen from the use ticks.
func (s *Server) useBundle(sess *Session, hand int32, st ItemStack) {
	sess.bundleUseLeft = bundleUseTicks
	sess.bundleUseHand = hand
	sess.bundleUseItem = st.ID
	sess.usingItemID = st.ID
}

// bundleUseTick is LivingEntity.updatingUsingItem for a bundle in use:
// a hand that no longer holds the same item stops the use; otherwise
// BundleItem.onUseTick drops one entry on the first use tick and on
// every even remaining count below 190 (one every two ticks after a
// ten-tick pause), and the use completes when the count runs out.
func (s *Server) bundleUseTick(sess *Session) {
	if sess.bundleUseLeft <= 0 {
		return
	}
	st, slot := sess.stackForHand(sess.bundleUseHand)
	if st.Empty() || st.ID != sess.bundleUseItem {
		sess.bundleUseLeft = 0
		sess.usingItemID = 0
		return
	}
	left := sess.bundleUseLeft
	if left == bundleUseTicks || (left < bundleUseTicks-10 && left%2 == 0) {
		s.bundleDropOne(sess, slot)
	}
	if sess.bundleUseLeft--; sess.bundleUseLeft == 0 {
		sess.usingItemID = 0
		x, y, z, _, _, _ := sess.Pos()
		s.emitGameEvent(sess.dimension(), "item_interact_finish", x, y, z, sess, "")
	}
}

// bundleDropOne is BundleItem.dropContent: the selected (else front)
// entry leaves the bundle with the remove sound, is thrown like a Q
// drop, and only then do the drop-contents sound (heard by everyone,
// the user included, at the block position) and the used stat follow.
func (s *Server) bundleDropOne(sess *Session, slot int16) {
	sess.invMu.Lock()
	st := sess.inv[slot]
	b, ok := bundleOf(st)
	if !ok {
		sess.invMu.Unlock()
		return
	}
	m := newBundleMut(b)
	out, ok := m.removeOne()
	if !ok {
		sess.invMu.Unlock()
		return
	}
	s.bundlePlayerSound(sess, "item.bundle.remove_one")
	m.store(&st)
	sess.inv[slot] = st
	s.dropItemLocked(sess, out)
	sess.invMu.Unlock()
	_ = s.sendContainerSlot(sess, slot, st)
	if snd, ok := soundEventIDs["item.bundle.drop_contents"]; ok {
		x, y, z, _, _, _ := sess.Pos()
		bx, by, bz := math.Floor(x)+0.5, math.Floor(y)+0.5, math.Floor(z)+0.5
		s.playSoundSrc(sess.dimName(), bx, by, bz, snd, srcPlayers, 0.8, 0.8+rand.Float32()*0.4, nil)
	}
	sess.addItemStat(statUsed, st.ID, 1)
}

// itemEntityDestroyed is ItemStack.onDestroyed for an item entity that
// health just ran out on, before it is discarded: a bundle
// (BundleItem) and a block item carrying container contents
// (BlockItem — the shulker box) empty their component and spill every
// stored stack through ItemUtils.onContainerDestroyed — a fresh item
// entity at the destroyed one's position with ItemEntity's default
// toss and no pickup delay.
func (s *Server) itemEntityDestroyed(e *Entity) {
	var spill []ItemStack
	if b := e.Item.Bundle; b != nil && isBundleItem(e.Item.ID) {
		spill = append(spill, b.Items...)
		e.Item.Bundle = nil
	}
	if c := e.Item.Contents; c != nil {
		for _, it := range c {
			if !it.Empty() {
				spill = append(spill, it)
			}
		}
		e.Item.Contents = nil
	}
	itemType := mustEntityType("item")
	for _, it := range spill {
		out := s.entities.Spawn(&Entity{
			Type:    itemType,
			Dim:     e.Dim,
			X:       e.X,
			Y:       e.Y,
			Z:       e.Z,
			MotionX: rand.Float64()*0.2 - 0.1,
			MotionY: 0.2,
			MotionZ: rand.Float64()*0.2 - 0.1,
			Item:    it,
		})
		s.broadcastEntitySpawn(nil, *out)
		s.broadcastEntityMetadata(out.ID, encodeItemMetadata(out.Item))
	}
}
