package event

import (
	"reflect"
	"sync"
)

// Priority levels for event handlers
type Priority int

const (
	PriorityLowest Priority = iota
	PriorityLow
	PriorityNormal
	PriorityHigh
	PriorityHighest
	PriorityMonitor // Read-only, cannot modify event
)

// Handler wraps an event handler function with priority
type Handler struct {
	Function reflect.Value
	Priority Priority
	Plugin   string
}

// Bus manages event registration and firing
type Bus struct {
	handlers map[reflect.Type][]Handler
	mu       sync.RWMutex
}

// NewBus creates a new event bus
func NewBus() *Bus {
	return &Bus{
		handlers: make(map[reflect.Type][]Handler),
	}
}

// Internal register function
func (b *Bus) register(plugin string, priority Priority, eventType reflect.Type, handler reflect.Value) {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	h := Handler{
		Function: handler,
		Priority: priority,
		Plugin:   plugin,
	}
	
	// Add handler in priority order
	handlers := b.handlers[eventType]
	inserted := false
	for i, existing := range handlers {
		if priority < existing.Priority {
			// Insert before this handler
			handlers = append(handlers[:i], append([]Handler{h}, handlers[i:]...)...)
			inserted = true
			break
		}
	}
	
	if !inserted {
		handlers = append(handlers, h)
	}
	
	b.handlers[eventType] = handlers
}

// Strongly typed registration functions for each event type

// Server Events
func (b *Bus) RegisterServerStart(plugin string, priority Priority, handler func(*ServerStartEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&ServerStartEvent{}), reflect.ValueOf(handler))
}

func (b *Bus) RegisterServerStop(plugin string, priority Priority, handler func(*ServerStopEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&ServerStopEvent{}), reflect.ValueOf(handler))
}

func (b *Bus) RegisterServerTick(plugin string, priority Priority, handler func(*ServerTickEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&ServerTickEvent{}), reflect.ValueOf(handler))
}

// Player Events
func (b *Bus) RegisterPlayerPreLogin(plugin string, priority Priority, handler func(*PlayerPreLoginEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&PlayerPreLoginEvent{}), reflect.ValueOf(handler))
}

func (b *Bus) RegisterPlayerLogin(plugin string, priority Priority, handler func(*PlayerLoginEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&PlayerLoginEvent{}), reflect.ValueOf(handler))
}

func (b *Bus) RegisterPlayerJoin(plugin string, priority Priority, handler func(*PlayerJoinEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&PlayerJoinEvent{}), reflect.ValueOf(handler))
}

func (b *Bus) RegisterPlayerQuit(plugin string, priority Priority, handler func(*PlayerQuitEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&PlayerQuitEvent{}), reflect.ValueOf(handler))
}

func (b *Bus) RegisterPlayerChat(plugin string, priority Priority, handler func(*PlayerChatEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&PlayerChatEvent{}), reflect.ValueOf(handler))
}

func (b *Bus) RegisterPlayerCommand(plugin string, priority Priority, handler func(*PlayerCommandEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&PlayerCommandEvent{}), reflect.ValueOf(handler))
}

func (b *Bus) RegisterPlayerMove(plugin string, priority Priority, handler func(*PlayerMoveEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&PlayerMoveEvent{}), reflect.ValueOf(handler))
}

func (b *Bus) RegisterPlayerTeleport(plugin string, priority Priority, handler func(*PlayerTeleportEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&PlayerTeleportEvent{}), reflect.ValueOf(handler))
}

func (b *Bus) RegisterPlayerGameModeChange(plugin string, priority Priority, handler func(*PlayerGameModeChangeEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&PlayerGameModeChangeEvent{}), reflect.ValueOf(handler))
}

// Block Events
func (b *Bus) RegisterBlockBreak(plugin string, priority Priority, handler func(*BlockBreakEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&BlockBreakEvent{}), reflect.ValueOf(handler))
}

func (b *Bus) RegisterBlockPlace(plugin string, priority Priority, handler func(*BlockPlaceEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&BlockPlaceEvent{}), reflect.ValueOf(handler))
}

// World Events
func (b *Bus) RegisterChunkGenerate(plugin string, priority Priority, handler func(*ChunkGenerateEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&ChunkGenerateEvent{}), reflect.ValueOf(handler))
}

func (b *Bus) RegisterChunkLoad(plugin string, priority Priority, handler func(*ChunkLoadEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&ChunkLoadEvent{}), reflect.ValueOf(handler))
}

func (b *Bus) RegisterChunkUnload(plugin string, priority Priority, handler func(*ChunkUnloadEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&ChunkUnloadEvent{}), reflect.ValueOf(handler))
}

// Packet Events
func (b *Bus) RegisterPacketReceive(plugin string, priority Priority, handler func(*PacketReceiveEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&PacketReceiveEvent{}), reflect.ValueOf(handler))
}

func (b *Bus) RegisterPacketSend(plugin string, priority Priority, handler func(*PacketSendEvent)) {
	b.register(plugin, priority, reflect.TypeOf(&PacketSendEvent{}), reflect.ValueOf(handler))
}

// Fire calls all handlers for the given event
func (b *Bus) Fire(event Event) {
	eventType := reflect.TypeOf(event)
	
	b.mu.RLock()
	handlers, exists := b.handlers[eventType]
	b.mu.RUnlock()
	
	if !exists {
		return
	}
	
	eventValue := reflect.ValueOf(event)
	
	for _, handler := range handlers {
		// Skip if event was cancelled and handler isn't monitor priority
		if event.IsCancelled() && handler.Priority != PriorityMonitor {
			continue
		}
		
		// Call the handler
		handler.Function.Call([]reflect.Value{eventValue})
		
		// Monitor handlers cannot modify the event
		if handler.Priority == PriorityMonitor && event.IsCancelled() {
			event.SetCancelled(false) // Reset if monitor tried to cancel
		}
	}
}

// Unregister removes all handlers for a plugin
func (b *Bus) Unregister(plugin string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	for eventType, handlers := range b.handlers {
		filtered := handlers[:0]
		for _, h := range handlers {
			if h.Plugin != plugin {
				filtered = append(filtered, h)
			}
		}
		if len(filtered) > 0 {
			b.handlers[eventType] = filtered
		} else {
			delete(b.handlers, eventType)
		}
	}
}

// Clear removes all handlers
func (b *Bus) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	
	b.handlers = make(map[reflect.Type][]Handler)
}