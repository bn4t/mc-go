package server

import (
	"strings"
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
	// Loom/stonecutter result sounds are limited to one per game tick.
	lastTakeSoundTick int64
	smithingError     int32
	smithingErrorSent bool
	commandUpdates    *menuCommandUpdates
	// ench is the enchantment menu's offer state for this open window,
	// nil until the menu is an enchanting table.
	ench *enchantMenuState
	// merchant is the trading menu's state for this open window, nil
	// until an entity-bound merchant screen opens (villager/trader).
	merchant *merchantState
	// mount is the entity whose mount inventory this window shows
	// (AbstractHorse sneak-interact); mountSlots is its inventory
	// size — saddle + body armor + 3*columns chest cells.
	mount      *Entity
	mountSlots int
	// closeEmitted dedupes the container_close gameEvent between the
	// server-initiated closeWindow path and menuRemoved.
	closeEmitted bool
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
	case w.merchant != nil:
		return len(w.merchant.slots)
	case w.mount != nil:
		return w.mountSlots
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
		ch.lootTable = ""
		ch.lootSeed = 0
		unpackLootInto(ch.slots[:], table, seed)
		s.firePlayerGeneratesContainerLoot(sess, table)
	}
	id := sess.server.nextWindow.Add(1)
	sess.windowMu.Lock()
	sess.window = &openWindow{id: id, pos: pos, dim: sess.dimension(), chest: s.chestAt(sess.dimension(), pos), kind: menuGeneric9x3}
	sess.windowState = 0
	s.emitContainerOpen(sess, pos)
	sess.windowMu.Unlock()

	var b protocol.Buffer
	b.VarInt(id)
	b.VarInt(menuGeneric9x3)
	b.Raw(menuTitle("container.chest"))
	_ = sess.write(sess.ids().PlayOpenScreen, b.Bytes())
	s.sendChestContent(sess)
}

// mountScreenTypes is the AbstractHorse family — sneak-interacting a
// tamed member opens the mount inventory (AbstractHorse.mobInteract's
// isTamed && isSecondaryUseActive → openCustomInventoryScreen).
var mountScreenTypes = map[string]bool{
	"horse": true, "zombie_horse": true, "skeleton_horse": true,
	"donkey": true, "mule": true, "llama": true, "trader_llama": true,
	"camel": true, "camel_husk": true,
}

// chestedMountTypes is the AbstractChestedHorse family — a chested
// member's inventory reports 5 columns (15 slots).
var chestedMountTypes = map[string]bool{
	"donkey": true, "mule": true, "llama": true, "trader_llama": true,
}

// mountInventoryColumns is the family's getInventoryColumns: a
// chested llama's columns equal its Strength (1-5); donkey/mule are
// fixed 5; every non-chested animal reports 0.
func mountInventoryColumns(e *Entity) int {
	if !e.HasChest {
		return 0
	}
	if name, _ := EntityTypeName(e.Type); llamaDecorTypes[name] {
		return int(e.Strength)
	}
	return 5
}

// equipMountChest is AbstractChestedHorse.mobInteract's chest branch:
// tamed, not sneaking, not already chested, held chest → the flag
// flips, DONKEY_CHEST/LLAMA_CHEST plays, the stack consumes one (not
// for hasInfiniteMaterials), and the mount inventory rebuilds at the
// new column count.
func (s *Server) equipMountChest(sess *Session, eid int32, hand int32, sneaking bool) bool {
	if sneaking {
		return false
	}
	e, ok := s.entities.GetPtr(eid)
	if !ok || e.Dying || e.Dim != sess.dimName() {
		return false
	}
	name, _ := EntityTypeName(e.Type)
	if !chestedMountTypes[name] || !e.Tamed || e.HasChest || e.Baby {
		return false
	}
	held, hslot := sess.stackForHand(hand)
	if held.Empty() {
		return false
	}
	if iname, ok := itemNameOf(held.ID); !ok || iname != "chest" {
		return false
	}
	e.HasChest = true
	if cells := 3 * mountInventoryColumns(e); len(e.MountInv) != cells {
		next := make([]ItemStack, cells)
		copy(next, e.MountInv)
		e.MountInv = next
	}
	snd := "entity.donkey.chest"
	if llamaDecorTypes[name] {
		snd = "entity.llama.chest"
	}
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, soundEventIDs[snd], srcNeutral, 1.0, 1.0, nil)
	if sess.mode() != GameModeCreative {
		held.Count--
		sess.setStack(hslot, held)
		sess.invState.Add(1)
		_ = s.sendContainerSlot(sess, hslot, held)
	}
	return true
}

// mountSaddleable is the entity_type tag can_equip_saddle intersected
// with the mount-screen family — llama and trader_llama take a carpet
// in the body slot instead of a saddle.
var mountSaddleable = map[string]bool{
	"horse": true, "zombie_horse": true, "skeleton_horse": true,
	"donkey": true, "mule": true, "camel": true,
}

// mountBodySlot active-set: can_wear_horse_armor (horse and
// zombie_horse) plus the llamas, whose body slot takes wool carpets.
var mountBodyArmorTypes = map[string]bool{
	"horse": true, "zombie_horse": true,
}
var llamaDecorTypes = map[string]bool{
	"llama": true, "trader_llama": true,
}

// mountMenuStack reads one mount-menu cell: slot 0 the saddle, slot 1
// the body slot, 2.. the chest cells on a chested pack animal.
func mountMenuStack(e *Entity, slot int) ItemStack {
	switch {
	case slot == 0:
		if e.Saddle {
			if sid, ok := world.ItemID("saddle"); ok {
				return ItemStack{ID: sid, Count: 1}
			}
		}
	case slot == 1:
		return e.BodyItem
	default:
		if i := slot - 2; i >= 0 && i < len(e.MountInv) {
			return e.MountInv[i]
		}
	}
	return ItemStack{}
}

// setMountMenuStack writes one mount-menu cell and flags the equipment
// delta when the saddle or body slot moved — the entity broadcast
// picks it up as a set_equipment (saddle=7, body=6).
func setMountMenuStack(e *Entity, slot int, it ItemStack) {
	switch {
	case slot == 0:
		e.Saddle = !it.Empty()
		e.markEquipDelta(7)
	case slot == 1:
		e.BodyItem = it
		e.markEquipDelta(6)
	default:
		if i := slot - 2; i >= 0 && i < len(e.MountInv) {
			e.MountInv[i] = it
		}
	}
}

// mountMayPlace is the menu slot's mayPlace/isActive pair: slot 0
// takes a saddle on the can_equip_saddle types, slot 1 takes horse
// armor on can_wear_horse_armor types or a carpet on llamas, chest
// cells accept anything on a chested animal.
func mountMayPlace(e *Entity, slot int, it ItemStack) bool {
	name, _ := EntityTypeName(e.Type)
	iname, _ := itemNameOf(it.ID)
	switch {
	case slot == 0:
		return mountSaddleable[name] && iname == "saddle"
	case slot == 1:
		if mountBodyArmorTypes[name] {
			return strings.HasSuffix(iname, "_horse_armor")
		}
		return llamaDecorTypes[name] && strings.HasSuffix(iname, "_carpet")
	default:
		return e.HasChest && slot-2 < len(e.MountInv)
	}
}

// handleMountClick applies click modes on a mount screen. Only the
// pickup/place modes are wired; anything else resyncs the menu.
func (s *Server) handleMountClick(sess *Session, w *openWindow, slot int16, button byte, mode int32) {
	if slot < 0 || int(slot) >= w.menuSize() {
		return
	}
	if mode != 0 {
		s.sendChestContent(sess)
		return
	}
	sess.invMu.RLock()
	cur := sess.carried
	sess.invMu.RUnlock()

	e := w.mount
	mountSide := int(slot) < w.containerSize()
	target := sess.menuStack(int(slot))
	switch {
	case button != 0:
		ncur, ntgt := clickRight(cur, target, nil, -1)
		if mountSide && !ntgt.Empty() && ntgt != target && !mountMayPlace(e, int(slot), ntgt) {
			break
		}
		cur, target = ncur, ntgt
	case cur.Empty():
		cur, target = target, ItemStack{}
	case target.Empty():
		if mountSide && !mountMayPlace(e, int(slot), cur) {
			break
		}
		cur, target = ItemStack{}, cur
	case !target.Stackable(cur):
		if mountSide && !mountMayPlace(e, int(slot), cur) {
			break
		}
		cur, target = target, cur
	default:
		// Same-stack merge — chest cells only; equipment slots cap at 1.
		if mountSide && slot < 2 {
			break
		}
		room := maxStackOfID(target.ID) - target.Count
		move := cur.Count
		if move > room {
			move = room
		}
		target.Count += move
		cur.Count -= move
		if cur.Count <= 0 {
			cur = ItemStack{}
		}
	}
	sess.setMenuStack(int(slot), target)
	s.setCarriedLocked(sess, cur)
	s.syncContainerMenuSlots(sess, w, []int{int(slot)})
}

// openMountInventory is AbstractHorse.mobInteract's tamed+sneak branch:
// mount_screen_open carries (containerId, inventoryColumns, entityId
// as a plain int32) then container_set_content lays out the menu —
// 2 + 3*columns mount slots plus the 36 player rows.
func (s *Server) openMountInventory(sess *Session, eid int32, sneaking bool) {
	if !sneaking {
		return
	}
	s.sendMountMenu(sess, eid)
}

// sendMountMenu is AbstractHorse.openCustomInventoryScreen: the mount
// screen opens when the entity is tamed AND carries no rider other
// than the requesting player (vanilla: (!isVehicle ||
// hasPassenger(player)) && isTamed()). The sneak-interact and the
// OPEN_INVENTORY player_command both funnel here.
func (s *Server) sendMountMenu(sess *Session, eid int32) {
	e, ok := s.entities.GetPtr(eid)
	if !ok || e == nil || e.Dying || e.Dim != sess.dimName() {
		return
	}
	name, ok := EntityTypeName(e.Type)
	// Camel.isTamed() is literal true — every camel family member
	// passes openCustomInventoryScreen's isTamed arm.
	if !ok || !mountScreenTypes[name] || (!e.Tamed && name != "camel" && name != "camel_husk") {
		return
	}
	if len(e.Passengers) > 0 {
		me := sess.entityID.Load()
		riding := false
		for _, p := range e.Passengers {
			if p == me {
				riding = true
				break
			}
		}
		if !riding {
			return
		}
	}
	cols := mountInventoryColumns(e)
	inv := 2 + 3*cols // AbstractMountInventoryMenu.getInventorySize
	if cells := 3 * cols; len(e.MountInv) != cells {
		next := make([]ItemStack, cells)
		copy(next, e.MountInv)
		e.MountInv = next
	}
	id := sess.server.nextWindow.Add(1)
	sess.windowMu.Lock()
	sess.window = &openWindow{id: id, dim: sess.dimension(), mount: e, mountSlots: inv}
	// Vanilla's first container_set_content after openMenu carries
	// stateId 1 — initializeMenu increments the container's counter.
	sess.windowState = 1
	sess.windowMu.Unlock()

	var b protocol.Buffer
	b.VarInt(id)
	b.VarInt(int32(cols))
	b.Int(e.ID)
	_ = sess.write(sess.ids().PlayMountScreenOpen, b.Bytes())
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
		sess.writeSlot(&b, sess.menuStack(i))
	}
	sess.writeSlot(&b, sess.carried)
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
	case w.merchant != nil:
		s.sendMerchantContent(sess, w)
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
		if w.merchant != nil {
			return w.merchant.slots[slot]
		}
		if w.mount != nil {
			return mountMenuStack(w.mount, slot)
		}
		if w.ender != nil {
			sess.invMu.RLock()
			defer sess.invMu.RUnlock()
			return (*w.ender)[slot]
		}
		if w.hopper != nil {
			return sess.hopperMenuStack(w.hopper, slot)
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
		if w.merchant != nil {
			w.merchant.slots[slot] = it
			return
		}
		if w.mount != nil {
			setMountMenuStack(w.mount, slot, it)
			return
		}
		if w.hopper != nil {
			sess.setHopperMenuStack(w.hopper, slot, it)
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
			sess.server.rsContainerChanged(w.dim, w.pos)
			return
		}
		if w.container != nil {
			w.dim.containers.mu.Lock()
			w.container.slots[slot] = it
			w.dim.containers.mu.Unlock()
			sess.server.rsContainerChanged(w.dim, w.pos)
			return
		}
		w.dim.chests.mu.Lock()
		w.chest.slots[slot] = it
		w.dim.chests.mu.Unlock()
		sess.server.rsContainerChanged(w.dim, w.pos)
	case slot < container+27:
		sess.setStack(int16(slotMainStart+(slot-container)), it)
	default:
		sess.setStack(int16(slotHotbarStart+(slot-container-27)), it)
	}
}

// handleChestClick implements the core click modes for an open chest
// window. Unsupported modes fall back to a full menu resync.
func (s *Server) handleChestClick(sess *Session, w *openWindow, slot int16, button byte, mode int32, seq bool) {
	var furnaceBefore ItemStack
	if w.furnace != nil {
		furnaceBefore = sess.menuStack(furnaceOutput)
	}
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
		if _, valid := inventorySwapSlot(button); !valid {
			return
		}
		if button == 40 {
			if w.furnace != nil && slot == furnaceOutput && !sess.stackAt(slotOffhand).Empty() {
				break
			}
			s.swapMenuOffhand(sess, int(slot))
			touched = append(touched, int(slot))
			break
		}
		swap := w.menuSize() - 9 + int(button)
		if w.furnace != nil && slot == furnaceOutput && !sess.menuStack(swap).Empty() {
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

	if w.furnace != nil && !furnaceBefore.Empty() {
		after := sess.menuStack(furnaceOutput)
		if !after.Stackable(furnaceBefore) || after.Count < furnaceBefore.Count {
			taken := furnaceBefore.withCount(furnaceBefore.Count - after.Count)
			if mode == 2 {
				// SWAP invokes onTake without FurnaceResultSlot.remove: its
				// crafted-item counter stays zero, while XP/recipes settle.
				taken.Count = 0
			}
			s.furnaceOutputTaken(sess, w, taken)
		}
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
		sess.writeSlot(&b, sess.menuStack(mslot))
		_ = sess.write(sess.ids().PlayContainerSlot, b.Bytes())
	}
	var cb protocol.Buffer
	sess.writeSlot(&cb, carried)
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
	if _, ok := sess.readChangedSlots(r); !ok {
		return
	}
	if _, ok := sess.readHashedSlot(r); !ok || r.Remaining() != 0 {
		return
	} // server keeps its own cursor
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
	sess.quickCraft = quickCraft{}
	s.menuRemoved(sess, w, false)
	// No Container Close echo: the real server never answers a
	// client-initiated close (four anvil closes, zero 0x11), the
	// client already retired its own window.
}

// menuRemoved runs the open menu's removal cleanup — vanilla's
// AbstractContainerMenu.removed(player): menu inputs and the cursor
// stack go back to the player — dropped as items when the menu died
// with the session (kick, disconnect), placed in the inventory on a
// client close.
func (s *Server) menuRemoved(sess *Session, w *openWindow, dropped bool) {
	s.emitContainerClose(sess, w)
	// AbstractContainerMenu.removed returns the cursor before subclasses
	// return their transient inputs. This also fixes their insertion order.
	if dropped {
		s.dropCarried(sess)
	} else {
		s.returnCarried(sess)
	}
	if w.merchant != nil {
		s.merchantClose(sess, w)
	} else if w.craft != nil {
		s.closeCraftTable(sess, w.craft)
	} else if openWindowKindAnvil(w) {
		// The anvil keeps no storage: its inputs come back to the
		// player; the cursor was returned by the common removal above.
		s.closeAnvil(sess, w, w.container)
	} else if w.container != nil && w.container.spec.menu == menuBeacon {
		s.closeBeaconMenu(sess, w, w.container, dropped)
	} else if w.container != nil && w.container.spec.menu == menuStonecutter {
		s.closeStonecutter(sess, w, w.container)
	} else if w.container != nil && w.container.spec.menu == menuGrindstone {
		s.transientMenuReturn(sess, w, w.container, []int{grindstoneTop, grindstoneBottom})
	} else if w.container != nil && w.container.spec.menu == menuSmithing {
		s.transientMenuReturn(sess, w, w.container, []int{smithingTemplate, smithingBase, smithingAddition})
	} else if w.container != nil && w.container.spec.menu == menuLoom {
		s.transientMenuReturn(sess, w, w.container, []int{loomBanner, loomDye, loomPattern})
	} else if w.container != nil && w.container.spec.menu == menuCartographyTable {
		s.transientMenuReturn(sess, w, w.container, []int{cartographyMap, cartographyAdditional})
	} else if w.container != nil && w.container.spec.menu == menuEnchantment {
		s.transientMenuReturn(sess, w, w.container, []int{enchantItem, enchantLapis})
	}
}

// dropCarried drops the cursor stack as an item entity — the
// dropOrPlaceInInventory branch a removed player takes (kick,
// disconnect), versus the placeItemBackInInventory on a clean close.
func (s *Server) dropCarried(sess *Session) {
	carried := sess.carriedBuffer()
	if carried.Empty() {
		return
	}
	sess.carriedMuSet(ItemStack{})
	s.dropItemLocked(sess, carried)
}

// returnCarried moves the cursor stack back into the player inventory,
// vanilla's placeItemBackInInventory on a clean menu close: the merge
// lands silently on the cursor's old home and the slot it touched goes
// out as Set Player Inventory. A remainder that fits nowhere drops.
func (s *Server) returnCarried(sess *Session) {
	if carried := sess.carriedBuffer(); !carried.Empty() {
		sess.carriedMuSet(ItemStack{})
		s.placeBackInInventory(sess, carried)
	}
}

// emitContainerClose is ContainerOpenersCounter.onClose's emission:
// CONTAINER_CLOSE at the container block when an opener-counted
// window retires — client close, death, respawn, or disconnect.
// 26.2 counts openers only on chests, barrels, ender chests, and
// shulker boxes; furnaces, hoppers, and the spec menus emit nothing.
func (s *Server) emitContainerClose(sess *Session, w *openWindow) {
	if w.closeEmitted {
		return
	}
	w.closeEmitted = true
	if w.chest == nil && w.ender == nil &&
		(w.container == nil || (!w.container.spec.shulker && w.container.block != "barrel")) {
		return
	}
	s.emitGameEvent(w.dim, "container_close",
		float64(w.pos.X)+0.5, float64(w.pos.Y)+0.5, float64(w.pos.Z)+0.5,
		sess, "")
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
	// ContainerOpenersCounter.onClose → CONTAINER_CLOSE at the
	// container block when the last viewer leaves. mc-go emits per
	// close on every opener-counted window.
	s.emitContainerClose(sess, w)
	var b protocol.Buffer
	b.VarInt(w.id)
	_ = sess.write(sess.ids().PlayContainerClose, b.Bytes())
}
