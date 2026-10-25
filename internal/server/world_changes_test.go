package server

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"mc-go/api/worldview"
	"mc-go/internal/world"
)

func watchTestVolume(t *testing.T, s *Server, request worldview.Request) (worldview.Subscription, worldview.Update) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	core := attachmentCore{frontendCore: frontendCore{s: s}, attachment: &frontendAttachment{ctx: ctx}}
	view, err := core.WatchBlocks(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = view.Close() })
	first := nextTestWorldUpdate(t, view)
	if first.Kind != worldview.Snapshot {
		t.Fatalf("first update=%+v", first)
	}
	return view, first
}

func nextTestWorldUpdate(t *testing.T, view worldview.Subscription) worldview.Update {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	u, err := view.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func noTestWorldUpdate(t *testing.T, view worldview.Subscription) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
	defer cancel()
	if u, err := view.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unexpected update=%+v error=%v", u, err)
	}
}

func TestWorldChangesDirectBulkRestoreAndTick(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	box := worldview.Box{Min: worldview.Pos{X: -17, Y: 10, Z: -1}, Max: worldview.Pos{X: -14, Y: 12, Z: 1}}
	view, snapshot := watchTestVolume(t, s, worldview.Request{Dimension: dimOverworld, Bounds: box})
	if len(snapshot.Blocks) != 12 {
		t.Fatalf("snapshot cells=%d", len(snapshot.Blocks))
	}
	for _, b := range snapshot.Blocks {
		if b != world.Air {
			t.Fatalf("unexpected initial block%d", b)
		}
	}
	last := snapshot.Revision
	check := func(count int, want world.Block) worldview.Update {
		t.Helper()
		u := nextTestWorldUpdate(t, view)
		if u.Kind != worldview.Delta || u.Revision <= last || u.Dimension != dimOverworld || len(u.Changes) != count {
			t.Fatalf("delta=%+v last%d", u, last)
		}
		last = u.Revision
		seen := make(map[worldview.Pos]bool)
		for _, c := range u.Changes {
			if seen[c.Position] || c.State != want {
				t.Fatalf("duplicate or wrong final change=%+v", c)
			}
			seen[c.Position] = true
		}
		return u
	}
	a, b := world.Pos{X: -17, Y: 10, Z: -1}, world.Pos{X: -15, Y: 10, Z: 0}
	s.simulationAction(func() {
		s.world.SetBlock(a, world.Dirt)
		s.world.SetBlock(a, world.Air)
		s.world.SetBlock(a, world.Stone)
		s.world.SetBlock(b, world.Stone)
		s.world.SetBlock(world.Pos{X: -18, Y: 10, Z: -1}, world.Stone)
	})
	check(2, world.Stone)
	s.simulationAction(func() {
		s.world.SetBlock(a, world.Stone)
		s.world.SetBlock(b, world.Air)
		s.world.SetBlock(b, world.Stone)
	})
	noTestWorldUpdate(t, view)
	s.simulationAction(func() { s.world.Restore(nil) })
	check(2, world.Air)
	_ = s.rconRun("fill -17 10 -1 -15 10 0 minecraft:stone")
	check(6, world.Stone)
	// A real scheduled block tick rewrites a raw-set lamp through the
	// redstone pipeline rather than a test-only publication callback.
	lamp, ok := parseBlockName("redstone_lamp[lit=true]")
	if !ok {
		t.Fatal("missing lit lamp")
	}
	s.simulationAction(func() { s.world.SetBlock(a, lamp); s.scheduleBlockTick(s.dims[dimOverworld], a, 1) })
	check(1, lamp)
	s.GameTick()
	s.GameTick()
	lampOff, _ := parseBlockName("redstone_lamp[lit=false]")
	check(1, lampOff)
}

func TestWorldChangesSnapshotRegistrationLinearizesWithOwnedWrites(t *testing.T) {
	s := simulationTestServer(t)
	t.Cleanup(s.Stop)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	core := attachmentCore{frontendCore: frontendCore{s: s}, attachment: &frontendAttachment{ctx: ctx}}
	request := worldview.Request{Dimension: dimOverworld, Bounds: worldview.Box{Min: worldview.Pos{X: -1, Y: 10}, Max: worldview.Pos{X: 1, Y: 11, Z: 1}}}
	s.simulation.mu.Lock()
	var view worldview.Subscription
	var watchErr error
	var workers sync.WaitGroup
	workers.Add(2)
	go func() { defer workers.Done(); view, watchErr = core.WatchBlocks(ctx, request) }()
	go func() {
		defer workers.Done()
		s.simulationAction(func() {
			s.world.SetBlock(world.Pos{X: -1, Y: 10}, world.Stone)
			s.world.SetBlock(world.Pos{X: 0, Y: 10}, world.Stone)
		})
	}()
	s.simulation.mu.Unlock()
	workers.Wait()
	if watchErr != nil {
		t.Fatal(watchErr)
	}
	defer view.Close()
	snapshot := nextTestWorldUpdate(t, view)
	if snapshot.Kind != worldview.Snapshot || len(snapshot.Blocks) != 2 || snapshot.Blocks[0] != snapshot.Blocks[1] {
		t.Fatalf("partial snapshot=%+v", snapshot)
	}
	if snapshot.Blocks[0] == world.Air {
		delta := nextTestWorldUpdate(t, view)
		if delta.Kind != worldview.Delta || len(delta.Changes) != 2 || delta.Revision <= snapshot.Revision {
			t.Fatalf("missing post-snapshot transaction=%+v", delta)
		}
		for _, c := range delta.Changes {
			if c.State != world.Stone {
				t.Fatalf("wrong post-snapshot value=%+v", c)
			}
		}
	} else if snapshot.Blocks[0] != world.Stone {
		t.Fatalf("unexpected snapshot=%+v", snapshot)
	}
	noTestWorldUpdate(t, view)
}
