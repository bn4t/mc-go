package types

import "github.com/google/uuid"

// Entity represents any entity in the game
type Entity interface {
	// Identity
	GetEntityID() int32
	GetUUID() uuid.UUID
	GetType() EntityType
	
	// Position and Movement
	GetPosition() (float64, float64, float64)
	SetPosition(x, y, z float64)
	GetVelocity() (float64, float64, float64)
	SetVelocity(x, y, z float64)
	GetRotation() (float32, float32)
	SetRotation(yaw, pitch float32)
	
	// World
	GetWorld() World
	SetWorld(world World)
	IsOnGround() bool
	
	// Lifecycle
	Spawn() error
	Despawn() error
	IsAlive() bool
	Remove()
	
	// Metadata
	GetMetadata(key string) interface{}
	SetMetadata(key string, value interface{})
	HasMetadata(key string) bool
	
	// Ticking
	Tick()
}

// LivingEntity represents entities with health
type LivingEntity interface {
	Entity
	
	// Health
	GetHealth() float32
	SetHealth(health float32)
	GetMaxHealth() float32
	SetMaxHealth(maxHealth float32)
	Damage(amount float32)
	Heal(amount float32)
	
	// Effects
	AddEffect(effect Effect)
	RemoveEffect(effectType EffectType)
	HasEffect(effectType EffectType) bool
	GetActiveEffects() []Effect
}

// EntityType represents the type of entity
type EntityType int

const (
	EntityTypePlayer EntityType = iota
	EntityTypeMob
	EntityTypeAnimal
	EntityTypeProjectile
	EntityTypeItem
	EntityTypeExperienceOrb
	EntityTypePainting
	EntityTypeArmorStand
	EntityTypeMinecart
	EntityTypeBoat
	EntityTypeOther
)