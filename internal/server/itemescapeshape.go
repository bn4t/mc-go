package server

import (
	"math"
	"mc-go/internal/world"
	"sort"
)

// Full-cube equality uses occupied volume, not just the outer bounds or
// six faces. Tiny planes merge at the native shape epsilon.
func collisionShapeIsFullCube(boxes []cellBox) bool {
	const epsilon = 1e-7
	if len(boxes) == 0 {
		return false
	}
	if len(boxes) == 1 {
		b := boxes[0]
		for axis := 0; axis < 3; axis++ {
			if math.Abs(b[axis]) > epsilon || math.Abs(b[axis+3]-1) > epsilon {
				return false
			}
		}
		return true
	}
	cuts := [3][]float64{{0, 1}, {0, 1}, {0, 1}}
	normalized := make([]cellBox, 0, len(boxes))
	for _, b := range boxes {
		empty := false
		for axis := 0; axis < 3; axis++ {
			if b[axis+3]-b[axis] <= epsilon {
				empty = true
			}
		}
		if empty {
			continue
		}
		for axis := 0; axis < 3; axis++ {
			if b[axis] < -epsilon || b[axis+3] > 1+epsilon {
				return false
			}
			if math.Abs(b[axis]) <= epsilon {
				b[axis] = 0
			}
			if math.Abs(b[axis+3]-1) <= epsilon {
				b[axis+3] = 1
			}
			cuts[axis] = append(cuts[axis], b[axis], b[axis+3])
		}
		normalized = append(normalized, b)
	}
	if len(normalized) == 0 {
		return false
	}
	for axis := range cuts {
		sort.Float64s(cuts[axis])
		merged := cuts[axis][:0]
		for _, v := range cuts[axis] {
			if len(merged) == 0 || v-merged[len(merged)-1] > epsilon {
				merged = append(merged, v)
			}
		}
		cuts[axis] = merged
	}
	for ix := 0; ix+1 < len(cuts[0]); ix++ {
		for iy := 0; iy+1 < len(cuts[1]); iy++ {
			for iz := 0; iz+1 < len(cuts[2]); iz++ {
				p := [3]float64{(cuts[0][ix] + cuts[0][ix+1]) / 2, (cuts[1][iy] + cuts[1][iy+1]) / 2, (cuts[2][iz] + cuts[2][iz+1]) / 2}
				covered := false
				for _, b := range normalized {
					if p[0] >= b[0] && p[0] <= b[3] && p[1] >= b[1] && p[1] <= b[4] && p[2] >= b[2] && p[2] <= b[5] {
						covered = true
						break
					}
				}
				if !covered {
					return false
				}
			}
		}
	}
	return true
}

func (s *Server) setEmbeddedWorldEscapeVelocity(d *dimension, e *Entity, height float64) {
	setEmbeddedEscapeVelocity(e, height, func(pos world.Pos) bool {
		return !collisionShapeIsFullCube(s.worldCollisionShapeAt(d, d.w.GetBlock(pos), pos))
	})
}
