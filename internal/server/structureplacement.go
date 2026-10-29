package server

import "mc-go/internal/world"

// Skipping BlockEntity.preRemoveSideEffects still retires its storage. Clear
// stale window pointers first; the shared validity path owns menu retirement.
func (s *Server) structureRetireBlockEntity(d *dimension, pos world.Pos, old, next world.Block) {
	oldName, _ := world.StateOwner(old)
	newName, _ := world.StateOwner(next)
	if oldName == newName {
		return
	}
	if _, has := blockEntityOf(old); !has {
		return
	}
	d.chests.mu.Lock()
	if c := d.chests.chests[pos]; c != nil {
		clear(c.slots[:])
	}
	delete(d.chests.chests, pos)
	d.chests.mu.Unlock()
	d.hoppers.mu.Lock()
	if c := d.hoppers.hoppers[pos]; c != nil {
		clear(c.slots[:])
	}
	delete(d.hoppers.hoppers, pos)
	d.hoppers.mu.Unlock()
	d.furnaces.mu.Lock()
	if c := d.furnaces.furnaces[pos]; c != nil {
		clear(c.slots[:])
	}
	delete(d.furnaces.furnaces, pos)
	d.furnaces.mu.Unlock()
	d.containers.mu.Lock()
	if c := d.containers.containers[pos]; c != nil {
		clear(c.slots[:])
	}
	delete(d.containers.containers, pos)
	d.containers.mu.Unlock()
	delete(d.beNames, pos)
}
func (s *Server) structureRestorePendingWater(d *dimension, pending []world.Pos, introduced map[world.Pos]bool) {
	for changed := true; changed && len(pending) > 0; {
		changed = false
		remaining := pending[:0]
		for _, p := range pending {
			cur := d.w.GetBlock(p)
			if fluidIsSourceState(cur) {
				continue
			}
			restored := false
			for _, off := range []world.Pos{{Y: 1}, {Z: -1}, {X: 1}, {Z: 1}, {X: -1}} {
				q := world.Pos{X: p.X + off.X, Y: p.Y + off.Y, Z: p.Z + off.Z}
				source := d.w.GetBlock(q)
				kind, _, has := fluidOf(source)
				if has && kind == fluidWater && fluidIsSourceState(source) && !introduced[q] && fluidWritable(fluidWater, cur, true) {
					s.structurePlaceLiquid(d, p, cur)
					restored = fluidIsSourceState(d.w.GetBlock(p))
					if restored {
						break
					}
				}
			}
			if restored {
				changed = true
			} else {
				remaining = append(remaining, p)
			}
		}
		pending = remaining
	}
}
func (s *Server) structureUpdateShapes(d *dimension, placed []world.Pos) {
	restoreNames := structureKeepNames(d, placed)
	defer restoreNames()
	inside := make(map[world.Pos]bool, len(placed))
	for _, p := range placed {
		inside[p] = true
	}
	offsets := []world.Pos{{X: -1}, {X: 1}, {Z: -1}, {Z: 1}, {Y: -1}, {Y: 1}}
	for _, p := range placed {
		for _, off := range offsets {
			q := world.Pos{X: p.X + off.X, Y: p.Y + off.Y, Z: p.Z + off.Z}
			if inside[q] {
				continue
			}
			s.checkSurvival(d, p, q, d.w.GetBlock(q))
			s.checkSurvival(d, q, p, d.w.GetBlock(p))
		}
	}
	for _, p := range placed {
		for _, off := range offsets {
			q := world.Pos{X: p.X + off.X, Y: p.Y + off.Y, Z: p.Z + off.Z}
			s.checkSurvival(d, p, q, d.w.GetBlock(q))
		}
		s.rsNeighborFanOut(d, p)
		s.neighborSurvival(d, p, d.w.GetBlock(p))
	}
}

// Shared shape/liquid writers retire typed names during bookkeeping. Native
// template updates to an existing block entity preserve those component fields.
func structureKeepNames(d *dimension, positions []world.Pos) func() {
	type savedName struct{ name, id string }
	saved := map[world.Pos]savedName{}
	offsets := []world.Pos{{}, {X: -1}, {X: 1}, {Z: -1}, {Z: 1}, {Y: -1}, {Y: 1}}
	for _, p := range positions {
		for _, off := range offsets {
			q := world.Pos{X: p.X + off.X, Y: p.Y + off.Y, Z: p.Z + off.Z}
			if name := d.beNames[q]; name != "" {
				if spec, ok := blockEntityOf(d.w.GetBlock(q)); ok {
					saved[q] = savedName{name, spec.name}
				}
			}
		}
	}
	return func() {
		for p, value := range saved {
			if d.beNames[p] == "" {
				if spec, ok := blockEntityOf(d.w.GetBlock(p)); ok && spec.name == value.id {
					d.beNames[p] = value.name
				}
			}
		}
	}
}
func (s *Server) structurePlaceLiquid(d *dimension, p world.Pos, state world.Block) {
	restore := structureKeepNames(d, []world.Pos{p})
	defer restore()
	s.placeLiquid(nil, d, p, state)
}

func (s *Server) structureLoadName(d *dimension, p world.Pos, state world.Block, tag map[string]any) {
	name, ok := tag["CustomName"].(string)
	if !ok {
		return
	}
	_, container := containerSpecFor(state)
	if !container && !isChestBlock(state) && !isFurnaceBlock(state) && !isHopperBlock(state) && bstate(state) != "beacon" {
		return
	}
	if d.beNames == nil {
		d.beNames = map[world.Pos]string{}
	}
	d.beNames[p] = name
}
