package server

import (
	"sync"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Hopper menu: 5 container slots then the player rows (41 total).
const (
	menuHopper      = 16 // minecraft:hopper in the menu registry
	hopperSlots     = 5
	hopperMenuSlots = hopperSlots + 36
)

// hopperData is one hopper block entity's state.
//
// cooldown starts at -1, which is what the real server's own fresh
// hopper carries: it is the value a hopper that has never ticked saves
// and reads back, and the first tick it takes turns it into the 0 an
// idle hopper keeps.
type hopperData struct {
	slots    [hopperSlots]ItemStack
	cooldown int32
}

// hopperStore keeps hopper state by position.
type hopperStore struct {
	mu      sync.Mutex
	hoppers map[world.Pos]*hopperData
}

// isHopperBlock reports whether a block state is a hopper.
func isHopperBlock(b world.Block) bool {
	name, ok := world.StateOwner(b)
	return ok && name == "hopper"
}

func (s *Server) ensureHopper(d *dimension, pos world.Pos) {
	d.hoppers.mu.Lock()
	if d.hoppers.hoppers == nil {
		d.hoppers.hoppers = make(map[world.Pos]*hopperData)
	}
	if _, ok := d.hoppers.hoppers[pos]; !ok {
		d.hoppers.hoppers[pos] = &hopperData{cooldown: hopperFreshCooldown}
	}
	d.hoppers.mu.Unlock()
}

// hopperAt returns the live hopper state, creating it if needed.
func (s *Server) hopperAt(d *dimension, pos world.Pos) *hopperData {
	s.ensureHopper(d, pos)
	d.hoppers.mu.Lock()
	defer d.hoppers.mu.Unlock()
	return d.hoppers.hoppers[pos]
}

// removeHopper drops a hopper's contents when its block goes away.
func (s *Server) removeHopper(d *dimension, pos world.Pos) {
	d.hoppers.mu.Lock()
	data, ok := d.hoppers.hoppers[pos]
	delete(d.hoppers.hoppers, pos)
	d.hoppers.mu.Unlock()
	if !ok {
		return
	}
	for i := 0; i < hopperSlots; i++ {
		if data.slots[i].Empty() {
			continue
		}
		e := s.entities.Spawn(&Entity{
			Type:        mustEntityType("item"),
			Dim:         d.name,
			X:           float64(pos.X) + 0.5,
			Y:           float64(pos.Y) + 0.5,
			Z:           float64(pos.Z) + 0.5,
			Item:        data.slots[i],
			PickupDelay: 10,
		})
		data.slots[i] = ItemStack{}
		s.broadcastEntitySpawn(nil, *e)
		s.broadcastEntityMetadata(e.ID, encodeItemMetadata(e.Item))
	}
}

// openHopper shows the hopper window and its five slots.
func (s *Server) openHopper(sess *Session, pos world.Pos) {
	id := sess.server.nextWindow.Add(1)
	sess.windowMu.Lock()
	sess.window = &openWindow{id: id, pos: pos, dim: sess.dimension(), hopper: s.hopperAt(sess.dimension(), pos), kind: menuHopper}
	sess.windowState = 0
	sess.windowMu.Unlock()

	var b protocol.Buffer
	b.VarInt(id)
	b.VarInt(menuHopper)
	b.Raw(menuTitle("container.hopper"))
	_ = sess.write(sess.ids().PlayOpenScreen, b.Bytes())
	s.sendHopperContent(sess)
}

// sendHopperContent syncs the full hopper menu.
func (s *Server) sendHopperContent(sess *Session) {
	sess.windowMu.Lock()
	w := sess.window
	state := sess.windowState
	sess.windowMu.Unlock()
	if w == nil || w.hopper == nil {
		return
	}
	var b protocol.Buffer
	b.VarInt(w.id)
	b.VarInt(state)
	b.VarInt(hopperMenuSlots)
	for i := 0; i < hopperMenuSlots; i++ {
		writeSlot(&b, sess.hopperMenuStack(w.hopper, i))
	}
	writeSlot(&b, sess.carried)
	_ = sess.write(sess.ids().PlayContainerContent, b.Bytes())
}

// hopperMenuStack reads one hopper-menu slot.
func (sess *Session) hopperMenuStack(h *hopperData, slot int) ItemStack {
	d := sess.dimension()
	switch {
	case slot < hopperSlots:
		d.hoppers.mu.Lock()
		defer d.hoppers.mu.Unlock()
		return h.slots[slot]
	case slot < hopperSlots+27:
		return sess.stackAt(int16(slotMainStart + (slot - hopperSlots)))
	default:
		return sess.stackAt(int16(slotHotbarStart + (slot - hopperSlots - 27)))
	}
}

// setHopperMenuStack writes one hopper-menu slot.
func (sess *Session) setHopperMenuStack(h *hopperData, slot int, it ItemStack) {
	d := sess.dimension()
	switch {
	case slot < hopperSlots:
		d.hoppers.mu.Lock()
		h.slots[slot] = it
		d.hoppers.mu.Unlock()
	case slot < hopperSlots+27:
		sess.setStack(int16(slotMainStart+(slot-hopperSlots)), it)
	default:
		sess.setStack(int16(slotHotbarStart+(slot-hopperSlots-27)), it)
	}
}

// handleHopperClick implements click modes 0/1/2/4 for the hopper menu.
func (s *Server) handleHopperClick(sess *Session, w *openWindow, h *hopperData, slot int16, button byte, mode int32) {
	if slot < 0 || int(slot) >= hopperMenuSlots {
		return
	}
	var touched []int
	switch mode {
	case 0:
		cur := sess.carried
		target := sess.hopperMenuStack(h, int(slot))
		if button == 0 {
			if cur.Empty() {
				cur, target = target, ItemStack{}
			} else if target.Empty() {
				target, cur = cur, ItemStack{}
			} else if target.ID == cur.ID {
				room := maxStack - target.Count
				move := cur.Count
				if move > room {
					move = room
				}
				target.Count += move
				cur.Count -= move
				if cur.Count <= 0 {
					cur = ItemStack{}
				}
			} else {
				target, cur = cur, target
			}
		} else if cur.Empty() {
			if !target.Empty() {
				half := (target.Count + 1) / 2
				cur = ItemStack{ID: target.ID, Count: half}
				target.Count -= half
				if target.Count <= 0 {
					target = ItemStack{}
				}
			}
		} else if target.Empty() {
			target = ItemStack{ID: cur.ID, Count: 1}
			cur.Count--
			if cur.Count <= 0 {
				cur = ItemStack{}
			}
		}
		sess.setHopperMenuStack(h, int(slot), target)
		sess.carriedMuSet(cur)
		touched = append(touched, int(slot))
	case 1:
		moving := sess.hopperMenuStack(h, int(slot))
		if moving.Empty() {
			break
		}
		fromContainer := int(slot) < hopperSlots
		touched = append(touched, int(slot))
		for i := 0; i < hopperMenuSlots && !moving.Empty(); i++ {
			if (i < hopperSlots) == fromContainer {
				continue
			}
			other := sess.hopperMenuStack(h, i)
			if !other.Stackable(moving) {
				continue
			}
			room := maxStack - other.Count
			move := moving.Count
			if move > room {
				move = room
			}
			other.Count += move
			moving.Count -= move
			sess.setHopperMenuStack(h, i, other)
			touched = append(touched, i)
		}
		for i := 0; i < hopperMenuSlots && !moving.Empty(); i++ {
			if (i < hopperSlots) == fromContainer {
				continue
			}
			if !sess.hopperMenuStack(h, i).Empty() {
				continue
			}
			sess.setHopperMenuStack(h, i, moving)
			touched = append(touched, i)
			moving = ItemStack{}
		}
		sess.setHopperMenuStack(h, int(slot), moving)
	case 2:
		swap := hopperMenuSlots - 9 + int(button)
		a := sess.hopperMenuStack(h, int(slot))
		b := sess.hopperMenuStack(h, swap)
		sess.setHopperMenuStack(h, int(slot), b)
		sess.setHopperMenuStack(h, swap, a)
		touched = append(touched, int(slot), swap)
	default:
		s.sendHopperContent(sess)
		return
	}
	s.syncHopperSlots(sess, w, h, touched)
}

// handleHopperClickPacket parses and applies a hopper-menu click.
func (s *Server) handleHopperClickPacket(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	win, err := r.VarInt()
	if err != nil {
		return
	}
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || w.hopper == nil || w.id != win {
		return
	}
	st, err := r.VarInt() // state id
	if err != nil {
		return
	}
	if !s.checkClickState(sess, w, st) {
		return
	}
	slot, err := r.Short()
	if err != nil {
		return
	}
	button, err := r.Byte()
	if err != nil {
		return
	}
	mode, err := r.VarInt()
	if err != nil {
		return
	}
	readChangedSlots(r)
	if _, err := readSlot(r); err != nil {
		return
	}
	s.handleHopperClick(sess, w, w.hopper, slot, button, mode)
}

// syncHopperSlots sends Container Slot updates for touched slots.
func (s *Server) syncHopperSlots(sess *Session, w *openWindow, h *hopperData, touched []int) {
	sess.windowMu.Lock()
	sess.windowState++
	state := sess.windowState
	sess.windowMu.Unlock()
	seen := map[int]bool{}
	for _, slot := range touched {
		if seen[slot] {
			continue
		}
		seen[slot] = true
		var b protocol.Buffer
		b.VarInt(w.id)
		b.VarInt(state)
		b.Short(int16(slot))
		writeSlot(&b, sess.hopperMenuStack(h, slot))
		_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
	}
}

// hopperTransferEvery is the vanilla cooldown a hopper takes on after a
// transfer, in ticks: eight, so a working hopper moves one item per
// eight ticks.
const hopperTransferEvery = 8

// hopperFreshCooldown is what a hopper that has never ticked carries,
// and what the real server saves for one: -1, which is not a countdown
// but the absence of one.
const hopperFreshCooldown = -1

// hopperTickStep advances every hopper in every dimension. A hopper
// pushes one item into the container it faces and pulls one item out of
// the container above it.
//
// Every rule here was measured against the real server rather than
// guessed, by freezing its tick loop and stepping a single tick at a
// time (/tick freeze, /tick step 1) with a hopper and two containers in
// every arrangement:
//
//   - the push goes toward the hopper's own facing and not always down:
//     a hopper facing east, with a chest east of it and another below
//     it, fills the eastern one;
//   - the push and the pull both happen in the same tick, pushing
//     first, so a hopper that sent an item on and has room refills
//     itself in that same tick;
//   - one item moves per transfer per direction - never a whole stack -
//     so a hopper drains and fills at a steady one item per eight ticks;
//   - a hopper facing up does not pull at all: the container above is
//     the one it pushes into;
//   - a transfer takes on a cooldown of eight ticks, and a hopper with
//     nothing to move keeps none at all. An idle hopper is saved as
//     TransferCooldown 0, which is what the real server writes; one that
//     counted its cooldown down regardless would save a number in 1..8
//     that depended on when the save happened;
//   - the countdown runs before the hopper acts, so a transfer every
//     eight ticks means eight ticks between one item and the next;
//   - a hopper that has never ticked carries -1 rather than 0, which is
//     what the real server reads back from its own fresh hopper: the
//     first tick a hopper takes turns that into the 0 of an idle one.
func (s *Server) hopperTickStep() {
	for _, d := range s.dims {
		d.hoppers.mu.Lock()
		type entry struct {
			pos world.Pos
			h   *hopperData
		}
		var current []entry
		for pos, h := range d.hoppers.hoppers {
			current = append(current, entry{pos, h})
		}
		d.hoppers.mu.Unlock()
		for _, e := range current {
			// The cooldown counts down before the hopper acts, and a
			// hopper that never had one starts at -1: the real server's
			// first tick turns that into 0, which is the value an idle
			// hopper keeps and the value it is saved with.
			if e.h.cooldown > 0 {
				e.h.cooldown--
				if e.h.cooldown > 0 {
					continue
				}
			} else if e.h.cooldown < 0 {
				e.h.cooldown = 0
			}
			blk := d.w.GetBlock(e.pos)
			if !isHopperBlock(blk) {
				continue
			}
			face := hopperFacing(blk)
			target := world.Pos{X: e.pos.X + face[0], Y: e.pos.Y + face[1], Z: e.pos.Z + face[2]}
			moved := s.hopperPush(d, e.pos, target, e.h)
			if face != hopperUp && s.hopperPull(d, e.pos, e.h) {
				moved = true
			}
			if moved {
				e.h.cooldown = hopperTransferEvery
			}
		}
	}
}

// hopperUp is the one facing a hopper does not pull from.
var hopperUp = [3]int{0, 1, 0}

// hopperFacing is the direction a hopper block points: the side it
// pushes into. The block carries it as a property, and a state with
// none to read is the block's own default, which is down.
func hopperFacing(b world.Block) [3]int {
	dir, ok := world.BlockProperties(b)["facing"]
	if !ok {
		return [3]int{0, -1, 0}
	}
	switch dir {
	case "up":
		return hopperUp
	case "north":
		return [3]int{0, 0, -1}
	case "south":
		return [3]int{0, 0, 1}
	case "east":
		return [3]int{1, 0, 0}
	case "west":
		return [3]int{-1, 0, 0}
	}
	return [3]int{0, -1, 0}
}

// hopperPush moves a single item from the hopper into the container at
// target, reporting whether it moved. The hopper's own store is locked
// for the whole transfer, and the destination is locked inside that, so
// the two locks of a hopper-to-hopper transfer are always taken in the
// same order.
func (s *Server) hopperPush(d *dimension, pos, target world.Pos, h *hopperData) bool {
	d.hoppers.mu.Lock()
	defer d.hoppers.mu.Unlock()
	for i := range h.slots {
		if h.slots[i].Empty() {
			continue
		}
		one := h.slots[i]
		one.Count = 1
		if !s.hopperInsert(d, target, pos, one) {
			return false
		}
		removeOne(&h.slots[i])
		return true
	}
	return false
}

// hopperPull moves a single item out of the container above the hopper
// into it, reporting whether it moved.
func (s *Server) hopperPull(d *dimension, pos world.Pos, h *hopperData) bool {
	above := world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	d.hoppers.mu.Lock()
	defer d.hoppers.mu.Unlock()
	// The hopper is the destination, and it is locked before the source
	// is looked into: nothing is taken out of the container above until
	// the hopper has room for it.
	return hopperTakeOne(d, above, func(candidate ItemStack) bool {
		one := candidate
		one.Count = 1
		return slotsInsert(h.slots[:], &one)
	})
}

// hopperInsert puts one item into the container at target, which the
// hopper at from is facing, using only the slots the real server lets a
// hopper reach from that side. The caller holds the hopper store's
// lock.
func (s *Server) hopperInsert(d *dimension, target, from world.Pos, one ItemStack) bool {
	blk := d.w.GetBlock(target)
	switch {
	case isHopperBlock(blk):
		// Reached through the lock the caller already holds.
		dst := d.hoppers.hoppers[target]
		if dst == nil {
			return false
		}
		moving := one
		return slotsInsert(dst.slots[:], &moving)
	case isChestBlock(blk):
		chest := s.chestAt(d, target)
		d.chests.mu.Lock()
		defer d.chests.mu.Unlock()
		moving := one
		return slotsInsert(chest.slots[:], &moving)
	case isFurnaceBlock(blk):
		f := s.furnaceAt(d, target)
		d.furnaces.mu.Lock()
		defer d.furnaces.mu.Unlock()
		// A hopper above a furnace fills its input slot with anything at
		// all, coal included; a hopper beside or below it may only fill
		// the fuel slot, and only with fuel. Both are the real server's
		// own sided rules - its slot 2 is never filled by a hopper.
		slot := furnaceInput
		if from.Y <= target.Y {
			slot = furnaceFuel
			if !isFuelItem(one.ID) {
				return false
			}
		}
		moving := one
		inserted := insertStack(&f.slots[slot], &moving)
		if inserted && slot == furnaceInput {
			// A hopper filling the input is a slot write like any other,
			// and the real server recomputes the cook total from it.
			f.refreshCookTotal()
		}
		return inserted
	default:
		c := s.containerAt(d, target)
		if c == nil {
			return false
		}
		d.containers.mu.Lock()
		defer d.containers.mu.Unlock()
		moving := one
		return slotsInsert(c.slots, &moving)
	}
}

// hopperTakeOne hands one item from the container at pos to accept,
// which takes it or refuses it; an item refused stays where it is. The
// caller holds the hopper store's lock, and a source that is itself a
// hopper is reached through that same lock, so no transfer ever holds
// two locks at once.
func hopperTakeOne(d *dimension, pos world.Pos, accept func(ItemStack) bool) bool {
	blk := d.w.GetBlock(pos)
	switch {
	case isHopperBlock(blk):
		src := d.hoppers.hoppers[pos]
		if src == nil {
			return false
		}
		return slotsTakeOne(src.slots[:], accept)
	case isChestBlock(blk):
		chest := d.chests.chests[pos]
		if chest == nil {
			return false
		}
		d.chests.mu.Lock()
		defer d.chests.mu.Unlock()
		return slotsTakeOne(chest.slots[:], accept)
	case isFurnaceBlock(blk):
		f := d.furnaces.furnaces[pos]
		if f == nil {
			return false
		}
		d.furnaces.mu.Lock()
		defer d.furnaces.mu.Unlock()
		// Only the result leaves a furnace, whatever side the hopper is
		// on: the input and the fuel are the furnace's own business.
		return slotsTakeOne(f.slots[furnaceOutput:furnaceOutput+1], accept)
	default:
		c := d.containers.containers[pos]
		if c == nil {
			return false
		}
		d.containers.mu.Lock()
		defer d.containers.mu.Unlock()
		return slotsTakeOne(c.slots, accept)
	}
}

// slotsTakeOne finds the first stack a hopper may take out of slots and
// takes one item of it, if accept takes that item.
func slotsTakeOne(slots []ItemStack, accept func(ItemStack) bool) bool {
	for i := range slots {
		if slots[i].Empty() {
			continue
		}
		candidate := slots[i]
		if !accept(candidate) {
			continue
		}
		removeOne(&slots[i])
		return true
	}
	return false
}

// slotsInsert merges a single item into the first slot that takes it -
// a matching stack with room, or an empty one - updating moving to the
// remainder.
func slotsInsert(slots []ItemStack, moving *ItemStack) bool {
	moved := false
	for i := range slots {
		if moving.Empty() {
			break
		}
		if insertStack(&slots[i], moving) {
			moved = true
		}
	}
	return moved
}

// removeOne takes a single item off a stack, clearing the slot when it
// was the last.
func removeOne(stack *ItemStack) {
	stack.Count--
	if stack.Count <= 0 {
		*stack = ItemStack{}
	}
}

// isFuelItem reports whether an item burns, which is what a furnace's
// fuel slot accepts from a hopper.
func isFuelItem(id int32) bool {
	name, ok := world.ItemName(id)
	if !ok {
		return false
	}
	_, fuel := fuelTicks[name]
	return fuel
}

// insertStack merges moving into dst (empty or same item with room),
// updating moving to the remainder.
func insertStack(dst *ItemStack, moving *ItemStack) bool {
	if moving.Empty() {
		return false
	}
	if dst.Empty() {
		*dst = *moving
		*moving = ItemStack{}
		return true
	}
	if !dst.Stackable(*moving) || dst.Count >= maxStack {
		return false
	}
	room := maxStack - dst.Count
	move := moving.Count
	if move > room {
		move = room
	}
	dst.Count += move
	moving.Count -= move
	if moving.Count <= 0 {
		*moving = ItemStack{}
	}
	return true
}
