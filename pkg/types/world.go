package types

// World represents a game world
type World interface {
	// Identity
	GetName() string
	GetDimension() int8
	GetSeed() int64
	
	// Chunks
	GetChunk(x, z int32) Chunk
	LoadChunk(x, z int32) (Chunk, error)
	UnloadChunk(x, z int32) error
	IsChunkLoaded(x, z int32) bool
	GenerateChunk(x, z int32) Chunk
	
	// Blocks
	GetBlock(x, y, z int32) Block
	SetBlock(x, y, z int32, block Block) error
	
	// Entities
	SpawnEntity(entity Entity) error
	RemoveEntity(entityID int32) error
	GetEntity(entityID int32) Entity
	GetEntities() []Entity
	GetNearbyEntities(x, y, z float64, radius float64) []Entity
	
	// Players
	SpawnPlayer(player Player) error
	RemovePlayer(player Player) error
	GetPlayers() []Player
	BroadcastMessage(message string)
	BroadcastPacket(packet interface{})
	
	// Time and Weather
	GetTime() int64
	SetTime(time int64)
	GetWeather() Weather
	SetWeather(weather Weather)
	
	// Spawn
	GetSpawnLocation() Location
	SetSpawnLocation(loc Location)
	
	// Saving
	Save() error
	IsModified() bool
}