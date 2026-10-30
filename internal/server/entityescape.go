package server

import (
	"math"
	"math/rand"
	"mc-go/internal/world"
)

// Closest-space selection and its velocity update are shared with the
// existing orb path; items invoke it after fluid/gravity acceleration.
func setEmbeddedEntityEscapeVelocity(w *world.World, e *Entity, heightBox float64) {
	setEmbeddedEscapeVelocity(e, heightBox, func(pos world.Pos) bool {
		b := w.GetBlock(pos)
		return b == world.Air || world.NonSolid(b)
	})
}

func setEmbeddedEscapeVelocity(e *Entity, heightBox float64, eligible func(world.Pos) bool) {
	centerY := e.Y + heightBox/2
	feet := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(centerY)), Z: int(math.Floor(e.Z))}
	fx := e.X - float64(feet.X)
	fy := centerY - float64(feet.Y)
	fz := e.Z - float64(feet.Z)
	best := math.MaxFloat64
	axis, sign := 'y', 1.0
	for _, d := range [5][4]int{{0, 0, -1, 'z'}, {0, 0, 1, 'z'}, {-1, 0, 0, 'x'}, {1, 0, 0, 'x'}, {0, 1, 0, 'y'}} {
		nb := world.Pos{X: feet.X + d[0], Y: feet.Y + d[1], Z: feet.Z + d[2]}
		if !eligible(nb) {
			continue
		}
		var dist float64
		switch d[3] {
		case 'x':
			dist = fx
		case 'y':
			dist = fy
		default:
			dist = fz
		}
		s := 1.0
		if d[0]+d[1]+d[2] < 0 {
			s = -1.0
		} else {
			dist = 1.0 - dist
		}
		if dist < best {
			best = dist
			axis = rune(d[3])
			sign = s
		}
	}
	push := 0.1 + rand.Float64()*0.2
	e.MotionX *= 0.75
	e.MotionY *= 0.75
	e.MotionZ *= 0.75
	switch axis {
	case 'x':
		e.MotionX = push * sign
	case 'y':
		e.MotionY = push * sign
	default:
		e.MotionZ = push * sign
	}
}
