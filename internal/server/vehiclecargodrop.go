package server

import (
	"math"
	"math/rand"
)

// Killed container vehicles spill their inventory independently of entity_drops.
// Java26.2 /kill measurements distinguish this removal callback from combat
// destruction, which separately decides whether to drop the vehicle itself.
func (s *Server) spillVehicleCargo(e *Entity) {
	c := s.vehicleCargoInventory(e)
	if c == nil {
		return
	}
	s.unpackVehicleCargo(e, c, nil)
	for slot, stack := range c.slots {
		if stack.Empty() {
			continue
		}
		c.slots[slot] = ItemStack{}
		s.queueVehicleCargoSlot(e, slot, stack, nil)
		// Containers' measured scatter: each occupied slot shares one position,
		// bursts hold10..30 items, and each burst has independent triangular motion.
		x := math.Floor(e.X) + .125 + rand.Float64()*.75
		y := math.Floor(e.Y) + rand.Float64()*.75
		z := math.Floor(e.Z) + .125 + rand.Float64()*.75
		for stack.Count > 0 {
			part := stack
			part.Count = min(stack.Count, int32(10+rand.Intn(21)))
			stack.Count -= part.Count
			triangle := func(center float64) float64 { return center + (rand.Float64()-rand.Float64())*.11485000171139836 }
			drop := s.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: e.Dim, X: x, Y: y, Z: z, Item: part, MotionX: triangle(0), MotionY: triangle(.2), MotionZ: triangle(0)})
			s.broadcastEntitySpawn(nil, *drop)
			s.broadcastEntityMetadata(drop.ID, encodeItemMetadata(part))
		}
	}
}
