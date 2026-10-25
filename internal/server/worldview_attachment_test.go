package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"mc-go/api/worldview"
	"mc-go/internal/world"
)

func testWorldviewCore(s *Server, ctx context.Context) attachmentCore {
	return attachmentCore{frontendCore: frontendCore{s}, attachment: &frontendAttachment{ctx: ctx, label: "test"}}
}

func oneCellWorldview() worldview.Request {
	return worldview.Request{Dimension: dimOverworld, Bounds: worldview.Box{Min: worldview.Pos{Y: -64}, Max: worldview.Pos{X: 1, Y: -63, Z: 1}}}
}

func nextWorldview(t *testing.T, view worldview.Subscription) worldview.Update {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	update, err := view.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return update
}

func TestWorldviewAttachmentSnapshotAndCanonicalCommitDeltas(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	core := testWorldviewCore(s, context.Background())
	request := worldview.Request{Dimension: dimOverworld, Bounds: worldview.Box{Min: worldview.Pos{X: -2, Y: -64, Z: -1}, Max: worldview.Pos{X: 2, Y: -60, Z: 1}}}
	s.simulationAction(func() {
		s.world.SetBlock(world.Pos{X: -1, Y: -62, Z: 0}, world.Stone)
		s.world.SetBlock(world.Pos{X: 1, Y: -63, Z: -1}, world.Air)
	})
	view, err := core.WatchBlocks(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	first := nextWorldview(t, view)
	if first.Kind != worldview.Snapshot || first.Catalog != world.BlockCatalog() || first.Bounds != request.Bounds || len(first.Blocks) != 32 {
		t.Fatalf("initial snapshot=%+v", first)
	}
	for y := -64; y < -60; y++ {
		for z := -1; z < 1; z++ {
			for x := -2; x < 2; x++ {
				index := ((y+64)*2+(z+1))*4 + x + 2
				if expected := s.world.GetBlock(world.Pos{X: x, Y: y, Z: z}); first.Blocks[index] != expected {
					t.Fatalf("x-fastest cell %d,%d,%d=%d want %d", x, y, z, first.Blocks[index], expected)
				}
			}
		}
	}
	// A consumer owns its snapshot array; changing it cannot corrupt the
	// private comparison baseline or manufacture later unchanged deltas.
	first.Blocks[0] = world.Stone
	changed := world.Pos{X: 0, Y: -62, Z: 0}
	reverted := world.Pos{X: -2, Y: -64, Z: -1}
	original := s.world.GetBlock(reverted)
	s.simulationAction(func() {
		s.world.SetBlock(reverted, world.Air)
		s.world.SetBlock(reverted, original)
		s.world.SetBlock(changed, world.Air)
		s.world.SetBlock(changed, world.Stone)
		s.world.SetBlock(world.Pos{X: 99, Y: -62, Z: 99}, world.Stone)
	})
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := view.Next(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled read=%v", err)
	}
	delta := nextWorldview(t, view)
	if delta.Kind != worldview.Delta || delta.Revision <= first.Revision || len(delta.Changes) != 1 || delta.Changes[0] != (worldview.Change{Position: worldview.Pos{X: 0, Y: -62, Z: 0}, State: world.Stone}) {
		t.Fatalf("coalesced canonical delta=%+v", delta)
	}
	if s.world.GetBlock(changed) != world.Stone {
		t.Fatal("consumer snapshot mutation affected canonical storage")
	}
}

func TestWorldviewAttachmentValidationBusyAndCancellation(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	core := testWorldviewCore(s, context.Background())
	for _, request := range []worldview.Request{
		{Dimension: "minecraft:missing", Bounds: oneCellWorldview().Bounds},
		{Dimension: dimOverworld},
		{Dimension: dimOverworld, Bounds: worldview.Box{Min: worldview.Pos{Y: -65}, Max: worldview.Pos{X: 1, Y: -64, Z: 1}}},
		{Dimension: dimOverworld, Bounds: worldview.Box{Min: worldview.Pos{X: -2147483648, Y: -64}, Max: worldview.Pos{X: 2147483647, Y: -63, Z: 1}}},
		{Dimension: dimOverworld, Bounds: worldview.Box{Min: worldview.Pos{Y: -64}, Max: worldview.Pos{X: 257, Y: -63, Z: 256}}},
	} {
		if view, err := core.WatchBlocks(context.Background(), request); err == nil || view != nil {
			t.Fatalf("invalid request admitted: %+v", request)
		}
	}
	if len(s.worldviews) != 0 || s.worldviewBudget.watches != 0 {
		t.Fatal("invalid request reserved a watch")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.simulation.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.simulation.mu.Unlock()
		}
	}()
	result := make(chan error, 1)
	go func() { _, err := core.WatchBlocks(ctx, oneCellWorldview()); result <- err }()
	awaitPlayerAction(t, core.attachment.blockAction.Load)
	if _, err := core.WatchBlocks(context.Background(), oneCellWorldview()); !errors.Is(err, worldview.ErrBusy) {
		t.Fatalf("concurrent setup did not reject promptly: %v", err)
	}
	cancel()
	s.simulation.mu.Unlock()
	locked = false
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled setup=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("setup did not exit after ownership was released")
	}
	if len(s.worldviews) != 0 || s.worldviewBudget.watches != 0 {
		t.Fatal("canceled setup reserved a watch")
	}
}

func TestWorldviewAttachmentCloseKeepsCellQuotaUntilOwnedRemoval(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	core := testWorldviewCore(s, context.Background())
	var views []worldview.Subscription
	for range worldview.MaxPerAttachment {
		view, err := core.WatchBlocks(context.Background(), oneCellWorldview())
		if err != nil {
			t.Fatal(err)
		}
		views = append(views, view)
	}
	s.simulation.mu.Lock()
	locked := true
	defer func() {
		if locked {
			s.simulation.mu.Unlock()
		}
	}()
	for _, view := range views {
		_ = view.Close()
	}
	s.worldviewBudget.mu.Lock()
	watches, cells, bytes := s.worldviewBudget.watches, s.worldviewBudget.cells, s.worldviewBudget.queuedBytes
	s.worldviewBudget.mu.Unlock()
	if watches != worldview.MaxPerAttachment || cells != worldview.MaxPerAttachment || bytes != 0 {
		t.Fatalf("Close released interest quota before cleanup: watches=%d cells=%d bytes=%d", watches, cells, bytes)
	}
	if mailbox, err := newWorldviewMailbox(&s.worldviewBudget, &core.attachment.worldviewOwner, 1, dimOverworld, worldview.Limits{}); err == nil {
		mailbox.Close(worldview.ErrClosed)
		mailbox.releaseWatch()
		t.Fatal("closed watches allowed quota churn while ownership was blocked")
	}
	s.simulation.mu.Unlock()
	locked = false
	// Deterministically drain the same idempotent cleanup used by Close.
	s.simulationAction(func() { s.closeAttachmentWorldviewsOwned(core.attachment) })
	newView, err := core.WatchBlocks(context.Background(), oneCellWorldview())
	if err != nil {
		t.Fatalf("owned removal did not release capacity: %v", err)
	}
	defer newView.Close()
	for _, view := range views {
		_ = view.Close()
	}
	if snapshot := nextWorldview(t, newView); snapshot.Kind != worldview.Snapshot {
		t.Fatal("stale cleanup affected the successor registration")
	}
}

func TestWorldviewAttachmentRetiredCloseStopsCallbacksAndDoesNotAllocate(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	attachment, detach := context.WithCancel(context.Background())
	defer detach()
	lifetime, cancel := context.WithCancel(context.Background())
	defer cancel()
	core := testWorldviewCore(s, attachment)
	subscription, err := core.WatchBlocks(lifetime, oneCellWorldview())
	if err != nil {
		t.Fatal(err)
	}
	view := subscription.(*worldviewSubscription)
	var stopped atomic.Int32
	view.stopsMu.Lock()
	if len(view.stops) != 2 {
		view.stopsMu.Unlock()
		t.Fatal("watch did not bind both lifetime contexts")
	}
	view.stops = append(view.stops, func() bool { stopped.Add(1); return true })
	view.stopsMu.Unlock()
	// Overflow and attachment cleanup retire directly on the owner, before
	// an adapter necessarily calls Close on its retained handle.
	s.simulationAction(func() { s.removeWorldviewOwned(view, worldview.ErrOverflow) })
	view.stopsMu.Lock()
	retained := len(view.stops)
	view.stopsMu.Unlock()
	if retained != 0 || stopped.Load() != 1 {
		t.Fatal("retirement retained registered lifetime callbacks")
	}
	if allocations := testing.AllocsPerRun(100, func() { _ = view.Close() }); allocations != 0 {
		t.Fatalf("already-retired Close allocated cleanup work: %v", allocations)
	}
	if _, err := view.Next(context.Background()); !errors.Is(err, worldview.ErrOverflow) {
		t.Fatalf("retained handle lost terminal cause: %v", err)
	}
}
