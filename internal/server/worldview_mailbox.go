package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"unsafe"

	"mc-go/api/registry"
	"mc-go/api/worldview"
)

const (
	defaultWorldviewQueueMessages       = 64
	defaultWorldviewQueueBytes    int64 = 2 << 20
	worldviewUpdateBytes                = int64(unsafe.Sizeof(worldview.Update{}))
	worldviewChangeBytes                = int64(unsafe.Sizeof(worldview.Change{}))
)

var errWorldviewOrder = errors.New("worldview: invalid update order")
var errWorldviewPayload = errors.New("worldview: invalid canonical payload")

// worldviewMailbox owns a finite canonical baseline and detached queued values.
// Queue limits end at Next: checked-out values and adapter copies are outside
// these limits. Persistent baseline/journal cells use a separate reservation.
// Lock order is mailbox -> budget; neither calls a frontend or acquires owner.
type worldviewMailbox struct {
	mu          sync.Mutex
	limits      worldview.Limits
	reservation *worldviewReservation
	cells       int64
	dimension   string
	bounds      worldview.Box
	catalog     *registry.BlockCatalog
	baseline    []registry.BlockStateID
	dx, dz      int64
	queue       []worldview.Update
	head        int
	queuedBytes int64
	initial     int64
	initialized bool
	last        uint64
	terminal    error
	ready       chan struct{}
	done        chan struct{}
}

func newWorldviewMailbox(budget *worldviewBudget, owner *worldviewOwner, cells int64, dimension string, limits worldview.Limits) (*worldviewMailbox, error) {
	if cells <= 0 || cells > worldview.MaxCells {
		return nil, worldview.ErrInvalidBounds
	}
	if dimension == "" {
		return nil, worldview.ErrUnknownDimension
	}
	if limits.Messages <= 0 {
		limits.Messages = defaultWorldviewQueueMessages
	}
	if limits.Bytes <= 0 {
		limits.Bytes = defaultWorldviewQueueBytes
	}
	initial := worldviewUpdateBytes + cells*4 + int64(len(dimension))
	if initial > limits.Bytes {
		return nil, worldview.ErrOverflow
	}
	reservation, err := budget.reserveWatch(owner, cells, initial)
	if err != nil {
		return nil, err
	}
	return &worldviewMailbox{limits: limits, reservation: reservation, cells: cells, dimension: strings.Clone(dimension), queuedBytes: initial, initial: initial, ready: make(chan struct{}, 1), done: make(chan struct{})}, nil
}

func worldviewCellCount(bounds worldview.Box) (int64, error) {
	dx := int64(bounds.Max.X) - int64(bounds.Min.X)
	dy := int64(bounds.Max.Y) - int64(bounds.Min.Y)
	dz := int64(bounds.Max.Z) - int64(bounds.Min.Z)
	if dx <= 0 || dy <= 0 || dz <= 0 || dx > worldview.MaxCells || dy > worldview.MaxCells/dx || dz > worldview.MaxCells/(dx*dy) {
		return 0, worldview.ErrInvalidBounds
	}
	return dx * dy * dz, nil
}

// initialize ADOPTS the newly allocated Blocks slice as its private baseline.
// The caller relinquishes all mutable aliases. Only the consumer snapshot is
// cloned, after reservation; baseline/journal storage is bounded by cell quota.
func (m *worldviewMailbox) initialize(update worldview.Update) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.terminal != nil {
		return m.terminal
	}
	if m.initialized || update.Kind != worldview.Snapshot {
		m.closeLocked(errWorldviewOrder)
		return m.terminal
	}
	cells, err := worldviewCellCount(update.Bounds)
	if err != nil || cells != m.cells || int64(len(update.Blocks)) != cells || cap(update.Blocks) != len(update.Blocks) || update.Dimension != m.dimension || update.Catalog == nil || len(update.Changes) != 0 {
		m.closeLocked(errWorldviewPayload)
		return m.terminal
	}
	for _, state := range update.Blocks {
		if uint64(state) >= uint64(update.Catalog.Len()) {
			m.closeLocked(errWorldviewPayload)
			return m.terminal
		}
	}
	// No payload allocation precedes the initial local/global reservation.
	blocks := make([]registry.BlockStateID, len(update.Blocks))
	copy(blocks, update.Blocks)
	m.bounds, m.catalog, m.baseline = update.Bounds, update.Catalog, update.Blocks
	m.dx = int64(m.bounds.Max.X) - int64(m.bounds.Min.X)
	m.dz = int64(m.bounds.Max.Z) - int64(m.bounds.Min.Z)
	m.queue = append(m.queue, worldview.Update{Revision: update.Revision, Kind: worldview.Snapshot, Dimension: m.dimension, Bounds: m.bounds, Catalog: m.catalog, Blocks: blocks})
	m.initialized, m.last = true, update.Revision
	m.initial = 0
	m.signalLocked()
	return nil
}

func (m *worldviewMailbox) changeIndex(pos worldview.Pos) (int, bool) {
	if pos.X < m.bounds.Min.X || pos.X >= m.bounds.Max.X || pos.Y < m.bounds.Min.Y || pos.Y >= m.bounds.Max.Y || pos.Z < m.bounds.Min.Z || pos.Z >= m.bounds.Max.Z {
		return 0, false
	}
	return int(((int64(pos.Y)-int64(m.bounds.Min.Y))*m.dz+(int64(pos.Z)-int64(m.bounds.Min.Z)))*m.dx + (int64(pos.X) - int64(m.bounds.Min.X))), true
}

func (m *worldviewMailbox) tryPublish(update worldview.Update) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.terminal != nil {
		return false
	}
	if update.Kind != worldview.Delta || len(update.Blocks) != 0 || update.Dimension != m.dimension || update.Bounds != m.bounds || update.Catalog != m.catalog {
		m.closeLocked(errWorldviewPayload)
		return false
	}
	return m.publishDeltaLocked(update.Revision, update.Changes)
}

// tryPublishDelta accepts a shared canonical batch with at most one final value
// per position (the world journal's contract). It counts in-box effective changes
// before reserving bytes, then allocates only its private filtered payload. No
// per-watch temporary delta or frontend callback is needed.
func (m *worldviewMailbox) tryPublishDelta(revision uint64, changes []worldview.Change) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.terminal != nil {
		return false
	}
	return m.publishDeltaLocked(revision, changes)
}

func (m *worldviewMailbox) publishDeltaLocked(revision uint64, changes []worldview.Change) bool {
	if !m.initialized || revision <= m.last {
		m.closeLocked(errWorldviewOrder)
		return false
	}
	count := 0
	for _, change := range changes {
		if index, inBounds := m.changeIndex(change.Position); inBounds {
			if uint64(change.State) >= uint64(m.catalog.Len()) {
				m.closeLocked(errWorldviewPayload)
				return false
			}
			if change.State != m.baseline[index] {
				count++
			}
		}
	}
	if count == 0 {
		return true
	}
	if int64(count) > m.cells {
		m.closeLocked(errWorldviewPayload)
		return false
	}
	size := worldviewUpdateBytes + int64(count)*worldviewChangeBytes + int64(len(m.dimension))
	if len(m.queue)-m.head >= m.limits.Messages || size > m.limits.Bytes-m.queuedBytes || !m.reservation.reserveQueue(size) {
		m.closeLocked(worldview.ErrOverflow)
		return false
	}
	// Local/global quotas are reserved before allocating a queued change array.
	delta := make([]worldview.Change, 0, count)
	for _, change := range changes {
		if index, inBounds := m.changeIndex(change.Position); inBounds && change.State != m.baseline[index] {
			delta = append(delta, change)
		}
	}
	if m.head > 0 && len(m.queue) == cap(m.queue) {
		n := copy(m.queue, m.queue[m.head:])
		clear(m.queue[n:])
		m.queue = m.queue[:n]
		m.head = 0
	}
	m.queue = append(m.queue, worldview.Update{Revision: revision, Kind: worldview.Delta, Dimension: m.dimension, Bounds: m.bounds, Catalog: m.catalog, Changes: delta})
	m.queuedBytes += size
	m.last = revision
	for _, change := range delta {
		index, _ := m.changeIndex(change.Position)
		m.baseline[index] = change.State
	}
	m.signalLocked()
	return true
}

func (m *worldviewMailbox) signalLocked() {
	select {
	case m.ready <- struct{}{}:
	default:
	}
}

// Next has one consumer. Its context cancels only this read, never the watch.
// Ownership of the detached update transfers on dequeue, releasing queue quota.
func (m *worldviewMailbox) Next(ctx context.Context) (worldview.Update, error) {
	for {
		if err := ctx.Err(); err != nil {
			return worldview.Update{}, err
		}
		m.mu.Lock()
		if err := ctx.Err(); err != nil {
			m.mu.Unlock()
			return worldview.Update{}, err
		}
		if m.terminal != nil {
			err := m.terminal
			m.mu.Unlock()
			return worldview.Update{}, err
		}
		if m.head < len(m.queue) {
			update := m.queue[m.head]
			m.queue[m.head] = worldview.Update{}
			m.head++
			size := worldviewUpdateBytes + int64(len(update.Blocks))*4 + int64(len(update.Changes))*worldviewChangeBytes + int64(len(update.Dimension))
			m.queuedBytes -= size
			m.reservation.releaseQueued(1, size)
			if m.head == len(m.queue) {
				m.queue = m.queue[:0]
				m.head = 0
			}
			m.mu.Unlock()
			return update, nil
		}
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return worldview.Update{}, ctx.Err()
		case <-m.done:
		case <-m.ready:
		}
	}
}

func (m *worldviewMailbox) closeLocked(err error) {
	if m.terminal != nil {
		return
	}
	if err == nil {
		err = worldview.ErrClosed
	}
	m.terminal = err
	clear(m.queue)
	m.queue = nil
	m.head, m.queuedBytes, m.initial = 0, 0, 0
	m.reservation.closeQueue()
	close(m.done)
}

func (m *worldviewMailbox) Close(err error)       { m.mu.Lock(); m.closeLocked(err); m.mu.Unlock() }
func (m *worldviewMailbox) Err() error            { m.mu.Lock(); defer m.mu.Unlock(); return m.terminal }
func (m *worldviewMailbox) Done() <-chan struct{} { return m.done }

// releaseWatch requires owned cleanup AFTER removing the world interest. Close
// alone deliberately retains this quota so churn cannot overbook live journals.
func (m *worldviewMailbox) releaseWatch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closeLocked(nil)
	m.baseline = nil
	m.catalog = nil
	m.dimension = ""
	m.reservation.releaseWatch()
}
