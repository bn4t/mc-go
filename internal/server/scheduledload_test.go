package server

import (
	"testing"

	"mc-go/internal/world"
)

func TestRestoredTickDuplicatesAndMembership(t *testing.T) {
	q := newTickScheduler()
	k := schedKey{dim: dimOverworld, pos: world.Pos{X: 1, Y: -60, Z: 1}, kind: tickBlock, block: "stone_button"}
	assertDeadline := func(want int64, present bool) {
		t.Helper()
		if at, ok := q.queuedAt(k); at != want || ok != present {
			t.Fatalf("owner deadline got%d,%v want%d,%v", at, ok, want, present)
		}
	}
	q.restoreTick(k, 1100, 0)
	q.restoreTick(k, 1101, -3)
	q.schedule(k, 1090)
	assertDeadline(1100, true)
	if q.pending() != 2 || !q.hasScheduled(k) {
		t.Fatalf("restored duplicates missing or ordinary duplicate added: count%d has%v", q.pending(), q.hasScheduled(k))
	}
	if got := q.poll(1100); len(got) != 1 || got[0].recordID == 0 {
		t.Fatalf("first restored callback %#v", got)
	}
	if q.pending() != 1 || q.hasScheduled(k) {
		t.Fatalf("native removes membership when first duplicate polls: count%d has%v", q.pending(), q.hasScheduled(k))
	}
	assertDeadline(0, false)
	q.schedule(k, 1200)
	assertDeadline(1101, true)
	if q.pending() != 2 || !q.hasScheduled(k) {
		t.Fatal("new request must be accepted after first loaded duplicate polls")
	}
	if got := q.poll(1101); len(got) != 1 {
		t.Fatalf("remaining duplicate lost: %#v", got)
	}
	if q.pending() != 1 {
		t.Fatal("loaded duplicate consumed separately scheduled record")
	}
	if q.hasScheduled(k) {
		t.Fatal("remaining loaded duplicate must clear membership even after a new request")
	}
	assertDeadline(0, false)
	q.schedule(k, 1300)
	assertDeadline(1200, true)
	if q.pending() != 2 {
		t.Fatal("membership-free owner must accept another request while older record remains")
	}
	if got := q.poll(1200); len(got) != 1 || got[0].recordID != 0 {
		t.Fatalf("ordinary request lost %#v", got)
	}
	assertDeadline(0, false)
}

func TestRestoredTickOwnerDeadlineIndexWithUnrelatedQueues(t *testing.T) {
	q := newTickScheduler()
	for i := 0; i < 1000; i++ {
		q.schedule(schedKey{dim: dimOverworld, pos: world.Pos{X: i + 100}, kind: tickBlock, block: "stone_button"}, int64(i+1000))
	}
	k := schedKey{dim: dimOverworld, pos: world.Pos{X: 1, Y: 2, Z: 3}, kind: tickBlock, block: "stone_button"}
	q.restoreTick(k, 100, 0)
	q.restoreTick(k, 80, 0)
	q.restoreTick(k, 120, 0)
	if at, ok := q.queuedAt(k); !ok || at != 80 {
		t.Fatalf("out-of-order restored minimum%d,%v", at, ok)
	}
	q.poll(80)
	q.schedule(k, 200)
	if at, ok := q.queuedAt(k); !ok || at != 100 {
		t.Fatalf("minimum after earliest duplicate poll/new request%d,%v", at, ok)
	}
	q.clearBlockArea(dimOverworld, k.pos, k.pos)
	if _, ok := q.queuedAt(k); ok || q.pending() != 1000 || len(q.ownerDeadlines) != 1000 {
		t.Fatalf("clear retained owner index or removed unrelated callbacks: count%d owners%d", q.pending(), len(q.ownerDeadlines))
	}
}

func TestRestoredTickClearRetainsFluidDomain(t *testing.T) {
	q := newTickScheduler()
	p := world.Pos{X: 1, Y: 2, Z: 3}
	k := schedKey{dim: dimOverworld, pos: p, kind: tickBlock, block: "stone_button"}
	fluid := schedKey{dim: dimOverworld, pos: p, kind: tickFluid, fluid: "water"}
	q.restoreTick(k, 10, 0)
	q.restoreTick(k, 11, -3)
	q.restoreTick(fluid, 12, 0)
	q.clearBlockArea(dimOverworld, p, p)
	if q.pending() != 1 || q.hasScheduled(k) || !q.hasScheduled(fluid) {
		t.Fatal("block clear must remove both loaded duplicates and membership, retaining fluid")
	}
	q.schedule(k, 20)
	if q.pending() != 2 {
		t.Fatal("block clear retained stale loaded membership")
	}
}

func TestRestoredTickCloneDeduplicatesDestinationAndCapturesAllPending(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	k := schedKey{dim: dimOverworld, pos: world.Pos{X: 1, Y: -60, Z: 1}, kind: tickBlock, block: "stone_button"}
	s.ticks.restoreTick(k, 10, 0)
	s.ticks.restoreTick(k, 11, -3)
	s.ticks.copyBlockTicks(dimOverworld, dimOverworld, k.pos, k.pos, world.Pos{X: 16})
	if s.ticks.pending() != 3 {
		t.Fatalf("ordinary clone schedule must deduplicate destination, count%d", s.ticks.pending())
	}
	captured := s.captureScheduledChunkTicksOwned()[dimOverworld]
	if source := captured[[2]int32{}].Blocks; len(source) != 2 || source[0].Delay != 10 || source[1].Delay != 11 {
		t.Fatalf("snapshot lost restored duplicates: %#v", source)
	}
	if dest := captured[[2]int32{1, 0}].Blocks; len(dest) != 1 || dest[0].Delay != 10 || dest[0].Priority != 0 {
		t.Fatalf("clone changed first-request metadata: %#v", dest)
	}
	s.ticks.poll(10)
	s.clock.mu.Lock()
	s.clock.count = 10
	s.clock.mu.Unlock()
	remaining := s.captureScheduledChunkTicksOwned()[dimOverworld][[2]int32{}].Blocks
	if len(remaining) != 1 || remaining[0].Delay != 1 || s.ticks.hasScheduled(k) {
		t.Fatalf("snapshot must preserve remaining duplicate after membership cleared: %#v", remaining)
	}
}

func TestRestoredTickDispatchKeepsSavedOwner(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	p := world.Pos{X: 1, Y: -60, Z: 1}
	oak, ok := world.BlockStateOf("oak_button", map[string]string{"face": "floor", "powered": "true"})
	if !ok {
		t.Fatal("fixture state")
	}
	s.dims[dimOverworld].w.SetBlock(p, oak)
	s.ticks.restoreTick(schedKey{dim: dimOverworld, pos: p, kind: tickBlock, block: "stone_button"}, 0, 0)
	s.scheduledTickStep()
	if got := s.dims[dimOverworld].w.GetBlock(p); got != oak || s.ticks.pending() != 0 {
		t.Fatalf("stale saved owner ran replacement callback: state%d pending%d", got, s.ticks.pending())
	}
}
