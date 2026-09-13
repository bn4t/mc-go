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
	name, ok := world.BlockName(b)
	return ok && name == "hopper"
}

func (s *Server) ensureHopper(d *dimension, pos world.Pos) {
	d.hoppers.mu.Lock()
	if d.hoppers.hoppers == nil {
		d.hoppers.hoppers = make(map[world.Pos]*hopperData)
	}
	if _, ok := d.hoppers.hoppers[pos]; !ok {
		d.hoppers.hoppers[pos] = &hopperData{}
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
	b.Raw(textComponentContent("Hopper"))
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
			if other.Empty() || other.ID != moving.ID {
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
	if _, err := r.VarInt(); err != nil {
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
		b.VarInt(int32(slot))
		writeSlot(&b, sess.hopperMenuStack(h, slot))
		_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
	}
}

// hopperTransferEvery is the vanilla cooldown between transfer attempts.
const hopperTransferEvery = 8

// hopperTickStep advances every hopper in every dimension: pull from
// the container above, push into the container below.
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
			e.h.cooldown--
			if e.h.cooldown > 0 {
				continue
			}
			e.h.cooldown = hopperTransferEvery
			if s.hopperPull(d, e.pos, e.h) {
				continue
			}
			s.hopperPush(d, e.pos, e.h)
		}
	}
}

// hopperPull moves one item from the container above into the hopper.
func (s *Server) hopperPull(d *dimension, pos world.Pos, h *hopperData) bool {
	above := world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	blk := d.w.GetBlock(above)
	if !isChestBlock(blk) {
		return false
	}
	chest := s.chestAt(d, above)
	d.chests.mu.Lock()
	var source *ItemStack
	if chest != nil {
		for i := range chest.slots {
			if !chest.slots[i].Empty() {
				source = &chest.slots[i]
				break
			}
		}
	}
	if source == nil {
		d.chests.mu.Unlock()
		return false
	}
	moving := *source
	if !s.hopperAccept(h, &moving) {
		d.chests.mu.Unlock()
		return false
	}
	*source = moving
	d.chests.mu.Unlock()
	return true
}

// hopperPush moves one item from the hopper into the container below.
func (s *Server) hopperPush(d *dimension, pos world.Pos, h *hopperData) bool {
	below := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	blk := d.w.GetBlock(below)
	d.hoppers.mu.Lock()
	defer d.hoppers.mu.Unlock()
	for i := range h.slots {
		if h.slots[i].Empty() {
			continue
		}
		moving := h.slots[i]
		switch {
		case isChestBlock(blk):
			chest := s.chestAt(d, below)
			d.chests.mu.Lock()
			ok := chestInsert(chest, &moving)
			d.chests.mu.Unlock()
			if !ok {
				return false
			}
		case isFurnaceBlock(blk):
			f := s.furnaceAt(d, below)
			d.furnaces.mu.Lock()
			ok := false
			if name, ok := itemNameOf(moving.ID); ok {
				if _, smelts := smeltResult[name]; smelts {
					ok = insertStack(&f.slots[furnaceInput], &moving)
				} else if _, fuel := fuelTicks[name]; fuel {
					ok = insertStack(&f.slots[furnaceFuel], &moving)
				}
			}
			d.furnaces.mu.Unlock()
			if !ok {
				return false
			}
		default:
			return false
		}
		h.slots[i] = moving
		return true
	}
	return false
}

// hopperAccept merges a moving stack into the hopper (merge then empty
// slots), reporting whether anything moved.
func (s *Server) hopperAccept(h *hopperData, moving *ItemStack) bool {
	accepted := false
	for i := range h.slots {
		if moving.Empty() {
			break
		}
		if !insertStack(&h.slots[i], moving) {
			continue
		}
		accepted = true
	}
	return accepted
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
	if dst.ID != moving.ID || dst.Count >= maxStack {
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

// chestInsert merges a moving stack into a chest.
func chestInsert(chest *chestData, moving *ItemStack) bool {
	if chest == nil {
		return false
	}
	accepted := false
	for i := range chest.slots {
		if moving.Empty() {
			break
		}
		if insertStack(&chest.slots[i], moving) {
			accepted = true
		}
	}
	return accepted
}
