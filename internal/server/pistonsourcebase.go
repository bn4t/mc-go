package server

// Source-base movement is pinned for armor stands and cows. NoGravity
// stands bypass clipping; ordinary bodies retain the piston move context.
// Other entity move policies are measured separately before using this route.
func (s *Server) pistonSourceBaseBodyPush(d *dimension, mp movingPiston, step float64) {
	if !mp.source || mp.extend || !rsIsPiston(mp.state) {
		return
	}
	axis, sign := pistonMotionAxis(mp.dir)
	boxes := pistonSourceBaseCarriedBoxes(mp, movingPistonProgress(mp))
	base := cellBox{float64(mp.pos.X), float64(mp.pos.Y), float64(mp.pos.Z), float64(mp.pos.X) + 1, float64(mp.pos.Y) + 1, float64(mp.pos.Z) + 1}
	for _, e := range s.entities.All() {
		if (e.Type != mustEntityType("armor_stand") && e.Type != mustEntityType("cow")) || e.Dim != d.name || e.Dying || e.presenceOnly || entityIgnoresPistonPush(&e) {
			continue
		}
		dims := entityBBDims(&e)
		// Native dimensions are floats before widening into the AABB.
		dims[0], dims[1] = float64(float32(dims[0])), float64(float32(dims[1]))
		half := dims[0] / 2
		body := cellBox{e.X - half, e.Y, e.Z - half, e.X + half, e.Y + dims[1], e.Z + half}
		contact := pistonForwardContact(body, boxes, axis, sign, step)
		if contact <= 0 {
			continue
		}
		delta := sign * (contact + .01)
		noPhysics := e.Type == mustEntityType("armor_stand") && e.NoGravity
		if !noPhysics {
			delta = s.clipPistonBodyMove(d, body, axis, delta, mp.dir)
		}
		body[axis] += delta
		body[axis+3] += delta
		backoff := pistonBaseBackoff(body, base, axis, sign, step)
		if !noPhysics {
			backoff = s.clipPistonBodyMove(d, body, axis, backoff, mp.dir)
		}
		delta += backoff
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
