package server

// NoGravity armor stands have NORMAL piston reaction but noPhysics motion.
// They expose source-head/backoff geometry without block clipping or travel.
// Other entity move policies are measured separately before using this route.
func (s *Server) pistonSourceBaseStandPush(d *dimension, mp movingPiston, step float64) {
	if !mp.source || mp.extend || !rsIsPiston(mp.state) {
		return
	}
	axis, sign := pistonMotionAxis(mp.dir)
	boxes := pistonSourceBaseCarriedBoxes(mp, movingPistonProgress(mp))
	base := cellBox{float64(mp.pos.X), float64(mp.pos.Y), float64(mp.pos.Z), float64(mp.pos.X) + 1, float64(mp.pos.Y) + 1, float64(mp.pos.Z) + 1}
	for _, e := range s.entities.All() {
		if e.Type != mustEntityType("armor_stand") || !e.NoGravity || e.Dim != d.name || e.Dying || e.presenceOnly || entityIgnoresPistonPush(&e) {
			continue
		}
		dims := entityBBDims(&e)
		half := dims[0] / 2
		body := cellBox{e.X - half, e.Y, e.Z - half, e.X + half, e.Y + dims[1], e.Z + half}
		contact := pistonForwardContact(body, boxes, axis, sign, step)
		if contact <= 0 {
			continue
		}
		delta := sign * (contact + .01)
		body[axis] += delta
		body[axis+3] += delta
		delta += pistonBaseBackoff(body, base, axis, sign, step)
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
