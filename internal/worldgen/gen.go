package worldgen

import (
	"sync"

	"mc-go/internal/world"
)

// Generator drives the per-chunk stages for one dimension.
type Generator struct {
	RS       *RandomState
	Biomes   *BiomeSource
	zoomSeed int64
	// Carve runs the carver stage after the surface (default on).
	Carve bool
	// WriteLog, when set, sees every feature write: the position, the
	// chunk being decorated and the state (order-sensitivity checks).
	WriteLog func(p BlockPos, decorating [2]int32, b world.Block)

	mu         sync.Mutex
	biomeCache map[[2]int32][]string

	featOnce        sync.Once
	steps           []stepFeatures
	featMu          sync.Mutex
	placedCache     map[string]*placedFeature
	configuredCache map[string]*configuredFeature
}

// NewGenerator builds a generator for noise settings and a biome
// parameter list.
func NewGenerator(data Data, settings, biomeList string, seed int64) (*Generator, error) {
	rs, err := NewRandomState(data, settings, seed)
	if err != nil {
		return nil, err
	}
	bs, err := NewMultiNoiseBiomeSource(data, biomeList)
	if err != nil {
		return nil, err
	}
	return &Generator{RS: rs, Biomes: bs, zoomSeed: obfuscateSeed(seed), Carve: true, biomeCache: map[[2]int32][]string{}}, nil
}

// ChunkBiomes returns (and caches) a chunk's quart biome cells.
func (g *Generator) ChunkBiomes(cx, cz int32, nc *NoiseChunk) []string {
	k := [2]int32{cx, cz}
	g.mu.Lock()
	b, ok := g.biomeCache[k]
	g.mu.Unlock()
	if ok {
		return b
	}
	if nc == nil {
		nc = g.RS.NewChunk(cx, cz)
	}
	b = g.RS.ChunkBiomes(g.Biomes, nc, cx, cz)
	g.mu.Lock()
	g.biomeCache[k] = b
	g.mu.Unlock()
	return b
}

// quartBiome is ChunkAccess.getNoiseBiome with the section clamp.
func (g *Generator) quartBiome(qx, qy, qz int32) string {
	cx, cz := qx>>2, qz>>2
	cells := g.ChunkBiomes(cx, cz, nil)
	minQ := floorDiv(g.RS.settings.minY, 4)
	maxQ := minQ + g.RS.settings.height/4 - 1
	qy = min(max(qy, minQ), maxQ)
	return cells[((qy-minQ)*4+(qz&3))*4+(qx&3)]
}

// BiomeAt is the zoomed per-block biome (BiomeManager.getBiome).
func (g *Generator) BiomeAt(x, y, z int32) string {
	return zoomBiome(g.zoomSeed, x, y, z, g.quartBiome)
}

var substanceBlocks = sync.OnceValue(func() map[Substance]world.Block {
	return map[Substance]world.Block{
		SubAir: world.Air, SubWater: blockNamed("water"), SubLava: blockNamed("lava"),
		SubCopperOre: blockNamed("copper_ore"), SubRawCopperBlock: blockNamed("raw_copper_block"),
		SubGranite: blockNamed("granite"), SubDeepslateIronOre: blockNamed("deepslate_iron_ore"),
		SubRawIronBlock: blockNamed("raw_iron_block"), SubTuff: blockNamed("tuff"),
	}
})

// Terrain runs noise, biomes and surface for one chunk.
func (g *Generator) Terrain(cx, cz int32) *ProtoChunk {
	rs := g.RS
	nc := rs.NewChunk(cx, cz)
	g.ChunkBiomes(cx, cz, nc)
	col := rs.fillNoise(nc, cx, cz)
	p := newProto(cx, cz, col.MinY, col.Height)
	def := blockNamed(rs.settings.defaultBlock)
	subs := substanceBlocks()
	// doFill writes cell by cell, top down within each cell column; the
	// heightmaps only depend on the final column contents, so writing in
	// y order gives the same maps.
	for y := col.MinY; y < col.MinY+col.Height; y++ {
		for z := int32(0); z < 16; z++ {
			for x := int32(0); x < 16; x++ {
				s := col.At(x, y, z)
				if s == SubAir {
					continue
				}
				b := def
				if s != SubDefault {
					b = subs[s]
				}
				p.Set(x, y, z, b)
			}
		}
	}
	p.FluidMarks = col.FluidMarks
	p.Biomes = g.ChunkBiomes(cx, cz, nil)
	rs.BuildSurface(p, nc, g.BiomeAt)
	if g.Carve {
		g.ApplyCarvers(p, nc)
	}
	return p
}
