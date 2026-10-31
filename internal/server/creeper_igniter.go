package server

import "math/rand"

// creeperIgniterInteract uses the actual selected hand and leaves reach and
// dimension admission to handleInteract's shared entity gate.
func (s *Server) creeperIgniterInteract(sess *Session, eid int32, hand int32) bool {
	if hand != 0 && hand != 1 {
		return false
	}
	e, ok := s.entities.GetPtr(eid)
	if !ok || e.Dying {
		return false
	}
	if name, _ := EntityTypeName(e.Type); name != "creeper" {
		return false
	}
	held, slot := sess.stackForHand(hand)
	name, ok := itemNameOf(held.ID)
	if !ok || held.Empty() || !itemTagHas("creeper_igniters", name) {
		return false
	}
	sound := int32(642)
	if name == "fire_charge" {
		sound = 626
	}
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, sound, 5, 1, 0.8+rand.Float32()*0.4, sess)
	e.Ignited = true
	if name == "flint_and_steel" {
		s.wearSlot(sess, slot, 1)
	} else if sess.mode() != GameModeCreative {
		sess.consumeSlot(slot, 1)
	}
	return true
}
