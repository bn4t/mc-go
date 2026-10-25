package server

import "sync"

// simulationExecutor is the ownership boundary for gameplay state. A connection
// reads packet frames outside this boundary, then runs their handlers as actions;
// joining, leaving, the wall tick and the game tick use the same boundary.
// Existing subsystem locks still protect independently readable snapshots and
// shard storage. They do not make an arbitrary gameplay operation safe to run
// concurrently with an action.
//
// Internal methods suffixed Owned require this lock. They must never enter
// simulationAction recursively. In particular, commands use owned tick helpers
// while public tick methods acquire ownership for external callers.
type simulationExecutor struct {
	mu sync.Mutex

	// Deferred mutations use a separate mutex so a synchronous gameplay hook can
	// submit work without reentering the owner. A bounded batch runs after the
	// current action, never halfway through a movement commit.
	pendingMu sync.Mutex
	pending   []func()
}

const maxPendingSimulationActions = 256

func (s *Server) simulationAction(action func()) {
	s.simulation.mu.Lock()
	defer s.simulation.mu.Unlock()
	action()
	s.drainSimulationActionsOwned()
	s.flushPlayerUpdatesOwned()
}

// queueSimulationAction accepts deferred work from observers or hooks. The
// caller must not wait for completion from inside a synchronous hook. A full
// queue rejects work instead of spawning an unbounded number of goroutines.
func (s *Server) queueSimulationAction(action func()) bool {
	if action == nil {
		return false
	}
	s.simulation.pendingMu.Lock()
	defer s.simulation.pendingMu.Unlock()
	if s.shuttingDown.Load() || len(s.simulation.pending) >= maxPendingSimulationActions {
		return false
	}
	s.simulation.pending = append(s.simulation.pending, action)
	return true
}

func (s *Server) drainSimulationActionsOwned() {
	s.simulation.pendingMu.Lock()
	pending := s.simulation.pending
	s.simulation.pending = nil
	s.simulation.pendingMu.Unlock()
	for _, action := range pending {
		if s.shuttingDown.Load() {
			return
		}
		action()
	}
}

// discardSimulationActions drops callback requests once shutdown begins. It does
// not take the owner lock, so stopping runtime workers cannot deadlock on it.
func (s *Server) discardSimulationActions() {
	s.simulation.pendingMu.Lock()
	s.simulation.pending = nil
	s.simulation.pendingMu.Unlock()
}
