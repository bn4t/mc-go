package server

import (
	"math"
	"mc-go/internal/world"
)

// Each carried box sweeps its forward face. Items move just beyond that
// face, capped by this half-step, rather than by a whole-cell overlap.
func (s *Server) pistonPushItems(d *dimension, mp movingPiston, step float64) {
	axis, sign := pistonMotionAxis(mp.dir)
	boxes := pistonPositionCarriedBoxes(mp, measuredCollisionShapeAt(mp.state, mp.pos), movingPistonProgress(mp))
	for _, e := range s.entities.All() {
		if e.Type != mustEntityType("item") || e.Dim != d.name || e.Dying || e.presenceOnly {
			continue
		}
		dims := entityBBDims(&e)
		half := dims[0] / 2
		body := cellBox{e.X - half, e.Y, e.Z - half, e.X + half, e.Y + dims[1], e.Z + half}
		amount := pistonForwardContact(body, boxes, axis, sign, step)
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
