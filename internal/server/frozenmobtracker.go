package server

// frozenMobTrackerStep mirrors the tracker pass vanilla keeps running while
// the world is frozen. ServerLevel.tick runs ServerChunkCache.tick even
// when TickRateManager freezes the level's entity and block ticks, so every
// tracked mob's ServerEntity.sendChanges keeps firing once per server tick:
// the updateInterval gate, the tickCount%60 forced-position resync and the
// per-gate teleportDelay all advance on wall frames. Players keep ticking
// through a freeze, so the pass rides playerTickOwned's owner-frame
// cadence — one sendChanges-equivalent flush per frame, before the frozen
// mounted-callback work, matching vanilla's chunkSource.tick before the
// connection pass.
//
// Only the sync flush runs here. No AI, body counters, collision scale or
// effects advance: those belong to the frozen owner-frame callbacks and the
// gated fluid pass, and a stepped or sprinting run drives its own game
// ticks through gameTickOwned's mobTickStep instead.
func (s *Server) frozenMobTrackerStep() {
	if !s.clock.isFrozen() || s.clock.isSprinting() {
		return
	}
	s.entities.mu.RLock()
	mobs := make([]*Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		// The ender dragon runs its own tick (enderdragon.go); players
		// never pair through the mob tracker.
		if e.Living && e.dragon == nil {
			mobs = append(mobs, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range mobs {
		// Vanilla's frozen tracker still only covers simulation-range
		// entities, and a dormant entity's chunk is unticketed: the same
		// admission mobTickStep applies.
		if e.Dormant || !s.mobInSimulationRange(e) {
			continue
		}
		s.mobSyncFlush(e)
	}
}
