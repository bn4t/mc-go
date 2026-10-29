package server

import "math"

// blastItemEntities receives the pre-damage snapshot: loot and bundle children
// born during this explosion are not recursively damaged by the same blast.
func (s *Server) blastItemEntities(src *Entity, items []*Entity, x, y, z float64, radius float32, affectsBlocks bool) {
	if !affectsBlocks && !s.gameRuleBool("minecraft:mob_griefing") {
		return
	}
	d := s.dimensionByName(src.Dim)
	if d == nil {
		return
	}
	rangeR := float64(radius * 2)
	if rangeR <= 0 {
		return
	}
	cause := src.ID
	if src.arrowShooter != 0 {
		cause = src.arrowShooter
	}
	for _, e := range items {
		if _, ok := s.entities.GetPtr(e.ID); !ok {
			continue
		}
		dist := math.Sqrt((e.X-x)*(e.X-x)+(e.Y-y)*(e.Y-y)+(e.Z-z)*(e.Z-z)) / rangeR
		if dist > 1 {
			continue
		}
		seen := 0
		// Item's 0.25 cube gives a 2x2x2 exposure lattice. Entity's
		// getSeenPercent pads the X/Z lattice, while Y starts at the feet.
		for ix := 0; ix < 2; ix++ {
			for iy := 0; iy < 2; iy++ {
				for iz := 0; iz < 2; iz++ {
					px := e.X - .125 + float64(ix)/6 + 1.0/6
					py := e.Y + float64(iy)/6
					pz := e.Z - .125 + float64(iz)/6 + 1.0/6
					if _, _, _, _, _, _, _, hit := s.clipSolidSegment(d, px, py, pz, x, y, z); !hit {
						seen++
					}
				}
			}
		}
		impact := (1 - dist) * float64(float32(seen)/8)
		damage := float32((impact*impact+impact)/2*7*rangeR + 1)
		s.damageItemEntity(e, damage, "explosion", cause)
		if _, ok := s.entities.GetPtr(e.ID); !ok {
			continue
		}
		dx, dy, dz := e.X-x, e.Y+float64(float32(.2125))-y, e.Z-z
		length := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if length >= 1e-5 {
			e.MotionX += dx / length * impact
			e.MotionY += dy / length * impact
			e.MotionZ += dz / length * impact
		}
		e.NeedsSync = true
		// Resistant stacks still receive the normal additive impulse.
		body := encodeEntityMotion(e.ID, e.MotionX, e.MotionY, e.MotionZ)
		s.sessions.Range(func(_, v any) bool {
			p := v.(*Session)
			if p.state.Load() == StatePlay && p.dimName() == e.Dim {
				_ = p.write(p.ids().PlaySetEntityMotion, body)
			}
			return true
		})
	}
}
