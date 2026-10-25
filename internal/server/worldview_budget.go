package server

import (
	"fmt"
	"sync"

	"mc-go/api/worldview"
)

const (
	defaultWorldviewWatches        = 64
	defaultWorldviewCells    int64 = 1 << 20
	defaultWorldviewBytes    int64 = 64 << 20
	defaultWorldviewMessages       = 4096
)

var errWorldviewAdmissionLimit = fmt.Errorf("%w: host watch resource limits exceeded", worldview.ErrRejected)

// worldviewOwner identifies an attachment without retaining a frontend callback.
// Keep it nonzero-sized: distinct attachments require distinct pointer keys.
type worldviewOwner struct{ identity byte }

type worldviewBudgetLimits struct {
	watches  int
	cells    int64
	bytes    int64
	messages int
}

// worldviewBudget is zero-ready. Cell reservations separately bound persistent
// baselines and journal interests; bytes/messages bound reserved or queued
// payloads. Checked-out values, capture temporaries, maps and GC are not a heap
// cap. The budget never calls a mailbox, simulation owner or frontend.
type worldviewBudget struct {
	mu             sync.Mutex
	limits         worldviewBudgetLimits
	watches        int
	cells          int64
	queuedBytes    int64
	queuedMessages int
	owners         map[*worldviewOwner]int
}

type worldviewReservation struct {
	budget         *worldviewBudget
	owner          *worldviewOwner
	cells          int64
	queuedBytes    int64
	queuedMessages int
	queueClosed    bool
	watchReleased  bool
}

func (b *worldviewBudget) effectiveLimits() worldviewBudgetLimits {
	l := b.limits
	if l.watches <= 0 {
		l.watches = defaultWorldviewWatches
	}
	if l.cells <= 0 {
		l.cells = defaultWorldviewCells
	}
	if l.bytes <= 0 {
		l.bytes = defaultWorldviewBytes
	}
	if l.messages <= 0 {
		l.messages = defaultWorldviewMessages
	}
	return l
}

// reserveWatch includes the initial snapshot charge. A rejection occurs before
// allocating its block array, journal interest or baseline. Admission rejection
// is distinct from overflow of an already registered stream.
func (b *worldviewBudget) reserveWatch(owner *worldviewOwner, cells, snapshotBytes int64) (*worldviewReservation, error) {
	if b == nil || owner == nil {
		return nil, worldview.ErrRejected
	}
	if cells <= 0 || cells > worldview.MaxCells || snapshotBytes <= 0 {
		return nil, worldview.ErrInvalidBounds
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	l := b.effectiveLimits()
	if b.watches >= l.watches || b.owners[owner] >= worldview.MaxPerAttachment || cells > l.cells-b.cells || snapshotBytes > l.bytes-b.queuedBytes || b.queuedMessages >= l.messages {
		return nil, errWorldviewAdmissionLimit
	}
	if b.owners == nil {
		b.owners = make(map[*worldviewOwner]int)
	}
	b.owners[owner]++
	b.watches++
	b.cells += cells
	b.queuedBytes += snapshotBytes
	b.queuedMessages++
	return &worldviewReservation{budget: b, owner: owner, cells: cells, queuedBytes: snapshotBytes, queuedMessages: 1}, nil
}

func (r *worldviewReservation) reserveQueue(bytes int64) bool {
	b := r.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	l := b.effectiveLimits()
	if r.queueClosed || r.watchReleased || bytes <= 0 || bytes > l.bytes-b.queuedBytes || b.queuedMessages >= l.messages {
		return false
	}
	r.queuedMessages++
	r.queuedBytes += bytes
	b.queuedMessages++
	b.queuedBytes += bytes
	return true
}

func (r *worldviewReservation) releaseQueued(messages int, bytes int64) {
	b := r.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.queueClosed {
		return
	}
	if messages < 0 || bytes < 0 || messages > r.queuedMessages || bytes > r.queuedBytes {
		panic("worldview queue accounting underflow")
	}
	r.queuedMessages -= messages
	r.queuedBytes -= bytes
	b.queuedMessages -= messages
	b.queuedBytes -= bytes
}

func (r *worldviewReservation) closeQueueLocked() {
	if r.queueClosed {
		return
	}
	b := r.budget
	b.queuedMessages -= r.queuedMessages
	b.queuedBytes -= r.queuedBytes
	r.queuedMessages, r.queuedBytes = 0, 0
	r.queueClosed = true
}

// closeQueue releases only payloads. Cell/watch reservations must outlive the
// mailbox until owned cleanup removes the actual interest and baseline.
func (r *worldviewReservation) closeQueue() {
	b := r.budget
	b.mu.Lock()
	r.closeQueueLocked()
	b.mu.Unlock()
}

func (r *worldviewReservation) releaseWatch() {
	b := r.budget
	b.mu.Lock()
	defer b.mu.Unlock()
	if r.watchReleased {
		return
	}
	r.closeQueueLocked()
	r.watchReleased = true
	b.watches--
	b.cells -= r.cells
	b.owners[r.owner]--
	if b.owners[r.owner] == 0 {
		delete(b.owners, r.owner)
	}
}
