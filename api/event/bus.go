// Package event provides lossy, asynchronous observation of committed actions.
// Use plugin.Hooks for synchronous decisions before an action commits.
package event

import (
	"context"
	"sync"
	"time"
)

type Event struct {
	Topic string
	Data  any
}

// Bus never blocks publishers. Full subscriber buffers drop observations.
type Bus struct {
	root        *Bus
	scope       context.Context
	scopeCancel context.CancelFunc
	mu          sync.RWMutex
	subs        map[string]map[chan Event]struct{}
	closed      bool
}

func New() *Bus { return &Bus{subs: make(map[string]map[chan Event]struct{})} }

// WithContext returns a lifecycle-bound view. Its Close ends only that view;
// callers cannot close the shared bus or bypass the enclosing lifetime.
func (b *Bus) WithContext(ctx context.Context) *Bus {
	scoped, cancel := context.WithCancel(ctx)
	if b.scope != nil {
		context.AfterFunc(b.scope, cancel)
	}
	return &Bus{root: b, scope: scoped, scopeCancel: cancel}
}

// Subscribe subscribes until unsubscribe or Close. Unsubscribe is idempotent;
// it does not close the channel. Close closes all still-subscribed channels.
func (b *Bus) Subscribe(topic string, size int) (<-chan Event, func()) {
	if b.root != nil {
		if b.scope.Err() != nil {
			ch := make(chan Event)
			close(ch)
			return ch, func() {}
		}
		ch, unsubscribe := b.root.Subscribe(topic, size)
		stop := context.AfterFunc(b.scope, unsubscribe)
		return ch, func() { stop(); unsubscribe() }
	}

	if size <= 0 {
		size = 16
	}
	ch := make(chan Event, size)
	b.mu.Lock()
	if b.closed {
		close(ch)
		b.mu.Unlock()
		return ch, func() {}
	}
	if b.subs == nil {
		b.subs = make(map[string]map[chan Event]struct{})
	}
	if b.subs[topic] == nil {
		b.subs[topic] = make(map[chan Event]struct{})
	}
	b.subs[topic][ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() { b.mu.Lock(); delete(b.subs[topic], ch); b.mu.Unlock() }
}

// SubscribeContext releases the subscription on cancellation. Consumers should
// select on ctx.Done as well as the channel, which unsubscribe leaves open.
func (b *Bus) SubscribeContext(ctx context.Context, topic string, size int) (<-chan Event, func()) {
	ch, unsub := b.Subscribe(topic, size)
	stop := context.AfterFunc(ctx, unsub)
	return ch, func() { stop(); unsub() }
}

func (b *Bus) Publish(topic string, data any) {
	if b.root != nil {
		if b.scope.Err() == nil {
			b.root.Publish(topic, data)
		}
		return
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[topic] {
		select {
		case ch <- Event{topic, data}:
		default:
		}
	}
}

func (b *Bus) Close() {
	if b.root != nil {
		b.scopeCancel()
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for _, subs := range b.subs {
		for ch := range subs {
			close(ch)
		}
	}
	b.subs = nil
}

const (
	TopicPlayerLogin    = "player.login"
	TopicPlayerJoin     = "player.join"
	TopicPlayerLeave    = "player.leave"
	TopicPlayerMove     = "player.move"
	TopicPlayerTeleport = "player.teleport"
	TopicTick           = "world.tick"
	TopicChat           = "player.chat"
	TopicBlockChange    = "world.blockchange"
	TopicViolation      = "anticheat.violation"
)

type PlayerMove struct {
	Name                string
	X, Y, Z             float64
	FromX, FromY, FromZ float64
	Yaw, Pitch          float32
	OnGround            bool
	At                  time.Time
	Latency             time.Duration
}

type PlayerTeleport struct {
	Name    string
	X, Y, Z float64
}

// Block is an opaque block-state identifier in the server's current registry.
// IDs are version-specific; this is not a stable cross-protocol block identity.
type Block uint16

type BlockChange struct {
	Player     string
	X, Y, Z    int
	Block      Block
	PX, PY, PZ float64
}
type ChatMessage struct {
	Name, Text string
	Command    bool
}
