package server

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"mc-go/api/player"
)

func mailboxState(n byte) player.State {
	return player.State{ID: player.ID{n}, Profile: player.Profile{Name: "Player"}, Dimension: "minecraft:overworld", EntityID: int32(n)}
}

func mailboxInitial(t *testing.T, limits player.Limits, revision uint64, states ...player.State) *playerMailbox {
	t.Helper()
	var text int64
	for _, state := range states {
		text += int64(len(state.Profile.Name) + len(state.Dimension))
	}
	m, err := newPlayerMailbox(limits, len(states), text)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.initialize(player.Update{Revision: revision, Kind: player.Snapshot, Players: states}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(nil) })
	return m
}

func mailboxNext(t *testing.T, m *playerMailbox) player.Update {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	update, err := m.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return update
}

func TestPlayerReplicationMailboxSnapshotReservationBeforeAllocation(t *testing.T) {
	for _, tc := range []struct {
		count int
		text  int64
	}{{1, 1 << 20}, {-1, 0}, {0, -1}, {math.MaxInt, math.MaxInt64}, {0, math.MaxInt64}} {
		allocs := testing.AllocsPerRun(10, func() {
			m, err := newPlayerMailbox(player.Limits{Messages: 1, Bytes: 256}, tc.count, tc.text)
			if m != nil || !errors.Is(err, player.ErrOverflow) {
				panic("invalid reservation admitted")
			}
		})
		if allocs != 0 {
			t.Fatalf("rejected reservation allocated %v objects", allocs)
		}
	}
	m, err := newPlayerMailbox(player.Limits{}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if m.queuedBytes != playerMailboxUpdateBytes || m.initial != playerMailboxUpdateBytes {
		t.Fatal("initial snapshot was not reserved")
	}
	if err := m.initialize(player.Update{Kind: player.Snapshot, Players: []player.State{mailboxState(1)}}); !errors.Is(err, player.ErrOverflow) {
		t.Fatalf("under-reserved snapshot: %v", err)
	}
	if m.queue != nil || m.queuedBytes != 0 {
		t.Fatal("failed initialization retained snapshot")
	}
}

func TestPlayerReplicationMailboxSnapshotFirstOrderedDetachedValues(t *testing.T) {
	states := []player.State{mailboxState(1), mailboxState(2)}
	m := mailboxInitial(t, player.Limits{}, 10, states...)
	states[0].EntityID = 999
	states[1].Profile.Name = "mutated"
	kinds := []player.UpdateKind{player.Added, player.Moved, player.Correction, player.Removed}
	for i, kind := range kinds {
		update := player.Update{Revision: uint64(11 + i), Kind: kind, Player: mailboxState(3), Reason: strings.Repeat("reason", 5)}
		if !m.tryPublish(update) {
			t.Fatalf("publish %v: %v", kind, m.Err())
		}
	}
	initial := mailboxNext(t, m)
	if initial.Kind != player.Snapshot || initial.Revision != 10 || len(initial.Players) != 2 || initial.Players[0].EntityID != 1 || initial.Players[1].Profile.Name != "Player" {
		t.Fatalf("initial snapshot=%+v", initial)
	}
	if unsafe.StringData(initial.Players[0].Profile.Name) == unsafe.StringData(states[0].Profile.Name) || unsafe.StringData(initial.Players[0].Dimension) == unsafe.StringData(states[0].Dimension) {
		t.Fatal("snapshot retained source string storage")
	}
	initial.Players[0].EntityID = 333
	if states[0].EntityID != 999 {
		t.Fatal("consumer mutated producer slice")
	}
	for i, kind := range kinds {
		update := mailboxNext(t, m)
		if update.Kind != kind || update.Revision != uint64(11+i) || update.Player.ID != (player.ID{3}) {
			t.Fatalf("delta=%+v", update)
		}
	}
	if m.queuedBytes != 0 || m.head != 0 || len(m.queue) != 0 {
		t.Fatal("dequeue did not release queued charge")
	}
}

func TestPlayerReplicationMailboxOverflowIsolatesSlowConsumer(t *testing.T) {
	slow := mailboxInitial(t, player.Limits{Messages: 2}, 1, mailboxState(1))
	healthy := mailboxInitial(t, player.Limits{Messages: 2}, 1, mailboxState(1))
	_ = mailboxNext(t, healthy)
	for revision := uint64(2); revision <= 3; revision++ {
		update := player.Update{Revision: revision, Kind: player.Moved, Player: mailboxState(1)}
		accepted := slow.tryPublish(update)
		if accepted != (revision == 2) {
			t.Fatalf("slow accepted revision %d=%v", revision, accepted)
		}
		if !healthy.tryPublish(update) {
			t.Fatal("slow consumer affected healthy mailbox")
		}
		if got := mailboxNext(t, healthy); got.Revision != revision {
			t.Fatal("healthy revision out of order")
		}
	}
	if !errors.Is(slow.Err(), player.ErrOverflow) || healthy.Err() != nil {
		t.Fatal("overflow was not isolated")
	}
	if slow.queue != nil || slow.queuedBytes != 0 {
		t.Fatal("terminal overflow retained queued values")
	}
	if _, err := slow.Next(context.Background()); !errors.Is(err, player.ErrOverflow) {
		t.Fatalf("overflow terminal=%v", err)
	}
	slow.Close(player.ErrClosed)
	if !errors.Is(slow.Err(), player.ErrOverflow) {
		t.Fatal("cleanup replaced original terminal reason")
	}
}

func TestPlayerReplicationMailboxBytesAndConsumerOwnership(t *testing.T) {
	initialSize, _ := playerSnapshotReservation(0, 0)
	update := player.Update{Revision: 2, Kind: player.Moved, Player: mailboxState(1), Reason: strings.Repeat("x", 37)}
	size, _ := playerUpdateBytes(update)
	m := mailboxInitial(t, player.Limits{Bytes: size}, 1)
	if m.queuedBytes != initialSize {
		t.Fatal("initial queued bytes missing")
	}
	initial := mailboxNext(t, m)
	if !m.tryPublish(update) {
		t.Fatal("dequeued snapshot still consumed quota")
	}
	got := mailboxNext(t, m)
	if unsafe.StringData(got.Reason) == unsafe.StringData(update.Reason) {
		t.Fatal("delta reason retained producer storage")
	}
	if m.queuedBytes != 0 || initial.Kind != player.Snapshot || got.Reason != update.Reason {
		t.Fatal("checked-out values remained queue charged")
	}
	// A retained consumer value is outside the explicitly queued-only quota.
	update.Revision++
	if !m.tryPublish(update) {
		t.Fatal("checked-out value blocked new admission")
	}
	update.Revision++
	if m.tryPublish(update) || !errors.Is(m.Err(), player.ErrOverflow) {
		t.Fatal("byte budget did not terminate lagging subscriber")
	}
}

func TestPlayerReplicationMailboxNextCancellationPreservesQueue(t *testing.T) {
	m := mailboxInitial(t, player.Limits{}, 7, mailboxState(1))
	before := m.queuedBytes
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("read cancellation=%v", err)
	}
	if m.Err() != nil || m.queuedBytes != before || mailboxNext(t, m).Revision != 7 {
		t.Fatal("canceled read consumed or closed subscription")
	}
	ctx, cancel = context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := m.Next(ctx); result <- err }()
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("Next ignored cancellation")
	}
	if m.Err() != nil {
		t.Fatal("read cancellation retired connection")
	}
}

func TestPlayerReplicationMailboxCloseUnblocksWaiterAndIsIdempotent(t *testing.T) {
	m := mailboxInitial(t, player.Limits{}, 1)
	_ = mailboxNext(t, m)
	result := make(chan error, 1)
	go func() { _, err := m.Next(context.Background()); result <- err }()
	m.Close(nil)
	m.Close(nil)
	select {
	case err := <-result:
		if !errors.Is(err, player.ErrClosed) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("close did not wake Next")
	}
	select {
	case <-m.Done():
	default:
		t.Fatal("terminal signal missing")
	}
	if m.tryPublish(player.Update{Revision: 2, Kind: player.Removed}) {
		t.Fatal("closed mailbox admitted update")
	}
}

func TestPlayerReplicationMailboxConcurrentPublishReadAndStop(t *testing.T) {
	for range 20 {
		m := mailboxInitial(t, player.Limits{}, 0)
		var wg sync.WaitGroup
		wg.Add(3)
		go func() {
			defer wg.Done()
			for revision := uint64(1); revision < 100; revision++ {
				if !m.tryPublish(player.Update{Revision: revision, Kind: player.Moved, Player: mailboxState(1)}) {
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			var last uint64
			first := true
			for {
				update, err := m.Next(context.Background())
				if err != nil {
					return
				}
				if (!first && update.Revision <= last) || (first && update.Kind != player.Snapshot) {
					t.Error("concurrent delivery lost snapshot/order")
				}
				first = false
				last = update.Revision
			}
		}()
		go func() { defer wg.Done(); m.Close(player.ErrClosed) }()
		wg.Wait()
		if m.queue != nil || m.queuedBytes != 0 {
			t.Fatal("concurrent stop leaked retained values")
		}
	}
}

func TestPlayerReplicationMailboxRejectsRevisionRewind(t *testing.T) {
	for _, update := range []player.Update{{Revision: 9, Kind: player.Moved}, {Revision: 10, Kind: player.Moved}, {Revision: 11, Kind: player.Snapshot}} {
		m := mailboxInitial(t, player.Limits{}, 10)
		if m.tryPublish(update) || !errors.Is(m.Err(), errPlayerMailboxOrder) {
			t.Fatal("invalid stream transition admitted")
		}
	}
}
