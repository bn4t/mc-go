package world

import (
	"fmt"
	"sync"
	
	"github.com/mc-go/server/pkg/types"
)

type World struct {
	name       string
	chunks     map[ChunkPos]*Chunk
	spawn      Position
	time       int64
	weather    Weather
	difficulty Difficulty
	mu         sync.RWMutex
}

type Position struct {
	X, Y, Z float64
}

type ChunkPos struct {
	X, Z int32
}

type Weather int

const (
	Clear Weather = iota
	Rain
	Thunder
)

type Difficulty int

const (
	Peaceful Difficulty = iota
	Easy
	Normal
	Hard
)

func NewWorld(name string) *World {
	return &World{
		name:       name,
		chunks:     make(map[ChunkPos]*Chunk),
		spawn:      Position{X: 0, Y: 64, Z: 0},
		time:       0,
		weather:    Clear,
		difficulty: Normal,
	}
}

func (w *World) GetName() string {
	return w.name
}

func (w *World) GetChunk(x, z int32) types.Chunk {
	w.mu.RLock()
	defer w.mu.RUnlock()
	
	pos := ChunkPos{X: x, Z: z}
	chunk, exists := w.chunks[pos]
	if !exists {
		return w.generateChunk(x, z)
	}
	
	return chunk
}

func (w *World) generateChunk(x, z int32) *Chunk {
	chunk := NewChunk(x, z)
	chunk.Generate()
	
	w.mu.Lock()
	w.chunks[ChunkPos{X: x, Z: z}] = chunk
	w.mu.Unlock()
	
	return chunk
}

func (w *World) SpawnPlayer(p types.Player) error {
	return nil
}

func (w *World) GetSpawn() Position {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.spawn
}

func (w *World) SetSpawn(x, y, z float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.spawn = Position{X: x, Y: y, Z: z}
}

func (w *World) GetTime() int64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.time
}

func (w *World) SetTime(time int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.time = time
}

// Additional methods to implement types.World interface

func (w *World) GetDimension() int8 {
	return 0 // Overworld
}

func (w *World) GetSeed() int64 {
	return 0 // TODO: Implement seed
}

func (w *World) LoadChunk(x, z int32) (types.Chunk, error) {
	chunk := w.GetChunk(x, z)
	if chunk == nil {
		return nil, fmt.Errorf("failed to load chunk %d,%d", x, z)
	}
	return chunk, nil
}

func (w *World) UnloadChunk(x, z int32) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.chunks, ChunkPos{X: x, Z: z})
	return nil
}

func (w *World) IsChunkLoaded(x, z int32) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, exists := w.chunks[ChunkPos{X: x, Z: z}]
	return exists
}

func (w *World) GenerateChunk(x, z int32) types.Chunk {
	return w.generateChunk(x, z)
}

func (w *World) GetBlock(x, y, z int32) types.Block {
	// TODO: Implement block access
	return nil
}

func (w *World) SetBlock(x, y, z int32, block types.Block) error {
	// TODO: Implement block setting
	return nil
}

func (w *World) SpawnEntity(entity types.Entity) error {
	// TODO: Implement entity spawning
	return nil
}

func (w *World) RemoveEntity(entityID int32) error {
	// TODO: Implement entity removal
	return nil
}

func (w *World) GetEntity(entityID int32) types.Entity {
	// TODO: Implement entity lookup
	return nil
}

func (w *World) GetEntities() []types.Entity {
	// TODO: Implement entity list
	return nil
}

func (w *World) GetNearbyEntities(x, y, z float64, radius float64) []types.Entity {
	// TODO: Implement nearby entity search
	return nil
}

func (w *World) RemovePlayer(player types.Player) error {
	// TODO: Implement player removal
	return nil
}

func (w *World) GetPlayers() []types.Player {
	// TODO: Implement player list
	return nil
}

func (w *World) BroadcastMessage(message string) {
	// TODO: Implement message broadcast
}

func (w *World) BroadcastPacket(packet interface{}) {
	// TODO: Implement packet broadcast
}

func (w *World) GetWeather() types.Weather {
	// TODO: Implement weather
	return nil
}

func (w *World) SetWeather(weather types.Weather) {
	// TODO: Implement weather setting
}

func (w *World) GetSpawnLocation() types.Location {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return types.Location{
		World: w.name,
		X:     w.spawn.X,
		Y:     w.spawn.Y,
		Z:     w.spawn.Z,
	}
}

func (w *World) SetSpawnLocation(loc types.Location) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.spawn = Position{X: loc.X, Y: loc.Y, Z: loc.Z}
}

func (w *World) Save() error {
	// TODO: Implement world saving
	return nil
}

func (w *World) IsModified() bool {
	// TODO: Track modifications
	return false
}

func (w *World) GetDifficulty() Difficulty {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.difficulty
}

func (w *World) SetDifficulty(difficulty Difficulty) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.difficulty = difficulty
}

func (w *World) Tick() {
	w.mu.Lock()
	w.time++
	if w.time >= 24000 {
		w.time = 0
	}
	w.mu.Unlock()
}

func (w *World) Load() error {
	return nil
}