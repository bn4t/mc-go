package world

import (
	"sync"
)

type BlockID uint16

const (
	Air BlockID = iota
	Stone
	Grass
	Dirt
	Cobblestone
	Wood
	Sapling
	Bedrock
	Water
	StationaryWater
	Lava
	StationaryLava
	Sand
	Gravel
	GoldOre
	IronOre
	CoalOre
	Log
	Leaves
	Sponge
	Glass
)

const ChunkHeight = 256
const ChunkSize = 16

type Chunk struct {
	x, z      int32
	blocks    [ChunkSize][ChunkHeight][ChunkSize]BlockID
	blockData [ChunkSize][ChunkHeight][ChunkSize]byte
	skyLight  [ChunkSize][ChunkHeight][ChunkSize]byte
	blockLight [ChunkSize][ChunkHeight][ChunkSize]byte
	biomes    [ChunkSize][ChunkSize]byte
	entities  []interface{}
	modified  bool
	mu        sync.RWMutex
}

func NewChunk(x, z int32) *Chunk {
	chunk := &Chunk{
		x:        x,
		z:        z,
		entities: make([]interface{}, 0),
	}
	
	for x := 0; x < ChunkSize; x++ {
		for z := 0; z < ChunkSize; z++ {
			for y := 0; y < ChunkHeight; y++ {
				chunk.skyLight[x][y][z] = 15
			}
		}
	}
	
	return chunk
}

func (c *Chunk) Generate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	for x := 0; x < ChunkSize; x++ {
		for z := 0; z < ChunkSize; z++ {
			for y := 0; y < 4; y++ {
				c.blocks[x][y][z] = Bedrock
			}
			
			for y := 4; y < 60; y++ {
				c.blocks[x][y][z] = Stone
			}
			
			for y := 60; y < 63; y++ {
				c.blocks[x][y][z] = Dirt
			}
			
			c.blocks[x][63][z] = Grass
			
			c.biomes[x][z] = 1
		}
	}
	
	c.modified = true
}

func (c *Chunk) GetBlock(x, y, z int32) BlockID {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	if x < 0 || x >= ChunkSize || y < 0 || y >= ChunkHeight || z < 0 || z >= ChunkSize {
		return Air
	}
	
	return c.blocks[x][y][z]
}

func (c *Chunk) SetBlock(x, y, z int32, blockID BlockID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if x < 0 || x >= ChunkSize || y < 0 || y >= ChunkHeight || z < 0 || z >= ChunkSize {
		return
	}
	
	c.blocks[x][y][z] = blockID
	c.modified = true
}

func (c *Chunk) GetBlockData(x, y, z int32) byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	if x < 0 || x >= ChunkSize || y < 0 || y >= ChunkHeight || z < 0 || z >= ChunkSize {
		return 0
	}
	
	return c.blockData[x][y][z]
}

func (c *Chunk) SetBlockData(x, y, z int32, data byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if x < 0 || x >= ChunkSize || y < 0 || y >= ChunkHeight || z < 0 || z >= ChunkSize {
		return
	}
	
	c.blockData[x][y][z] = data
	c.modified = true
}

func (c *Chunk) GetSkyLight(x, y, z int32) byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	if x < 0 || x >= ChunkSize || y < 0 || y >= ChunkHeight || z < 0 || z >= ChunkSize {
		return 15
	}
	
	return c.skyLight[x][y][z]
}

func (c *Chunk) SetSkyLight(x, y, z int32, light byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if x < 0 || x >= ChunkSize || y < 0 || y >= ChunkHeight || z < 0 || z >= ChunkSize {
		return
	}
	
	c.skyLight[x][y][z] = light
	c.modified = true
}

func (c *Chunk) GetBlockLight(x, y, z int32) byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	if x < 0 || x >= ChunkSize || y < 0 || y >= ChunkHeight || z < 0 || z >= ChunkSize {
		return 0
	}
	
	return c.blockLight[x][y][z]
}

func (c *Chunk) SetBlockLight(x, y, z int32, light byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if x < 0 || x >= ChunkSize || y < 0 || y >= ChunkHeight || z < 0 || z >= ChunkSize {
		return
	}
	
	c.blockLight[x][y][z] = light
	c.modified = true
}

func (c *Chunk) GetBiome(x, z int32) byte {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	if x < 0 || x >= ChunkSize || z < 0 || z >= ChunkSize {
		return 0
	}
	
	return c.biomes[x][z]
}

func (c *Chunk) SetBiome(x, z int32, biome byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if x < 0 || x >= ChunkSize || z < 0 || z >= ChunkSize {
		return
	}
	
	c.biomes[x][z] = biome
	c.modified = true
}

func (c *Chunk) IsModified() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.modified
}

func (c *Chunk) SetModified(modified bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.modified = modified
}

func (c *Chunk) GetPosition() (int32, int32) {
	return c.x, c.z
}

func (c *Chunk) AddEntity(entity interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entities = append(c.entities, entity)
}

func (c *Chunk) RemoveEntity(entity interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	for i, e := range c.entities {
		if e == entity {
			c.entities = append(c.entities[:i], c.entities[i+1:]...)
			break
		}
	}
}

func (c *Chunk) GetEntities() []interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()
	
	entities := make([]interface{}, len(c.entities))
	copy(entities, c.entities)
	return entities
}