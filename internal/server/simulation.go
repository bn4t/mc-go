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
	pending   []simulationWork
}

type simulationWork struct {
	run     func()
	discard func()
}

const maxPendingSimulationActions = 256

func (s *Server) simulationAction(action func()) {
	s.simulation.mu.Lock()
	defer s.simulation.mu.Unlock()
	action()
	s.drainSimulationActionsOwned()
	s.flushWorldUpdatesOwned()
	s.flushPlayerUpdatesOwned()
}

// queueSimulationAction accepts deferred work from observers or hooks. The
// caller must not wait for completion from inside a synchronous hook. A full
// queue rejects work instead of spawning an unbounded number of goroutines.
func (s *Server) queueSimulationAction(action func()) bool {
	return s.queueSimulationActionWithDiscard(action, nil)
}

// queueSimulationActionWithDiscard adds a terminal completion for host-owned
// requests. Both callbacks must return promptly; discard never runs under the
// pending mutex and cannot wait for simulation ownership.
func (s *Server) queueSimulationActionWithDiscard(action, discard func()) bool {
	if action == nil {
		return false
	}
	s.simulation.pendingMu.Lock()
	defer s.simulation.pendingMu.Unlock()
	if s.shuttingDown.Load() || len(s.simulation.pending) >= maxPendingSimulationActions {
		return false
	}
	s.simulation.pending = append(s.simulation.pending, simulationWork{action, discard})
	return true
}

func (s *Server) drainSimulationActionsOwned() {
	s.simulation.pendingMu.Lock()
	pending := s.simulation.pending
	s.simulation.pending = nil
	s.simulation.pendingMu.Unlock()
	for i, action := range pending {
		if s.shuttingDown.Load() {
			for _, skipped := range pending[i:] {
				if skipped.discard != nil {
					skipped.discard()
				}
			}
			return
		}
		action.run()
	}
}

// discardSimulationActions drops callback requests once shutdown begins. It does
// not take the owner lock, so stopping runtime workers cannot deadlock on it.
func (s *Server) discardSimulationActions() {
	s.simulation.pendingMu.Lock()
	pending := s.simulation.pending
	s.simulation.pending = nil
	s.simulation.pendingMu.Unlock()
	for _, skipped := range pending {
		if skipped.discard != nil {
			skipped.discard()
		}
	}
}
