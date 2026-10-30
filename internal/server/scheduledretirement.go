package server

import (
	"mc-go/internal/world"
	"strings"
	"time"
)

type scheduledRetirementSnapshot struct {
	dir        string
	dimensions map[string]*world.DimensionSaveSnapshot
}

func (snapshot *scheduledRetirementSnapshot) write() error {
	for name, chunks := range snapshot.dimensions {
		if err := chunks.WriteMerged(snapshot.dir, name); err != nil {
			return err
		}
	}
	return nil
}

type scheduledRetirementJob struct {
	done  chan struct{}
	err   error
	saves []detachedTickSave
}

// This retires scheduled containers, not entities or the backend's block cache.
// Native persistence is required; ephemeral/manual embeddings keep their prior
// unrestricted queue lifetime. Detach leaves resident deadlines absolute until
// a successful write is observed while the same generation is still detached.
func (s *Server) scheduledRetirementStepOwned(manual bool) {
	s.scheduledRetirementOwned(true)
}

func (s *Server) scheduledRetirementOwned(submit bool) {
	if s.WorldDir == "" || s.ticks == nil {
		return
	}
	s.ticks.mu.Lock()
	empty := len(s.ticks.registered) == 0 && len(s.ticks.detached) == 0
	s.ticks.mu.Unlock()
	if empty && s.tickRetirement == nil {
		return
	}
	admission := s.scheduledAdmissionsOwned()
	if admission.controllers {
		available := func(c scheduledChunk) bool {
			d := s.dims[c.dim]
			return d != nil && scheduledContains(admission.full, c) && chunkLoaded(d, c.x, c.z)
		}
		// Re-ticket before applying a completed write: even a completed old snapshot
		// cannot replace a live resident queue or later edits on this owner turn.
		s.ticks.mu.Lock()
		var reattach []scheduledChunk
		cold := map[scheduledChunk]bool{}
		for _, area := range admission.full {
			for x := area.x - area.radius; x <= area.x+area.radius; x++ {
				for z := area.z - area.radius; z <= area.z+area.radius; z++ {
					c := scheduledChunk{dim: area.dim, x: x, z: z}
					if s.ticks.detached[c] != nil && available(c) {
						reattach = append(reattach, c)
					}
				}
			}
		}
		for c := range s.ticks.registered {
			d := s.dims[c.dim]
			// Never turn an unmaterialized future row into a loading request.
			// Only a resident backend column can begin native retirement.
			if d != nil && chunkLoaded(d, c.x, c.z) && !available(c) {
				cold[c] = true
			}
		}
		s.ticks.mu.Unlock()
		for _, c := range reattach {
			s.ticks.reattachChunk(c)
		}
		if !s.ShuttingDown() {
			s.ticks.detachChunks(cold)
		}
	}
	if job := s.tickRetirement; job != nil {
		select {
		case <-job.done:
			if job.err == nil {
				for _, saved := range job.saves {
					s.ticks.discardSerializedChunk(saved)
				}
			}
			s.tickRetirement = nil
			// Retry a failed write on a later owner pass, retaining absolute deadlines.
			if job.err != nil {
				s.tickRetirementRetryAt = time.Now().Add(time.Second)
				return
			}
		default:
			return
		}
	}
	// Completion accounting above is unconditional. Controller/shutdown guards
	// prevent new submissions, never overwrite a successfully frozen snapshot.
	if !submit || !admission.controllers || s.ShuttingDown() {
		return
	}
	if time.Now().Before(s.tickRetirementRetryAt) {
		return
	}
	// Do not reserve snapshots behind a busy full/subset write. At most one
	// retirement payload is retained; capture uses the current resident deadline.
	if tail := s.nativeWriteTail; tail != nil {
		select {
		case <-tail:
		default:
			return
		}
	}
	now := s.TickCount()
	job := &scheduledRetirementJob{done: make(chan struct{})}
	selections := map[string]map[[2]int32]world.SavedChunkTicks{}
	s.ticks.mu.Lock()
	for c := range s.ticks.residentDetached {
		saved, ok := s.ticks.snapshotDetachedChunkLocked(c, now)
		if !ok {
			continue
		}
		job.saves = append(job.saves, saved)
		if selections[c.dim] == nil {
			selections[c.dim] = map[[2]int32]world.SavedChunkTicks{}
		}
		selections[c.dim][[2]int32{int32(c.x), int32(c.z)}] = retirementSavedTicks(saved.rows)
	}
	s.ticks.mu.Unlock()
	if len(job.saves) == 0 {
		return
	}
	snapshot := &scheduledRetirementSnapshot{dir: s.WorldDir, dimensions: map[string]*world.DimensionSaveSnapshot{}}
	for name, selection := range selections {
		d := s.dims[name]
		if d == nil {
			continue
		}
		snapshot.dimensions[name] = d.w.CaptureDimensionChunksWithTicks(d.minY, d.height,
			func(x, z int) []map[string]any { return s.chunkBlockEntities(d, x, z) },
			func(x, z int) *world.ChunkLight { return d.savedLight(x, z) }, selection)
	}
	write := s.tickRetirementWrite
	if write == nil {
		write = func(snapshot *scheduledRetirementSnapshot) error { return snapshot.write() }
	}
	s.tickRetirement = job
	s.submitNativeWriteOwned(job.done, func() { job.err = write(snapshot) })
}

func retirementSavedTicks(rows []relativeScheduledTick) world.SavedChunkTicks {
	var out world.SavedChunkTicks
	for _, row := range rows {
		name := row.key.block
		if row.key.kind == tickFluid {
			name = row.key.fluid
		}
		if name == "" {
			continue
		}
		if !strings.ContainsRune(name, ':') {
			name = "minecraft:" + name
		}
		tick := world.SavedTick{Type: name, Pos: row.key.pos, Delay: row.delay, Priority: int32(row.priority)}
		if row.key.kind == tickFluid {
			out.Fluids = append(out.Fluids, tick)
		} else {
			out.Blocks = append(out.Blocks, tick)
		}
	}
	return out
}
