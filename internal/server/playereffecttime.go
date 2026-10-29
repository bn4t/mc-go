package server

// effectNow follows entity ticks: the world tick for ordinary/sprint runs,
// and the paced player tick while the world is frozen. All writes belong to
// the simulation owner, just like the world's local tick counter.
func (sess *Session) effectNow() int64 {
	return sess.server.clock.count + sess.effectFrozenTicks
}

func (s *Server) frozenPlayerEffectTick() {
	if !s.clock.isFrozen() || s.clock.isSprinting() {
		return
	}
	s.sessions.Range(func(_, value any) bool {
		sess := value.(*Session)
		if sess.gameplayTickReady() {
			sess.effectFrozenTicks++
		}
		return true
	})
	s.playerEffectTickStep()
}

// A frozen /tick step advances the world; players already follow the paced
// player tick. Compensate that world-age change without moving any finite or
// infinite deadlines, including hidden instances.
func (s *Server) compensateFrozenPlayerEffectStep() {
	if !s.clock.isFrozen() {
		return
	}
	s.sessions.Range(func(_, value any) bool {
		value.(*Session).effectFrozenTicks--
		return true
	})
}
