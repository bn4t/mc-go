package server

import (
	"sync"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// chestBlockEntityType is minecraft:chest in the block_entity_type
// registry; menuGeneric9x3 is minecraft:generic_9x3 in the menu
// registry (both from the official report).
const (
	chestBlockEntityType = 1
	menuGeneric9x3       = 2
	chestSlots           = 27
	chestMenuSlots       = chestSlots + 36
)

// chestData is one chest block entity's storage.
type chestData struct {
	slots [chestSlots]ItemStack
}

// chestStore keeps chest contents by position.
type chestStore struct {
	mu     sync.Mutex
	chests map[world.Pos]*chestData
}

// isChestBlock reports whether a block state is a chest.
func isChestBlock(b world.Block) bool {
	name, ok := world.StateOwner(b)
	if !ok {
		return false
	}
	return name == "chest" || name == "trapped_chest"
}

// ensureChest creates empty storage for a placed chest in dimension d.
func (s *Server) ensureChest(d *dimension, pos world.Pos) {
	d.chests.mu.Lock()
	if d.chests.chests == nil {
		d.chests.chests = make(map[world.Pos]*chestData)
	}
	if _, ok := d.chests.chests[pos]; !ok {
		d.chests.chests[pos] = &chestData{}
	}
	d.chests.mu.Unlock()
}

// chestAt returns the live storage pointer, creating it if needed.
func (s *Server) chestAt(d *dimension, pos world.Pos) *chestData {
	s.ensureChest(d, pos)
	d.chests.mu.Lock()
	defer d.chests.mu.Unlock()
	return d.chests.chests[pos]
}

// removeChest drops a chest's contents when its block goes away.
func (s *Server) removeChest(d *dimension, pos world.Pos) {
	d.chests.mu.Lock()
	data, ok := d.chests.chests[pos]
	delete(d.chests.chests, pos)
	d.chests.mu.Unlock()
	if !ok {
		return
	}
	var stacks []ItemStack
	for i := 0; i < chestSlots; i++ {
		if !data.slots[i].Empty() {
			stacks = append(stacks, data.slots[i])
			data.slots[i] = ItemStack{}
		}
	}
	for _, stack := range stacks {
		e := s.entities.Spawn(&Entity{
			Type:        mustEntityType("item"),
			Dim:         d.name,
			X:           float64(pos.X) + 0.5,
			Y:           float64(pos.Y) + 0.5,
			Z:           float64(pos.Z) + 0.5,
			Item:        stack,
			PickupDelay: 10,
		})
		s.broadcastEntitySpawn(nil, *e)
		s.broadcastEntityMetadata(e.ID, encodeItemMetadata(e.Item))
	}
}

// openWindow describes one session's open container window. dim is the
// dimension whose block-entity store owns the container.
type openWindow struct {
	id        int32
	pos       world.Pos
	dim       *dimension
	chest     *chestData
	furnace   *furnaceData
	craft     *craftTable
	hopper    *hopperData
	container *containerData
	ender     *[27]ItemStack
	kind      int32 // menu registry id
}

// containerSize is the number of container-side slots (before the
// player rows).
func (w *openWindow) containerSize() int {
	switch {
	case w.furnace != nil:
		return furnaceSlots
	case w.hopper != nil:
		return hopperSlots
	case w.craft != nil:
		return 1 + craftTableGrid
	case w.container != nil:
		return w.container.spec.slots
	}
	return chestSlots
}

// menuSize is the full menu slot count.
func (w *openWindow) menuSize() int { return w.containerSize() + 36 }

// openChest shows the chest window and its contents to one session.
func (s *Server) openChest(sess *Session, pos world.Pos) {
	id := sess.server.nextWindow.Add(1)
	sess.windowMu.Lock()
	sess.window = &openWindow{id: id, pos: pos, dim: sess.dimension(), chest: s.chestAt(sess.dimension(), pos), kind: menuGeneric9x3}
	sess.windowState = 0
	sess.windowMu.Unlock()

	var b protocol.Buffer
	b.VarInt(id)
	b.VarInt(menuGeneric9x3)
	b.Raw(menuTitle("container.chest"))
	_ = sess.write(sess.ids().PlayOpenScreen, b.Bytes())
	s.sendChestContent(sess)
}

// sendChestContent syncs the whole chest menu (chest + player rows).
func (s *Server) sendChestContent(sess *Session) {
	sess.windowMu.Lock()
	w := sess.window
	state := sess.windowState
	sess.windowMu.Unlock()
	if w == nil {
		return
	}
	var b protocol.Buffer
	b.VarInt(w.id)
	b.VarInt(state)
	b.VarInt(int32(w.menuSize()))
	for i := 0; i < w.menuSize(); i++ {
		writeSlot(&b, sess.menuStack(i))
	}
	writeSlot(&b, sess.carried)
	_ = sess.write(sess.ids().PlayContainerContent, b.Bytes())
}

// checkClickState validates a Click Container state id against the
// window's current state. Vanilla ignores a stale click and resyncs the
// whole window instead of applying it, so a desynced client recovers.
func (s *Server) checkClickState(sess *Session, w *openWindow, st int32) bool {
	sess.windowMu.Lock()
	cur := sess.windowState
	sess.windowMu.Unlock()
	if st == cur {
		return true
	}
	switch {
	case w.furnace != nil:
		s.sendFurnaceContent(sess)
	case w.container != nil:
		s.sendContainerMenuContent(sess)
	case w.craft != nil:
		s.sendCraftTableContent(sess, w.craft)
	case w.hopper != nil:
		s.sendHopperContent(sess)
	default:
		s.sendChestContent(sess)
	}
	return false
}

// menuStack reads one chest-menu slot: 0-26 chest, 27-53 player main,
// 54-62 player hotbar.
func (sess *Session) menuStack(slot int) ItemStack {
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || slot < 0 || slot >= w.menuSize() {
		return ItemStack{}
	}
	container := w.containerSize()
	switch {
	case slot < container:
		if w.ender != nil {
			sess.invMu.RLock()
			defer sess.invMu.RUnlock()
			return (*w.ender)[slot]
		}
		if w.furnace != nil {
			w.dim.furnaces.mu.Lock()
			defer w.dim.furnaces.mu.Unlock()
			return w.furnace.slots[slot]
		}
		if w.container != nil {
			w.dim.containers.mu.Lock()
			defer w.dim.containers.mu.Unlock()
			return w.container.slots[slot]
		}
		w.dim.chests.mu.Lock()
		defer w.dim.chests.mu.Unlock()
		return w.chest.slots[slot]
	case slot < container+27:
		return sess.stackAt(int16(slotMainStart + (slot - container)))
	default:
		return sess.stackAt(int16(slotHotbarStart + (slot - container - 27)))
	}
}

// setMenuStack writes one chest-menu slot.
func (sess *Session) setMenuStack(slot int, it ItemStack) {
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || slot < 0 || slot >= w.menuSize() {
		return
	}
	container := w.containerSize()
	switch {
	case slot < container:
		if w.ender != nil {
			sess.invMu.Lock()
			(*w.ender)[slot] = it
			sess.invMu.Unlock()
			return
		}
		if w.furnace != nil {
			w.dim.furnaces.mu.Lock()
			w.furnace.slots[slot] = it
			if slot == furnaceInput {
				// Writing the input slot is what makes the real server
				// recompute the cook total: a player dropping an item in
				// is one of the three ways it happens.
				w.furnace.refreshCookTotal()
			}
			w.dim.furnaces.mu.Unlock()
			return
		}
		if w.container != nil {
			w.dim.containers.mu.Lock()
			w.container.slots[slot] = it
			w.dim.containers.mu.Unlock()
			return
		}
		w.dim.chests.mu.Lock()
		w.chest.slots[slot] = it
		w.dim.chests.mu.Unlock()
	case slot < container+27:
		sess.setStack(int16(slotMainStart+(slot-container)), it)
	default:
		sess.setStack(int16(slotHotbarStart+(slot-container-27)), it)
	}
}

// handleChestClick implements the core click modes for an open chest
// window. Unsupported modes fall back to a full menu resync.
func (s *Server) handleChestClick(sess *Session, w *openWindow, slot int16, button byte, mode int32, seq bool) {
	sess.windowMu.Lock()
	carried := sess.carried
	sess.windowMu.Unlock()

	var touched []int
	container := w.containerSize()
	switch mode {
	case 0:
		// The furnace output is take-only.
		if w.furnace != nil && slot == furnaceOutput {
			break
		}
		cur := carried
		target := sess.menuStack(int(slot))
		if button == 0 {
			if cur.Empty() {
				carried, target = target, ItemStack{}
			} else if target.Empty() {
				target, carried = cur, ItemStack{}
			} else if target.ID == cur.ID {
				room := maxStack - target.Count
				move := cur.Count
				if move > room {
					move = room
				}
				target.Count += move
				carried.Count -= move
				if carried.Count <= 0 {
					carried = ItemStack{}
				}
			} else {
				target, carried = cur, target
			}
		} else {
			if cur.Empty() {
				if !target.Empty() {
					half := (target.Count + 1) / 2
					carried = ItemStack{ID: target.ID, Count: half}
					target.Count -= half
					if target.Count <= 0 {
						target = ItemStack{}
					}
				}
			} else if target.Empty() {
				target = ItemStack{ID: cur.ID, Count: 1}
				carried.Count--
				if carried.Count <= 0 {
					carried = ItemStack{}
				}
			} else if target.ID == cur.ID && target.Count < maxStack {
				target.Count++
				carried.Count--
				if carried.Count <= 0 {
					carried = ItemStack{}
				}
			}
		}
		sess.setMenuStack(int(slot), target)
		touched = append(touched, int(slot))
	case 1:
		moving := sess.menuStack(int(slot))
		if moving.Empty() {
			break
		}
		containerSide := int(slot) < container
		touched = append(touched, int(slot))
		// Destination order is vanilla's: into the player side
		// backward (hotbar last slot first), into the container side
		// forward. Measured on 26.2.
		var order []int
		if containerSide {
			for i := w.menuSize() - 1; i >= 0; i-- {
				if i >= container {
					order = append(order, i)
				}
			}
		} else {
			for i := 0; i < container; i++ {
				order = append(order, i)
			}
		}
		// Merge into any matching stacks first.
		for _, i := range order {
			if moving.Empty() {
				break
			}
			if w.furnace != nil && i == furnaceOutput {
				continue // never quick-move into the output
			}
			other := sess.menuStack(i)
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
			sess.setMenuStack(i, other)
			touched = append(touched, i)
		}
		// Then empty slots on the destination side.
		for _, i := range order {
			if moving.Empty() {
				break
			}
			if w.furnace != nil && i == furnaceOutput {
				continue // never quick-move into the output
			}
			if !sess.menuStack(i).Empty() {
				continue
			}
			sess.setMenuStack(i, moving)
			touched = append(touched, i)
			moving = ItemStack{}
		}
		sess.setMenuStack(int(slot), moving)
	case 2:
		swap := w.menuSize() - 9 + int(button)
		if w.furnace != nil && slot == furnaceOutput {
			break
		}
		if swap >= w.menuSize()-9 && swap < w.menuSize() {
			a := sess.menuStack(int(slot))
			b := sess.menuStack(swap)
			sess.setMenuStack(int(slot), b)
			sess.setMenuStack(swap, a)
			touched = append(touched, int(slot), swap)
		}
	case 3:
		// Creative middle-click clone: a full stack onto the cursor,
		// the slot unchanged. A clone carries the slot's update so the
		// client stays live; anything else resyncs like an
		// unsupported mode instead of diverging.
		if out, ok := creativeClone(sess, sess.menuStack(int(slot)), button); ok {
			carried = out
			touched = append(touched, int(slot))
			break
		}
		if w.furnace != nil {
			s.sendFurnaceContent(sess)
		} else {
			s.sendChestContent(sess)
		}
		return
	case 4:
		it := sess.menuStack(int(slot))
		if !it.Empty() {
			drop := ItemStack{ID: it.ID, Count: 1, Contents: it.Contents}
			if button == 1 {
				drop.Count = it.Count
			}
			it.Count -= drop.Count
			if it.Count <= 0 {
				it = ItemStack{}
			}
			sess.setMenuStack(int(slot), it)
			s.dropFromMenu(sess, drop)
			touched = append(touched, int(slot))
		}
	default:
		if w.furnace != nil {
			s.sendFurnaceContent(sess)
		} else {
			s.sendChestContent(sess)
		}
		return
	}

	sess.windowMu.Lock()
	sess.carried = carried
	sess.windowState++
	state := sess.windowState
	wid := w.id
	sess.windowMu.Unlock()

	for _, mslot := range touched {
		var b protocol.Buffer
		b.VarInt(wid)
		b.VarInt(state)
		b.Short(int16(mslot))
		writeSlot(&b, sess.menuStack(mslot))
		_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
	}
	var cb protocol.Buffer
	writeSlot(&cb, carried)
	_ = sess.write(sess.ids().PlaySetCarriedItem, cb.Bytes())
	_ = seq
}

// handleChestClickPacket parses a Click Container for a chest window
// and applies it.
func (s *Server) handleChestClickPacket(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	win, err := r.VarInt()
	if err != nil {
		return
	}
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || w.id != win {
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
	readSlot(r) // carried item (server keeps its own cursor)
	s.handleChestClick(sess, w, slot, button, mode, true)
}

// handleCloseContainer retires a container window at the client's
// request.
func (s *Server) handleCloseContainer(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	win, err := r.VarInt()
	if err != nil {
		return
	}
	sess.windowMu.Lock()
	w := sess.window
	if w == nil || w.id != win {
		sess.windowMu.Unlock()
		return
	}
	sess.window = nil
	sess.windowMu.Unlock()
	if w.craft != nil {
		s.closeCraftTable(sess, w.craft)
	} else {
		s.returnCarried(sess)
	}
	var b protocol.Buffer
	b.VarInt(win)
	_ = sess.write(sess.ids().PlayContainerClose, b.Bytes())
}

// returnCarried moves the cursor stack back into the player inventory,
// vanilla's answer to closing a menu with something on the cursor:
// hotbar first, and no cursor packet when it empties. A remainder that
// fits nowhere (a full inventory) stays on the cursor.
func (s *Server) returnCarried(sess *Session) {
	if carried := sess.carriedBuffer(); !carried.Empty() {
		rest, _ := sess.addStack(carried)
		sess.carriedMuSet(rest)
		if !rest.Empty() {
			_ = s.sendCarriedItem(sess, rest)
		}
	}
}

// closeWindow retires any open window without waiting for the client
// (death, disconnect, respawn).
func (s *Server) closeWindow(sess *Session) {
	sess.windowMu.Lock()
	w := sess.window
	sess.window = nil
	sess.windowMu.Unlock()
	if w == nil {
		return
	}
	var b protocol.Buffer
	b.VarInt(w.id)
	_ = sess.write(sess.ids().PlayContainerClose, b.Bytes())
}
