package world

import (
	"fmt"
	"sync"
	
	"github.com/mc-go/server/pkg/player"
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

func (w *World) GetChunk(x, z int32) interface{} {
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

func (w *World) SpawnPlayer(p *player.Player) error {
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

func (w *World) GetWeather() Weather {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.weather
}

func (w *World) SetWeather(weather Weather) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.weather = weather
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

func (w *World) Save() error {
	return nil
}

func (w *World) Load() error {
	return nil
}

func (w *World) GetBlock(x, y, z int32) (BlockID, error) {
	chunkX := x >> 4
	chunkZ := z >> 4
	
	w.mu.RLock()
	chunk, exists := w.chunks[ChunkPos{X: chunkX, Z: chunkZ}]
	w.mu.RUnlock()
	
	if !exists {
		return 0, fmt.Errorf("chunk not loaded")
	}
	
	localX := x & 0xF
	localZ := z & 0xF
	
	return chunk.GetBlock(localX, y, localZ), nil
}

func (w *World) SetBlock(x, y, z int32, blockID BlockID) error {
	chunkX := x >> 4
	chunkZ := z >> 4
	
	w.mu.RLock()
	chunk, exists := w.chunks[ChunkPos{X: chunkX, Z: chunkZ}]
	w.mu.RUnlock()
	
	if !exists {
		chunk = w.generateChunk(chunkX, chunkZ)
	}
	
	localX := x & 0xF
	localZ := z & 0xF
	
	chunk.SetBlock(localX, y, localZ, blockID)
	
	return nil
}