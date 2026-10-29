package worldgen

import "mc-go/internal/world"

// Neighbour shape updates for block families the legacy structures
// place (registered into shapeByClass): StairBlock.updateShape
// recomputes the stair shape from horizontal neighbours.

func init() {
	shapeByClass["StairBlock"] = stairShapeUpdate
	shapeByClass["FenceBlock"] = fenceShapeUpdate
	shapeByClass["IronBarsBlock"] = barsShapeUpdate
	shapeByClass["DoorBlock"] = doorShapeUpdate
}

// doorShapeUpdate is DoorBlock.updateShape: the other half's door
// (any door, its properties) becomes this half, air without one; a
// lower half without a sturdy floor breaks.
func doorShapeUpdate(rg *Region, st world.Block, p BlockPos, dir int, nb world.Block) world.Block {
	half, _ := world.StateProperty(st, "half")
	if (dir == dirUp || dir == dirDown) && (half == "lower") == (dir == dirUp) {
		nn, _ := world.StateOwner(nb)
		if nh, _ := world.StateProperty(nb, "half"); hasClass(nn, "DoorBlock") && nh != half {
			out, _ := world.WithProperty(nb, "half", half)
			return out
		}
		return world.Air
	}
	if half == "lower" && dir == dirDown && !sturdyFull(rg.at(offset(p, 0, -1, 0)), dirUp) {
		return world.Air
	}
	return st
}

// isExceptionForConnection is Block.isExceptionForConnection.
func isExceptionForConnection(b world.Block) bool {
	n, _ := world.StateOwner(b)
	return hasClass(n, "LeavesBlock") || n == "barrier" || n == "carved_pumpkin" || n == "jack_o_lantern" ||
		n == "melon" || n == "pumpkin" || blockInTag(b, "shulker_boxes")
}

// fenceShapeUpdate is FenceBlock.updateShape: a horizontal neighbour
// connects when it is a sturdy face (not an exception), a fence of the
// same wooden-ness, or a gate across the direction (connectsTo).
func fenceShapeUpdate(rg *Region, st world.Block, p BlockPos, dir int, nb world.Block) world.Block {
	if dir == dirUp || dir == dirDown {
		return st
	}
	opp := oppositeDir(dir)
	n, _ := world.StateOwner(st)
	same := blockInTag(nb, "fences") && blockInTag(nb, "wooden_fences") == blockInTag(blockNamed(n), "wooden_fences")
	nn, _ := world.StateOwner(nb)
	gate := hasClass(nn, "FenceGateBlock") && dirAxis(stateDir(nb, "facing")) == dirAxis(clockwise(opp))
	c := !isExceptionForConnection(nb) && sturdyFull(nb, opp) || same || gate
	out, _ := world.WithProperty(st, dirNameOf[dir], boolStr(c))
	return out
}

// barsShapeUpdate is IronBarsBlock.updateShape (attachsTo): sturdy
// faces, other bars and panes, and walls.
func barsShapeUpdate(rg *Region, st world.Block, p BlockPos, dir int, nb world.Block) world.Block {
	if dir == dirUp || dir == dirDown {
		return st
	}
	nn, _ := world.StateOwner(nb)
	c := !isExceptionForConnection(nb) && sturdyFull(nb, oppositeDir(dir)) || hasClass(nn, "IronBarsBlock") || blockInTag(nb, "walls")
	out, _ := world.WithProperty(st, dirNameOf[dir], boolStr(c))
	return out
}

func isStairState(b world.Block) bool {
	n, _ := world.StateOwner(b)
	return hasClass(n, "StairBlock")
}

func stateDir(b world.Block, prop string) int {
	v, _ := world.StateProperty(b, prop)
	if d, ok := dirNames[v]; ok {
		return d
	}
	return -1
}

func dirAxis(d int) int { return d >> 1 }

// counterClockwise is Direction.getCounterClockWise (horizontal).
func counterClockwise(d int) int { return clockwise(clockwise(clockwise(d))) }

func stairShapeUpdate(rg *Region, st world.Block, p BlockPos, dir int, nb world.Block) world.Block {
	if dir == dirUp || dir == dirDown {
		return st
	}
	out, _ := world.WithProperty(st, "shape", stairsShape(rg, st, p))
	return out
}

// stairsShape is StairBlock.getStairsShape: outer corners from the
// stair in front, inner corners from the one behind, each only where
// the side cell does not continue the same run.
func stairsShape(rg *Region, st world.Block, p BlockPos) string {
	f := stateDir(st, "facing")
	half, _ := world.StateProperty(st, "half")
	rel := func(d int) world.Block {
		o := dirOffsets[d]
		return rg.at(offset(p, o[0], o[1], o[2]))
	}
	sameHalf := func(b world.Block) bool { h, _ := world.StateProperty(b, "half"); return h == half }
	canTake := func(d int) bool {
		n := rel(d)
		return !isStairState(n) || stateDir(n, "facing") != f || !sameHalf(n)
	}
	if front := rel(f); isStairState(front) && sameHalf(front) {
		ff := stateDir(front, "facing")
		if dirAxis(ff) != dirAxis(f) && canTake(oppositeDir(ff)) {
			if ff == counterClockwise(f) {
				return "outer_left"
			}
			return "outer_right"
		}
	}
	if back := rel(oppositeDir(f)); isStairState(back) && sameHalf(back) {
		bf := stateDir(back, "facing")
		if dirAxis(bf) != dirAxis(f) && canTake(bf) {
			if bf == counterClockwise(f) {
				return "inner_left"
			}
			return "inner_right"
		}
	}
	return "straight"
}

// mirrorStair is StairBlock.mirror: a stair facing along the mirrored
// axis turns around and swaps left and right shapes; one across it is
// unchanged.
func mirrorStair(b world.Block, mirror int) world.Block {
	f := stateDir(b, "facing")
	if (mirror == mirLeftRight && dirAxis(f) != dirAxis(dirNorth)) || (mirror == mirFrontBack && dirAxis(f) != dirAxis(dirEast)) {
		return b
	}
	out, _ := world.WithProperty(b, "facing", dirNameOf[oppositeDir(f)])
	sh, _ := world.StateProperty(b, "shape")
	swap := map[string]string{"inner_left": "inner_right", "inner_right": "inner_left", "outer_left": "outer_right", "outer_right": "outer_left"}
	if n, ok := swap[sh]; ok {
		out, _ = world.WithProperty(out, "shape", n)
	}
	return out
}
