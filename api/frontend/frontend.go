// Package frontend defines the protocol-neutral boundary for server entry points.
// Implementations own their transport, framing, authentication and codecs. Core
// actions operate on authenticated existing players; player admission and world
// replication are not yet part of this boundary.
package frontend

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"time"
)

// Core exposes detached observations and serialized gameplay actions. A frontend
// must authenticate and authorize its callers before submitting privileged actions.
type Core interface {
	Snapshot(context.Context) (Snapshot, error)
	Execute(context.Context, Action) (Result, error)
}

// Frontend serves one listener until it closes or ctx is canceled. Returning
// stops this attachment only; the shared runtime continues until explicitly stopped.
type Frontend interface {
	Name() string
	Serve(context.Context, Core, net.Listener) error
}

type Player struct {
	Name       string
	X, Y, Z    float64
	Yaw, Pitch float32
	OnGround   bool
	Latency    time.Duration
	Gamemode   int32
}

type Snapshot struct {
	MOTD       string
	MaxPlayers int
	Tick       int64
	Frozen     bool
	Players    []Player
}

// Movement is an absolute, fully decoded player movement. Frontends fill omitted
// wire fields from their last snapshot before submission. The core applies the
// same validity, collision and gameplay rules as Java movement.
type Movement struct {
	X, Y, Z    float64
	Yaw, Pitch float32
	OnGround   bool
}

type ActionKind uint8

const (
	Command  ActionKind = iota + 1 // privileged console command
	Teleport                       // privileged server teleport
	Kick                           // privileged disconnect
	Move                           // movement for an authenticated player
)

type Action struct {
	Kind     ActionKind
	Player   string
	Command  string
	Reason   string
	Movement Movement
}

type Result struct {
	Applied bool
	Output  string
}

var ErrUnsupportedAction = errors.New("frontend: unsupported action")

// Factory creates an independent transport frontend, without access to concrete
// server or session types. Register factories before starting listeners.
type Factory func() Frontend

var registry = struct {
	sync.RWMutex
	factories map[string]Factory
}{factories: map[string]Factory{}}

func Register(name string, factory Factory) {
	if name == "" || factory == nil {
		panic("frontend: empty name or nil factory")
	}
	registry.Lock()
	defer registry.Unlock()
	if _, exists := registry.factories[name]; exists {
		panic("frontend: duplicate registration: " + name)
	}
	registry.factories[name] = factory
}
func New(name string) (Frontend, bool) {
	registry.RLock()
	factory, ok := registry.factories[name]
	registry.RUnlock()
	if !ok {
		return nil, false
	}
	return factory(), true
}
func Names() []string {
	registry.RLock()
	defer registry.RUnlock()
	names := make([]string, 0, len(registry.factories))
	for name := range registry.factories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
