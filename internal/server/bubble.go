package server

import "mc-go/internal/world"

// Bubble columns — BubbleColumnBlock and the LiquidBlock hooks that
// feed it, all measured against 26.2:
//
//   - A water SOURCE cell (level 0) whose below neighbour is soul_sand
//     or magma_block arms a 20-tick BLOCK tick
//     (LiquidBlock.tryScheduleBubbleBlockColumn runs from
//     neighbourChanged on any adjacent write and from updateShape on a
//     below-write). The occupy tag is the WATER fluid tag, so lava
//     never qualifies and neither do flowing (level 1-7) or falling
//     cells.
//   - That tick — and the bubble cell's own tick — run updateColumn:
//     derive the column state from the block below the start cell
//     (soul_sand → drag=false, magma_block → drag=true, a bubble cell
//     inherits its state, anything else writes WATER back into a
//     bubble cell and leaves water alone), setBlock it flag-2, then
//     keep writing the SAME state upward through every occupiable
//     cell until the first non-occupiable one.
//   - A bubble cell's updateShape always schedules a water FLUID tick
//     on itself, plus its own 5-tick block tick when it can't survive
//     (below not bubble/soul/magma), when the write arrived from
//     below, or when the cell above became occupiable-but-not-bubble
//     (column extension).

// bubbleCanOccupy is BubbleColumnBlock.canOccupy: a bubble cell, or a
// full water source (tag BUBBLE_COLUMN_CAN_OCCUPY — water only).
func bubbleCanOccupy(b world.Block) bool {
	name, ok := world.StateOwner(b)
	if ok && name == "bubble_column" {
		return true
	}
	k, level, ok := fluidOf(b)
	return ok && k == fluidWater && level == 0
}

// bubbleCanSurvive is BubbleColumnBlock.canSurvive: the below cell is
// a bubble column or a column-enabling block.
func bubbleCanSurvive(d *dimension, pos world.Pos) bool {
	name, _ := world.StateOwner(d.w.GetBlock(posBelow(pos, 1)))
	return name == "bubble_column" || name == "soul_sand" || name == "magma_block"
}

// bubbleColumnState is BubbleColumnBlock.getColumnState: what the
// start cell becomes given the state below it.
func bubbleColumnState(below, cur world.Block) world.Block {
	name, _ := world.StateOwner(below)
	switch name {
	case "bubble_column":
		return below
	case "soul_sand":
		if nb, ok := world.BlockStateOf("bubble_column", map[string]string{"drag": "false"}); ok {
			return nb
		}
	case "magma_block":
		if nb, ok := world.BlockStateOf("bubble_column", map[string]string{"drag": "true"}); ok {
			return nb
		}
	default:
		if cn, _ := world.StateOwner(cur); cn == "bubble_column" {
			if w, ok := world.BlockStateOf("water", nil); ok {
				return w
			}
		}
	}
	return cur
}

// bubbleUpdateColumn is BubbleColumnBlock.updateColumn: rewrite pos
// from the state below it, then write the same state upward through
// every occupiable cell.
func (s *Server) bubbleUpdateColumn(d *dimension, pos world.Pos) {
	cur := d.w.GetBlock(pos)
	if !bubbleCanOccupy(cur) {
		return
	}
	next := bubbleColumnState(d.w.GetBlock(posBelow(pos, 1)), cur)
	s.setSilentBlock(d, pos, next)
	for p := posAbove(pos, 1); bubbleCanOccupy(d.w.GetBlock(p)); p = posAbove(p, 1) {
		s.setSilentBlock(d, p, next)
	}
}

// bubbleLiquidTick is LiquidBlock.tick for a water cell: still a full
// source that can occupy, hand the cell to updateColumn — which either
// converts it to a bubble (supported below) or writes it back and
// drains whatever column sits above it.
func (s *Server) bubbleLiquidTick(d *dimension, pos world.Pos) {
	b := d.w.GetBlock(pos)
	k, level, ok := fluidOf(b)
	if !ok || k != fluidWater || level != 0 {
		return
	}
	s.bubbleUpdateColumn(d, pos)
}

// bubbleColumnTick is BubbleColumnBlock.tick: updateColumn starting at
// this cell with the state currently below.
func (s *Server) bubbleColumnTick(d *dimension, pos world.Pos) {
	s.bubbleUpdateColumn(d, pos)
}

// bubbleNeighborChanged runs when a neighbour of a cell is written —
// LiquidBlock.neighbourChanged/updateShape for water cells (arm the
// 20-tick conversion check) and BubbleColumnBlock.updateShape for
// bubble cells (self fluid tick + the conditional 5-tick).
func (s *Server) bubbleNeighborChanged(d *dimension, pos, changed world.Pos) {
	b := d.w.GetBlock(pos)
	name, _ := world.StateOwner(b)
	switch name {
	case "water":
		k, level, ok := fluidOf(b)
		if !ok || k != fluidWater || level != 0 {
			return
		}
		below, _ := world.StateOwner(d.w.GetBlock(posBelow(pos, 1)))
		if below == "soul_sand" || below == "magma_block" {
			s.scheduleBlockTick(d, pos, 20)
		}
	case "bubble_column":
		s.scheduleFluidTick(d, pos, waterTickDelay)
		if !bubbleCanSurvive(d, pos) ||
			(changed.Y == pos.Y-1 && changed.X == pos.X && changed.Z == pos.Z) {
			s.scheduleBlockTick(d, pos, 5)
			return
		}
		if changed.Y == pos.Y+1 && changed.X == pos.X && changed.Z == pos.Z {
			above := d.w.GetBlock(changed)
			an, _ := world.StateOwner(above)
			if an != "bubble_column" && bubbleCanOccupy(above) {
				s.scheduleBlockTick(d, pos, 5)
			}
		}
	}
}
