package server

import (
	"math"
	"math/rand"
	"strings"

	"mc-go/internal/world"
)

// crafterNeighborChanged is CrafterBlock.neighborChanged: on a rising
// edge (signal && !triggered) the craft schedules 4 ticks out and
// TRIGGERED latches through a flag-2 write; on a falling edge
// TRIGGERED and CRAFTING clear together, still flag 2. A scheduled
// craft is not cancelled when the power drops first — vanilla's
// scheduleTick has no cancel.
func (s *Server) crafterNeighborChanged(d *dimension, pos world.Pos, blk world.Block) {
	powered := rsHasNeighborSignal(d, pos)
	triggered := rsPropsOf(blk)("triggered") == "true"
	switch {
	case powered && !triggered:
		if c := s.containerAt(d, pos); c != nil {
			d.containers.mu.Lock()
			c.craftTicks = 4
			d.containers.mu.Unlock()
		}
		if nb, ok := world.WithProperty(blk, "triggered", "true"); ok {
			s.rsWriteBlockFlag2(d, pos, nb)
		}
		s.crafterSetTriggered(d, pos, true)
	case !powered && triggered:
		if nb, ok := world.WithProperty(blk, "triggered", "false"); ok {
			if nb2, ok2 := world.WithProperty(nb, "crafting", "false"); ok2 {
				nb = nb2
			}
			s.rsWriteBlockFlag2(d, pos, nb)
		}
		s.crafterSetTriggered(d, pos, false)
	}
}

// crafterSetTriggered is CrafterBlockEntity.setTriggered: the BE flag
// behind containerData property 9 (DATA_TRIGGERED), pushed to every
// viewer of the open menu.
func (s *Server) crafterSetTriggered(d *dimension, pos world.Pos, v bool) {
	c := s.containerAt(d, pos)
	if c == nil {
		return
	}
	d.containers.mu.Lock()
	c.triggered = v
	d.containers.mu.Unlock()
	val := int32(0)
	if v {
		val = 1
	}
	s.broadcastContainerProp(c, 9, val)
}

// crafterSetCrafting writes the CRAFTING blockstate. The set is a
// client-only flag-2 write from dispenseFrom; the clear is a flag-3
// write from serverTick — vanilla's per-write flags differ.
func (s *Server) crafterSetCrafting(d *dimension, pos world.Pos, v bool) {
	blk := d.w.GetBlock(pos)
	if nb, ok := world.WithProperty(blk, "crafting", boolStr(v)); ok {
		if v {
			s.rsWriteBlockFlag2(d, pos, nb)
		} else {
			s.rsWriteBlock(d, pos, nb, nb, nil)
		}
	}
}

// broadcastContainerProp sends one Container Property to every session
// viewing container c.
func (s *Server) broadcastContainerProp(c *containerData, prop int16, val int32) {
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay {
			return true
		}
		sess.windowMu.Lock()
		w := sess.window
		sess.windowMu.Unlock()
		if w != nil && w.container == c {
			s.sendContainerProperty(sess, w.id, prop, val)
		}
		return true
	})
}

// crafterTickStep counts armed crafters down and runs the craft, and
// counts the 6-tick CRAFTING lifetime down to the flag-3 clear —
// CrafterBlockEntity.serverTick stores t-1 and clears at 0.
func (s *Server) crafterTickStep() {
	for _, d := range s.dims {
		d.containers.mu.Lock()
		var armed, uncraft []world.Pos
		for pos, c := range d.containers.containers {
			if c.craftTicks > 0 {
				c.craftTicks--
				if c.craftTicks == 0 {
					armed = append(armed, pos)
				}
			}
			if c.craftingTicks > 0 {
				c.craftingTicks--
				if c.craftingTicks == 0 {
					uncraft = append(uncraft, pos)
				}
			}
		}
		d.containers.mu.Unlock()
		for _, pos := range armed {
			s.crafterDispenseFrom(d, pos)
		}
		for _, pos := range uncraft {
			s.crafterSetCrafting(d, pos, false)
		}
	}
}

// crafterDispenseFrom is CrafterBlock.dispenseFrom: the 3x3 grid
// (disabled slots read as empty) is matched through the recipe cache.
// On failure level event 1050 clicks; on success CRAFTING latches for
// 6 ticks, the result dispenses out the front face, every non-empty
// slot shrinks by one (disabled slots included — vanilla quirk), and
// setChanged fires.
func (s *Server) crafterDispenseFrom(d *dimension, pos world.Pos) {
	c := s.containerAt(d, pos)
	if c == nil {
		return
	}
	d.containers.mu.Lock()
	var grid craftGrid
	for i := 0; i < 9 && i < len(c.slots); i++ {
		if c.disabled&(1<<uint(i)) != 0 {
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
	c.craftingTicks = 6
	d.containers.mu.Unlock()
	s.crafterSetCrafting(d, pos, true)

	s.crafterDispenseItem(d, pos, result, recipeID, preShrink)

	// getRemainingItems over the crafting input: each non-empty
	// remainder dispenses out the front. Disabled slots read as empty
	// in preShrink, so they yield no remainder.
	for i := 0; i < 9; i++ {
		if preShrink[i].Empty() {
			continue
		}
		if rem, ok := craftRemainderOf(preShrink[i].ID); ok {
			s.crafterDispenseItem(d, pos, rem, recipeID, preShrink)
		}
	}

	d.containers.mu.Lock()
	for i := range c.slots {
		if !c.slots[i].Empty() {
			removeOne(&c.slots[i])
		}
	}
	d.containers.mu.Unlock()
	s.rsContainerChanged(d, pos)
}

// crafterDispenseItem is CrafterBlock.dispenseItem: the stack inserts
// into the container in front of the crafter — one item at a time when
// the front container is itself a crafter or the stack exceeds its max
// size, whole at a time otherwise — and any remainder spawns as a
// tossed item. The item throw, the crafter_recipe_crafted trigger,
// and the 1049/2010 level events only run when a remainder exists:
// a fully-inserted result is silent.
func (s *Server) crafterDispenseItem(d *dimension, pos world.Pos, stack ItemStack, recipeID string, grid craftGrid) {
	front := crafterFront(d, pos)
	rest := stack
	if s.crafterHasContainer(d, front) {
		if crafterIsCrafter(d, front) || rest.Count > maxStackOfID(rest.ID) {
			// DispenseItemBehavior's crafter/large-stack branch: one
			// item per addItem call until the container refuses.
			for !rest.Empty() {
				moving := rest.withCount(1)
				s.crafterAddStack(d, front, pos, &moving)
				if !moving.Empty() {
					break
				}
				rest.Count--
			}
		} else {
			// Whole-stack branch: keep calling addItem until it stops
			// moving items.
			for !rest.Empty() {
				n := rest.Count
				s.crafterAddStack(d, front, pos, &rest)
				if rest.Count == n {
					break
				}
			}
		}
		if rest.Count != stack.Count {
			s.rsContainerChanged(d, front)
		}
	}
	if rest.Empty() {
		return
	}

	// DefaultDispenseItemBehavior.spawnItem: the item entity spawns
	// 0.7 blocks out the front face and gets dir*j + triangle jitter
	// per axis (speed 6 → triangle spread 0.103365); the small y dip
	// depends on the face's axis.
	dx, dy, dz := float64(front.X-pos.X), float64(front.Y-pos.Y), float64(front.Z-pos.Z)
	yOff := 0.15625
	if dy != 0 {
		yOff = 0.125
	}
	j := rand.Float64()*0.1 + 0.2
	const spread = 0.0172275 * 6
	e := &Entity{
		Type:    mustEntityType("item"),
		Dim:     d.name,
		X:       float64(pos.X) + 0.5 + 0.7*dx,
		Y:       float64(pos.Y) + 0.5 + 0.7*dy - yOff,
		Z:       float64(pos.Z) + 0.5 + 0.7*dz,
		MotionX: dx*j + (rand.Float64()-rand.Float64())*spread,
		MotionY: dy*j + (rand.Float64()-rand.Float64())*spread,
		MotionZ: dz*j + (rand.Float64()-rand.Float64())*spread,
		Item:    rest,
	}
	s.entities.Spawn(e)
	s.broadcastEntitySpawn(nil, *e)
	s.broadcastEntityMetadata(e.ID, encodeItemMetadata(rest))

	cx, cy, cz := float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay || sess.dimName() != d.name {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if math.Abs(px-cx) <= 8.5 && math.Abs(py-cy) <= 8.5 && math.Abs(pz-cz) <= 8.5 {
			s.fireCrafterRecipeCrafted(sess, recipeID, grid)
		}
		return true
	})
	s.broadcastLevelEvent(d.name, 1049, pos, 0)
	s.broadcastLevelEvent(d.name, 2010, pos, frontData(d, pos))
}

// crafterContainerInsert is HopperBlockEntity.addItem: stackable-then-
// free per slot, gated by the destination crafter's canPlaceItem when
// it is one — a disabled slot refuses everything and a slot holding a
// smaller remainder elsewhere in the grid is skipped (the even-fill
// rule that spreads a hopper feed across enabled slots).
func crafterContainerInsert(c *containerData, moving *ItemStack) {
	if !c.spec.crafter {
		slotsInsert(c.slots, moving)
		return
	}
	for i := 0; i < len(c.slots) && !moving.Empty(); i++ {
		if !crafterCanPlace(c, i, *moving) {
			continue
		}
		insertStack(&c.slots[i], moving)
	}
}

// crafterCanPlace is CrafterBlockEntity.canPlaceItem: slot enabled,
// below its max, and — once a stack is present — only fillable while
// no later enabled slot holds fewer of the same item (or is empty).
func crafterCanPlace(c *containerData, i int, stack ItemStack) bool {
	if c.disabled&(1<<uint(i)) != 0 {
		return false
	}
	cur := c.slots[i]
	if !cur.Empty() && cur.Count >= maxStackOfID(cur.ID) {
		return false
	}
	if cur.Empty() {
		return true
	}
	return !crafterSmallerStackExist(c, cur.Count, stack, i)
}

// crafterSmallerStackExist is CrafterBlockEntity.smallerStackExist:
// scan enabled slots after i for an empty slot, or one holding fewer
// of the same item.
func crafterSmallerStackExist(c *containerData, n int32, stack ItemStack, i int) bool {
	for j := i + 1; j < len(c.slots); j++ {
		if c.disabled&(1<<uint(j)) != 0 {
			continue
		}
		other := c.slots[j]
		if other.Empty() {
			return true
		}
		if other.Count < n && other.Stackable(stack) {
			return true
		}
	}
	return false
}

// crafterPlaceState is CrafterBlock.getStateForPlacement: the front
// faces the player (nearest look direction's opposite); the top is
// the horizontal look direction flipped when facing down, unflipped
// when facing up, UP otherwise; TRIGGERED starts powered iff the cell
// already sees a signal.
func (s *Server) crafterPlaceState(d *dimension, pos world.Pos, blk world.Block, sess *Session) world.Block {
	name, ok := world.StateOwner(blk)
	if !ok || strings.TrimPrefix(name, "minecraft:") != "crafter" {
		return blk
	}
	_, _, _, yaw, pitch, _ := sess.Pos()
	front := dirOpposite(nearestLookDir(yaw, pitch))
	var top string
	switch front {
	case "down":
		top = dirOpposite(yawDir(yaw))
	case "up":
		top = yawDir(yaw)
	default:
		top = "up"
	}
	if nb, ok := world.WithProperty(blk, "orientation", front+"_"+top); ok {
		blk = nb
	}
	if nb, ok := world.WithProperty(blk, "triggered", boolStr(rsHasNeighborSignal(d, pos))); ok {
		blk = nb
	}
	if nb, ok := world.WithProperty(blk, "crafting", "false"); ok {
		blk = nb
	}
	return blk
}

// crafterFront reads the crafter's facing from its orientation
// property (front is the first half of front_and_top).
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

// crafterNBTExtra writes the crafter's save keys beyond Items:
// crafting_ticks_remaining, the disabled_slots IntArray of disabled
// indices, and the triggered int — CrafterBlockEntity.saveAdditional's
// own fields (all unconditional).
func crafterNBTExtra(extra map[string]any, disabled uint16, triggered bool, craftingTicks int32) {
	extra["crafting_ticks_remaining"] = craftingTicks
	dis := []int32{}
	for i := 0; i < 9; i++ {
		if disabled&(1<<uint(i)) != 0 {
			dis = append(dis, int32(i))
		}
	}
	extra["disabled_slots"] = dis
	tv := int32(0)
	if triggered {
		tv = 1
	}
	extra["triggered"] = tv
}

// crafterHasContainer is HopperBlockEntity.getContainerAt's found
// check — a hopper, chest, furnace, or any generic container block.
func (s *Server) crafterHasContainer(d *dimension, pos world.Pos) bool {
	blk := d.w.GetBlock(pos)
	if isHopperBlock(blk) || isChestBlock(blk) || isFurnaceBlock(blk) {
		return true
	}
	_, ok := containerSpecFor(blk)
	return ok
}

// crafterIsCrafter reports whether the block at pos is a crafter —
// the dst check that switches dispenseItem to one-at-a-time inserts.
func crafterIsCrafter(d *dimension, pos world.Pos) bool {
	name, ok := world.StateOwner(d.w.GetBlock(pos))
	return ok && strings.TrimPrefix(name, "minecraft:") == "crafter"
}

// crafterAddStack is HopperBlockEntity.addItem: move as much of moving
// into the container at target as it takes, honoring the crafter
// destination's canPlaceItem even-fill gate. Returns whether a
// container lives at target at all.
func (s *Server) crafterAddStack(d *dimension, target, from world.Pos, moving *ItemStack) bool {
	blk := d.w.GetBlock(target)
	switch {
	case isHopperBlock(blk):
		d.hoppers.mu.Lock()
		defer d.hoppers.mu.Unlock()
		dst := d.hoppers.hoppers[target]
		if dst == nil {
			return false
		}
		slotsInsert(dst.slots[:], moving)
		return true
	case isChestBlock(blk):
		chest := s.chestAt(d, target)
		d.chests.mu.Lock()
		defer d.chests.mu.Unlock()
		slotsInsert(chest.slots[:], moving)
		return true
	case isFurnaceBlock(blk):
		f := s.furnaceAt(d, target)
		d.furnaces.mu.Lock()
		defer d.furnaces.mu.Unlock()
		// Same sided rules the hopper path uses: input from above,
		// fuel slot otherwise — and only fuel.
		slot := furnaceInput
		if from.Y <= target.Y {
			slot = furnaceFuel
			if !isFuelItem(moving.ID) {
				return true
			}
		}
		before := moving.Count
		insertStack(&f.slots[slot], moving)
		if moving.Count != before && slot == furnaceInput {
			f.refreshCookTotal()
		}
		return true
	default:
		c := s.containerAt(d, target)
		if c == nil {
			return false
		}
		d.containers.mu.Lock()
		defer d.containers.mu.Unlock()
		crafterContainerInsert(c, moving)
		return true
	}
}
