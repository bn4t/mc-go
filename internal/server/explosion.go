package server

import (
	"math"

	"mc-go/internal/world"
)

// windChargeTickStep moves wind_charge entities each tick: no gravity
// (AbstractHurtingProjectile), explode on block or player contact.
func (s *Server) windChargeTickStep() {
	wc := mustEntityType("wind_charge")
	s.entities.mu.RLock()
	var charges []*Entity
	for _, e := range s.entities.byID {
		if e.Type == wc && !e.Dying {
			charges = append(charges, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range charges {
		nx, ny, nz := e.X+e.MotionX, e.Y+e.MotionY, e.Z+e.MotionZ
		w := s.dimWorld(e.Dim)
		if w != nil && e.MotionY < 0 {
			if b := w.GetBlock(world.Pos{X: int(math.Floor(nx)), Y: int(math.Floor(ny)), Z: int(math.Floor(nz))}); b != 0 && !world.NonSolid(b) {
				s.windBurst(e, nx, ny, nz)
				continue
			}
		}
		if s.windChargeHitsPlayer(e, nx, ny, nz) {
			s.windBurst(e, nx, ny, nz)
			continue
		}
		s.entities.Update(e.ID, nx, ny, nz, e.Yaw, e.Pitch, false)
	}
}

// windChargeHitsPlayer reports whether the charge's next position
// intersects a player body (vanilla's entity-target ray hits the 0.6
// x 1.8 box).
func (s *Server) windChargeHitsPlayer(e *Entity, x, y, z float64) bool {
	hit := false
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay || sess.dimName() != e.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if math.Abs(x-px) < 0.9 && math.Abs(z-pz) < 0.9 && y > py-0.1 && y < py+1.9 {
			hit = true
			return false
		}
		return true
	})
	return hit
}

// windBurst explodes a wind charge at the contact point: entities in
// the 2*radius box get directional knockback. For players the impulse
// is (1 - dist/(2r)) * 1.22 along eyePos->center normalized, they get
// their own entity-motion update, and the impact position + cause are
// latched for fall_after_explosion.
func (s *Server) windBurst(src *Entity, x, y, z float64) {
	// The player-thrown charge bursts at 1.2 power; the breeze's is 3.0.
	radius := 1.2
	if src.Type == mustEntityType("breeze_wind_charge") {
		radius = 3.0
	}
	s.entities.Remove(src.ID)
	s.broadcastEntityRemoveIn(src.Dim, nil, []int32{src.ID})

	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay || sess.dimName() != src.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		dist := math.Sqrt((px-x)*(px-x)+(py-y)*(py-y)+(pz-z)*(pz-z)) / (2 * radius)
		if dist > 1 {
			return true
		}
		dx, dy, dz := px-x, py+1.62-y, pz-z
		n := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if n == 0 {
			dy = 1
			n = 1
		}
		// Flat open world means getSeenPercent is always 1; damage is
		// zero (SimpleExplosionDamageCalculator damagesEntities=false)
		// so only knockback applies.
		scale := (1 - dist) * 1.22
		vx, vy, vz := dx/n*scale, dy/n*scale, dz/n*scale
		sess.explodeImpact = &[3]float64{px, py, pz}
		sess.explodeCause = mustEntityType("wind_charge")
		if eid := sess.entityID.Load(); eid != 0 {
			s.entities.SetMotion(eid, vx, vy, vz)
			_ = sess.write(sess.ids().PlaySetEntityMotion, encodeEntityMotion(eid, vx, vy, vz))
		}
		return true
	})
}
