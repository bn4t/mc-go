package server

import (
	"math"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// stateShapes is one state's row of tools/genshapes facts.
type stateShapes struct {
	outline, interact uint16
	flags             uint8
}

const (
	shapeReplaceWater = 1 << iota // canBeReplaced(Fluids.WATER)
	shapeReplaceLava              // canBeReplaced(Fluids.LAVA)
	shapePlaceWater               // LiquidBlockContainer.canPlaceLiquid(water)
	shapePlaceLava                // ... (lava)
)

// blockOffset is a block's OffsetType (1 XZ, 2 XYZ) and maxima.
type blockOffset struct {
	kind       uint8
	maxH, maxV float64
}

// bucketBlock names the classes declaring a block's BucketPickup.pickupBlock
// and LiquidBlockContainer.placeLiquid, and its pickup sound.
type bucketBlock struct {
	pickup, place, sound string
}

func stateShapeRow(st world.Block) stateShapes {
	if int(st) < 0 || int(st) >= len(vanillaStateShapes) {
		return stateShapes{outline: 1}
	}
	return vanillaStateShapes[st]
}

// blockOffsetAt is BlockStateBase.getOffset: Mth.getSeed(x, 0, z)
// nibbles, x and z mapped to ±0.25 then clamped to the block's
// horizontal maximum, y (XYZ only) to [-maxV, 0].
func blockOffsetAt(st world.Block, p world.Pos) (float64, float64, float64) {
	o, ok := vanillaBlockOffsets[bstate(st)]
	if !ok {
		return 0, 0, 0
	}
	seed := worldgen.BlockSeed(int32(p.X), 0, int32(p.Z))
	clamp := func(v float64) float64 { return math.Max(-o.maxH, math.Min(o.maxH, v)) }
	x := clamp((float64(float32(seed&15)/15) - 0.5) * 0.5)
	z := clamp((float64(float32(seed>>8&15)/15) - 0.5) * 0.5)
	y := 0.0
	if o.kind == 2 {
		y = (float64(float32(seed>>4&15)/15) - 1) * o.maxV
	}
	return x, y, z
}

// outlineBoxes is getShape(level, pos, context) for a context holding
// neither light nor scaffolding: the state's outline boxes moved by its
// random offset.
func outlineBoxes(st world.Block, p world.Pos) []cellBox {
	boxes := vanillaShapes[stateShapeRow(st).outline]
	ox, oy, oz := blockOffsetAt(st, p)
	if ox == 0 && oy == 0 && oz == 0 {
		return boxes
	}
	out := make([]cellBox, len(boxes))
	for i, b := range boxes {
		out[i] = cellBox{b[0] + ox, b[1] + oy, b[2] + oz, b[3] + ox, b[4] + oy, b[5] + oz}
	}
	return out
}

// blockHit is a BlockHitResult: the hit block, face and location;
// miss marks HitResult.Type.MISS, inside a hit from within the shape.
type blockHit struct {
	pos    world.Pos
	face   pdir
	loc    [3]float64
	inside bool
	miss   bool
}

// clipShape is VoxelShape.clip(from, to, pos): nil for an empty shape or
// a segment shorter than sqrt(1e-7); a start a thousandth of the way in
// that sits inside the shape hits there, inside, on the face against
// the travel; otherwise the nearest AABB face crossing.
func clipShape(boxes []cellBox, from, to [3]float64, p world.Pos) (blockHit, bool) {
	if len(boxes) == 0 {
		return blockHit{}, false
	}
	d := [3]float64{to[0] - from[0], to[1] - from[1], to[2] - from[2]}
	if d[0]*d[0]+d[1]*d[1]+d[2]*d[2] < 1.0e-7 {
		return blockHit{}, false
	}
	in := [3]float64{from[0] + d[0]*0.001, from[1] + d[1]*0.001, from[2] + d[2]*0.001}
	lx, ly, lz := in[0]-float64(p.X), in[1]-float64(p.Y), in[2]-float64(p.Z)
	for _, b := range boxes {
		if lx >= b[0] && lx < b[3] && ly >= b[1] && ly < b[4] && lz >= b[2] && lz < b[5] {
			return blockHit{pos: p, face: approxNearestDir(d[0], d[1], d[2]).opp(), loc: in, inside: true}, true
		}
	}
	minT := 1.0
	face := pdir(-1)
	for _, b := range boxes {
		mn := [3]float64{b[0] + float64(p.X), b[1] + float64(p.Y), b[2] + float64(p.Z)}
		mx := [3]float64{b[3] + float64(p.X), b[4] + float64(p.Y), b[5] + float64(p.Z)}
		// Each axis tries the face the ray enters through; the other two
		// coordinates must land within the face, widened by 1e-7.
		for a, dirs := range [3][2]pdir{{pWest, pEast}, {pDown, pUp}, {pNorth, pSouth}} {
			var plane float64
			var dir pdir
			switch {
			case d[a] > 1.0e-7:
				plane, dir = mn[a], dirs[0]
			case d[a] < -1.0e-7:
				plane, dir = mx[a], dirs[1]
			default:
				continue
			}
			t := (plane - from[a]) / d[a]
			if !(t > 0 && t < minT) {
				continue
			}
			ok := true
			for _, o := range [2]int{(a + 1) % 3, (a + 2) % 3} {
				v := from[o] + t*d[o]
				if !(mn[o]-1.0e-7 < v && v < mx[o]+1.0e-7) {
					ok = false
				}
			}
			if ok {
				minT, face = t, dir
			}
		}
	}
	if face < 0 {
		return blockHit{}, false
	}
	return blockHit{pos: p, face: face, loc: [3]float64{from[0] + minT*d[0], from[1] + minT*d[1], from[2] + minT*d[2]}}, true
}

// approxNearestDir is Direction.getApproximateNearest: the direction
// whose normal has the largest float dot product with v, above the
// smallest positive float (first wins ties, in DOWN, UP, NORTH, SOUTH,
// WEST, EAST order); NORTH when none is.
func approxNearestDir(x, y, z float64) pdir {
	best, bestDot := pNorth, float32(math.SmallestNonzeroFloat32)
	fx, fy, fz := float32(x), float32(y), float32(z)
	for _, dir := range [6]pdir{pDown, pUp, pNorth, pSouth, pWest, pEast} {
		sx, sy, sz := dir.step()
		if dot := fx*float32(sx) + fy*float32(sy) + fz*float32(sz); dot > bestDot {
			best, bestDot = dir, dot
		}
	}
	return best
}

// clipFluid is ClipContext.Fluid.
type clipFluid int

// srcBlocks is SoundSource.BLOCKS.
const srcBlocks = 4

const (
	clipFluidNone clipFluid = iota
	clipFluidSourceOnly
	clipFluidAny
	clipFluidWater
)

// fluidClipBoxes is ClipContext.getFluidShape: the fluid's box when
// the mode picks it. FlowingFluid.getShape caches one box per fluid
// state, its height the first query's getHeight; mc-go takes the
// state's own height (amount/9), which a lone source reads.
func fluidClipBoxes(st world.Block, mode clipFluid) []cellBox {
	k, lv, ok := fluidOf(st)
	if !ok || mode == clipFluidNone {
		return nil
	}
	switch mode {
	case clipFluidSourceOnly:
		if lv != 0 {
			return nil
		}
	case clipFluidWater:
		if k != fluidWater {
			return nil
		}
	}
	return []cellBox{{0, 0, 0, 1, fluidOwnHeight(lv), 1}}
}

// levelClip is BlockGetter.clip with ClipContext.Block.OUTLINE: the
// voxel walk of traverseBlocks (both ends pushed 1e-7 outward), each
// cell's outline clip refined by the interaction shape's face
// (clipWithInteractionOverride) against its fluid clip, the nearer
// winning a tie for the block; no hit is a miss at the end point.
func (s *Server) levelClip(d *dimension, from, to [3]float64, fluid clipFluid) blockHit {
	at := func(p world.Pos) (blockHit, bool) {
		st := d.w.GetBlock(p)
		var bh, fh blockHit
		bok, fok := false, false
		if bh, bok = clipShape(outlineBoxes(st, p), from, to, p); bok {
			if ih, iok := clipShape(vanillaShapes[stateShapeRow(st).interact], from, to, p); iok && dist2(ih.loc, from) < dist2(bh.loc, from) {
				bh.face = ih.face
			}
		}
		fh, fok = clipShape(fluidClipBoxes(st, fluid), from, to, p)
		switch {
		case bok && (!fok || dist2(bh.loc, from) <= dist2(fh.loc, from)):
			return bh, true
		case fok:
			return fh, true
		}
		return blockHit{}, false
	}
	miss := func() blockHit {
		p := world.Pos{X: int(math.Floor(to[0])), Y: int(math.Floor(to[1])), Z: int(math.Floor(to[2]))}
		return blockHit{pos: p, face: approxNearestDir(from[0]-to[0], from[1]-to[1], from[2]-to[2]), loc: to, miss: true}
	}
	if from == to {
		return miss()
	}
	lerp := func(t, a, b float64) float64 { return a + t*(b-a) }
	var e, o [3]float64
	for i := 0; i < 3; i++ {
		e[i] = lerp(-1.0e-7, to[i], from[i])
		o[i] = lerp(-1.0e-7, from[i], to[i])
	}
	c := [3]int{int(math.Floor(o[0])), int(math.Floor(o[1])), int(math.Floor(o[2]))}
	if h, ok := at(world.Pos{X: c[0], Y: c[1], Z: c[2]}); ok {
		return h
	}
	var step [3]int
	var tDelta, tMax [3]float64
	for i := 0; i < 3; i++ {
		dd := e[i] - o[i]
		switch {
		case dd > 0:
			step[i] = 1
		case dd < 0:
			step[i] = -1
		}
		if step[i] == 0 {
			tDelta[i] = math.MaxFloat64
		} else {
			tDelta[i] = float64(step[i]) / dd
		}
		frac := o[i] - math.Floor(o[i])
		if step[i] > 0 {
			tMax[i] = tDelta[i] * (1 - frac)
		} else {
			tMax[i] = tDelta[i] * frac
		}
	}
	for tMax[0] <= 1 || tMax[1] <= 1 || tMax[2] <= 1 {
		a := 2
		if tMax[0] < tMax[1] {
			if tMax[0] < tMax[2] {
				a = 0
			}
		} else if tMax[1] < tMax[2] {
			a = 1
		}
		c[a] += step[a]
		tMax[a] += tDelta[a]
		if h, ok := at(world.Pos{X: c[0], Y: c[1], Z: c[2]}); ok {
			return h
		}
	}
	return miss()
}

func dist2(a, b [3]float64) float64 {
	x, y, z := a[0]-b[0], a[1]-b[1], a[2]-b[2]
	return x*x + y*y + z*z
}

// viewVector is Entity.calculateViewVector(xRot, yRot) over Mth's
// sine table.
func viewVector(pitch, yaw float32) [3]float64 {
	v := dragonViewVector(pitch, yaw)
	return [3]float64{v.x, v.y, v.z}
}

// playerPOVHit is Item.getPlayerPOVHitResult: from the eye along the
// view vector for the block interaction range (4.5, creative 5.0),
// OUTLINE shapes and the given fluid mode. The rotation is the use
// packet's, which handleUseItem snaps the player to first.
func (s *Server) playerPOVHit(sess *Session, d *dimension, fluid clipFluid, yaw, pitch float32) blockHit {
	x, y, z, _, _, _ := sess.Pos()
	eye := 1.62
	if sess.sneaking {
		eye = 1.27
	}
	from := [3]float64{x, y + eye, z}
	reach := reachSurvival
	if sess.mode() == GameModeCreative {
		reach += 0.5
	}
	v := viewVector(pitch, yaw)
	to := [3]float64{from[0] + v[0]*reach, from[1] + v[1]*reach, from[2] + v[2]*reach}
	return s.levelClip(d, from, to, fluid)
}
