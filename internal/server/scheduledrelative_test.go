package server

import (
	"testing"

	"mc-go/internal/world"
)

func TestRelativeSavedTicksActivateOnceAndKeepDuplicates(t *testing.T) {
	q := newTickScheduler()
	k := schedKey{dim: dimOverworld, pos: world.Pos{X: 1}, kind: tickBlock, block: "stone_button"}
	q.deferSavedTick(k, 17, 0)
	q.deferSavedTick(k, 18, -3)
	q.schedule(k, 1)
	if q.pending() != 2 || !q.hasScheduled(k) {
		t.Fatal("saved membership did not suppress ordinary duplicate")
	}
	if len(q.poll(100)) != 0 {
		t.Fatal("relative records aged before activation")
	}
	q.clearBlockArea(dimOverworld, k.pos, k.pos)
	q.copyBlockTicks(dimOverworld, dimOverworld, k.pos, k.pos, world.Pos{X: 16})
	if q.pending() != 2 {
		t.Fatal("clear/copy touched unactivated saved records")
	}
	q.activateSavedChunk(scheduledChunkOf(k), 100)
	q.activateSavedChunk(scheduledChunkOf(k), 200)
	if q.pending() != 2 || len(q.poll(116)) != 0 {
		t.Fatal("activation dropped duplicates or changed deadline")
	}
	if got := q.poll(117); len(got) != 1 || q.hasScheduled(k) {
		t.Fatal("first restored duplicate/membership mismatch")
	}
	if got := q.poll(118); len(got) != 1 {
		t.Fatal("second restored duplicate lost")
	}
}

func TestEligiblePollingKeepsBlockedAbsoluteDeadlineAndMembership(t *testing.T) {
	q := newTickScheduler()
	k := schedKey{dim: dimOverworld, pos: world.Pos{X: 1}, kind: tickBlock, block: "stone_button"}
	q.deferSavedTick(k, 17, 0)
	q.activateSavedChunk(scheduledChunkOf(k), 100)
	if got := q.pollEligible(140, map[scheduledChunk]bool{}); len(got) != 0 || !q.hasScheduled(k) {
		t.Fatal("blocked due entry was collected")
	}
	if at, ok := q.queuedAt(k); !ok || at != 117 {
		t.Fatalf("blocked deadline changed: %d,%v", at, ok)
	}
	q.schedule(k, 141)
	if q.pending() != 1 {
		t.Fatal("blocked membership stopped deduplicating")
	}
	if got := q.pollEligible(141, map[scheduledChunk]bool{scheduledChunkOf(k): true}); len(got) != 1 {
		t.Fatal("overdue record did not fire on eligibility")
	}
}

func TestRelativeSnapshotDoesNotAgeAndIncludesBothPhases(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	k := schedKey{dim: dimOverworld, pos: world.Pos{X: 1}, kind: tickBlock, block: "stone_button"}
	second := k
	second.pos.X = 2
	s.ticks.deferSavedTick(k, 17, 0)
	s.ticks.deferSavedTick(k, 18, -3)
	s.ticks.restoreTick(second, 117, 0)
	s.clock.mu.Lock()
	s.clock.count = 140
	s.clock.mu.Unlock()
	rows := s.captureScheduledChunkTicksOwned()[dimOverworld][[2]int32{}].Blocks
	if len(rows) != 3 || rows[0].Delay != 17 || rows[1].Delay != 18 || rows[2].Delay != -23 {
		t.Fatalf("snapshot phases/order %#v", rows)
	}
	s.ticks.activateSavedChunk(scheduledChunkOf(k), 150)
	if rows[0].Delay != 17 || rows[1].Delay != 18 {
		t.Fatal("snapshot retained mutable pending data")
	}
}

func TestEligiblePollingPartitionsManyBlockedRecordsOnce(t *testing.T) {
	q := newTickScheduler()
	for i := 0; i < 1000; i++ {
		q.schedule(schedKey{dim: dimOverworld, pos: world.Pos{X: (100 + i) * 16}, kind: tickBlock, block: "stone_button"}, 10)
	}
	for i, priority := range []int8{0, -1, -3} {
		q.schedulePrio(schedKey{dim: dimOverworld, pos: world.Pos{X: i + 1}, kind: tickBlock, block: "stone_button"}, 10, priority)
	}
	got := q.pollEligible(10, map[scheduledChunk]bool{{dim: dimOverworld}: true})
	if len(got) != 3 || got[0].pos.X != 3 || got[1].pos.X != 2 || got[2].pos.X != 1 {
		t.Fatalf("eligible priority order %#v", got)
	}
	if q.pending() != 1000 || len(q.active) != 3 {
		t.Fatalf("blocked records collected: pending%d active%d", q.pending(), len(q.active))
	}
	for i := 0; i < 1000; i++ {
		key := schedKey{dim: dimOverworld, pos: world.Pos{X: (100 + i) * 16}, kind: tickBlock, block: "stone_button"}
		if at, ok := q.queuedAt(key); !ok || at != 10 || !q.hasScheduled(key) {
			t.Fatalf("blocked record %d lost deadline/membership", i)
		}
	}
	got = q.pollEligible(20, map[scheduledChunk]bool{{dim: dimOverworld, x: 101}: true})
	if len(got) != 1 || got[0].pos.X != 101*16 || q.pending() != 999 {
		t.Fatalf("newly eligible overdue record %#v pending%d", got, q.pending())
	}
}
