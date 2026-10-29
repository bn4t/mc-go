package server

import (
	"math"
	"sort"

	"mc-go/internal/world"
)

// fluidCanPassThroughWall tests the empty-context collision shapes at a shared
// cell face. FlowingFluid permits passage unless their opposing face slices
// together cover the complete unit face (Shapes.mergedFaceOccludes).
func (s *Server) fluidCanPassThroughWall(d *dimension, from, to world.Pos) bool {
	axis := -1
	positive := false
	for i, step := range [3]int{to.X - from.X, to.Y - from.Y, to.Z - from.Z} {
		if step == 0 {
			continue
		}
		if axis != -1 || (step != 1 && step != -1) {
			return false
		}
		axis, positive = i, step > 0
	}
	if axis < 0 {
		return false
	}
	a := fluidCollisionShapeAt(d.w.GetBlock(from), from)
	b := fluidCollisionShapeAt(d.w.GetBlock(to), to)
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	faces := fluidFaceRectangles(a, axis, positive)
	faces = append(faces, fluidFaceRectangles(b, axis, !positive)...)
	return !fluidFaceCovered(faces)
}

// FlowingFluid queries collision shapes with an empty entity context.
func fluidCollisionShapeAt(block world.Block, pos world.Pos) []cellBox {
	return measuredCollisionShapeAt(block, pos)
}

type fluidFaceRectangle struct{ minU, minV, maxU, maxV float64 }

const fluidFaceEpsilon = 1e-7

// A shape contributes only when its outermost boundary reaches the shared
// plane. Shapes extending beyond that plane do not supply a boundary slice.
func fluidFaceRectangles(boxes []cellBox, axis int, positive bool) []fluidFaceRectangle {
	if len(boxes) == 0 {
		return nil
	}
	extreme := math.Inf(1)
	edge, want := axis, 0.0
	if positive {
		extreme = math.Inf(-1)
		edge = axis + 3
		want = 1
	}
	for _, box := range boxes {
		if positive {
			extreme = math.Max(extreme, box[edge])
		} else {
			extreme = math.Min(extreme, box[edge])
		}
	}
	if math.Abs(extreme-want) > fluidFaceEpsilon {
		return nil
	}
	u, v := (axis+1)%3, (axis+2)%3
	var out []fluidFaceRectangle
	for _, box := range boxes {
		if math.Abs(box[edge]-extreme) > fluidFaceEpsilon {
			continue
		}
		r := fluidFaceRectangle{math.Max(0, box[u]), math.Max(0, box[v]), math.Min(1, box[u+3]), math.Min(1, box[v+3])}
		if r.maxU-r.minU > fluidFaceEpsilon && r.maxV-r.minV > fluidFaceEpsilon {
			out = append(out, r)
		}
	}
	return out
}

// Sweep strips between rectangle edges; within each strip the covered
// vertical intervals are constant. This preserves gaps and complementary
// shapes without a voxel-resolution approximation.
func fluidFaceCovered(rects []fluidFaceRectangle) bool {
	if len(rects) == 0 {
		return false
	}
	cuts := []float64{0, 1}
	for _, r := range rects {
		cuts = append(cuts, r.minU, r.maxU)
	}
	sort.Float64s(cuts)
	intervals := make([][2]float64, 0, len(rects))
	for i := 1; i < len(cuts); i++ {
		if cuts[i]-cuts[i-1] <= fluidFaceEpsilon {
			continue
		}
		u := (cuts[i] + cuts[i-1]) / 2
		intervals = intervals[:0]
		for _, r := range rects {
			if r.minU <= u && u <= r.maxU {
				intervals = append(intervals, [2]float64{r.minV, r.maxV})
			}
		}
		sort.Slice(intervals, func(i, j int) bool { return intervals[i][0] < intervals[j][0] })
		reached := 0.0
		for _, interval := range intervals {
			if interval[0] > reached+fluidFaceEpsilon {
				return false
			}
			reached = math.Max(reached, interval[1])
		}
		if reached < 1-fluidFaceEpsilon {
			return false
		}
	}
	return true
}
