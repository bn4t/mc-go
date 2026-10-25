// Package event provides lossy, asynchronous observation of committed actions.
// Use plugin.Hooks for synchronous decisions before an action commits.
package event

import (
	"context"
	"mc-go/api/player"
	"mc-go/api/registry"
	"mc-go/api/worldview"
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
	TopicPlayerLogin = "player.login"
	// TopicPlayerJoin and TopicPlayerLeave preserve API v1 map[string]string
	// payloads containing only the player name under the "name" key.
	TopicPlayerJoin  = "player.join"
	TopicPlayerLeave = "player.leave"
	// Typed lifecycle values use distinct topics. Subscribing to both topic
	// families observes the same lifecycle transition twice.
	TopicPlayerJoined   = "player.joined"
	TopicPlayerLeft     = "player.left"
	TopicPlayerMove     = "player.move"
	TopicPlayerTeleport = "player.teleport"
	TopicTick           = "world.tick"
	TopicChat           = "player.chat"
	TopicBlockChange    = "world.blockchange"
	TopicBlockEdited    = "world.block.edited"
	TopicViolation      = "anticheat.violation"
)

type PlayerMove struct {
	ID                  player.ID
	Name                string
	X, Y, Z             float64
	FromX, FromY, FromZ float64
	Yaw, Pitch          float32
	OnGround            bool
	At                  time.Time
	Latency             time.Duration
}

type PlayerTeleport struct {
	ID      player.ID
	Name    string
	X, Y, Z float64
}

// PlayerJoined is published on TopicPlayerJoined after admission commits. All
// fields are values; changing a received event cannot mutate the live player.
type PlayerJoined struct{ Player player.State }

// PlayerLeft is published on TopicPlayerLeft after retirement. A reconnect receives a new ID.
type PlayerLeft struct {
	Player player.State
	Reason string
}

// Block is a catalog-local runtime handle, never a protocol numeric ID.
// Resolve it through the runtime BlockCatalog; persist its canonical StateKey.
type Block = registry.BlockStateID

type BlockChange struct {
	Player     string
	X, Y, Z    int
	Block      Block
	PX, PY, PZ float64
}

// BlockEdited observes the typed privileged single-cell entry point after its
// immediate side effects. It does not describe every physics write or imply a
// corresponding world-view delta. All handles belong to the runtime catalog.
type BlockEdited struct {
	Origin                string
	Dimension             string
	Position              worldview.Pos
	Before, Target, After registry.BlockStateID
}
type ChatMessage struct {
	Name, Text string
	Command    bool
}
