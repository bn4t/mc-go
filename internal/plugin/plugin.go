package plugin

import (
	"fmt"
	"sync"
	"time"

	"mc-go/internal/event"
)

// API is what compiled-in plugins receive. Observation flows through
// the event Bus; Enforcement (kick, teleport, state reads, violation
// reports) gives anticheat-style plugins something to act on. All
// methods are safe for concurrent use from any goroutine.
type API interface {
	// Bus observes core events: player login/join/leave/move,
	// teleports, chat, block changes, ticks, violations.
	Bus() *event.Bus
	// Commands registers chat commands.
	Commands() *Commands
	// ServerInfo exposes read-only server facts.
	ServerInfo() Info
	// Kick disconnects a live player with a plain-text reason,
	// reporting whether the player was online.
	Kick(player, reason string) bool
	// Teleport server-moves a live player, reporting whether the
	// player was online. Emits a teleport event for other plugins.
	Teleport(player string, x, y, z float64) bool
	// PlayerState snapshots a live player's observable state,
	// reporting whether the player is online.
	PlayerState(player string) (PlayerState, bool)
	// ReportViolation publishes a violation for other plugins
	// (punishers, loggers) to consume.
	ReportViolation(v Violation)
}

// PlayerState is one player's plugin-visible snapshot.
type PlayerState struct {
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
	registry[p.Name()] = p
}

// All returns registered plugins in unspecified order.
func All() []Plugin {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Plugin, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	return out
}

// InitAll runs every registered plugin against api.
func InitAll(api API) error {
	for _, p := range All() {
		if err := p.Init(api); err != nil {
			return fmt.Errorf("plugin %s: %w", p.Name(), err)
		}
	}
	return nil
}
