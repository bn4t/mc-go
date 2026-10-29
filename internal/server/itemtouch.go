package server

import "math"

// playerItemTouchTick belongs to Player.aiStep, so it keeps running while
// ItemEntity age, physics and pickup-delay countdown are frozen.
func (s *Server) playerItemTouchTick(sess *Session) {
	if !sess.gameplayTickReady() || !sess.spawned.Load() || sess.dead || sess.health <= 0 || sess.mode() == GameModeSpectator {
		return
	}
	x, y, z, _, _, _ := sess.Pos()
	half, height := s.playerCollisionDims(&sess.playerRecord)
	minX, maxX, minY, maxY, minZ, maxZ := x-half-1, x+half+1, y-.5, y+height+.5, z-half-1, z+half+1
	if id := sess.riding.Load(); id != 0 {
		if vehicle, ok := s.entities.GetPtr(id); ok {
			dims := entityBBDims(vehicle)
			minX = math.Min(x-half, vehicle.X-dims[0]/2) - 1
			maxX = math.Max(x+half, vehicle.X+dims[0]/2) + 1
			minY = math.Min(y, vehicle.Y)
			maxY = math.Max(y+height, vehicle.Y+dims[1])
			minZ = math.Min(z-half, vehicle.Z-dims[0]/2) - 1
			maxZ = math.Max(z+half, vehicle.Z+dims[0]/2) + 1
		}
	}
	itemType := mustEntityType("item")
	var nearby []*Entity
	s.entities.mu.RLock()
	for _, e := range s.entities.byID {
		if e.Type == itemType && e.Dim == sess.dimName() && e.X+.125 > minX && e.X-.125 < maxX && e.Y+.25 > minY && e.Y < maxY && e.Z+.125 > minZ && e.Z-.125 < maxZ {
			nearby = append(nearby, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range nearby {
		s.touchItemEntity(sess, e)
	}
}

// Inventory.add may transfer part of a stack and still return false. The
// successful-take callbacks and animation run only once it consumes the rest.
func (s *Server) touchItemEntity(sess *Session, e *Entity) {
	if current, ok := s.entities.GetPtr(e.ID); !ok || current != e || e.Item.Empty() || e.FakeItem || e.PickupDelay != 0 {
		return
	}
	if e.HasOwner && e.Owner != sess.getUUID() {
		return
	}
	original := e.Item
	remainder, changed := sess.insertPickupStack(original)
	if sess.mode() == GameModeCreative {
		remainder = ItemStack{}
	}
	if changed {
		s.fireInventoryChanged(sess)
	}
	if !remainder.Empty() {
		// ItemStack mutates in place in vanilla's Inventory.add. Its synced
		// data entry is not marked dirty merely by a partial pickup.
		e.Item = remainder
		return
	}
	s.broadcastTakeItem(e.Dim, e.ID, sess.entityID.Load(), original.Count)
	s.entities.Remove(e.ID)
	s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
	sess.addItemStat(statPickedUp, original.ID, original.Count)
	if e.Thrower != 0 {
		if owner, ok := s.entities.Get(e.Thrower); ok {
			if name, ok := EntityTypeName(owner.Type); ok {
				s.fireThrownItemPickedUpByPlayer(sess, original, name)
			}
		} else if s.sessionByEntityID(e.Thrower) != nil {
			s.fireThrownItemPickedUpByPlayer(sess, original, "player")
		}
	}
}

// insertPickupStack models Inventory.add's ordering without changing the
// independent insertion policies used by recipes and container callbacks.
// Existing stacks prefer selected hand, then offhand, then hotbar/main;
// fresh stacks use only the 36 main inventory slots.
func (sess *Session) insertPickupStack(it ItemStack) (ItemStack, bool) {
	if it.Empty() {
		return ItemStack{}, false
	}
	selected := int16(slotHotbarStart) + int16(sess.held.Load())
	order := []int16{selected, slotOffhand}
	for i := int16(slotHotbarStart); i < slotOffhand; i++ {
		if i != selected {
			order = append(order, i)
		}
	}
	for i := int16(slotMainStart); i < slotHotbarStart; i++ {
		order = append(order, i)
	}
	var free []int16
	for i := int16(slotHotbarStart); i < slotOffhand; i++ {
		free = append(free, i)
	}
	for i := int16(slotMainStart); i < slotHotbarStart; i++ {
		free = append(free, i)
	}
	type update struct {
		slot  int16
		stack ItemStack
	}
	var updates []update
	sess.invMu.Lock()
	for _, slot := range order {
		if it.Empty() {
			break
		}
		cur := sess.inv[slot]
		if !cur.Stackable(it) {
			continue
		}
		n := min(it.Count, maxStackOfID(cur.ID)-cur.Count)
		if n <= 0 {
			continue
		}
		sess.inv[slot].Count += n
		it.Count -= n
		updates = append(updates, update{slot, sess.inv[slot]})
	}
	for _, slot := range free {
		if it.Empty() {
			break
		}
		if !sess.inv[slot].Empty() {
			continue
		}
		n := min(it.Count, maxStackOfID(it.ID))
		sess.inv[slot] = it.withCount(n)
		it.Count -= n
		updates = append(updates, update{slot, sess.inv[slot]})
	}
	sess.invMu.Unlock()
	for _, u := range updates {
		_ = sess.server.sendContainerSlot(sess, u.slot, u.stack)
	}
	if it.Count <= 0 {
		it = ItemStack{}
	}
	return it, len(updates) > 0
}

// applyItemTouchSave reads ItemEntity's target UUID (the legacy Owner key)
// and short age/pickup-delay counters. A missing Owner clears the restriction.
func applyItemTouchSave(e *Entity, save map[string]any) {
	e.Owner = [16]byte{}
	e.HasOwner = false
	if owner, ok := foxTrustedUUID(save["Owner"]); ok {
		e.Owner = owner
		e.HasOwner = true
	}
	e.PickupDelay = int(itemSavedShort(save["PickupDelay"], 0))
	e.ItemAge = int32(itemSavedShort(save["Age"], 0))
}

// itemSavedShort follows NumericTag.shortValue: integer tags keep their low
// bits; floating tags floor to Java's saturated int range before narrowing.
func itemSavedShort(value any, fallback int16) int16 {
	switch n := value.(type) {
	case int8:
		return int16(n)
	case uint8:
		return int16(int8(n))
	case int16:
		return n
	case uint16:
		return int16(n)
	case int32:
		return int16(n)
	case uint32:
		return int16(n)
	case int64:
		return int16(n)
	case uint64:
		return int16(n)
	case int:
		return int16(n)
	case uint:
		return int16(n)
	case float32:
		return itemFloatShort(float64(n))
	case float64:
		return itemFloatShort(n)
	default:
		return fallback
	}
}

func itemFloatShort(value float64) int16 {
	value = math.Floor(value)
	if math.IsNaN(value) || value <= math.MinInt32 {
		return 0
	}
	if value >= math.MaxInt32 {
		return -1
	}
	return int16(int32(value))
}
