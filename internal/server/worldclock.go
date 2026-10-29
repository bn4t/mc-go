package server

import (
	"math"
	"mc-go/internal/protocol"
)

// A zero rate is the uninitialized/default rate of one; native saved rates
// are strictly positive. Totals live beside these fields under clock.mu.
type worldClockState struct {
	partial, rate float32
	paused        bool
}

func (state worldClockState) baseRate() float32 {
	if state.rate == 0 {
		return 1
	}
	return state.rate
}
func advanceWorldClock(total *int64, state *worldClockState) {
	if state.paused {
		return
	}
	sum := state.partial + state.baseRate()
	// The integral carry uses the native int32 floor boundary, not a long cast.
	integral := math.Floor(float64(sum))
	var whole int32
	switch {
	case math.IsNaN(integral):
	case integral > math.MaxInt32:
		whole = math.MaxInt32
	case integral < math.MinInt32:
		whole = math.MinInt32
	default:
		whole = int32(integral)
	}
	state.partial = sum - float32(whole)
	*total += int64(whole)
}

func (s *Server) worldClockTotal(id int32) int64 {
	s.clock.mu.Lock()
	defer s.clock.mu.Unlock()
	if id == 0 {
		return s.clock.day
	}
	return s.clock.end
}
func (s *Server) setWorldClockTotal(id int32, total int64) {
	s.clock.mu.Lock()
	if id == 0 {
		s.clock.day = total
		s.clock.dayState.partial = 0
	} else {
		s.clock.end = total
		s.clock.endState.partial = 0
	}
	s.clock.mu.Unlock()
	s.broadcastUpdateTimeBody(s.worldClockSyncBody([]int32{id}))
}

// Add preserves the sub-tick position, rate and pause state. Native long
// addition wraps before the resulting total is clamped to zero.
func (s *Server) addWorldClockTicks(id int32, ticks int64) {
	s.clock.mu.Lock()
	total := &s.clock.day
	if id == 1 {
		total = &s.clock.end
	}
	*total += ticks
	if *total < 0 {
		*total = 0
	}
	s.clock.mu.Unlock()
	s.broadcastUpdateTimeBody(s.worldClockSyncBody([]int32{id}))
}
func (s *Server) worldClockSyncBody(ids []int32) []byte {
	advance := s.gameRuleBool("minecraft:advance_time")
	s.clock.mu.Lock()
	age := s.clock.count + s.clock.base
	totals := [2]int64{s.clock.day, s.clock.end}
	states := [2]worldClockState{s.clock.dayState, s.clock.endState}
	s.clock.mu.Unlock()
	return worldClockPacket(age, ids, totals, states, advance)
}
func worldClockPacket(age int64, ids []int32, totals [2]int64, states [2]worldClockState, advance bool) []byte {
	var b protocol.Buffer
	b.Long(age)
	b.VarInt(int32(len(ids)))
	for _, id := range ids {
		state := states[id]
		rate := state.baseRate()
		if !advance || state.paused {
			rate = 0
		}
		b.VarInt(id)
		b.VarLong(totals[id])
		b.Float(state.partial)
		b.Float(rate)
	}
	return b.Bytes()
}
func (s *Server) worldClockRuleChanged() {
	s.broadcastUpdateTimeBody(s.worldClockSyncBody([]int32{0, 1}))
}

func worldClockID(name string) (int32, bool) {
	switch name {
	case "minecraft:overworld":
		return 0, true
	case "minecraft:the_end":
		return 1, true
	}
	return 0, false
}
