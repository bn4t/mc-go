package server

import "mc-go/api/player"

// Setters only mark records. Publishing happens at an owned commit boundary,
// after temporary command dimension forks and multi-field transitions finish.
// The queue is bounded by records currently admitted to the runtime; repeated
// writes to one record coalesce without scanning all players on each action.
func (s *Server) markPlayerDirty(p *playerRecord) {
	if p == nil || !p.active.Load() {
		return
	}
	s.playerDirtyMu.Lock()
	if s.playerDirty == nil {
		s.playerDirty = make(map[*playerRecord]struct{})
	}
	s.playerDirty[p] = struct{}{}
	s.playerDirtyMu.Unlock()
}

func (s *Server) flushPlayerUpdatesOwned() {
	s.playerDirtyMu.Lock()
	dirty := s.playerDirty
	s.playerDirty = nil
	s.playerDirtyMu.Unlock()
	for p := range dirty {
		if !p.active.Load() || p.left {
			continue
		}
		s.playersMu.RLock()
		registered := s.players[p.ID()] == p
		s.playersMu.RUnlock()
		if !registered {
			continue
		}
		state := p.snapshot()
		if previous := p.published.Load(); previous != nil && *previous == state {
			continue
		}
		p.published.Store(&state)
		s.publishPlayerUpdateOwned(player.Update{Kind: player.Moved, Player: state}, nil)
	}
}
