package worldgen

import "mc-go/internal/world"

// Shape updates for template placement's shape pass (shapeByClass):
// walls, stairs, fences and iron bars.
//
// WallBlock.updateShape, measured
// from the 26.2 bytecode: a vertical update from above re-evaluates the
// sides and post against the block above (topUpdate); a horizontal one
// reconnects that side (connectsTo: walls, sturdy non-exception faces,
// iron bars, perpendicular fence gates) and re-evaluates against the
// block above (sideUpdate); from below nothing changes. A connected side
// is tall when the face the block above presents downward covers the
// side's test rectangle (x 7..9, z 0..9 toward the side), low
// otherwise; the post rises unless the sides make a straight tall run,
// always for a lone, uneven or end post, and when the face above covers
// the center (x, z 7..9) or the block above is a raised wall or in
// wall_post_override.
//
// The face below the block above is modeled for full sturdy faces (it
// covers everything) and walls (post 4..12 when raised, sides 5..11 x
// 0..11 toward each connected side); other blocks present none.

var wallSides = [4]int{dirNorth, dirEast, dirSouth, dirWest}

func wallSideOf(b world.Block, d int) string {
	v, _ := world.StateProperty(b, dirNameOf[d])
	return v
}

// wallFaceCovers reports whether the downward face of above covers the
// side test of side d (d < 0: the post test).
func wallFaceCovers(above world.Block, d int) bool {
	if sturdyFull(above, dirDown) {
		return true
	}
	if !hasClass(blockName(above), "WallBlock") {
		return false
	}
	up, _ := world.StateProperty(above, "up")
	if d < 0 {
		if up == "true" {
			return true
		}
		for _, s := range wallSides {
			if wallSideOf(above, s) != "none" {
				return true
			}
		}
		return false
	}
	return wallSideOf(above, d) != "none"
}

func wallConnectsTo(nb world.Block, sturdy bool, d int) bool {
	n := blockName(nb)
	gate := false
	if hasClass(n, "FenceGateBlock") {
		f, _ := world.StateProperty(nb, "facing")
		gate = dirAxisZ(dirNames[f]) == dirAxisZ(clockwise(d))
	}
	return blockInTag(nb, "walls") || !connectionException(nb) && sturdy || hasClass(n, "IronBarsBlock") || gate
}

func wallUpdate(st, above world.Block, conn [4]bool) world.Block {
	for i, d := range wallSides {
		v := "none"
		if conn[i] {
			v = "low"
			if wallFaceCovers(above, d) {
				v = "tall"
			}
		}
		st = withProp(st, dirNameOf[d], v)
	}
	raise := func() bool {
		if hasClass(blockName(above), "WallBlock") {
			if u, _ := world.StateProperty(above, "up"); u == "true" {
				return true
			}
		}
		n, s, e, w := wallSideOf(st, dirNorth), wallSideOf(st, dirSouth), wallSideOf(st, dirEast), wallSideOf(st, dirWest)
		nN, sN, eN, wN := n == "none", s == "none", e == "none", w == "none"
		if nN && sN && wN && eN || nN != sN || wN != eN {
			return true
		}
		if n == "tall" && s == "tall" || e == "tall" && w == "tall" {
			return false
		}
		return blockInTag(above, "wall_post_override") || wallFaceCovers(above, -1)
	}()
	return withProp(st, "up", boolName(raise))
}

func wallShape(rg *Region, st world.Block, p BlockPos, dir int, nb world.Block) world.Block {
	if dir == dirDown {
		return st
	}
	var conn [4]bool
	for i, d := range wallSides {
		conn[i] = wallSideOf(st, d) != "none"
	}
	if dir == dirUp {
		return wallUpdate(st, nb, conn)
	}
	for i, d := range wallSides {
		if d == dir {
			conn[i] = wallConnectsTo(nb, sturdyFull(nb, oppositeDir(dir)), oppositeDir(dir))
		}
	}
	return wallUpdate(st, rg.at(offset(p, 0, 1, 0)), conn)
}

// StairBlock.updateShape (getStairsShape / canTakeShape): a horizontal
// update recomputes the shape: outer toward a perpendicular same-half
// stair in front (unless the stair on its other side continues this
// one), else inner toward one behind, else straight.
func stairShape(rg *Region, st world.Block, p BlockPos, dir int, nb world.Block) world.Block {
	if dir == dirUp || dir == dirDown {
		return st
	}
	isStairs := func(b world.Block) bool { return hasClass(blockName(b), "StairBlock") }
	prop := func(b world.Block, k string) string { v, _ := world.StateProperty(b, k); return v }
	facing := dirNames[prop(st, "facing")]
	half := prop(st, "half")
	at := func(d int) world.Block { o := dirOffsets[d]; return rg.at(offset(p, o[0], o[1], o[2])) }
	canTake := func(d int) bool {
		n := at(d)
		return !isStairs(n) || dirNames[prop(n, "facing")] != facing || prop(n, "half") != half
	}
	shape := "straight"
	if front := at(facing); isStairs(front) && prop(front, "half") == half {
		ff := dirNames[prop(front, "facing")]
		if dirAxisZ(ff) != dirAxisZ(facing) && canTake(oppositeDir(ff)) {
			shape = "outer_right"
			if ff == ccw(facing) {
				shape = "outer_left"
			}
			return withProp(st, "shape", shape)
		}
	}
	if back := at(oppositeDir(facing)); isStairs(back) && prop(back, "half") == half {
		bf := dirNames[prop(back, "facing")]
		if dirAxisZ(bf) != dirAxisZ(facing) && canTake(bf) {
			shape = "inner_right"
			if bf == ccw(facing) {
				shape = "inner_left"
			}
		}
	}
	return withProp(st, "shape", shape)
}

// fenceShape / barsShape are the FenceBlock / IronBarsBlock updateShape
// of struct_postshape.go for the template shape pass.
func connectingShape(rg *Region, st world.Block, p BlockPos, dir int, nb world.Block) world.Block {
	out, _ := postShape(func(q BlockPos) world.Block { return rg.at(q) }, st, p, dir)
	return out
}

func init() {
	shapeByClass["WallBlock"] = wallShape
	shapeByClass["StairBlock"] = stairShape
	shapeByClass["FenceBlock"] = connectingShape
	shapeByClass["IronBarsBlock"] = connectingShape
}
