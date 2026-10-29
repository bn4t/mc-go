package server

// ContainerEntity exposes global storage indices0..size-1 to /item. The
// ordinary slot registry therefore gives cargo the same hotbar/inventory
// aliases as block containers, with the vehicle's own capacity boundary.
func (s *Server) vehicleCargoSlot(e *Entity, id int32) (*containerData, bool) {
	if e == nil || id < 0 {
		return nil, false
	}
	name, _ := EntityTypeName(e.Type)
	if int(id) >= vehicleContainerSize(name) {
		return nil, false
	}
	return s.vehicleCargoInventory(e), true
}

func (s *Server) readVehicleCargoSlot(e *Entity, id int32) (ItemStack, bool) {
	c, ok := s.vehicleCargoSlot(e, id)
	if !ok {
		return ItemStack{}, false
	}
	s.unpackVehicleCargo(e, c, nil)
	return c.slots[id], true
}

func (s *Server) writeVehicleCargoSlot(e *Entity, id int32, stack ItemStack) bool {
	c, ok := s.vehicleCargoSlot(e, id)
	if !ok {
		return false
	}
	s.unpackVehicleCargo(e, c, nil)
	old := c.slots[id]
	c.slots[id] = stack
	s.queueVehicleCargoSlot(e, int(id), old, nil)
	return true
}
