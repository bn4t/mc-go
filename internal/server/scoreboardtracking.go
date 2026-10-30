package server

import "sort"

func (s *Server) scoreboardTrackedLocked(name string) bool {
	if name == "" {
		return false
	}
	for _, displayed := range s.board.displaySlots {
		if displayed == name {
			return true
		}
	}
	return false
}

func (s *Server) scoreboardTracked(name string) bool {
	s.board.mu.Lock()
	defer s.board.mu.Unlock()
	return s.scoreboardTrackedLocked(name)
}

func (s *Server) scoreboardSlotsLocked(name string) []int {
	var slots []int
	for slot, displayed := range s.board.displaySlots {
		if displayed == name {
			slots = append(slots, slot)
		}
	}
	sort.Ints(slots)
	return slots
}

// A newly visible objective arrives before its display assignments and score
// snapshot. Additional slots for an already visible objective need no snapshot.
func (s *Server) scoreboardStartTracking(name string) {
	s.board.mu.Lock()
	objective := s.board.objectives[name]
	slots := s.scoreboardSlotsLocked(name)
	holders := append([]string(nil), s.board.holderOrder...)
	type entry struct {
		holder string
		value  int32
	}
	var scores []entry
	javaHashOrder(holders, func(holder string) string { return holder })
	for _, holder := range holders {
		if score := s.board.scores[holder][name]; score != nil {
			scores = append(scores, entry{holder, score.value})
		}
	}
	s.board.mu.Unlock()
	s.sendObjective(name, 0, objective)
	for _, slot := range slots {
		s.broadcastDisplayObjective(int32(slot), name)
	}
	for _, score := range scores {
		s.broadcastScore(score.holder, name, score.value)
	}
}

// This packet plan is based on measured transitions, including the duplicate
// assignment when both the displaced and replacement objectives remain visible.
func (s *Server) scoreboardDisplayChanged(slot int, old, next string, alreadyVisible bool) {
	if old != "" && old != next {
		if s.scoreboardTracked(old) {
			s.broadcastDisplayObjective(int32(slot), next)
		} else {
			s.broadcastObjectiveRemove(old)
		}
	}
	if next != "" {
		if alreadyVisible {
			s.broadcastDisplayObjective(int32(slot), next)
		} else {
			s.scoreboardStartTracking(next)
		}
	}
}

func (s *Server) scoreboardRemovedTracking(name string, slots []int) {
	if len(slots) == 0 {
		return
	}
	for _, slot := range slots[:len(slots)-1] {
		s.broadcastDisplayObjective(int32(slot), "")
	}
	s.broadcastObjectiveRemove(name)
}
