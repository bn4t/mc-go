package server

// frozenTrackerStep mirrors the tracker pass vanilla keeps running while
// the world is frozen. ServerLevel.tick runs ServerChunkCache.tick even
// when TickRateManager freezes the level's entity and block ticks, and
// ChunkMap.tick walks every TrackedEntity in entityMap — every entity
// type, not only mobs — calling ServerEntity.sendChanges once per server
// tick for entities whose chunk is in entity-ticking range (plus section
// movers and needsSync bodies, whose pairing events mc-go already emits
// from their own command/frame paths). Each entity's updateInterval
// gate, the tickCount%60 forced-position resync and the per-gate
// teleportDelay all advance on wall frames: an item's iv-20 gate, a
// minecart's iv-3 gate and a mob's iv-3 gate ride the same pass. Players
// keep ticking through a freeze, so the pass rides playerTickOwned's
// owner-frame cadence — one sendChanges-equivalent flush per frame,
// before the frozen mounted-callback work, matching vanilla's
// chunkSource.tick before the connection pass.
//
// Only the sync flush runs here. No AI, gravity, item aging, body
// counters or collision scale advance: those belong to the frozen
// owner-frame callbacks and the gated family passes, and a stepped or
// sprinting run drives its own game ticks through gameTickOwned's
// per-family steps instead.
func (s *Server) frozenTrackerStep() {
	if !s.clock.isFrozen() || s.clock.isSprinting() {
		return
	}
	playerType := mustEntityType("player")
	s.entities.mu.RLock()
	ents := make([]*Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		// Players flush through their own per-frame pass
		// (playerHeadTrackingTick → mobSyncFlush, which also covers
		// them while frozen), so this pass skips them to avoid a
		// second flush per frame. Every other tracked type rides
		// this pass like vanilla's entityMap: living mobs, the ender
		// dragon, items, orbs, projectiles, vehicles, crystals and
		// displays alike.
		if e.Type == playerType {
			continue
		}
		ents = append(ents, e)
	}
	s.entities.mu.RUnlock()
	for _, e := range ents {
		// Vanilla's frozen tracker still only covers entity-ticking-range
		// entities, and a dormant entity's chunk is unticketed: the same
		// admission the unfrozen family passes apply.
		if e.Dormant || !s.mobInSimulationRange(e) {
			continue
		}
		s.mobSyncFlush(e)
	}
}
