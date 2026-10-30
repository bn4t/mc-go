package server

import (
	"mc-go/internal/world"
	"sort"
	"strings"
)

// Pending queues are copied while the simulation owner holds world state.
// The asynchronous native writer receives only primitive detached records.
func (s *Server) captureScheduledChunkTicksOwned() map[string]map[[2]int32]world.SavedChunkTicks {
	out := map[string]map[[2]int32]world.SavedChunkTicks{}
	if s.ticks == nil {
		return out
	}
	now := s.TickCount()
	type record struct {
		key      schedKey
		at       int64
		priority int8
	}
	s.ticks.mu.Lock()
	records := make([]record, 0, len(s.ticks.queued))
	for key, at := range s.ticks.queued {
		records = append(records, record{key, at, s.ticks.prio[key]})
	}
	s.ticks.mu.Unlock()
	for _, record := range records {
		key := s.ticks.typedKey(record.key)
		name := key.block
		if key.kind == tickFluid {
			name = key.fluid
		}
		if name == "" {
			continue
		}
		if !strings.ContainsRune(name, ':') {
			name = "minecraft:" + name
		}
		if out[key.dim] == nil {
			out[key.dim] = map[[2]int32]world.SavedChunkTicks{}
		}
		chunk := [2]int32{int32(key.pos.X >> 4), int32(key.pos.Z >> 4)}
		saved := out[key.dim][chunk]
		// ScheduledTick.toSavedTick narrows the relative long delay to an int.
		tick := world.SavedTick{Type: name, Pos: key.pos, Delay: int32(record.at - now), Priority: int32(record.priority)}
		if key.kind == tickFluid {
			saved.Fluids = append(saved.Fluids, tick)
		} else {
			saved.Blocks = append(saved.Blocks, tick)
		}
		out[key.dim][chunk] = saved
	}
	for _, chunks := range out {
		for _, chunk := range chunks {
			sortSavedTicks(chunk.Blocks)
			sortSavedTicks(chunk.Fluids)
		}
	}
	return out
}

// The queue has no native sub-tick sequence yet; deterministic output retains
// metadata and gives equal-time/priority records the existing position order.
func sortSavedTicks(records []world.SavedTick) {
	sort.Slice(records, func(i, j int) bool {
		a, b := records[i], records[j]
		if a.Delay != b.Delay {
			return a.Delay < b.Delay
		}
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if a.Pos.X != b.Pos.X {
			return a.Pos.X < b.Pos.X
		}
		if a.Pos.Y != b.Pos.Y {
			return a.Pos.Y < b.Pos.Y
		}
		if a.Pos.Z != b.Pos.Z {
			return a.Pos.Z < b.Pos.Z
		}
		return a.Type < b.Type
	})
}
