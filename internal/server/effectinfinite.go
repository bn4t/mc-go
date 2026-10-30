package server

// Finite instances retain their existing remaining-duration alignment;
// infinite instances schedule against the living entity's signed tick count.
func effectTickArgument(inst mobEffectInst, now, entityAge int64) int64 {
	if inst.expiry < 0 {
		return int64(int32(entityAge))
	}
	return inst.expiry - now + 1
}

func (s *Server) playerEffectEntityAge(sess *Session) int64 {
	age := sess.entityTicks
	// The frozen dispatcher runs before this frame's player base/fluid pass.
	// Supply that upcoming entity tick without moving any finite deadline.
	if s.clock.isFrozen() && !s.clock.isSprinting() {
		age++
	}
	return age
}

func (s *Server) consumeInfinitePlayerTick(sess *Session) bool {
	age := s.playerEffectEntityAge(sess)
	fresh := !sess.infiniteEffectAgeSet
	advanced := fresh || age != sess.infiniteEffectAge
	sess.infiniteEffectAge, sess.infiniteEffectAgeSet = age, true
	// Constructor age zero is not an entity tick. A later int32 wrap to zero
	// still has a nonzero full counter and remains a real tick.
	return advanced && (!fresh || age != 0)
}

// Eligibility is an actual living-tick receipt, not the predicate's clock.
// In particular a parked entity at a due age must not pulse repeatedly.
func (s *Server) consumeInfiniteMobTick(e *Entity) bool {
	serial := e.livingEffectTickSerial
	if serial == e.infiniteEffectTickSerial {
		return false
	}
	e.infiniteEffectTickSerial = serial
	return e.Living && !e.presenceOnly && !e.Dormant && s.mobInSimulationRange(e)
}
