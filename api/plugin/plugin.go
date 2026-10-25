// Package plugin is the public API for compiled-in Go extensions. Import a
// plugin from the host executable to register it; no dynamic loader is used.
package plugin

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"mc-go/api/event"
	"mc-go/api/player"
	blockregistry "mc-go/api/registry"
)

// API is what compiled-in plugins receive. Observation flows through
// the event Bus; Enforcement (kick, teleport, state reads, violation
// reports) gives anticheat-style plugins something to act on. All
// methods are safe for concurrent use from any goroutine.
type API interface {
	// BlockCatalog resolves catalog-local observation handles to canonical identities.
	BlockCatalog() *blockregistry.BlockCatalog
	// Context ends when this plugin is stopped.
	Context() context.Context
	// Hooks registers synchronous before-commit decisions.
	Hooks() *Hooks
	// Bus observes core events: player login/join/leave/move,
	// teleports, chat, block changes, ticks, violations.
	Bus() *event.Bus
	// Commands registers chat commands.
	Commands() *Commands
	// ServerInfo exposes read-only server facts.
	ServerInfo() Info
	// Kick queues a live player disconnect with a plain-text reason. The
	// result reports acceptance, not completion; it is safe inside a hook.
	Kick(player, reason string) bool
	// Teleport queues a live player move after the current action. The
	// result reports acceptance, not completion; it is safe inside a hook.
	// Offline players, invalid coordinates, stopped lifetimes and full queues
	// reject requests. A player may disconnect before accepted work executes.
	// Committed teleports emit an observation for other plugins.
	Teleport(player string, x, y, z float64) bool
	// PlayerState snapshots a live player's observable state,
	// reporting whether the player is online.
	PlayerState(player string) (PlayerState, bool)
	// ReportViolation publishes a violation for other plugins
	// (punishers, loggers) to consume.
	ReportViolation(v Violation)
}

// LifecycleEnforcement is an optional host capability for retaining a plugin's
// lifetime with queued enforcement. Hosts should reject work when ctx expires,
// including work accepted before a failed Init is rolled back.
type LifecycleEnforcement interface {
	KickContext(context.Context, string, string) bool
	TeleportContext(context.Context, string, float64, float64, float64) bool
}

// PlayerControl is an optional API capability addressing one admitted player
// incarnation. IDs identify targets; they do not authenticate frontend callers.
// Queued operations report acceptance and never retarget a later reconnect.
type PlayerControl interface {
	KickID(player.ID, string) bool
	TeleportID(player.ID, float64, float64, float64) bool
	PlayerStateID(player.ID) (PlayerState, bool)
}

// LifecyclePlayerControl lets hosts preserve the plugin lifetime until queued
// ID-addressed operations execute, just as LifecycleEnforcement does by name.
type LifecyclePlayerControl interface {
	KickIDContext(context.Context, player.ID, string) bool
	TeleportIDContext(context.Context, player.ID, float64, float64, float64) bool
}

// PlayerState is one player's plugin-visible snapshot.
type PlayerState struct {
	ID       player.ID
	Name     string
	X, Y, Z  float64
	Yaw      float32
	Pitch    float32
	OnGround bool
	// Latency is the last keep-alive round trip, 0 if unmeasured.
	Latency  time.Duration
	Gamemode int32
}

// Violation is one anticheat finding. Count is the detector's running
// total for this player+check, including this one.
type Violation struct {
	Player  string
	Check   string
	Details string
	At      time.Time
	Count   int64
}

// Info exposes read-only server facts to plugins.
type Info struct {
	VersionName     string
	ProtocolVersion int
	MOTD            string
	MaxPlayers      int
}

// Plugin is implemented by every compiled-in plugin.
type Plugin interface {
	Name() string
	Init(api API) error
}

var (
	mu       sync.Mutex
	registry = map[string]Plugin{}
)

// Register is called from a plugin's init() via blank import.
func Register(p Plugin) {
	mu.Lock()
	defer mu.Unlock()
	if p == nil || p.Name() == "" {
		panic("plugin: missing name")
	}
	if _, exists := registry[p.Name()]; exists {
		panic("plugin: duplicate name " + p.Name())
	}
	registry[p.Name()] = p
}

// All returns registered plugins in name order.
func All() []Plugin {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Plugin, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// InitAll initializes the registry. Servers should retain a Manager instead so
// they can cancel resources and invoke plugin Close methods at shutdown.
func InitAll(api API) error {
	_, err := NewManager(api.Context(), api, All())
	return err
}

const APIVersion = 1

// Metadata is optional for legacy plugins. Explicit versions must match the
// host API; dependencies are plugin names initialized before this plugin.
type Metadata struct {
	APIVersion   int
	Dependencies []string
}
type Described interface{ Metadata() Metadata }
type Closer interface{ Close() error }

// Manager owns one initialized set, its cancellation and reverse-order teardown.
type Manager struct {
	mu      sync.Mutex
	cancel  context.CancelFunc
	scopes  []context.CancelFunc
	started []Plugin
	closed  bool
	err     error
}

type scopedAPI struct {
	API
	ctx      context.Context
	commands *Commands
	hooks    *Hooks
	bus      *event.Bus
	name     string
}

type controlledAPI struct{ scopedAPI }
type editingAPI struct{ scopedAPI }
type controlledEditingAPI struct{ controlledAPI }

func (a scopedAPI) Hooks() *Hooks   { return a.hooks }
func (a scopedAPI) Bus() *event.Bus { return a.bus }
func (a scopedAPI) Kick(player, reason string) bool {
	if a.ctx.Err() != nil {
		return false
	}
	if host, ok := a.API.(LifecycleEnforcement); ok {
		return host.KickContext(a.ctx, player, reason)
	}
	return a.API.Kick(player, reason)
}
func (a scopedAPI) Teleport(player string, x, y, z float64) bool {
	if a.ctx.Err() != nil {
		return false
	}
	if host, ok := a.API.(LifecycleEnforcement); ok {
		return host.TeleportContext(a.ctx, player, x, y, z)
	}
	return a.API.Teleport(player, x, y, z)
}
func (a scopedAPI) ReportViolation(v Violation) {
	if a.ctx.Err() == nil {
		a.API.ReportViolation(v)
	}
}
func (a controlledAPI) KickID(id player.ID, reason string) bool {
	if a.ctx.Err() != nil {
		return false
	}
	if host, ok := a.API.(LifecyclePlayerControl); ok {
		return host.KickIDContext(a.ctx, id, reason)
	}
	return false
}
func (a controlledAPI) TeleportID(id player.ID, x, y, z float64) bool {
	if a.ctx.Err() != nil {
		return false
	}
	if host, ok := a.API.(LifecyclePlayerControl); ok {
		return host.TeleportIDContext(a.ctx, id, x, y, z)
	}
	return false
}
func (a controlledAPI) PlayerStateID(id player.ID) (PlayerState, bool) {
	if a.ctx.Err() == nil {
		if host, ok := a.API.(PlayerControl); ok {
			return host.PlayerStateID(id)
		}
	}
	return PlayerState{}, false
}
func (a scopedAPI) Context() context.Context { return a.ctx }
func (a scopedAPI) Commands() *Commands      { return a.commands }

// NewManager validates the complete dependency graph before initializing any
// plugin. On partial failure it cancels and closes already-started plugins.
func NewManager(parent context.Context, api API, plugins []Plugin) (*Manager, error) {
	if err := parent.Err(); err != nil {
		return nil, err
	}
	ordered, err := orderPlugins(plugins)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	m := &Manager{cancel: cancel}
	for _, p := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, m.Close())
		}
		lifetime, stop := context.WithCancel(ctx)
		m.scopes = append(m.scopes, stop)
		commands := api.Commands().WithContext(lifetime)
		commands.plugin = p.Name()
		scoped := scopedAPI{API: api, ctx: lifetime, commands: commands, hooks: api.Hooks().withContext(lifetime), bus: api.Bus().WithContext(lifetime), name: p.Name()}
		scoped.hooks.plugin = p.Name()
		var view API = scoped
		_, controls := api.(PlayerControl)
		_, lifecycleControls := api.(LifecyclePlayerControl)
		_, edits := api.(BlockEditor)
		_, lifecycleEdits := api.(LifecycleBlockEditor)
		if controls && lifecycleControls {
			view = controlledAPI{scoped}
			if edits && lifecycleEdits {
				view = controlledEditingAPI{controlledAPI{scoped}}
			}
		} else if edits && lifecycleEdits {
			view = editingAPI{scoped}
		}
		// Include a failing plugin in teardown: Init may have allocated resources.
		m.started = append(m.started, p)
		if err := callPlugin(p.Name(), "init", func() error { return p.Init(view) }); err != nil {
			cleanup := m.Close()
			return nil, errors.Join(fmt.Errorf("plugin %s: %w", p.Name(), err), cleanup)
		}
		if err := ctx.Err(); err != nil {
			return nil, errors.Join(err, m.Close())
		}
	}
	return m, nil
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return m.err
	}
	m.closed = true
	m.cancel()
	for _, stop := range m.scopes {
		stop()
	}
	for i := len(m.started) - 1; i >= 0; i-- {
		if c, ok := m.started[i].(Closer); ok {
			if err := callPlugin(m.started[i].Name(), "close", c.Close); err != nil {
				m.err = errors.Join(m.err, fmt.Errorf("plugin %s: %w", m.started[i].Name(), err))
			}
		}
	}
	return m.err
}

func orderPlugins(plugins []Plugin) ([]Plugin, error) {
	byName := make(map[string]Plugin)
	names := make([]string, 0, len(plugins))
	for _, p := range plugins {
		if p == nil || p.Name() == "" {
			return nil, fmt.Errorf("plugin: missing name")
		}
		if byName[p.Name()] != nil {
			return nil, fmt.Errorf("plugin: duplicate name %s", p.Name())
		}
		if d, ok := p.(Described); ok && d.Metadata().APIVersion != 0 && d.Metadata().APIVersion != APIVersion {
			return nil, fmt.Errorf("plugin %s: API version %d unsupported (host %d)", p.Name(), d.Metadata().APIVersion, APIVersion)
		}
		byName[p.Name()] = p
		names = append(names, p.Name())
	}
	sort.Strings(names)
	state := make(map[string]uint8)
	var ordered []Plugin
	var visit func(string) error
	visit = func(name string) error {
		if state[name] == 2 {
			return nil
		}
		if state[name] == 1 {
			return fmt.Errorf("plugin dependency cycle at %s", name)
		}
		p := byName[name]
		if p == nil {
			return fmt.Errorf("missing plugin dependency %s", name)
		}
		state[name] = 1
		if d, ok := p.(Described); ok {
			deps := append([]string(nil), d.Metadata().Dependencies...)
			sort.Strings(deps)
			for _, dep := range deps {
				if err := visit(dep); err != nil {
					return err
				}
			}
		}
		state[name] = 2
		ordered = append(ordered, p)
		return nil
	}
	for _, name := range names {
		if err := visit(name); err != nil {
			return nil, err
		}
	}
	return ordered, nil
}
