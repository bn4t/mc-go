package server

import "testing"

func TestSimulationDiscardCompletesPendingWithoutReenteringOwner(t *testing.T) {
	s := simulationTestServer(t)
	run, discarded := 0, 0
	if !s.queueSimulationActionWithDiscard(func() { run++ }, func() {
		discarded++
		// This would deadlock if discard still held pendingMu.
		if !s.queueSimulationAction(func() { run++ }) {
			t.Error("discard callback could not enqueue new work")
		}
	}) {
		t.Fatal("queue rejected")
	}
	s.discardSimulationActions()
	if run != 0 || discarded != 1 {
		t.Fatalf("run%d discard%d", run, discarded)
	}
	s.simulationAction(func() {})
	s.discardSimulationActions()
	if run != 1 || discarded != 1 {
		t.Fatalf("completion repeated: run%d discard%d", run, discarded)
	}
}

func TestSimulationShutdownDiscardsRestOfDrainedBatchExactlyOnce(t *testing.T) {
	s := simulationTestServer(t)
	run, discarded := 0, 0
	if !s.queueSimulationActionWithDiscard(func() { run++; s.shuttingDown.Store(true) }, func() { t.Error("executed work was discarded") }) {
		t.Fatal("queue rejected")
	}
	for i := 0; i < 3; i++ {
		if !s.queueSimulationActionWithDiscard(func() { t.Error("shutdown ran queued work") }, func() { discarded++ }) {
			t.Fatal("queue rejected")
		}
	}
	s.simulationAction(func() {})
	s.discardSimulationActions()
	if run != 1 || discarded != 3 {
		t.Fatalf("terminal outcomes run%d discard%d", run, discarded)
	}
}
