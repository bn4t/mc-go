package server

import (
	"math"
	"mc-go/internal/world"
	"path/filepath"
)

func packedWorldClock(total int64, state worldClockState) map[string]any {
	fields := map[string]any{"total_ticks": total}
	if math.Float32bits(state.partial) != 0 {
		fields["partial_tick"] = state.partial
	}
	if rate := state.baseRate(); rate != 1 {
		fields["rate"] = rate
	}
	if state.paused {
		fields["paused"] = int8(1)
	}
	return fields
}
func (s *Server) captureWorldClocks() map[string]any {
	s.clock.mu.Lock()
	day, end, dayState, endState := s.clock.day, s.clock.end, s.clock.dayState, s.clock.endState
	s.clock.mu.Unlock()
	return map[string]any{"DataVersion": int32(world.DataVersion26_2), "data": map[string]any{"minecraft:overworld": packedWorldClock(day, dayState), "minecraft:the_end": packedWorldClock(end, endState)}}
}
func (s *Server) loadWorldClocks() {
	s.restoreWorldClocks(nil)
	if s.WorldDir == "" {
		return
	}
	root, err := readPlayerGzipNBT(filepath.Join(s.WorldDir, "data", "minecraft", "world_clocks.dat"))
	if err == nil {
		s.restoreWorldClocks(root)
	}
}
func readWorldClock(value any) (int64, worldClockState, bool) {
	fields, ok := value.(map[string]any)
	if !ok {
		return 0, worldClockState{}, false
	}
	// DFU's LONG narrowing is shared with the scheduled-event codec.
	total, ok := savedFunctionAge(fields["total_ticks"])
	if !ok {
		return 0, worldClockState{}, false
	}
	state := worldClockState{}
	if value, present := fields["partial_tick"]; present {
		state.partial, ok = nbtFloat(value)
		if !ok {
			return 0, state, false
		}
	}
	if value, present := fields["rate"]; present {
		state.rate, ok = nbtFloat(value)
		if !ok || !(state.rate > 0 && state.rate <= math.MaxFloat32) {
			return 0, state, false
		}
	}
	if value, present := fields["paused"]; present {
		state.paused, ok = testInstanceCodecBool(value)
		if !ok {
			return 0, state, false
		}
	}
	return total, state, true
}
func (s *Server) restoreWorldClocks(value any) bool {
	// SavedData accepts a partial map: each malformed known row defaults, while
	// valid siblings survive. Legacy Time/DayTime never seed an absent clock.
	totals := [2]int64{}
	states := [2]worldClockState{}
	root, rootOK := value.(map[string]any)
	data, dataOK := root["data"].(map[string]any)
	if rootOK && dataOK {
		for id, name := range []string{"minecraft:overworld", "minecraft:the_end"} {
			if total, state, ok := readWorldClock(data[name]); ok {
				totals[id] = total
				states[id] = state
			}
		}
	}
	s.clock.mu.Lock()
	s.clock.day, s.clock.end = totals[0], totals[1]
	s.clock.dayState, s.clock.endState = states[0], states[1]
	s.clock.mu.Unlock()
	return rootOK && dataOK
}
