package event

import (
	"github.com/mc-go/server/pkg/protocol"
	"github.com/mc-go/server/pkg/types"
)

// Event is the base interface for all events
type Event interface {
	IsCancellable() bool
	IsCancelled() bool
	SetCancelled(bool)
}

// BaseEvent provides common event functionality
type BaseEvent struct {
	cancelled bool
}

func (e *BaseEvent) IsCancellable() bool { return false }
func (e *BaseEvent) IsCancelled() bool   { return e.cancelled }
func (e *BaseEvent) SetCancelled(c bool) { e.cancelled = c }

// CancellableEvent is for events that can be cancelled
type CancellableEvent struct {
	BaseEvent
}

func (e *CancellableEvent) IsCancellable() bool { return true }

// Server Events

type ServerStartEvent struct {
	BaseEvent
}

type ServerStopEvent struct {
	BaseEvent
}

type ServerTickEvent struct {
	BaseEvent
	Tick int64
}

// Player Events

type PlayerPreLoginEvent struct {
	CancellableEvent
	Username string
	UUID     string
	Address  string
	Reason   string // Kick reason if cancelled
}

type PlayerLoginEvent struct {
	BaseEvent
	Player types.Player
}

type PlayerJoinEvent struct {
	BaseEvent
	Player       types.Player
	JoinMessage  string
}

type PlayerQuitEvent struct {
	BaseEvent
	Player      types.Player
	QuitMessage string
}

type PlayerChatEvent struct {
	CancellableEvent
	Player  types.Player
	Message string
	Format  string
}

type PlayerCommandEvent struct {
	CancellableEvent
	Player  types.Player
	Command string
	Args    []string
}

type PlayerMoveEvent struct {
	CancellableEvent
	Player    types.Player
	From      Location
	To        Location
}

type PlayerTeleportEvent struct {
	CancellableEvent
	Player types.Player
	From   Location
	To     Location
	Cause  TeleportCause
}

type PlayerGameModeChangeEvent struct {
	CancellableEvent
	Player      types.Player
	OldGameMode int
	NewGameMode int
}

// Block Events

type BlockBreakEvent struct {
	CancellableEvent
	Player    types.Player
	Block     types.BlockLocation
	BlockType types.BlockType
	Drops     []ItemStack
}

type BlockPlaceEvent struct {
	CancellableEvent
	Player       types.Player
	Block        types.BlockLocation
	BlockType    types.BlockType
	BlockAgainst types.BlockLocation
	ItemInHand   ItemStack
}

// World Events

type ChunkGenerateEvent struct {
	BaseEvent
	ChunkX int32
	ChunkZ int32
	World  string
	Data   []byte
}

type ChunkLoadEvent struct {
	BaseEvent
	ChunkX int32
	ChunkZ int32
	World  string
	IsNew  bool
}

type ChunkUnloadEvent struct {
	CancellableEvent
	ChunkX int32
	ChunkZ int32
	World  string
	Save   bool
}

// Packet Events

type PacketReceiveEvent struct {
	CancellableEvent
	Player types.Player
	Packet *protocol.Packet
}

type PacketSendEvent struct {
	CancellableEvent
	Player types.Player
	Packet *protocol.Packet
}

// Re-export types for convenience
type Location = types.Location
type ItemStack = types.ItemStack

// TeleportCause represents the cause of teleportation
type TeleportCause int

const (
	TeleportCauseUnknown TeleportCause = iota
	TeleportCauseCommand
	TeleportCausePlugin
	TeleportCauseSpawn
	TeleportCausePortal
)