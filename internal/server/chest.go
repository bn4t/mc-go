package server

import (
	"math/rand"
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
	// lootTable is an ungenerated container loot table (vanilla's
	// ChestBlockEntity LootTable NBT); the first open rolls it, clears
	// it, and fires player_generates_container_loot. lootSeed is the
	// table's deterministic seed (vanilla LootSeed), 0 = random.
	lootTable string
	lootSeed  int64
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
	// pendingName is the anvil's rename field for this open menu: per
	// session, empty at open, surviving takes, gone with the window.
	pendingName string
	// ench is the enchantment menu's offer state for this open window,
	// nil until the menu is an enchanting table.
	ench *enchantMenuState
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
	d := sess.dimension()
	if ch := s.chestAt(d, pos); ch.lootTable != "" {
		table := ch.lootTable
		seed := ch.lootSeed
		if seed == 0 {
			seed = rand.Int63()
		}
		ch.lootTable = ""
		ch.lootSeed = 0
		// Vanilla scatters generated stacks into random empty slots.
		items := rollLootTable(table, seed)
		free := []int{}
		for i := range ch.slots {
			if ch.slots[i].Empty() {
				free = append(free, i)
			}
		}
		for _, it := range items {
			if len(free) == 0 {
				break
			}
			i := rand.Intn(len(free))
			ch.slots[free[i]] = it
			free = append(free[:i], free[i+1:]...)
		}
		s.firePlayerGeneratesContainerLoot(sess, table)
	}
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
		// The furnace output is take-only: takes move to the cursor,
		// placements resync instead of diverging.
		if w.furnace != nil && slot == furnaceOutput {
			target := sess.menuStack(int(slot))
			if target.Empty() {
				break
			}
			if button == 0 {
				switch {
				case carried.Empty():
					carried, target = target, ItemStack{}
				case carried.ID == target.ID:
					room := maxStackOfID(carried.ID) - carried.Count
					move := target.Count
					if move > room {
						move = room
					}
					carried.Count += move
					target.Count -= move
					if target.Count <= 0 {
						target = ItemStack{}
					}
				default:
					s.sendFurnaceContent(sess)
					return
				}
			} else if carried.Empty() {
				half := (target.Count + 1) / 2
				carried = target.withCount(half)
				target.Count -= half
				if target.Count <= 0 {
					target = ItemStack{}
				}
			} else {
				s.sendFurnaceContent(sess)
				return
			}
			sess.setMenuStack(int(slot), target)
			touched = append(touched, int(slot))
			break
		}
		// Manual placement into furnace input and fuel is unfiltered:
		// the differential caught vanilla accepting dirt in the input
		// by hand. Quick-move below still honors the accept rules, and
		// the output stays take-only.
		cur := carried
		target := sess.menuStack(int(slot))
		if button == 0 {
			if cur.Empty() {
				carried, target = target, ItemStack{}
			} else if target.Empty() {
				target, carried = cur, ItemStack{}
			} else if target.Stackable(cur) {
				room := maxStackOfID(target.ID) - target.Count
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
					carried = target.withCount(half)
					target.Count -= half
					if target.Count <= 0 {
						target = ItemStack{}
					}
				}
			} else if target.Empty() {
				target = cur.withCount(1)
				carried.Count--
				if carried.Count <= 0 {
					carried = ItemStack{}
				}
			} else if target.Stackable(cur) && target.Count < maxStackOfID(target.ID) {
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
			if w.furnace != nil && (i == furnaceInput || i == furnaceFuel) &&
				!furnaceAccepts(w.furnace, i, moving) {
				continue // the slot would not take the stack by hand either
			}
			other := sess.menuStack(i)
			if !other.Stackable(moving) {
				continue
			}
			room := maxStackOfID(other.ID) - other.Count
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
			if w.furnace != nil && (i == furnaceInput || i == furnaceFuel) &&
				!furnaceAccepts(w.furnace, i, moving) {
				continue // the slot would not take the stack by hand either
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
			// Hotbar swaps ignore the accept rules, like manual
			// placement: the differential shows vanilla swapping
			// sticks into the input.
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
			drop := it
			if button == 0 {
				drop = it.withCount(1)
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
	} else if openWindowKindAnvil(w) {
		// The anvil keeps no storage: its inputs come back to the
		// player and the cursor stays where it is, measured on 26.2.
		s.closeAnvil(sess, w, w.container)
	} else {
		s.returnCarried(sess)
	}
	// No Container Close echo: the real server never answers a
	// client-initiated close (four anvil closes, zero 0x11), the
	// client already retired its own window.
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
