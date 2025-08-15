package plugin

import (
	"github.com/mc-go/server/pkg/player"
	"github.com/mc-go/server/pkg/protocol"
)

// ChunkGenerateEvent is fired when a chunk needs to be generated
type ChunkGenerateEvent struct {
	*BaseEvent
	ChunkX int32
	ChunkZ int32
	Data   []byte // The chunk data that will be sent
	Cancel bool   // If true, prevents default generation
}

// ChunkSendEvent is fired before sending a chunk to a player
type ChunkSendEvent struct {
	*BaseEvent
	Player *player.Player
	ChunkX int32
	ChunkZ int32
	Data   []byte
}

// PlayerMoveEvent is fired when a player moves
type PlayerMoveEvent struct {
	*BaseEvent
	Player *player.Player
	FromX, FromY, FromZ float64
	ToX, ToY, ToZ float64
	FromYaw, FromPitch float32
	ToYaw, ToPitch float32
}

// PlayerChatEvent is fired when a player sends a chat message
type PlayerChatEvent struct {
	*BaseEvent
	Player  *player.Player
	Message string
	Format  string // Can be modified by plugins
}

// PlayerCommandEvent is fired when a player executes a command
type PlayerCommandEvent struct {
	*BaseEvent
	Player  *player.Player
	Command string
	Args    []string
}

// BlockBreakEvent is fired when a player breaks a block
type BlockBreakEvent struct {
	*BaseEvent
	Player  *player.Player
	X, Y, Z int32
	BlockID uint16
	Cancel  bool
}

// BlockPlaceEvent is fired when a player places a block
type BlockPlaceEvent struct {
	*BaseEvent
	Player  *player.Player
	X, Y, Z int32
	BlockID uint16
	Cancel  bool
}

// PacketReceiveEvent is fired when receiving a packet from a player
type PacketReceiveEvent struct {
	*BaseEvent
	Player *player.Player
	Packet *protocol.Packet
	Cancel bool // If true, packet won't be processed
}

// PacketSendEvent is fired before sending a packet to a player
type PacketSendEvent struct {
	*BaseEvent
	Player *player.Player
	Packet *protocol.Packet
	Cancel bool // If true, packet won't be sent
}

// PlayerGameModeChangeEvent is fired when a player's game mode changes
type PlayerGameModeChangeEvent struct {
	*BaseEvent
	Player      *player.Player
	OldGameMode int
	NewGameMode int
	Cancel      bool
}

// PlayerTeleportEvent is fired when a player teleports
type PlayerTeleportEvent struct {
	*BaseEvent
	Player *player.Player
	FromX, FromY, FromZ float64
	ToX, ToY, ToZ float64
	Cancel bool
}

// ServerTickEvent is fired every server tick (20 times per second)
type ServerTickEvent struct {
	*BaseEvent
	Tick int64
}

// Helper functions to create events
func NewChunkGenerateEvent(chunkX, chunkZ int32) *ChunkGenerateEvent {
	return &ChunkGenerateEvent{
		BaseEvent: &BaseEvent{eventType: EventChunkGenerate},
		ChunkX:    chunkX,
		ChunkZ:    chunkZ,
	}
}

func NewPlayerMoveEvent(p *player.Player, fromX, fromY, fromZ, toX, toY, toZ float64, fromYaw, fromPitch, toYaw, toPitch float32) *PlayerMoveEvent {
	return &PlayerMoveEvent{
		BaseEvent: &BaseEvent{eventType: EventPlayerMove, data: p},
		Player:    p,
		FromX:     fromX, FromY: fromY, FromZ: fromZ,
		ToX: toX, ToY: toY, ToZ: toZ,
		FromYaw: fromYaw, FromPitch: fromPitch,
		ToYaw: toYaw, ToPitch: toPitch,
	}
}

func NewPlayerChatEvent(p *player.Player, message string) *PlayerChatEvent {
	return &PlayerChatEvent{
		BaseEvent: &BaseEvent{eventType: EventPlayerChat, data: p},
		Player:    p,
		Message:   message,
		Format:    "<%s> %s", // Default format
	}
}

func NewPacketReceiveEvent(p *player.Player, packet *protocol.Packet) *PacketReceiveEvent {
	return &PacketReceiveEvent{
		BaseEvent: &BaseEvent{eventType: EventPacketReceive, data: packet},
		Player:    p,
		Packet:    packet,
	}
}

func NewPacketSendEvent(p *player.Player, packet *protocol.Packet) *PacketSendEvent {
	return &PacketSendEvent{
		BaseEvent: &BaseEvent{eventType: EventPacketSend, data: packet},
		Player:    p,
		Packet:    packet,
	}
}