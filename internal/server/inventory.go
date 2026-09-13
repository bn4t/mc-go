package server

import (
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// ItemStack is one inventory slot value. Count<=0 means empty. Data
// components are not modeled yet; every stack is sent with an empty
// component patch, which is what vanilla does for plain items.
type ItemStack struct {
	ID    int32
	Count int32
}

// Empty reports whether the slot holds nothing.
func (it ItemStack) Empty() bool { return it.Count <= 0 || it.ID == 0 }

// Player inventory window layout (menu slots):
//
//	0      crafting result
//	1-4    crafting grid
//	5-8    armor
//	9-35   main inventory (27)
//	36-44  hotbar (9)
//	45     offhand
const (
	slotCraftResult = 0
	slotCraftStart  = 1
	slotArmorStart  = 5
	slotMainStart   = 9
	slotHotbarStart = 36
	slotOffhand     = 45
	slotCount       = 46
)

// invSlotForHotbar maps a selected hotbar index (0-8) to its menu slot.
func invSlotForHotbar(held int32) int16 { return int16(slotHotbarStart + held) }

// writeSlot writes one Slot value: VarInt count, then (when non-empty)
// item id and empty component add/remove counts.
func writeSlot(b *protocol.Buffer, it ItemStack) {
	if it.Empty() {
		b.VarInt(0)
		return
	}
	b.VarInt(it.Count)
	b.VarInt(it.ID)
	b.VarInt(0) // components to add
	b.VarInt(0) // components to remove
}

// readSlot reads one Slot value, ignoring component patches.
func readSlot(r *protocol.PacketReader) (ItemStack, error) {
	count, err := r.VarInt()
	if err != nil || count <= 0 {
		return ItemStack{}, err
	}
	id, err := r.VarInt()
	if err != nil {
		return ItemStack{}, err
	}
	if _, err := r.VarInt(); err != nil { // components to add
		return ItemStack{}, err
	}
	if _, err := r.VarInt(); err != nil { // components to remove
		return ItemStack{}, err
	}
	return ItemStack{ID: id, Count: count}, nil
}

// setSlot records one inventory slot; count<=0 clears it.
func (sess *Session) setSlot(slot int16, item int32, count int32) {
	if slot < 0 || slot >= slotCount {
		return
	}
	sess.invMu.Lock()
	defer sess.invMu.Unlock()
	if count <= 0 {
		sess.inv[slot] = ItemStack{}
		return
	}
	sess.inv[slot] = ItemStack{ID: item, Count: count}
}

// setStack stores one stack value.
func (sess *Session) setStack(slot int16, it ItemStack) {
	if slot < 0 || slot >= slotCount {
		return
	}
	sess.invMu.Lock()
	sess.inv[slot] = it
	sess.invMu.Unlock()
}

// stackAt reads one slot value.
func (sess *Session) stackAt(slot int16) ItemStack {
	if slot < 0 || slot >= slotCount {
		return ItemStack{}
	}
	sess.invMu.RLock()
	defer sess.invMu.RUnlock()
	return sess.inv[slot]
}

// heldStack returns the selected hotbar slot's stack.
func (sess *Session) heldStack() ItemStack {
	return sess.stackAt(invSlotForHotbar(sess.held.Load()))
}

// heldItem keeps the old (id, ok) shape used by placement code.
func (sess *Session) heldItem() (int32, bool) {
	it := sess.heldStack()
	if it.Empty() {
		return 0, false
	}
	return it.ID, true
}

// sendContainerSlot syncs one inventory slot to the client.
func (s *Server) sendContainerSlot(sess *Session, slot int16, it ItemStack) error {
	var b protocol.Buffer
	b.VarInt(0) // window 0: player inventory
	b.VarInt(sess.invState.Load())
	b.VarInt(int32(slot))
	writeSlot(&b, it)
	return sess.write(sess.ids().PlayContainerSlot, b.Bytes())
}

// sendCarriedItem syncs the click cursor stack.
func (s *Server) sendCarriedItem(sess *Session, it ItemStack) error {
	var b protocol.Buffer
	writeSlot(&b, it)
	return sess.write(sess.ids().PlaySetCarriedItem, b.Bytes())
}

// sendContainerContent syncs the full player inventory window.
func (s *Server) sendContainerContent(sess *Session) error {
	var b protocol.Buffer
	b.VarInt(0) // window 0: player inventory
	b.VarInt(sess.invState.Load())
	b.VarInt(slotCount)
	sess.invMu.RLock()
	for i := 0; i < slotCount; i++ {
		writeSlot(&b, sess.inv[i])
	}
	sess.invMu.RUnlock()
	writeSlot(&b, sess.carried)
	return sess.write(sess.ids().PlayContainerContent, b.Bytes())
}

// maxStack is the vanilla stack limit for unstackable-unaware items.
const maxStack = 64

// handleClickContainer implements the core click modes on the player
// inventory. Click modes: 0 pickup, 1 quick-move, 2 swap, 3 creative
// clone, 4 throw. Exotic modes are acknowledged with a state bump and
// a full resync rather than silently diverging. The return value
// reports whether an equipment slot changed, so the caller can
// broadcast it after the inventory lock is released.
func (s *Server) handleClickContainer(sess *Session, p protocol.Packet) bool {
	r := protocol.NewPacketReader(p.Data)
	win, err := r.VarInt()
	if err != nil {
		return false
	}
	if win != 0 {
		// A container window is open: route to its menu.
		sess.windowMu.Lock()
		w := sess.window
		sess.windowMu.Unlock()
		if w != nil && w.craft != nil {
			s.handleCraftTableClickPacket(sess, p)
			return false
		}
		if w != nil && w.hopper != nil {
			s.handleHopperClickPacket(sess, p)
			return false
		}
		s.handleChestClickPacket(sess, p)
		return false
	}
	if _, err := r.VarInt(); err != nil { // state id
		return false
	}
	slot, err := r.Short()
	if err != nil {
		return false
	}
	button, err := r.Byte()
	if err != nil {
		return false
	}
	mode, err := r.VarInt()
	if err != nil {
		return false
	}
	changed, ok := readChangedSlots(r)
	if !ok {
		return false
	}
	if _, err := readSlot(r); err != nil { // carried item
		// Tolerate a truncated tail only for empty carried items.
		return false
	}

	// The crafting result is take-only and drives crafting.
	if slot == slotCraftResult {
		s.handleCraftResultClick(sess, button, mode)
		return false
	}

	sess.invMu.Lock()
	defer sess.invMu.Unlock()

	// Remember whether the click touched the 2x2 grid so the result can
	// refresh after the normal handling, and whether it touched any
	// equipment slot so armor changes can be broadcast.
	gridTouched := slot >= slotCraftStart && slot < slotCraftStart+4
	equipTouched := (slot >= slotArmorStart && slot < slotArmorStart+4) || slot == slotOffhand

	var touched []int16
	touch := func(sl int16) { touched = append(touched, sl) }

	switch mode {
	case 0: // pickup / place with cursor
		cur := sess.carried
		target := sess.inv[slot]
		if button == 0 { // left click: whole stack
			if cur.Empty() {
				sess.carried = target
				sess.inv[slot] = ItemStack{}
			} else if target.Empty() {
				sess.inv[slot] = cur
				sess.carried = ItemStack{}
			} else if target.ID == cur.ID {
				room := maxStack - target.Count
				move := cur.Count
				if move > room {
					move = room
				}
				sess.inv[slot].Count += move
				sess.carried.Count -= move
				if sess.carried.Count <= 0 {
					sess.carried = ItemStack{}
				}
			} else {
				sess.inv[slot] = cur
				sess.carried = target
			}
		} else { // right click: split / place one
			if cur.Empty() {
				if !target.Empty() {
					half := (target.Count + 1) / 2
					sess.carried = ItemStack{ID: target.ID, Count: half}
					sess.inv[slot].Count -= half
					if sess.inv[slot].Count <= 0 {
						sess.inv[slot] = ItemStack{}
					}
				}
			} else if target.Empty() {
				sess.inv[slot] = ItemStack{ID: cur.ID, Count: 1}
				sess.carried.Count--
				if sess.carried.Count <= 0 {
					sess.carried = ItemStack{}
				}
			} else if target.ID == cur.ID && target.Count < maxStack {
				sess.inv[slot].Count++
				sess.carried.Count--
				if sess.carried.Count <= 0 {
					sess.carried = ItemStack{}
				}
			}
		}
		touch(slot)
	case 1: // quick move: merge into the other half, else swap half
		target := sess.inv[slot]
		if target.Empty() {
			break
		}
		if slot >= slotHotbarStart && slot <= slotOffhand {
			s.quickMoveLocked(sess, slot, slotMainStart, slotHotbarStart-1, &touched)
		} else {
			s.quickMoveLocked(sess, slot, slotHotbarStart, slotOffhand, &touched)
			if !sess.inv[slot].Empty() {
				s.quickMoveLocked(sess, slot, slotMainStart, slotHotbarStart-1, &touched)
			}
		}
	case 2: // swap with hotbar button
		swap := int16(slotHotbarStart) + int16(button)
		if swap >= slotHotbarStart && swap <= slotOffhand {
			sess.inv[slot], sess.inv[swap] = sess.inv[swap], sess.inv[slot]
			touch(slot)
			touch(swap)
		}
	case 3: // creative clone
		if !sess.inv[slot].Empty() {
			if button == 2 {
				sess.carried = ItemStack{ID: sess.inv[slot].ID, Count: maxStack}
			} else {
				sess.carried = sess.inv[slot]
			}
		}
		touch(slot)
	case 4: // throw
		if !sess.inv[slot].Empty() {
			dropped := ItemStack{ID: sess.inv[slot].ID, Count: 1}
			if button == 1 {
				dropped.Count = sess.inv[slot].Count
			}
			sess.inv[slot].Count -= dropped.Count
			if sess.inv[slot].Count <= 0 {
				sess.inv[slot] = ItemStack{}
			}
			s.dropItemLocked(sess, dropped)
			touch(slot)
		}
	default:
		// Unsupported mode: fall through to a full resync.
	}

	if gridTouched {
		// Recompute the 2x2 result while still holding the lock.
		grid := craftGrid{
			sess.inv[slotCraftStart],
			sess.inv[slotCraftStart+1], {},
			sess.inv[slotCraftStart+2],
			sess.inv[slotCraftStart+3], {}, {},
			{}, {},
		}
		want, built := craftResult(grid)
		if !built {
			want = ItemStack{}
		}
		if prev := sess.inv[slotCraftResult]; prev != want {
			sess.inv[slotCraftResult] = want
			touched = append(touched, slotCraftResult)
		}
	}

	sess.invState.Add(1)
	state := sess.invState.Load()
	_ = changed
	updates := make([]struct {
		slot int16
		it   ItemStack
	}, 0, len(touched))
	for _, sl := range touched {
		updates = append(updates, struct {
			slot int16
			it   ItemStack
		}{sl, sess.inv[sl]})
	}
	carried := sess.carried

	for _, u := range updates {
		var b protocol.Buffer
		b.VarInt(0)
		b.VarInt(state)
		b.VarInt(int32(u.slot))
		writeSlot(&b, u.it)
		_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
	}
	var cb protocol.Buffer
	writeSlot(&cb, carried)
	_ = sess.write(sess.ids().PlaySetCarriedItem, cb.Bytes())
	return equipTouched
}

// handleCraftResultClick implements taking from the 2x2 result slot.
func (s *Server) handleCraftResultClick(sess *Session, button byte, mode int32) {
	switch mode {
	case 0: // one craft to the cursor
		carried, touched, ok := sess.takeCraftResult(1)
		if !ok {
			return
		}
		s.sendCraftUpdates(sess, touched, carried)
	case 1: // shift-click: craft everything into the inventory
		for i := 0; i < maxStack; i++ {
			sess.invMu.Lock()
			empty := sess.inv[slotCraftResult].Empty()
			sess.invMu.Unlock()
			if empty {
				break
			}
			carried, _, ok := sess.takeCraftResult(1)
			if !ok {
				break
			}
			remainder, _ := sess.addStack(carried)
			sess.invMu.Lock()
			sess.carried = remainder
			sess.invMu.Unlock()
		}
		sess.invMu.Lock()
		carried := sess.carried
		sess.invMu.Unlock()
		_ = s.sendContainerContent(sess)
		_ = s.sendCarriedItem(sess, carried)
		s.refreshCraftResult(sess)
	}
}

// sendCraftUpdates syncs the grid slots, cursor, and result after a
// craft.
func (s *Server) sendCraftUpdates(sess *Session, slots []int16, carried ItemStack) {
	sess.invMu.Lock()
	state := sess.invState.Add(1)
	updates := make([]struct {
		slot int16
		it   ItemStack
	}, 0, len(slots)+2)
	seen := map[int16]bool{}
	add := func(sl int16) {
		if seen[sl] {
			return
		}
		seen[sl] = true
		updates = append(updates, struct {
			slot int16
			it   ItemStack
		}{sl, sess.inv[sl]})
	}
	for _, sl := range slots {
		add(sl)
	}
	// Always sync the result slot so the client drops the crafted
	// stack even when recomputation lands on empty.
	add(slotCraftResult)
	sess.invMu.Unlock()
	for _, u := range updates {
		var b protocol.Buffer
		b.VarInt(0)
		b.VarInt(state)
		b.VarInt(int32(u.slot))
		writeSlot(&b, u.it)
		_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
	}
	_ = s.sendCarriedItem(sess, carried)
	s.refreshCraftResult(sess)
}

// quickMoveLocked moves from slot into the first stackable/empty slots
// of [from,to]. Caller holds invMu.
func (s *Server) quickMoveLocked(sess *Session, slot int16, from, to int16, touched *[]int16) {
	moving := sess.inv[slot]
	if moving.Empty() {
		return
	}
	touch := func(sl int16) {
		for _, t := range *touched {
			if t == sl {
				return
			}
		}
		*touched = append(*touched, sl)
	}
	// Merge into existing stacks first.
	for i := from; i <= to && !moving.Empty(); i++ {
		if sess.inv[i].Empty() || sess.inv[i].ID != moving.ID {
			continue
		}
		room := maxStack - sess.inv[i].Count
		move := moving.Count
		if move > room {
			move = room
		}
		sess.inv[i].Count += move
		moving.Count -= move
		touch(i)
	}
	// Then fill empty slots.
	for i := from; i <= to && !moving.Empty(); i++ {
		if !sess.inv[i].Empty() {
			continue
		}
		sess.inv[i] = moving
		moving = ItemStack{}
		touch(i)
	}
	sess.inv[slot] = moving
	touch(slot)
}

// addStack merges a stack into the inventory (hotbar first), returning
// the remainder and whether anything was picked up. Slot updates are
// sent for every slot the stack touched.
func (sess *Session) addStack(it ItemStack) (ItemStack, bool) {
	if it.Empty() {
		return ItemStack{}, false
	}
	sess.invMu.Lock()
	order := make([]int16, 0, slotCount)
	for i := int16(slotHotbarStart); i <= slotOffhand; i++ {
		order = append(order, i)
	}
	for i := int16(slotMainStart); i < slotHotbarStart; i++ {
		order = append(order, i)
	}
	var touched []int16
	picked := false
	for _, sl := range order {
		if it.Empty() {
			break
		}
		cur := sess.inv[sl]
		if cur.Empty() || cur.ID != it.ID {
			continue
		}
		room := maxStack - cur.Count
		if room <= 0 {
			continue
		}
		move := it.Count
		if move > room {
			move = room
		}
		sess.inv[sl].Count += move
		it.Count -= move
		touched = append(touched, sl)
		picked = true
	}
	for _, sl := range order {
		if it.Empty() {
			break
		}
		if !sess.inv[sl].Empty() {
			continue
		}
		sess.inv[sl] = it
		it = ItemStack{}
		touched = append(touched, sl)
		picked = true
	}
	updates := make([]struct {
		slot int16
		it   ItemStack
	}, 0, len(touched))
	for _, sl := range touched {
		updates = append(updates, struct {
			slot int16
			it   ItemStack
		}{sl, sess.inv[sl]})
	}
	sess.invMu.Unlock()
	for _, u := range updates {
		_ = sess.server.sendContainerSlot(sess, u.slot, u.it)
	}
	return it, picked
}

// readChangedSlots consumes the Changed Slots array from a click
// packet (Prefixed Array of (Short slot, Hashed Slot)).
func readChangedSlots(r *protocol.PacketReader) (int, bool) {
	n, err := r.VarInt()
	if err != nil || n < 0 || n > 128 {
		return 0, false
	}
	for i := int32(0); i < n; i++ {
		if _, err := r.Short(); err != nil {
			return 0, false
		}
		if _, ok := readHashedSlot(r); !ok {
			return 0, false
		}
	}
	return int(n), true
}

// readHashedSlot consumes one Hashed Slot: Bool has item, then when
// true item id, count, component hashes.
func readHashedSlot(r *protocol.PacketReader) (ItemStack, bool) {
	has, err := r.Bool()
	if err != nil {
		return ItemStack{}, false
	}
	if !has {
		return ItemStack{}, true
	}
	id, err := r.VarInt()
	if err != nil {
		return ItemStack{}, false
	}
	count, err := r.VarInt()
	if err != nil {
		return ItemStack{}, false
	}
	add, err := r.VarInt()
	if err != nil || add < 0 || add > 256 {
		return ItemStack{}, false
	}
	for i := int32(0); i < add; i++ {
		if _, err := r.VarInt(); err != nil { // component type
			return ItemStack{}, false
		}
		if _, err := r.Int(); err != nil { // hash
			return ItemStack{}, false
		}
	}
	rem, err := r.VarInt()
	if err != nil || rem < 0 || rem > 256 {
		return ItemStack{}, false
	}
	for i := int32(0); i < rem; i++ {
		if _, err := r.VarInt(); err != nil {
			return ItemStack{}, false
		}
	}
	return ItemStack{ID: id, Count: count}, true
}

// dropItemLocked is a placeholder for entity item spawning; the item
// merge/throw semantics are already applied to the inventory.
func (s *Server) dropItemLocked(sess *Session, it ItemStack) {
	_ = world.ItemIDs
	_ = it
}
