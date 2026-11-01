package server

import "sort"

// Frozen owner frames still consume dimensions in a player-mounted graph.
// This callback also advances age, but not other body counters, effects or AI.
// It runs once globally, before the existing per-player seating consumers.
func (s *Server) frozenMountedScaleCallbacksOwned() {
	if !s.clock.isFrozen() || s.clock.isSprinting() {
		return
	}
	s.forEachFrozenMountedEntityOwned(func(e *Entity) {
		name, _ := EntityTypeName(e.Type)
		if e.Living && name != "player" {
			before := [3]float64{e.X, e.Y, e.Z}
			if !e.syncInit {
				s.latchSyncBaseline(e)
			}
			s.refreshMobCollisionScaleFrame(e, e.Riding == 0)
			// A changed root can require one dirty movement update. Reuse the
			// quantized movement emitter without advancing the tracker cadence.
			// Sub-quantum free-center adjustments do not force zero packets.
			dx, dy, dz := mobFixed(e.X)-mobFixed(before[0]), mobFixed(e.Y)-mobFixed(before[1]), mobFixed(e.Z)-mobFixed(before[2])
			if e.Riding == 0 && float64(dx)*float64(dx)+float64(dy)*float64(dy)+float64(dz)*float64(dz) >= 128 {
				s.mobMoveSync(e)
			}
		}
		if e.Living && name != "player" && !e.Dying && e.dragon == nil {

			wasBaby := e.Baby
			mobAgeFrame(e, name)
			if wasBaby && !e.Baby && packsBabyAt16(name) && len(e.dataSync) == 0 {
				s.broadcastEntityMetadata(e.ID, []byte{16, 8, 0, 0xff})
				e.metaSent.baby = false
			}
		}
	})
}

// Each pass takes a fresh graph snapshot under simulation ownership. The world
// step can run before any paced player frame and must select independently.
func (s *Server) forEachFrozenMountedEntityOwned(visitEntity func(*Entity)) {
	if !s.clock.isFrozen() || s.clock.isSprinting() {
		return
	}
	// Only inspect the admitted riding graphs; frozen unrelated populations
	// do not need a world-wide scan on every owner frame.
	nodes := map[int32]*Entity{}
	lookup := func(id int32) *Entity {
		e, known := nodes[id]
		if !known {
			e, _ = s.entities.GetPtr(id)
			nodes[id] = e
		}
		return e
	}
	roots := map[int32]bool{}
	for _, p := range s.activePlayersOwned() {
		if !p.admitted || (p.java != nil && !p.java.gameplayTickReady()) {
			continue
		}
		e := lookup(p.entityID.Load())
		if e == nil || e.UUID != p.uuid {
			continue
		}
		name, _ := EntityTypeName(e.Type)
		if name != "player" {
			continue
		}
		seen := map[int32]bool{}
		valid := true
		for {
			if seen[e.ID] {
				valid = false
				break
			}
			seen[e.ID] = true
			if e.Riding == 0 {
				break
			}
			parent := lookup(e.Riding)
			if parent == nil || parent.Dim != e.Dim || !frozenMountedHasPassenger(parent, e.ID) {
				valid = false
				break
			}
			e = parent
		}
		if valid {
			roots[e.ID] = true
		}
	}
	ids := make([]int32, 0, len(roots))
	for id := range roots {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	seen := map[int32]bool{}
	var visit func(*Entity)
	visit = func(e *Entity) {
		if e == nil || seen[e.ID] || e.presenceOnly || e.Dormant {
			return
		}
		seen[e.ID] = true
		if !s.mobInSimulationRange(e) {
			return
		}
		visitEntity(e)
		for _, id := range e.Passengers {
			child := lookup(id)
			if child != nil && child.Riding == e.ID && child.Dim == e.Dim {
				visit(child)
			}
		}
	}
	for _, id := range ids {
		visit(lookup(id))
	}
}

func frozenMountedHasPassenger(e *Entity, id int32) bool {
	for _, pid := range e.Passengers {
		if pid == id {
			return true
		}
	}
	return false
}
