package server

import "mc-go/internal/world"

// The extended base's occupied grid retains quarter planes along its
// facing axis. Splitting at those measured planes preserves contacts
// inside its merged outer box as well as contacts at the outer faces.
func pistonBaseCollisionCells(state world.Block, pos world.Pos) []cellBox {
	boxes := measuredCollisionShapeAt(state, pos)
	facing, _ := world.StateProperty(state, "facing")
	axis := 0
	if facing == "up" || facing == "down" {
		axis = 1
	}
	if facing == "north" || facing == "south" {
		axis = 2
	}
	var cells []cellBox
	for _, box := range boxes {
		for lo := box[axis]; lo < box[axis+3]; lo += .25 {
			cell := box
			cell[axis], cell[axis+3] = lo, lo+.25
			if cell[axis+3] > box[axis+3] {
				cell[axis+3] = box[axis+3]
			}
			cells = append(cells, cell)
		}
	}
	return cells
}

// A piston-induced move suppresses carried shapes moving in that same
// direction while retaining a retracting source's stationary base.
// The context direction stays constant during the reverse escape move.
func (s *Server) pistonMoveShapeAt(d *dimension, state world.Block, pos world.Pos, direction rsDir) []cellBox {
	if isBlockNamed(state, "moving_piston") {
		for _, mp := range s.movingPistons {
			if mp.dim != d.name || mp.pos != pos {
				continue
			}
			if mp.dir == direction && movingPistonProgress(mp) < 1 {
				if mp.source && !mp.extend && rsIsPiston(mp.state) {
					base, _ := world.WithProperty(mp.state, "extended", "true")
					return pistonBaseCollisionCells(base, pos)
				}
				return nil
			}
			break
		}
	}
	return s.worldCollisionShapeAt(d, state, pos)
}

func (s *Server) clipPistonBodyMove(d *dimension, body cellBox, axis int, delta float64, direction rsDir) float64 {
	return s.clipBlockShapeMove(d, body, axis, delta, func(state world.Block, pos world.Pos) []cellBox {
		return s.pistonMoveShapeAt(d, state, pos, direction)
	})
}
