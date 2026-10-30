package server

import "math"

type scheduledArea struct {
	dim          string
	x, z, radius int
}

func (a scheduledArea) contains(c scheduledChunk) bool {
	dx, dz := c.x-a.x, c.z-a.z
	if dx < 0 {
		dx = -dx
	}
	if dz < 0 {
		dz = -dz
	}
	return c.dim == a.dim && max(dx, dz) <= a.radius
}

type scheduledAdmissions struct {
	controllers                       bool
	loading, simulation, full, entity []scheduledArea
}

func scheduledContains(areas []scheduledArea, chunk scheduledChunk) bool {
	for _, area := range areas {
		if area.contains(chunk) {
			return true
		}
	}
	return false
}

// Loading and simulation tickets are independent. Client render distance is
// not the server's loading distance; an entity-ready command predicate is not
// a FULL-only or block-ticking readiness test either.
func (s *Server) scheduledAdmissionsOwned() scheduledAdmissions {
	var out scheduledAdmissions
	view, sim := s.ViewDistance, s.SimulationDistance
	if view <= 0 {
		view = 10
	}
	if sim <= 0 {
		sim = 10
	}
	view, sim = max(2, min(32, view)), max(2, min(32, sim))
	genSpectators := s.gameRuleBool("minecraft:spectators_generate_chunks")
	s.sessions.Range(func(_, value any) bool {
		p := value.(*Session)
		if !p.gameplayTickReady() || !p.spawned.Load() {
			return true
		}
		if e, ok := playerEntity(p); ok && e.presenceOnly {
			return true
		}
		out.controllers = true
		if p.mode() == GameModeSpectator && !genSpectators {
			return true
		}
		dim := p.dimName()
		if previous := p.dimSwapPrev.Load(); previous != nil {
			dim = previous.name
		}
		x, _, z, _, _, _ := p.Pos()
		area := scheduledArea{dim: dim, x: int(math.Floor(x)) >> 4, z: int(math.Floor(z)) >> 4}
		area.radius = view + 1
		out.loading = append(out.loading, area)
		area.radius = sim + 1
		out.simulation = append(out.simulation, area)
		area.radius = view + 2
		out.full = append(out.full, area)
		area.radius = view
		out.entity = append(out.entity, area)
		return true
	})
	for dim, forced := range s.forcedChunks {
		for packed, yes := range forced {
			if !yes {
				continue
			}
			area := scheduledArea{dim: dim, x: int(int32(packed)), z: int(int32(uint64(packed) >> 32)), radius: 1}
			out.loading = append(out.loading, area)
			out.simulation = append(out.simulation, area)
			area.radius = 2
			out.full = append(out.full, area)
			area.radius = 0
			out.entity = append(out.entity, area)
		}
	}
	return out
}

func (s *Server) prepareScheduledChunkOwned(c scheduledChunk) bool {
	d := s.dims[c.dim]
	if d == nil {
		return false
	}
	if !chunkSpawnReady(d, c.x, c.z) {
		// The loading admission supplies the surrounding FULL border. Materialize
		// it through the existing synchronous backend before exposing callbacks.
		for dx := -1; dx <= 1; dx++ {
			for dz := -1; dz <= 1; dz++ {
				d.w.BaseChunk(c.x+dx, c.z+dz)
			}
		}
	}
	return chunkSpawnReady(d, c.x, c.z)
}

func (s *Server) activateSavedTicksOwned(manual bool) {
	if s.ticks == nil || s.shuttingDown.Load() {
		return
	}
	s.ticks.mu.Lock()
	empty := len(s.ticks.relative) == 0
	s.ticks.mu.Unlock()
	if empty {
		return
	}
	admission := s.scheduledAdmissionsOwned()
	candidates := map[scheduledChunk]bool{}
	s.ticks.mu.Lock()
	if manual && !admission.controllers {
		for chunk := range s.ticks.relative {
			candidates[chunk] = true
		}
	} else {
		// Look up only admitted windows, rather than scanning every cold saved
		// chunk each frame on a large persisted world.
		for _, area := range admission.loading {
			for x := area.x - area.radius; x <= area.x+area.radius; x++ {
				for z := area.z - area.radius; z <= area.z+area.radius; z++ {
					chunk := scheduledChunk{dim: area.dim, x: x, z: z}
					if len(s.ticks.relative[chunk]) != 0 {
						candidates[chunk] = true
					}
				}
			}
		}
	}
	s.ticks.mu.Unlock()
	now := s.TickCount()
	for chunk := range candidates {
		if manual && !admission.controllers || s.prepareScheduledChunkOwned(chunk) {
			s.ticks.activateSavedChunk(chunk, now)
		}
	}
}

func (s *Server) scheduledDispatchOwned() map[scheduledChunk]bool {
	admission := s.scheduledAdmissionsOwned()
	// Keep existing no-session/manual simulation embeddings unrestricted.
	if !admission.controllers {
		return nil
	}
	chunks := map[scheduledChunk]bool{}
	now := s.TickCount()
	s.ticks.mu.Lock()
	for at, keys := range s.ticks.due {
		if at > now {
			continue
		}
		for _, key := range keys {
			chunks[scheduledChunkOf(key)] = false
		}
	}
	s.ticks.mu.Unlock()
	for chunk := range chunks {
		chunks[chunk] = scheduledContains(admission.loading, chunk) && scheduledContains(admission.simulation, chunk) && s.prepareScheduledChunkOwned(chunk)
	}
	return chunks
}

func (s *Server) scheduledPositionAvailable(d *dimension, cx, cz int, entity bool) (bool, bool) {
	if d == nil {
		return false, false
	}
	admission := s.scheduledAdmissionsOwned()
	areas := admission.full
	if entity {
		areas = admission.entity
	}
	chunk := scheduledChunk{dim: d.name, x: cx, z: cz}
	covered := scheduledContains(areas, chunk)
	if !admission.controllers && !covered {
		return false, false
	}
	if !covered {
		return false, true
	}
	return chunkLoaded(d, cx, cz), true
}
