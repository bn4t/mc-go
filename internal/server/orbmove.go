package server

import "mc-go/internal/world"

// moveExperienceOrb preserves the common collision/restitution effects. The
// orb's later floor response uses the requested Y velocity, while damping
// uses the velocity left by those effects and the native float coefficients.
func (s *Server) moveExperienceOrb(e *Entity, w *world.World) {
	incomingY := e.MotionY
	drag := float32(.98)
	s.entityMove(e, w, .25, .5, e.MotionX, e.MotionY, e.MotionZ, .03, float64(drag), false)
	friction := drag
	if e.Grounded {
		friction *= float32(entityFrictionBelow(w, e.X, e.Y, e.Z))
	}
	scale := float64(friction)
	e.MotionX *= scale
	e.MotionY *= scale
	e.MotionZ *= scale
	gravity := .03
	if e.NoGravity {
		gravity = 0
	}
	if e.Grounded && incomingY < -gravity {
		e.MotionY = -incomingY * .4
	}
}
