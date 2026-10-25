package server

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"unsafe"

	"mc-go/api/player"
)

const (
	defaultPlayerMailboxMessages = 128
	defaultPlayerMailboxBytes    = 1 << 20
	playerMailboxUpdateBytes     = int64(unsafe.Sizeof(player.Update{}))
	playerMailboxStateBytes      = int64(unsafe.Sizeof(player.State{}))
)

var errPlayerMailboxOrder = errors.New("player replication: invalid update order")

// playerMailbox retains detached player values, never wire packets or gameplay
// pointers. Its limits cover queued values, including the initial reservation.
// Next transfers ownership to one consumer and releases that queue charge. The
// limits do not bound checked-out values, adapter copies, GC or total heap use.
// No method calls an adapter or enters simulation ownership.
type playerMailbox struct {
	mu          sync.Mutex
	limits      player.Limits
	queue       []player.Update
	head        int
	queuedBytes int64
	initial     int64
	initialized bool
	last        uint64
	terminal    error
	ready       chan struct{}
	done        chan struct{}
}

// newPlayerMailbox reserves the first snapshot before allocating its payload.
// The caller counts players and the bytes of their names/dimensions while it
// holds simulation ownership, then captures and registers at the same boundary.
func newPlayerMailbox(limits player.Limits, playerCount int, textBytes int64) (*playerMailbox, error) {
	if limits.Messages <= 0 {
		limits.Messages = defaultPlayerMailboxMessages
	}
	if limits.Bytes <= 0 {
		limits.Bytes = defaultPlayerMailboxBytes
	}
	size, ok := playerSnapshotReservation(playerCount, textBytes)
	if !ok || size > limits.Bytes {
		return nil, player.ErrOverflow
	}
	return &playerMailbox{
		limits: limits, queuedBytes: size, initial: size,
		ready: make(chan struct{}, 1), done: make(chan struct{}),
	}, nil
}

func playerSnapshotReservation(count int, textBytes int64) (int64, bool) {
	if count < 0 || textBytes < 0 || int64(count) > (math.MaxInt64-playerMailboxUpdateBytes)/playerMailboxStateBytes {
		return 0, false
	}
	base := playerMailboxUpdateBytes + int64(count)*playerMailboxStateBytes
	if textBytes > math.MaxInt64-base {
		return 0, false
	}
	return base + textBytes, true
}

func playerUpdateBytes(update player.Update) (int64, bool) {
	text := int64(len(update.Reason))
	add := func(state player.State) bool {
		for _, value := range []string{state.Profile.Name, state.Dimension} {
			if int64(len(value)) > math.MaxInt64-text {
				return false
			}
			text += int64(len(value))
		}
		return true
	}
	if !add(update.Player) {
		return 0, false
	}
	for _, state := range update.Players {
		if !add(state) {
			return 0, false
		}
	}
	return playerSnapshotReservation(len(update.Players), text)
}

func clonePlayerState(state player.State) player.State {
	state.Profile.Name = strings.Clone(state.Profile.Name)
	state.Dimension = strings.Clone(state.Dimension)
	return state
}

func clonePlayerUpdate(update player.Update) player.Update {
	update.Player = clonePlayerState(update.Player)
	update.Reason = strings.Clone(update.Reason)
	if update.Players != nil {
		states := make([]player.State, len(update.Players))
		for i, state := range update.Players {
			states[i] = clonePlayerState(state)
		}
		update.Players = states
	}
	return update
}

func (m *playerMailbox) initialize(update player.Update) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.terminal != nil {
		return m.terminal
	}
	if m.initialized || update.Kind != player.Snapshot {
		m.closeLocked(errPlayerMailboxOrder)
		return m.terminal
	}
	size, ok := playerUpdateBytes(update)
	if !ok || size > m.initial {
		m.closeLocked(player.ErrOverflow)
		return m.terminal
	}
	// The reservation is checked before the first private payload allocation.
	m.queue = append(m.queue, clonePlayerUpdate(update))
	m.queuedBytes, m.initial = size, 0
	m.initialized, m.last = true, update.Revision
	m.signalLocked()
	return nil
}

// tryPublish never waits for a consumer. On false the owner must retire only
// this connection's avatar. Collect failures until the current fanout finishes:
// publishing their removal inline would invert order for healthy subscribers.
func (m *playerMailbox) tryPublish(update player.Update) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.terminal != nil {
		return false
	}
	if !m.initialized || update.Kind == player.Snapshot || update.Kind < player.Added || update.Kind > player.Correction || update.Revision <= m.last {
		m.closeLocked(errPlayerMailboxOrder)
		return false
	}
	size, ok := playerUpdateBytes(update)
	if !ok || len(m.queue)-m.head >= m.limits.Messages || size > m.limits.Bytes-m.queuedBytes {
		m.closeLocked(player.ErrOverflow)
		return false
	}
	if m.head > 0 && len(m.queue) == cap(m.queue) {
		n := copy(m.queue, m.queue[m.head:])
		clear(m.queue[n:])
		m.queue = m.queue[:n]
		m.head = 0
	}
	m.queue = append(m.queue, clonePlayerUpdate(update))
	m.queuedBytes += size
	m.last = update.Revision
	m.signalLocked()
	return true
}

func (m *playerMailbox) signalLocked() {
	select {
	case m.ready <- struct{}{}:
	default:
	}
}

// Next supports one consumer per connection. Canceling this call leaves the
// connection and all queued updates intact; its lifetime is closed separately.
func (m *playerMailbox) Next(ctx context.Context) (player.Update, error) {
	for {
		if err := ctx.Err(); err != nil {
			return player.Update{}, err
		}
		m.mu.Lock()
		if err := ctx.Err(); err != nil {
			m.mu.Unlock()
			return player.Update{}, err
		}
		if m.terminal != nil {
			err := m.terminal
			m.mu.Unlock()
			return player.Update{}, err
		}
		if m.head < len(m.queue) {
			update := m.queue[m.head]
			m.queue[m.head] = player.Update{}
			m.head++
			size, _ := playerUpdateBytes(update)
			m.queuedBytes -= size
			if m.head == len(m.queue) {
				m.queue, m.head = m.queue[:0], 0
			}
			m.mu.Unlock()
			return update, nil
		}
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			return player.Update{}, ctx.Err()
		case <-m.done:
		case <-m.ready:
		}
	}
}

func (m *playerMailbox) closeLocked(err error) {
	if m.terminal != nil {
		return
	}
	if err == nil {
		err = player.ErrClosed
	}
	m.terminal = err
	clear(m.queue)
	m.queue, m.head, m.queuedBytes, m.initial = nil, 0, 0, 0
	close(m.done)
}

func (m *playerMailbox) Close(err error) {
	m.mu.Lock()
	m.closeLocked(err)
	m.mu.Unlock()
}

func (m *playerMailbox) Err() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.terminal
}

func (m *playerMailbox) Done() <-chan struct{} { return m.done }
