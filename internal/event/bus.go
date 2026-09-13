package event

import "sync"

// Bus is a thread-safe in-memory pub/sub hub.
// Topics are plain strings so plugins and core stay decoupled.
type Bus struct {
	mu   sync.RWMutex
	subs map[string]map[chan Event]struct{}
}

// Event is a single broadcast message.
type Event struct {
	Topic string
	Data  any
}

func New() *Bus {
	return &Bus{subs: make(map[string]map[chan Event]struct{})}
}

// Subscribe returns a buffered channel receiving events for topic.
// Caller must call returned unsubscribe func to release it.
func (b *Bus) Subscribe(topic string, buf int) (<-chan Event, func()) {
	if buf <= 0 {
		buf = 16
	}
	ch := make(chan Event, buf)
	b.mu.Lock()
	if b.subs[topic] == nil {
		b.subs[topic] = make(map[chan Event]struct{})
	}
	b.subs[topic][ch] = struct{}{}
	b.mu.Unlock()
	return ch, func() {
		b.mu.Lock()
		delete(b.subs[topic], ch)
		b.mu.Unlock()
	}
}

// Publish fans out without blocking the publisher: full subscriber
// buffers drop the event rather than stalling the tick/IO path.
func (b *Bus) Publish(topic string, data any) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subs[topic] {
		select {
		case ch <- Event{Topic: topic, Data: data}:
		default:
		}
	}
}

// Well-known core topics.
const (
	TopicPlayerLogin = "player.login"
	TopicPlayerJoin  = "player.join"
	TopicPlayerLeave = "player.leave"
	TopicPlayerMove  = "player.move"
	// TopicPlayerTeleport fires on server-initiated teleports only.
	TopicPlayerTeleport = "player.teleport"
	TopicTick           = "world.tick"
	TopicChat           = "player.chat"
	TopicBlockChange    = "world.blockchange"
	// TopicViolation carries plugin.Violation: detectors publish,
	// punishers and loggers subscribe.
	TopicViolation = "anticheat.violation"
)
