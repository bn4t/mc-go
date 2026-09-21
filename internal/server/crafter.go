package server

import (
	"math"
	"strings"

	"mc-go/internal/world"
)

// crafterSignal reports whether a block emits a redstone signal:
// redstone blocks and torches always do, and levers/buttons/observers
// do while their powered property is set.
func crafterSignal(b world.Block) bool {
	name, ok := world.StateOwner(b)
	if !ok {
		return false
	}
	switch strings.TrimPrefix(name, "minecraft:") {
	case "redstone_block", "redstone_torch", "redstone_wall_torch":
		return true
	}
	if p, ok := world.StateProperty(b, "powered"); ok && p == "true" {
		return true
	}
	return false
}

// crafterNeighborSignal reports whether any of pos's six neighbors
// emits a signal — vanilla's Level.hasNeighborSignal.
func (s *Server) crafterNeighborSignal(d *dimension, pos world.Pos) bool {
	for _, off := range faceOffsets {
		np := world.Pos{X: pos.X + off[0], Y: pos.Y + off[1], Z: pos.Z + off[2]}
		if crafterSignal(d.w.GetBlock(np)) {
			return true
		}
	}
	return false
}

// crafterNeighborCheck re-evaluates the crafters adjacent to a changed
// block: on a rising edge (signal && !triggered) the craft schedules
// for 4 ticks and TRIGGERED latches; on a falling edge TRIGGERED and
// CRAFTING clear. A queued tick still crafts even if the power drops
// first — vanilla's scheduleTick can't be cancelled.
func (s *Server) crafterNeighborCheck(d *dimension, pos world.Pos) {
	for _, off := range faceOffsets {
		np := world.Pos{X: pos.X + off[0], Y: pos.Y + off[1], Z: pos.Z + off[2]}
		blk := d.w.GetBlock(np)
		name, ok := world.StateOwner(blk)
		if !ok || strings.TrimPrefix(name, "minecraft:") != "crafter" {
			continue
		}
		c := s.containerAt(d, np)
		if c == nil {
			continue
		}
		d.containers.mu.Lock()
		sig := s.crafterNeighborSignal(d, np)
		if sig && !c.triggered {
			c.triggered = true
			c.craftTicks = 4
		} else if !sig && c.triggered {
			c.triggered = false
		}
		d.containers.mu.Unlock()
	}
}

// crafterTickStep counts armed crafters down and runs the craft.
func (s *Server) crafterTickStep() {
	for _, d := range s.dims {
		d.containers.mu.Lock()
		var armed []world.Pos
		for pos, c := range d.containers.containers {
			if c.craftTicks > 0 {
				c.craftTicks--
				if c.craftTicks == 0 {
					armed = append(armed, pos)
				}
			}
		}
		d.containers.mu.Unlock()
		for _, pos := range armed {
			s.crafterCraft(d, pos)
		}
	}
}

// crafterCraft runs CrafterBlock.dispenseFrom: the 3x3 grid (disabled
// slots read as empty) is matched through the recipe cache; on failure
// level event 1050 clicks; on success the result ejects into the
// faced container or spawns as an item, every non-empty slot shrinks
// by one (disabled slots included — vanilla quirk), players within
// 8.5 earn crafter_recipe_crafted, and the 1049/2010 events play.
func (s *Server) crafterCraft(d *dimension, pos world.Pos) {
	c := s.containerAt(d, pos)
	if c == nil {
		return
	}
	d.containers.mu.Lock()
	var grid craftGrid
	for i := 0; i < 9 && i < len(c.slots); i++ {
		if c.disabled&(1<<i) != 0 {
			continue
		}
		grid[i] = c.slots[i]
	}
	d.containers.mu.Unlock()

	result, recipeID, ok := craftResult(grid)
	if !ok || result.Empty() {
		s.broadcastLevelEvent(d.name, 1050, pos, 0)
		return
	}
	preShrink := grid

	d.containers.mu.Lock()
	for i := range c.slots {
		if !c.slots[i].Empty() {
			c.slots[i].Count--
			if c.slots[i].Count <= 0 {
				c.slots[i] = ItemStack{}
			}
		}
	}
	d.containers.mu.Unlock()

	// Eject toward the crafter's front face: into a container there
	// when one exists, else as a tossed item.
	front := crafterFront(d, pos)
	fx, fy, fz := float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5
	rest := result
	if fc := s.containerAt(d, front); fc != nil && !rest.Empty() {
		d.containers.mu.Lock()
		slotsInsert(fc.slots, &rest)
		d.containers.mu.Unlock()
	}
	if !rest.Empty() {
		dx, dy, dz := float64(front.X-pos.X), float64(front.Y-pos.Y), float64(front.Z-pos.Z)
		e := &Entity{
			Type: mustEntityType("item"),
			Dim:  d.name,
			X:    fx + 0.7*dx, Y: fy + 0.7*dy, Z: fz + 0.7*dz,
			MotionX: 0.1 * dx, MotionY: 0.1*dy + 0.2, MotionZ: 0.1 * dz,
			Item: rest,
		}
		s.entities.Spawn(e)
		s.broadcastEntitySpawn(nil, *e)
		s.broadcastEntityMetadata(e.ID, encodeItemMetadata(rest))
	}

	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay || sess.dimName() != d.name {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if math.Abs(px-fx) <= 8.5 && math.Abs(py-fy) <= 8.5 && math.Abs(pz-fz) <= 8.5 {
			s.fireCrafterRecipeCrafted(sess, recipeID, preShrink)
		}
		return true
	})
	s.broadcastLevelEvent(d.name, 1049, pos, 0)
	s.broadcastLevelEvent(d.name, 2010, pos, frontData(d, pos))
}

// crafterFront reads the crafter's facing from its orientation
// property (front is the second half of front-and-top).
func crafterFront(d *dimension, pos world.Pos) world.Pos {
	blk := d.w.GetBlock(pos)
	or, _ := world.StateProperty(blk, "orientation")
	front := "north"
	if i := strings.IndexByte(or, '_'); i >= 0 {
		front = or[:i]
	}
	switch front {
	case "down":
		return world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	case "up":
		return world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z}
	case "south":
		return world.Pos{X: pos.X, Y: pos.Y, Z: pos.Z + 1}
	case "west":
		return world.Pos{X: pos.X - 1, Y: pos.Y, Z: pos.Z}
	case "east":
		return world.Pos{X: pos.X + 1, Y: pos.Y, Z: pos.Z}
	}
	return world.Pos{X: pos.X, Y: pos.Y, Z: pos.Z - 1}
}

// frontData maps the front direction to the Direction.get3DDataValue
// vanilla sends in the 2010 level event.
func frontData(d *dimension, pos world.Pos) int32 {
	blk := d.w.GetBlock(pos)
	or, _ := world.StateProperty(blk, "orientation")
	front := "north"
	if i := strings.IndexByte(or, '_'); i >= 0 {
		front = or[:i]
	}
	switch front {
	case "down":
		return 0
	case "up":
		return 1
	case "north":
		return 2
	case "south":
		return 3
	case "west":
		return 4
	case "east":
		return 5
	}
	return 2
}

// faceOffsets are the six orthogonal neighbor offsets.
var faceOffsets = [6][3]int{
	{1, 0, 0}, {-1, 0, 0},
	{0, 1, 0}, {0, -1, 0},
	{0, 0, 1}, {0, 0, -1},
}
