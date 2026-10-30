package server

import "math/rand"

// Player.aiStep chooses one nearby experience orb per admitted player frame.
func (s *Server) touchNearbyExperienceOrb(sess *Session, nearby []*Entity) {
	if len(nearby) == 0 {
		return
	}
	e := nearby[rand.Intn(len(nearby))]
	if live, ok := s.entities.GetPtr(e.ID); !ok || live != e || sess.takeXpDelay > 0 {
		return
	}
	sess.takeXpDelay = 2
	s.broadcastTakeItem(e.Dim, e.ID, sess.entityID.Load(), 1)
	if remaining := s.repairPlayerItems(sess, e.OrbValue); remaining > 0 {
		sess.addXP(remaining)
	}
	e.OrbCount--
	if e.OrbCount <= 0 {
		s.entities.Remove(e.ID)
		s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
	}
}
