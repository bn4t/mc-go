package server

import (
	"math"
	"mc-go/internal/world"
)

// Each carried box sweeps its forward face. Items move just beyond that
// face, capped by this half-step, rather than by a whole-cell overlap.
func (s *Server) pistonPushItems(d *dimension, mp movingPiston, step float64) {
	axis, sign := 0, 1.0
	off := rsOff[mp.dir]
	if off.Y != 0 {
		axis = 1
	} else if off.Z != 0 {
		axis = 2
	}
	if []int{off.X, off.Y, off.Z}[axis] < 0 {
		sign = -1
	}
	old := movingPistonProgress(mp)
	shapes := measuredCollisionShapeAt(mp.state, mp.pos)
	shift := [3]float64{float64(mp.pos.X) + float64(float32(off.X)*(old-1)), float64(mp.pos.Y) + float64(float32(off.Y)*(old-1)), float64(mp.pos.Z) + float64(float32(off.Z)*(old-1))}
	for _, e := range s.entities.All() {
		if e.Type != mustEntityType("item") || e.Dim != d.name || e.Dying || e.presenceOnly {
			continue
		}
		dims := entityBBDims(&e)
		half := dims[0] / 2
		body := cellBox{e.X - half, e.Y, e.Z - half, e.X + half, e.Y + dims[1], e.Z + half}
		amount := 0.0
		for _, b := range shapes {
			for i := 0; i < 3; i++ {
				b[i] += shift[i]
				b[i+3] += shift[i]
			}
			if sign > 0 {
				b[axis] = b[axis+3]
				b[axis+3] += step
			} else {
				b[axis+3] = b[axis]
				b[axis] -= step
			}
			if !(body[0] < b[3] && body[3] > b[0] && body[1] < b[4] && body[4] > b[1] && body[2] < b[5] && body[5] > b[2]) {
				continue
			}
			penetration := b[axis+3] - body[axis]
			if sign < 0 {
				penetration = body[axis+3] - b[axis]
			}
			amount = math.Max(amount, math.Min(step, penetration))
		}
		if amount <= 0 {
			continue
		}
		delta := s.clipShulkerItemMove(d, body, axis, sign*(amount+.01))
		s.entities.mu.Lock()
		if live := s.entities.byID[e.ID]; live != nil && !live.presenceOnly {
			switch axis {
			case 0:
				live.X += delta
			case 1:
				live.Y += delta
			case 2:
				live.Z += delta
			}
			live.PosDirty = live.PosDirty || delta != 0
		}
		s.entities.mu.Unlock()
	}
}

// ItemEntity's noCollision gate uses positioned collision boxes, including
// live moving blocks. Merely touching a face is not being inside it.
func (s *Server) itemInsideWorldCollision(d *dimension, x, y, z, half, height float64) bool {
	if d == nil {
		return false
	}
	box := cellBox{x - half + 1e-7, y + 1e-7, z - half + 1e-7, x + half - 1e-7, y + height - 1e-7, z + half - 1e-7}
	for cx := int(math.Floor(box[0])) - 1; cx <= int(math.Floor(box[3]))+1; cx++ {
		for cy := int(math.Floor(box[1])) - 1; cy <= int(math.Floor(box[4]))+1; cy++ {
			for cz := int(math.Floor(box[2])) - 1; cz <= int(math.Floor(box[5]))+1; cz++ {
				p := world.Pos{X: cx, Y: cy, Z: cz}
				for _, b := range s.worldCollisionShapeAt(d, d.w.GetBlock(p), p) {
					if box[0] < float64(cx)+b[3] && box[3] > float64(cx)+b[0] && box[1] < float64(cy)+b[4] && box[4] > float64(cy)+b[1] && box[2] < float64(cz)+b[5] && box[5] > float64(cz)+b[2] {
						return true
					}
				}
			}
		}
	}
	return false
}
