package server

import (
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Registered RESET clears the test volume before placing the selected
// template. An omitted AIR cell in the saved template therefore becomes AIR.
func (s *Server) testInstancePlace(d *dimension, pos world.Pos, data testInstanceData) bool {
	definition := testInstanceDefinition(data.test)
	if definition == nil {
		return false
	}
	name, _ := definition["structure"].(string)
	native := s.structureTemplateGet(name)
	if native == nil {
		return false
	}
	low, high, origin := testInstanceFootprint(pos, data)
	forced := s.forceLoadSet(d.name)
	for z := low.Z >> 4; z <= high.Z>>4; z++ {
		for x := low.X >> 4; x <= high.X>>4; x++ {
			forced[int64(uint64(uint32(x))|uint64(uint32(z))<<32)] = true
		}
	}
	inside := func(p world.Pos) bool {
		return p.X >= low.X && p.X <= high.X && p.Y >= low.Y && p.Y <= high.Y && p.Z >= low.Z && p.Z <= high.Z
	}
	for z := low.Z; z <= high.Z; z++ {
		for y := max(low.Y, d.minY); y <= min(high.Y, d.minY+d.height-1); y++ {
			for x := low.X; x <= high.X; x++ {
				s.setSilentBlockWithPolicy(d, world.Pos{X: x, Y: y, Z: z}, world.Air, true, true)
			}
		}
	}
	// Clear pending block work, leaving fluid scheduling independent, as in
	// StructureUtils.clearSpaceForStructure. Stale queue entries are filtered
	// from both scheduler indexes.
	if s.ticks != nil {
		s.ticks.mu.Lock()
		for k := range s.ticks.queued {
			if k.dim == d.name && k.kind != tickFluid && inside(k.pos) {
				delete(s.ticks.queued, k)
				delete(s.ticks.prio, k)
			}
		}
		for at, list := range s.ticks.due {
			kept := list[:0]
			for _, k := range list {
				if k.dim != d.name || k.kind == tickFluid || !inside(k.pos) {
					kept = append(kept, k)
				}
			}
			if len(kept) == 0 {
				delete(s.ticks.due, at)
			} else {
				s.ticks.due[at] = kept
			}
		}
		s.ticks.mu.Unlock()
	}
	kept := s.blockEvents[:0]
	for _, e := range s.blockEvents {
		if e.dim != d.name || !inside(e.pos) {
			kept = append(kept, e)
		}
	}
	s.blockEvents = kept
	s.testInstanceDiscardEntities(d, low, high, 0)
	s.structureTemplatePlaceWithOptions(d, native, structureTemplatePlacement{origin: origin, rotation: data.rotation, integrity: 1, knownShape: true, skipSideEffects: true, includeEntities: !data.ignoreEntities, keepLiquids: true, settingsRandom: worldgen.NewLegacy(worldgen.BlockSeed(int32(origin.X), int32(origin.Y), int32(origin.Z))), placementRandom: worldgen.NewLegacy(int64(s.rng.next()))})
	return true
}

func testInstanceFootprint(pos world.Pos, data testInstanceData) (low, high, origin world.Pos) {
	size := data.size
	if data.rotation == 1 || data.rotation == 3 {
		size[0], size[2] = size[2], size[0]
	}
	base := world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z + 1}
	end := world.Pos{X: base.X + int(size[0]) - 1, Y: base.Y + int(size[1]) - 1, Z: base.Z + int(size[2]) - 1}
	low = world.Pos{X: min(base.X, end.X), Y: min(base.Y, end.Y), Z: min(base.Z, end.Z)}
	high = world.Pos{X: max(base.X, end.X), Y: max(base.Y, end.Y), Z: max(base.Z, end.Z)}

	origin = base
	switch data.rotation {
	case 1:
		origin.X += int(data.size[2]) - 1
	case 2:
		origin.X, origin.Z = base.X+int(data.size[0])-1, base.Z+int(data.size[2])-1
	case 3:
		origin.Z += int(data.size[0]) - 1
	}
	return low, high, origin
}
func (s *Server) testInstanceDiscardEntities(d *dimension, low, high world.Pos, padding int) {
	for _, e := range s.entities.All() {
		name, _ := EntityTypeName(e.Type)
		dims := entityBBDims(&e)
		if e.presenceOnly || name == "player" || e.Dim != d.name || e.X+dims[0]/2 <= float64(low.X-padding) || e.X-dims[0]/2 >= float64(high.X+1+padding) || e.Y+dims[1] <= float64(low.Y-padding) || e.Y >= float64(high.Y+1+padding) || e.Z+dims[0]/2 <= float64(low.Z-padding) || e.Z-dims[0]/2 >= float64(high.Z+1+padding) {
			continue
		}
		s.entities.Remove(e.ID)
		s.broadcastEntityRemoveIn(d.name, nil, []int32{e.ID})
	}
}
