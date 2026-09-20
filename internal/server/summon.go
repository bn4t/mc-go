package server

import (
	"mc-go/internal/world"
)

// checkSummonedEntity runs on every block write: a carved pumpkin on
// an iron-block T summons an iron golem (vanilla's BlockPattern
// ~^~/###/~#~ — the cells beside the pumpkin and beside the base must
// be air). The five pattern blocks are removed and the golem spawns
// where the center column stood; summoned_entity fires for every
// player in the dimension, placer or not.
func (s *Server) checkSummonedEntity(d *dimension, pos world.Pos, blk world.Block) {
	bname, ok := world.StateOwner(blk)
	if !ok || bname != "carved_pumpkin" {
		return
	}
	isIron := func(p world.Pos) bool {
		n, ok := world.StateOwner(d.w.GetBlock(p))
		return ok && n == "iron_block"
	}
	isAir := func(p world.Pos) bool {
		n, ok := world.StateOwner(d.w.GetBlock(p))
		return !ok || n == "air" || n == "cave_air" || n == "void_air"
	}
	for _, axis := range [2][2]int{{1, 0}, {0, 1}} {
		cx, cz := axis[0], axis[1]
		mid := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
		base := world.Pos{X: pos.X, Y: pos.Y - 2, Z: pos.Z}
		armA := world.Pos{X: pos.X + cx, Y: pos.Y - 1, Z: pos.Z + cz}
		armB := world.Pos{X: pos.X - cx, Y: pos.Y - 1, Z: pos.Z - cz}
		airA := world.Pos{X: pos.X + cx, Y: pos.Y, Z: pos.Z + cz}
		airB := world.Pos{X: pos.X - cx, Y: pos.Y, Z: pos.Z - cz}
		airC := world.Pos{X: pos.X + cx, Y: pos.Y - 2, Z: pos.Z + cz}
		airD := world.Pos{X: pos.X - cx, Y: pos.Y - 2, Z: pos.Z - cz}
		if !(isIron(mid) && isIron(base) && isIron(armA) && isIron(armB) &&
			isAir(airA) && isAir(airB) && isAir(airC) && isAir(airD)) {
			continue
		}
		for _, p := range []world.Pos{pos, mid, base, armA, armB} {
			s.broadcastBlockUpdateIn(d.name, p, world.Air)
		}
		e := s.entities.Spawn(&Entity{
			Type:   mustEntityType("iron_golem"),
			Dim:    d.name,
			X:      float64(mid.X) + 0.5,
			Y:      float64(base.Y),
			Z:      float64(mid.Z) + 0.5,
			Health: mobHealth["iron_golem"],
		})
		s.broadcastEntitySpawn(nil, *e)
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() == StatePlay && sess.dimName() == d.name {
				s.fireSummonedEntity(sess, "iron_golem")
			}
			return true
		})
		return
	}
}
