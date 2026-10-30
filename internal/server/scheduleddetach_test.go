package server

import (
	"sync"
	"testing"

	"mc-go/internal/world"
)

func detachedFixtureKey(x int) schedKey {
	return schedKey{dim: dimOverworld, pos: world.Pos{X: x, Y: -60, Z: 1}, kind: tickBlock, block: "stone_button"}
}

func TestDetachedTicksKeepAbsoluteAndDisappearGlobally(t *testing.T) {
	q := newTickScheduler()
	b := detachedFixtureKey(17)
	chunk := scheduledChunkOf(b)
	q.schedulePrio(b, 500, tickPrioHigh)
	q.detachChunks(map[scheduledChunk]bool{chunk: true})
	q.schedule(detachedFixtureKey(18), 200)
	if q.pending() != 0 || q.isQueued(b) || len(q.poll(100)) != 0 {
		t.Fatal("detached container remained globally visible")
	}
	q.copyBlockTicks(dimOverworld, dimOverworld, b.pos, b.pos, world.Pos{X: -16})
	q.clearBlockArea(dimOverworld, b.pos, b.pos)
	saved, ok := q.snapshotDetachedChunk(chunk, 100)
	if !ok || len(saved.rows) != 1 || saved.rows[0].delay != 400 || saved.rows[0].priority != tickPrioHigh {
		t.Fatalf("detached copy/clear/schedule altered local queue: %#v", saved)
	}
	if !q.reattachChunk(chunk) || !q.isQueued(b) || len(q.poll(499)) != 0 {
		t.Fatal("resident reattachment changed original membership/deadline")
	}
	if got := q.poll(500); len(got) != 1 || got[0] != b {
		t.Fatalf("resident deadline callback %#v", got)
	}
}

func TestDetachedCollectedTicksRemainRunnableAndCopyable(t *testing.T) {
	q := newTickScheduler()
	a, b := detachedFixtureKey(1), detachedFixtureKey(17)
	q.schedule(a, 10)
	q.schedule(b, 10)
	if got := q.poll(10); len(got) != 2 || !q.claimBlockTick(a) {
		t.Fatal("collected fixture")
	}
	q.detachChunks(map[scheduledChunk]bool{scheduledChunkOf(b): true})
	q.copyBlockTicks(dimOverworld, dimOverworld, b.pos, b.pos, world.Pos{X: -16})
	if !q.claimBlockTick(b) || q.pending() != 1 {
		t.Fatal("detachment canceled or hid an already-collected callback")
	}
	saved, _ := q.snapshotDetachedChunk(scheduledChunkOf(b), 10)
	if len(saved.rows) != 0 {
		t.Fatal("detachment resurrected a collected callback as pending storage")
	}
	q.finishBlockTicks()
	if got := q.poll(11); len(got) != 1 || got[0] != a {
		t.Fatalf("captured callback copy %#v", got)
	}
}

func TestDetachedClearCancelsCapturedButKeepsDetachedFuture(t *testing.T) {
	q := newTickScheduler()
	b := detachedFixtureKey(17)
	q.schedule(b, 10)
	q.poll(10)
	q.schedule(b, 20)
	q.detachChunks(map[scheduledChunk]bool{scheduledChunkOf(b): true})
	q.clearBlockArea(dimOverworld, b.pos, b.pos)
	if q.claimBlockTick(b) {
		t.Fatal("explicit area clear failed to cancel a collected callback")
	}
	q.reattachChunk(scheduledChunkOf(b))
	if got := q.poll(20); len(got) != 1 || got[0] != b {
		t.Fatalf("area clear touched detached future work: %#v", got)
	}
}

func TestDetachedSerializationResumesRelativeAndCopiesSnapshots(t *testing.T) {
	q := newTickScheduler()
	b, c := detachedFixtureKey(17), detachedFixtureKey(18)
	q.schedulePrio(b, 500, tickPrioHigh)
	q.deferSavedTick(c, 17, 0)
	chunk := scheduledChunkOf(b)
	q.detachChunks(map[scheduledChunk]bool{chunk: true})
	first, _ := q.snapshotDetachedChunk(chunk, 100)
	first.rows[0].delay = 999
	saved, _ := q.snapshotDetachedChunk(chunk, 100)
	if len(saved.rows) != 2 || saved.rows[0].delay != 17 || saved.rows[1].delay != 400 || !q.discardSerializedChunk(saved) {
		t.Fatalf("serialization snapshot %#v", saved)
	}
	saved.rows[0].delay = 888
	later, _ := q.snapshotDetachedChunk(chunk, 200)
	if later.rows[0].delay != 17 || later.rows[1].delay != 400 || q.pending() != 0 || q.isQueued(c) {
		t.Fatal("serialized storage aged or retained a snapshot alias/global membership")
	}
	q.reattachChunk(chunk)
	q.copyBlockTicks(dimOverworld, dimOverworld, b.pos, c.pos, world.Pos{X: -16})
	q.clearBlockArea(dimOverworld, b.pos, c.pos)
	if q.pending() != 2 || len(q.queued) != 0 || !q.isQueued(b) || !q.isQueued(c) {
		t.Fatal("FULL registration unpacked or cleared pending saved rows")
	}
	q.activateSavedChunk(chunk, 200)
	if len(q.poll(216)) != 0 || len(q.poll(217)) != 1 || len(q.poll(599)) != 0 {
		t.Fatal("pending relative deadline control")
	}
	if got := q.poll(600); len(got) != 1 || got[0].pos != b.pos {
		t.Fatalf("reloaded deadline %#v", got)
	}
}

func TestDetachedReticketRejectsStaleSerialization(t *testing.T) {
	q := newTickScheduler()
	b := detachedFixtureKey(17)
	chunk := scheduledChunkOf(b)
	q.schedule(b, 500)
	first := q.detachChunks(map[scheduledChunk]bool{chunk: true})[chunk]
	old, _ := q.snapshotDetachedChunk(chunk, 100)
	q.reattachChunk(chunk)
	if q.discardSerializedChunk(old) {
		t.Fatal("completed IO discarded a reticketed resident object")
	}
	second := q.detachChunks(map[scheduledChunk]bool{chunk: true})[chunk]
	if second == first || q.discardSerializedChunk(old) {
		t.Fatal("an older incarnation's completion discarded the new resident")
	}
	current, _ := q.snapshotDetachedChunk(chunk, 250)
	if !q.discardSerializedChunk(current) || q.discardSerializedChunk(current) {
		t.Fatal("serialization discard was not fenced and idempotent")
	}
	q.reattachChunk(chunk)
	q.activateSavedChunk(chunk, 300)
	if len(q.poll(549)) != 0 || len(q.poll(550)) != 1 {
		t.Fatal("stale snapshot replaced the current saved delay")
	}
}

func TestDetachedLoadedDuplicatesKeepClearedMembership(t *testing.T) {
	q := newTickScheduler()
	b := detachedFixtureKey(17)
	chunk := scheduledChunkOf(b)
	q.restoreTick(b, 500, 0)
	q.restoreTick(b, 600, tickPrioExtremelyHigh)
	q.detachChunks(map[scheduledChunk]bool{chunk: true})
	saved, _ := q.snapshotDetachedChunk(chunk, 100)
	if len(saved.rows) != 2 || saved.rows[0].delay != 400 || saved.rows[1].delay != 500 || saved.rows[1].priority != tickPrioExtremelyHigh {
		t.Fatalf("duplicate timing/priority lost: %#v", saved)
	}
	q.reattachChunk(chunk)
	q.poll(500)
	if q.isQueued(b) {
		t.Fatal("loaded duplicate poll did not clear membership")
	}
	q.detachChunks(map[scheduledChunk]bool{chunk: true})
	q.reattachChunk(chunk)
	if q.isQueued(b) {
		t.Fatal("resident reattachment revived cleared duplicate membership")
	}
	q.schedule(b, 650)
	if q.pending() != 2 || len(q.poll(600)) != 1 || len(q.poll(650)) != 1 {
		t.Fatal("first request after a loaded duplicate lost its independent record")
	}
}

func TestDetachBatchKeepsUnrelatedOwnerIndexes(t *testing.T) {
	q := newTickScheduler()
	for i := 0; i < 3000; i++ {
		q.schedule(detachedFixtureKey(i*16+1), int64(10000+i))
	}
	b := detachedFixtureKey(1)
	for i := 0; i < 1000; i++ {
		q.restoreTick(b, int64(20000+i), 0)
	}
	if ids := q.detachChunks(map[scheduledChunk]bool{scheduledChunkOf(b): false}); len(ids) != 0 || len(q.detached) != 0 || q.pending() != 4000 {
		t.Fatal("empty selection touched unrelated queued state")
	}
	chunks := map[scheduledChunk]bool{scheduledChunkOf(b): true, scheduledChunkOf(detachedFixtureKey(1500*16 + 1)): true}
	q.detachChunks(chunks)
	if q.pending() != 2998 {
		t.Fatalf("batch pending %d", q.pending())
	}
	for chunk := range chunks {
		q.reattachChunk(chunk)
	}
	if q.pending() != 4000 {
		t.Fatalf("reattached pending %d", q.pending())
	}
	if at, ok := q.queuedAt(b); !ok || at != 10000 {
		t.Fatalf("restored duplicate index %d %v", at, ok)
	}
	other := detachedFixtureKey(1501*16 + 1)
	if at, ok := q.queuedAt(other); !ok || at != 11501 {
		t.Fatalf("unrelated index %d %v", at, ok)
	}
}

func TestDetachedWorldSnapshotsIncludeResidentAndSerializedRows(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	b, c := detachedFixtureKey(17), detachedFixtureKey(19)
	f := schedKey{dim: dimOverworld, pos: world.Pos{X: 18, Y: -60, Z: 1}, kind: tickFluid, fluid: "water"}
	s.ticks.schedule(b, 500)
	s.ticks.schedulePrio(f, 550, tickPrioHigh)
	s.ticks.deferSavedTick(c, 17, tickPrioExtremelyHigh)
	chunk := scheduledChunkOf(b)
	s.ticks.detachChunks(map[scheduledChunk]bool{chunk: true})
	setAge := func(now int64) { s.clock.mu.Lock(); s.clock.count = now; s.clock.mu.Unlock() }
	check := func(blockDelay, fluidDelay int32) {
		t.Helper()
		rows := s.captureScheduledChunkTicksOwned()[dimOverworld][[2]int32{1, 0}]
		if len(rows.Blocks) != 2 || len(rows.Fluids) != 1 || rows.Blocks[0].Pos != c.pos || rows.Blocks[0].Delay != 17 || rows.Blocks[1].Delay != blockDelay || rows.Fluids[0].Delay != fluidDelay || rows.Fluids[0].Type != "minecraft:water" || rows.Fluids[0].Priority != int32(tickPrioHigh) {
			t.Fatalf("detached snapshot %#v", rows)
		}
		rows.Blocks[0].Delay = 999
	}
	setAge(100)
	check(400, 450)
	saved, _ := s.ticks.snapshotDetachedChunk(chunk, 100)
	setAge(150)
	check(350, 400)
	if !s.ticks.discardSerializedChunk(saved) {
		t.Fatal("discard")
	}
	setAge(250)
	check(400, 450)
	check(400, 450)
	if s.ticks.pending() != 0 {
		t.Fatal("world snapshot made detached rows globally visible")
	}
}

func TestDetachedConcurrentCompletionAndReticket(t *testing.T) {
	for i := 0; i < 100; i++ {
		q := newTickScheduler()
		b := detachedFixtureKey(17)
		chunk := scheduledChunkOf(b)
		q.schedule(b, 500)
		q.detachChunks(map[scheduledChunk]bool{chunk: true})
		saved, _ := q.snapshotDetachedChunk(chunk, 100)
		start := make(chan struct{})
		applied := make(chan bool, 1)
		var workers sync.WaitGroup
		workers.Add(2)
		go func() { defer workers.Done(); <-start; applied <- q.discardSerializedChunk(saved) }()
		go func() { defer workers.Done(); <-start; q.reattachChunk(chunk) }()
		close(start)
		workers.Wait()
		deadline := int64(500)
		if <-applied {
			deadline = 600
		}
		if q.pending() != 1 || !q.isQueued(b) || len(q.detached) != 0 {
			t.Fatal("completion race lost or duplicated the container")
		}
		q.activateSavedChunk(chunk, 200)
		if len(q.poll(deadline-1)) != 0 {
			t.Fatal("completion race shifted deadline early")
		}
		if got := q.poll(deadline); len(got) != 1 || got[0].pos != b.pos {
			t.Fatalf("completion race deadline %#v", got)
		}
	}
}
