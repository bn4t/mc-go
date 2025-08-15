package types

import "github.com/google/uuid"

// Player is the interface for player entities
type Player interface {
	// Identity
	GetUsername() string
	GetUUID() uuid.UUID
	GetEntityID() int32
	
	// Position and Movement
	GetPosition() (float64, float64, float64)
	SetPosition(x, y, z float64)
	GetRotation() (float32, float32)
	SetRotation(yaw, pitch float32)
	Teleport(x, y, z float64)
	
	// Game State
	GetGameMode() int
	SetGameMode(mode int)
	GetHealth() float32
	SetHealth(health float32)
	GetFood() int32
	SetFood(food int32)
	IsOnGround() bool
	
	// Communication
	SendMessage(message string)
	SendTitle(title, subtitle string, fadeIn, stay, fadeOut int)
	SendActionBar(message string)
	
	// Permissions
	HasPermission(permission string) bool
	AddPermission(permission string)
	RemovePermission(permission string)
	
	// World Interaction
	GetWorld() World
	SetWorld(world World)
	SendChunk(chunkX, chunkZ int32)
	
	// Inventory
	GetInventory() Inventory
	
	// Connection
	Kick(reason string)
	Disconnect(reason string)
	GetPing() int64
	IsOnline() bool
}