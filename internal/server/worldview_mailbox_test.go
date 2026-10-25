package server

import (
	"context"
	"errors"
	"math"
	"sync"
	"testing"
	"time"

	"mc-go/api/registry"
	"mc-go/api/worldview"
)

func worldviewTestCatalog(t *testing.T) *registry.BlockCatalog {
	t.Helper()
	air, _ := registry.NewStateKey("minecraft:air", nil)
	stone, _ := registry.NewStateKey("minecraft:stone", nil)
	catalog, err := registry.NewBlockCatalog([]registry.BlockDefinition{{State: air, Default: true}, {State: stone, Default: true}})
	if err != nil {
		t.Fatal(err)
	}
	return catalog
}

func worldviewTestMailbox(t *testing.T, b *worldviewBudget, owner *worldviewOwner, box worldview.Box, limits worldview.Limits) *worldviewMailbox {
	t.Helper()
	cells, err := worldviewCellCount(box)
	if err != nil {
		t.Fatal(err)
	}
	m, err := newWorldviewMailbox(b, owner, cells, "minecraft:overworld", limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.initialize(worldview.Update{Revision: 10, Kind: worldview.Snapshot, Dimension: "minecraft:overworld", Bounds: box, Catalog: worldviewTestCatalog(t), Blocks: make([]registry.BlockStateID, int(cells))}); err != nil {
		m.releaseWatch()
		t.Fatal(err)
	}
	t.Cleanup(m.releaseWatch)
	return m
}

func worldviewTestNext(t *testing.T, m *worldviewMailbox) worldview.Update {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	u, err := m.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestWorldviewMailboxInitialReserveBeforeAllocation(t *testing.T) {
	b := &worldviewBudget{}
	owner := &worldviewOwner{}
	for _, cells := range []int64{-1, 0, worldview.MaxCells + 1, math.MaxInt64} {
		if allocs := testing.AllocsPerRun(10, func() {
			m, err := newWorldviewMailbox(b, owner, cells, "minecraft:overworld", worldview.Limits{})
			if m != nil || !errors.Is(err, worldview.ErrInvalidBounds) {
				panic("bad cell reservation admitted")
			}
		}); allocs != 0 {
			t.Fatalf("invalid snapshot allocated %v objects", allocs)
		}
	}
	if allocs := testing.AllocsPerRun(10, func() {
		m, err := newWorldviewMailbox(b, owner, worldview.MaxCells, "minecraft:overworld", worldview.Limits{Bytes: 128})
		if m != nil || !errors.Is(err, worldview.ErrOverflow) {
			panic("underbudget snapshot admitted")
		}
	}); allocs != 0 {
		t.Fatalf("underbudget snapshot allocated %v objects", allocs)
	}
	if b.watches != 0 || b.cells != 0 || b.queuedBytes != 0 {
		t.Fatal("rejected snapshot reserved host state")
	}
}

func TestWorldviewMailboxDetachedSnapshotDeltaAndNoops(t *testing.T) {
	b := &worldviewBudget{}
	box := worldview.Box{Min: worldview.Pos{X: -1, Y: -2, Z: -3}, Max: worldview.Pos{X: 1, Y: 0, Z: -1}}
	m := worldviewTestMailbox(t, b, &worldviewOwner{}, box, worldview.Limits{})
	first := worldviewTestNext(t, m)
	if first.Kind != worldview.Snapshot || first.Revision != 10 || len(first.Blocks) != 8 || first.Catalog != m.catalog {
		t.Fatal("snapshot baseline missing")
	}
	first.Blocks[0] = 1
	if m.baseline[0] != 0 {
		t.Fatal("consumer mutated canonical watch baseline")
	}
	changes := []worldview.Change{{Position: box.Min, State: 1}, {Position: worldview.Pos{X: 500}, State: 1}, {Position: worldview.Pos{X: 0, Y: -2, Z: -3}, State: 0}}
	if !m.tryPublishDelta(12, changes) {
		t.Fatal(m.Err())
	}
	changes[0].State = 0
	delta := worldviewTestNext(t, m)
	if delta.Kind != worldview.Delta || delta.Revision != 12 || len(delta.Changes) != 1 || delta.Changes[0].Position != box.Min || delta.Changes[0].State != 1 {
		t.Fatalf("delta=%+v", delta)
	}
	delta.Changes[0].State = 0
	if m.baseline[0] != 1 {
		t.Fatal("consumer mutated baseline through delta")
	}
	if !m.tryPublishDelta(13, []worldview.Change{{Position: box.Min, State: 1}}) || len(m.queue) != 0 || b.queuedBytes != 0 {
		t.Fatal("no-op consumed queue quota")
	}
	if !m.tryPublishDelta(14, []worldview.Change{{Position: box.Min, State: 0}}) {
		t.Fatal(m.Err())
	}
	if u := worldviewTestNext(t, m); u.Changes[0].State != 0 || u.Revision != 14 {
		t.Fatal("later revision/revert missing")
	}
}

func TestWorldviewMailboxFiltersBeforeDeltaReservation(t *testing.T) {
	b := &worldviewBudget{}
	box := worldview.Box{Max: worldview.Pos{X: 1, Y: 1, Z: 1}}
	m := worldviewTestMailbox(t, b, &worldviewOwner{}, box, worldview.Limits{Messages: 1})
	// Snapshot fills the message quota. Irrelevant/no-op dirt must not close it.
	if allocs := testing.AllocsPerRun(10, func() {
		if !m.tryPublishDelta(11, []worldview.Change{{Position: worldview.Pos{X: 5}, State: 1}, {Position: worldview.Pos{}, State: 0}}) {
			panic("noop overflowed watch")
		}
	}); allocs != 0 {
		t.Fatalf("noop batch allocated %v objects", allocs)
	}
	if m.Err() != nil || len(m.queue) != 1 {
		t.Fatal("no-op altered stream")
	}
	if m.tryPublishDelta(12, []worldview.Change{{Position: worldview.Pos{}, State: 1}}) || !errors.Is(m.Err(), worldview.ErrOverflow) {
		t.Fatal("effective delta bypassed message limit")
	}
	if b.queuedBytes != 0 || b.watches != 1 || b.cells != 1 || m.baseline == nil {
		t.Fatal("overflow released interest quota before cleanup")
	}
	m.releaseWatch()
	m.releaseWatch()
	if b.watches != 0 || b.cells != 0 || m.baseline != nil {
		t.Fatal("owned cleanup leaked baseline/cells")
	}
}

func TestWorldviewMailboxGlobalOverflowIsolatesOnlyWatch(t *testing.T) {
	box := worldview.Box{Max: worldview.Pos{X: 1, Y: 1, Z: 1}}
	b := &worldviewBudget{limits: worldviewBudgetLimits{messages: 3}}
	slow := worldviewTestMailbox(t, b, &worldviewOwner{}, box, worldview.Limits{})
	healthy := worldviewTestMailbox(t, b, &worldviewOwner{}, box, worldview.Limits{})
	_ = worldviewTestNext(t, healthy)
	if !slow.tryPublishDelta(11, []worldview.Change{{Position: worldview.Pos{}, State: 1}}) {
		t.Fatal(slow.Err())
	}
	if !healthy.tryPublishDelta(11, []worldview.Change{{Position: worldview.Pos{}, State: 1}}) {
		t.Fatal(healthy.Err())
	}
	// All three global message slots are held. This publication retires only
	// slow; its queued reservations become available to healthy immediately.
	if slow.tryPublishDelta(12, []worldview.Change{{Position: worldview.Pos{}, State: 0}}) || !errors.Is(slow.Err(), worldview.ErrOverflow) {
		t.Fatal("global message limit not enforced")
	}
	if healthy.Err() != nil || worldviewTestNext(t, healthy).Revision != 11 {
		t.Fatal("other watch was affected")
	}
	if !healthy.tryPublishDelta(13, []worldview.Change{{Position: worldview.Pos{}, State: 0}}) || worldviewTestNext(t, healthy).Revision != 13 {
		t.Fatal("healthy ordering stopped")
	}
}

func TestWorldviewMailboxDeltaLocalByteBudgetBeforeCopy(t *testing.T) {
	dimension := "minecraft:overworld"
	snapshotBytes := worldviewUpdateBytes + 4 + int64(len(dimension))
	deltaBytes := worldviewUpdateBytes + worldviewChangeBytes + int64(len(dimension))
	b := &worldviewBudget{}
	m := worldviewTestMailbox(t, b, &worldviewOwner{}, worldview.Box{Max: worldview.Pos{X: 1, Y: 1, Z: 1}}, worldview.Limits{Bytes: snapshotBytes + deltaBytes - 1})
	if m.tryPublishDelta(11, []worldview.Change{{State: 1}}) || !errors.Is(m.Err(), worldview.ErrOverflow) {
		t.Fatal("delta bypassed local byte budget")
	}
	if m.baseline[0] != 0 || m.queue != nil || b.queuedBytes != 0 || b.cells != 1 {
		t.Fatal("rejected delta copied/committed state or leaked queued reservation")
	}
}

func TestWorldviewMailboxMaximumBoxFitsDefaultSnapshotAndFullDelta(t *testing.T) {
	box := worldview.Box{Max: worldview.Pos{X: 64, Y: 16, Z: 64}}
	b := &worldviewBudget{}
	m := worldviewTestMailbox(t, b, &worldviewOwner{}, box, worldview.Limits{})
	changes := make([]worldview.Change, 0, worldview.MaxCells)
	for y := int32(0); y < 16; y++ {
		for z := int32(0); z < 64; z++ {
			for x := int32(0); x < 64; x++ {
				changes = append(changes, worldview.Change{Position: worldview.Pos{X: x, Y: y, Z: z}, State: 1})
			}
		}
	}
	if !m.tryPublishDelta(11, changes) {
		t.Fatal(m.Err())
	}
	if b.queuedBytes > defaultWorldviewQueueBytes || b.queuedMessages != 2 {
		t.Fatalf("legitimate maximum batch exceeds defaults: %d bytes", b.queuedBytes)
	}
	if first := worldviewTestNext(t, m); len(first.Blocks) != worldview.MaxCells {
		t.Fatal("maximum snapshot truncated")
	}
	if delta := worldviewTestNext(t, m); len(delta.Changes) != worldview.MaxCells {
		t.Fatal("maximum delta truncated")
	}
	if b.queuedBytes != 0 || b.cells != worldview.MaxCells {
		t.Fatal("consumer ownership accounting wrong")
	}
}

func TestWorldviewMailboxCancellationCloseAndOwnedQuotaRelease(t *testing.T) {
	b := &worldviewBudget{}
	m := worldviewTestMailbox(t, b, &worldviewOwner{}, worldview.Box{Max: worldview.Pos{X: 1, Y: 1, Z: 1}}, worldview.Limits{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if m.Err() != nil || worldviewTestNext(t, m).Kind != worldview.Snapshot {
		t.Fatal("read cancellation consumed/retired watch")
	}
	result := make(chan error, 1)
	go func() { _, err := m.Next(context.Background()); result <- err }()
	m.Close(nil)
	m.Close(nil)
	select {
	case err := <-result:
		if !errors.Is(err, worldview.ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not wake reader")
	}
	if b.queuedBytes != 0 || b.watches != 1 || b.cells != 1 {
		t.Fatal("close freed lifetime resources before owned removal")
	}
	m.releaseWatch()
	if b.watches != 0 || b.cells != 0 {
		t.Fatal("owned removal failed")
	}
}

func TestWorldviewMailboxConcurrentPublishNextCloseRelease(t *testing.T) {
	for range 20 {
		b := &worldviewBudget{}
		m := worldviewTestMailbox(t, b, &worldviewOwner{}, worldview.Box{Max: worldview.Pos{X: 1, Y: 1, Z: 1}}, worldview.Limits{})
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			for rev := uint64(11); rev < 100; rev++ {
				if !m.tryPublishDelta(rev, []worldview.Change{{Position: worldview.Pos{}, State: registry.BlockStateID(rev % 2)}}) {
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			var last uint64
			for {
				update, err := m.Next(context.Background())
				if err != nil {
					return
				}
				if update.Revision <= last {
					t.Error("revision rewind")
				}
				last = update.Revision
			}
		}()
		go func() { defer wg.Done(); m.Close(nil); m.releaseWatch(); m.releaseWatch() }()
		wg.Wait()
		if b.watches != 0 || b.cells != 0 || b.queuedBytes != 0 || b.queuedMessages != 0 || m.baseline != nil {
			t.Fatal("concurrent cleanup leaked quota")
		}
	}
}

func TestWorldviewMailboxInvalidCanonicalDataAndOrderCloseOnlyWatch(t *testing.T) {
	for _, mode := range []string{"rewind", "unknown-state", "wrong-catalog"} {
		t.Run(mode, func(t *testing.T) {
			m := worldviewTestMailbox(t, &worldviewBudget{}, &worldviewOwner{}, worldview.Box{Max: worldview.Pos{X: 1, Y: 1, Z: 1}}, worldview.Limits{})
			var accepted bool
			switch mode {
			case "rewind":
				accepted = m.tryPublishDelta(10, []worldview.Change{{State: 1}})
			case "unknown-state":
				accepted = m.tryPublishDelta(11, []worldview.Change{{State: 99}})
			case "wrong-catalog":
				accepted = m.tryPublish(worldview.Update{Revision: 11, Kind: worldview.Delta, Dimension: m.dimension, Bounds: m.bounds, Catalog: worldviewTestCatalog(t), Changes: []worldview.Change{{State: 1}}})
			}
			if accepted || m.Err() == nil || m.queue != nil {
				t.Fatal("invalid publication entered queue")
			}
		})
	}
}

func TestWorldviewMailboxCheckedBoundsArithmetic(t *testing.T) {
	for _, box := range []worldview.Box{{}, {Min: worldview.Pos{X: math.MinInt32, Y: math.MinInt32, Z: math.MinInt32}, Max: worldview.Pos{X: math.MaxInt32, Y: math.MaxInt32, Z: math.MaxInt32}}, {Max: worldview.Pos{X: 65536, Y: 65536, Z: 65536}}} {
		if _, err := worldviewCellCount(box); !errors.Is(err, worldview.ErrInvalidBounds) {
			t.Fatal("overflow/empty bounds admitted")
		}
	}
}
