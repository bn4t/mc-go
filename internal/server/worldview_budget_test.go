package server

import (
	"errors"
	"sync"
	"testing"

	"mc-go/api/worldview"
)

func TestWorldviewBudgetAdmissionLimitsDistinctFromStreamOverflow(t *testing.T) {
	b := &worldviewBudget{limits: worldviewBudgetLimits{watches: 2, cells: 8, bytes: 1000, messages: 4}}
	owner := &worldviewOwner{}
	r, err := b.reserveWatch(owner, 8, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer r.releaseWatch()
	if _, err := b.reserveWatch(&worldviewOwner{}, 1, 100); !errors.Is(err, worldview.ErrRejected) || errors.Is(err, worldview.ErrOverflow) {
		t.Fatalf("global admission error=%v", err)
	}
	if !r.reserveQueue(900) || r.reserveQueue(1) {
		t.Fatal("global queue byte bound failed")
	}
	if b.queuedBytes != 1000 || b.queuedMessages != 2 {
		t.Fatal("failed reservation changed accounting")
	}
	r.closeQueue()
	if b.queuedBytes != 0 || b.queuedMessages != 0 || b.cells != 8 || b.watches != 1 {
		t.Fatal("Close released persistent interest quota early")
	}
	if _, err := b.reserveWatch(&worldviewOwner{}, 1, 100); !errors.Is(err, worldview.ErrRejected) {
		t.Fatal("close/reopen bypassed cell reservation")
	}
	r.releaseWatch()
	second, err := b.reserveWatch(&worldviewOwner{}, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	second.releaseWatch()
	if b.watches != 0 || b.cells != 0 || b.queuedBytes != 0 || b.queuedMessages != 0 || len(b.owners) != 0 {
		t.Fatal("reservation release leaked quota")
	}
}

func TestWorldviewBudgetPerAttachmentAndGlobalCount(t *testing.T) {
	b := &worldviewBudget{limits: worldviewBudgetLimits{watches: 5}}
	owner := &worldviewOwner{}
	var reservations []*worldviewReservation
	for range worldview.MaxPerAttachment {
		r, err := b.reserveWatch(owner, 1, 1)
		if err != nil {
			t.Fatal(err)
		}
		reservations = append(reservations, r)
	}
	if _, err := b.reserveWatch(owner, 1, 1); !errors.Is(err, worldview.ErrRejected) {
		t.Fatal("attachment watch limit bypassed")
	}
	r, err := b.reserveWatch(&worldviewOwner{}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	reservations = append(reservations, r)
	if _, err := b.reserveWatch(&worldviewOwner{}, 1, 1); !errors.Is(err, worldview.ErrRejected) {
		t.Fatal("global watch limit bypassed")
	}
	for _, r := range reservations {
		r.releaseWatch()
		r.releaseWatch()
	}
	if b.watches != 0 || len(b.owners) != 0 {
		t.Fatal("idempotent release corrupted watch counts")
	}
}

func TestWorldviewBudgetGlobalMessageLimit(t *testing.T) {
	b := &worldviewBudget{limits: worldviewBudgetLimits{messages: 2}}
	r, err := b.reserveWatch(&worldviewOwner{}, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer r.releaseWatch()
	if !r.reserveQueue(1) || r.reserveQueue(1) {
		t.Fatal("global message limit bypassed")
	}
	r.releaseQueued(1, 1)
	if !r.reserveQueue(1) {
		t.Fatal("dequeue did not return global message capacity")
	}
	r.closeQueue()
	if r.reserveQueue(1) || b.queuedMessages != 0 || b.queuedBytes != 0 {
		t.Fatal("terminal reservation admitted work")
	}
}

func TestWorldviewBudgetConcurrentReservationAndRelease(t *testing.T) {
	b := &worldviewBudget{}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			owner := &worldviewOwner{}
			for range 20 {
				r, err := b.reserveWatch(owner, 8, 100)
				if err != nil {
					t.Error(err)
					return
				}
				if !r.reserveQueue(50) {
					t.Error("queue reserve failed")
					r.releaseWatch()
					return
				}
				r.releaseQueued(1, 50)
				r.closeQueue()
				r.releaseWatch()
				r.releaseWatch()
			}
		}()
	}
	wg.Wait()
	if b.watches != 0 || b.cells != 0 || b.queuedMessages != 0 || b.queuedBytes != 0 || len(b.owners) != 0 {
		t.Fatal("concurrent quota leak")
	}
}
