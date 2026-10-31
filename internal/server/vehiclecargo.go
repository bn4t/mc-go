package server

import (
	"math"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Cargo vehicles share one inventory across menus, native saves and /data.
// Their menu shapes and decorated entity-name titles are measured on 26.2.
func (s *Server) vehicleCargoInventory(e *Entity) *containerData {
	name, registered := EntityTypeName(e.Type)
	size := vehicleContainerSize(name)
	if !registered || size == 0 {
		return nil
	}
	if e.vehicleCargo == nil {
		inputs := make([]int, size)
		for slot := range inputs {
			inputs[slot] = slot
		}
		menu := int32(menuGeneric9x3)
		if size == 5 {
			menu = menuHopper
		}
		e.vehicleCargo = &containerData{spec: &containerSpec{menu: menu, slots: size, inputs: inputs}, slots: make([]ItemStack, size), block: name}
		s.vehicleCargoReload(e)
	}
	return e.vehicleCargo
}

// Preserve inventory identity when /data reloads a viewed entity. Deferred
// menu deltas coalesce through the same player-tick path as block inventories.
func (s *Server) vehicleCargoReload(e *Entity) {
	c := e.vehicleCargo
	if c == nil {
		return
	}
	old := append([]ItemStack(nil), c.slots...)
	clear(c.slots)
	itemsInto(e.ExtraNBT["Items"], c.slots)
	c.lootTable, _ = e.ExtraNBT["LootTable"].(string)
	c.lootSeed, _ = nbtNumber(e.ExtraNBT["LootTableSeed"])
	for slot, previous := range old {
		if !menuRemoteStackEqual(previous, c.slots[slot]) {
			s.queueVehicleCargoSlot(e, slot, previous, nil)
		}
	}
}

func vehicleCargoSavedFields(e *Entity, save map[string]any) {
	c := e.vehicleCargo
	if c == nil {
		return
	}
	delete(save, "Items")
	delete(save, "LootTable")
	delete(save, "LootTableSeed")
	if c.lootTable != "" {
		save["LootTable"] = c.lootTable
		if c.lootSeed != 0 {
			save["LootTableSeed"] = c.lootSeed
		}
	} else {
		save["Items"] = itemsNBT(c.slots)
	}
}

func (s *Server) queueVehicleCargoSlot(e *Entity, slot int, old ItemStack, exclude *Session) {
	s.sessions.Range(func(_, value any) bool {
		viewer := value.(*Session)
		if viewer == exclude {
			return true
		}
		viewer.windowMu.Lock()
		w := viewer.window
		if w != nil && w.vehicle == e {
			if w.commandUpdates == nil {
				w.commandUpdates = &menuCommandUpdates{slots: map[int]ItemStack{}}
			}
			if _, queued := w.commandUpdates.slots[slot]; !queued {
				w.commandUpdates.slots[slot] = old
			}
		}
		viewer.windowMu.Unlock()
		return true
	})
}

func (s *Server) unpackVehicleCargo(e *Entity, c *containerData, player *Session) {
	if c.lootTable == "" {
		return
	}
	table, seed := c.lootTable, c.lootSeed
	c.lootTable = ""
	c.lootSeed = 0
	old := append([]ItemStack(nil), c.slots...)
	biome := ""
	if d := s.dims[e.Dim]; d != nil {
		biome = s.spawnBiomeAt(d, world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))})
	}
	unpackLootIntoInBiome(c.slots, table, seed, biome)
	for slot, previous := range old {
		if !menuRemoteStackEqual(previous, c.slots[slot]) {
			s.queueVehicleCargoSlot(e, slot, previous, nil)
		}
	}
	if player != nil {
		s.firePlayerGeneratesContainerLoot(player, table)
	}
}

// Chest boats first offer their one passenger seat. Sneaking, a full seat or
// spectator access opens cargo instead; cart containers always offer a menu.
func (s *Server) vehicleCargoInteract(sess *Session, id int32, sneaking bool) bool {
	e, ok := s.entities.GetPtr(id)
	if !ok || e.Dying || e.Dim != sess.dimName() {
		return false
	}
	name, _ := EntityTypeName(e.Type)
	if vehicleContainerSize(name) == 0 {
		return false
	}
	if isBoatEntityName(name) && sess.mode() != GameModeSpectator && !sneaking && len(e.Passengers) < 1 {
		return false
	}
	s.openVehicleCargo(sess, e)
	return true
}

func (s *Server) openVehicleCargo(sess *Session, e *Entity) bool {
	if e == nil || e.Dying || e.Dim != sess.dimName() {
		return false
	}
	c := s.vehicleCargoInventory(e)
	if c == nil {
		return false
	}
	sess.windowMu.Lock()
	previous := sess.window
	sess.windowMu.Unlock()
	if previous != nil {
		s.closeWindow(sess)
		s.menuRemoved(sess, previous, false)
	}
	if sess.mode() == GameModeSpectator && c.lootTable != "" {
		s.sendSystemChatComponent(sess, commandError("container.spectatorCantOpen"))
		return false
	}
	s.unpackVehicleCargo(e, c, sess)
	sess.quickCraft = quickCraft{}
	id := s.nextWindow.Add(1)
	w := &openWindow{id: id, dim: s.dimensionByName(e.Dim), container: c, vehicle: e, kind: c.spec.menu}
	sess.windowMu.Lock()
	sess.window = w
	sess.windowState = 0
	sess.windowMu.Unlock()
	var packet protocol.Buffer
	packet.VarInt(id)
	packet.VarInt(c.spec.menu)
	packet.Raw(entityDisplayName(e).content())
	_ = sess.write(sess.ids().PlayOpenScreen, packet.Bytes())
	s.sendContainerMenuContent(sess)
	name, _ := EntityTypeName(e.Type)
	if name == "chest_minecart" || isBoatEntityName(name) {
		s.emitGameEvent(w.dim, "container_open", e.X, e.Y, e.Z, sess, "")
	}
	return true
}

func vehicleEntityInteractionRange(sess *Session) float64 {
	base := 3.0
	mods := map[string]attrMod{}
	if player, ok := sess.server.entities.Get(sess.entityID.Load()); ok {
		if value, ok := player.AttrBase["entity_interaction_range"]; ok {
			base = value
		}
		for key, modifier := range player.AttrMods["entity_interaction_range"] {
			mods[key] = modifier
		}
	}
	if sess.mode() == GameModeCreative {
		if _, present := mods[attrModCreativeEntityRange]; !present {
			mods[attrModCreativeEntityRange] = attrMod{value: 2}
		}
	}
	value := attrValue(base, mods)
	if math.IsNaN(value) {
		return 0
	}
	return min(64, max(0, value))
}

// ContainerEntity validity is eye-to-entity-box distance, using entity reach
// plus four. The measured survival boundary lies between z-1.90 and z-1.96
// for a cart at(6.5,-60,5.5), unlike a block menu's position/range.
func (s *Server) vehicleCargoValid(sess *Session, w *openWindow) bool {
	e := w.vehicle
	current, exists := s.entities.GetPtr(e.ID)
	if !exists || current != e || e.Dying || e.Dim != sess.dimName() || w.dim != sess.dimension() {
		return false
	}
	bounds := chestBoundsOf(sess)
	size := entityBBDims(e)
	width, height := size[0], size[1]
	dx := bounds.x - min(max(bounds.x, e.X-width/2), e.X+width/2)
	dy := bounds.eyeY - min(max(bounds.eyeY, e.Y), e.Y+height)
	dz := bounds.z - min(max(bounds.z, e.Z-width/2), e.Z+width/2)
	reach := vehicleEntityInteractionRange(sess) + 4
	return dx*dx+dy*dy+dz*dz < reach*reach
}

func (s *Server) validateVehicleCargo(sess *Session) {
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || w.vehicle == nil || s.vehicleCargoValid(sess, w) {
		return
	}
	sess.windowMu.Lock()
	if sess.window != w {
		sess.windowMu.Unlock()
		return
	}
	sess.window = nil
	sess.windowMu.Unlock()
	sess.quickCraft = quickCraft{}
	var packet protocol.Buffer
	packet.VarInt(w.id)
	_ = sess.write(sess.ids().PlayContainerClose, packet.Bytes())
	s.menuRemoved(sess, w, false)
}

func (s *Server) closeVehicleCargo(sess *Session, w *openWindow) {
	if w.vehicle == nil {
		return
	}
	name, _ := EntityTypeName(w.vehicle.Type)
	if name == "chest_minecart" || isBoatEntityName(name) {
		e := w.vehicle
		s.emitGameEvent(s.dimensionByName(e.Dim), "container_close", e.X, e.Y, e.Z, sess, "")
	}
}

// A cargo menu uses the same click gestures as ordinary generic containers,
// while validity and spectator read-only behavior are entity-bound.
func (s *Server) vehicleCargoClickAllowed(sess *Session, p protocol.Packet) bool {
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || w.vehicle == nil {
		return true
	}
	win, err := protocol.NewPacketReader(p.Data).VarInt()
	if err != nil || win != w.id {
		return true
	}
	if !s.vehicleCargoValid(sess, w) {
		s.validateVehicleCargo(sess)
		return false
	}
	if sess.mode() == GameModeSpectator {
		s.sendContainerMenuContent(sess)
		return false
	}
	return true
}
