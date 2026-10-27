package worldgen

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
)

// Generator produces a dimension's terrain on demand, chunk by chunk:
// biomes, the noise stage, the surface stage and the carvers. Generated chunks and
// chunk biome storage are cached (bounded) and regenerated identically
// after eviction. It is safe for concurrent use.
type Generator struct {
	rs       *RandomState
	params   *MultiNoiseBiomes
	source   BiomeSource
	workers  chan *genWorker
	shards   [16]genShard
	perShard int
	clock    atomic.Int64

	prefetchOnce sync.Once
	prefetchQ    chan [2]int32
	prefetchMu   sync.Mutex
	prefetching  map[[2]int32]bool
}

type genWorker struct {
	filler   *NoiseChunkFiller
	searcher *BiomeSearcher
}

type genShard struct {
	mu     sync.Mutex
	chunks map[[2]int32]*chunkEntry
	biomes map[[2]int32]*biomeEntry
}

type chunkEntry struct {
	done  chan struct{}
	chunk *TerrainChunk
	used  atomic.Int64
}

type biomeEntry struct {
	done   chan struct{}
	biomes *ChunkBiomes
	used   atomic.Int64
}

// TerrainChunk is a generated chunk: its blocks by section (a uniform
// section stored as a single state) and its biome storage.
type TerrainChunk struct {
	CX, CZ   int32
	MinY     int32
	sections [][]StateID
	Biomes   *ChunkBiomes
}

// Block is the state at chunk-relative x, z and absolute y.
func (c *TerrainChunk) Block(x, y, z int32) StateID {
	sy := (y - c.MinY) >> 4
	if sy < 0 || int(sy) >= len(c.sections) {
		return stAir
	}
	sec := c.sections[sy]
	if len(sec) == 1 {
		return sec[0]
	}
	return sec[((y-c.MinY)&15)*256+z*16+x]
}

// NewGenerator builds a generator for registered noise settings and a
// multi-noise parameter list, e.g. ("minecraft:overworld",
// "minecraft:overworld").
func NewGenerator(settingsID, paramsID string, seed int64) (*Generator, error) {
	params, ok := BiomeParameters(paramsID)
	if !ok {
		return nil, fmt.Errorf("worldgen: unknown biome parameters %s", paramsID)
	}
	return newGenerator(settingsID, params, params, seed)
}

// NewDimensionGenerator builds the vanilla generator of a dimension:
// "minecraft:overworld", "minecraft:the_nether" or "minecraft:the_end".
func NewDimensionGenerator(dimension string, seed int64) (*Generator, error) {
	switch dimension {
	case "minecraft:overworld":
		return NewGenerator("minecraft:overworld", "minecraft:overworld", seed)
	case "minecraft:the_nether":
		return NewGenerator("minecraft:nether", "minecraft:nether", seed)
	case "minecraft:the_end":
		params, _ := BiomeParameters("minecraft:overworld")
		return newGenerator("minecraft:end", EndBiomeSource{}, params, seed)
	}
	return nil, fmt.Errorf("worldgen: no generator for %s", dimension)
}

func newGenerator(settingsID string, src BiomeSource, params *MultiNoiseBiomes, seed int64) (*Generator, error) {
	settings, ok := Settings(settingsID)
	if !ok {
		return nil, fmt.Errorf("worldgen: unknown noise settings %s", settingsID)
	}
	g := &Generator{rs: NewRandomState(settings, seed), params: params, source: src, perShard: 96}
	n := runtime.GOMAXPROCS(0)
	g.workers = make(chan *genWorker, n)
	for i := 0; i < n; i++ {
		g.workers <- &genWorker{filler: NewNoiseChunkFiller(g.rs), searcher: params.NewSearcher()}
	}
	for i := range g.shards {
		g.shards[i].chunks = map[[2]int32]*chunkEntry{}
		g.shards[i].biomes = map[[2]int32]*biomeEntry{}
	}
	return g, nil
}

// RandomState is the generator's per-seed state.
func (g *Generator) RandomState() *RandomState { return g.rs }

func (g *Generator) shard(cx, cz int32) *genShard {
	h := uint32(cx)*0x9E3779B1 ^ uint32(cz)*0x85EBCA77
	return &g.shards[h>>28]
}

// Chunk returns chunk (cx, cz), generating it if needed.
func (g *Generator) Chunk(cx, cz int32) *TerrainChunk {
	sh := g.shard(cx, cz)
	key := [2]int32{cx, cz}
	sh.mu.Lock()
	e, ok := sh.chunks[key]
	if !ok {
		e = &chunkEntry{done: make(chan struct{})}
		sh.chunks[key] = e
		evictChunks(sh.chunks, g.perShard)
	}
	sh.mu.Unlock()
	e.used.Store(g.clock.Add(1))
	if !ok {
		e.chunk = g.generate(cx, cz)
		close(e.done)
	}
	<-e.done
	return e.chunk
}

// Biomes returns chunk (cx, cz)'s biome storage, filling it if needed.
func (g *Generator) Biomes(cx, cz int32) *ChunkBiomes {
	sh := g.shard(cx, cz)
	key := [2]int32{cx, cz}
	sh.mu.Lock()
	e, ok := sh.biomes[key]
	if !ok {
		e = &biomeEntry{done: make(chan struct{})}
		sh.biomes[key] = e
		evictBiomes(sh.biomes, g.perShard*4)
	}
	sh.mu.Unlock()
	e.used.Store(g.clock.Add(1))
	if !ok {
		w := <-g.workers
		e.biomes = FillChunkBiomesFrom(g.rs, g.source, w.searcher, cx, cz)
		g.workers <- w
		close(e.done)
	}
	<-e.done
	return e.biomes
}

// evictChunks drops the least recently used finished entry once the map
// outgrows its limit. Caller holds the shard lock.
func evictChunks(m map[[2]int32]*chunkEntry, limit int) {
	for len(m) > limit {
		var victim [2]int32
		oldest := int64(-1)
		for k, e := range m {
			select {
			case <-e.done:
			default:
				continue
			}
			if u := e.used.Load(); oldest < 0 || u < oldest {
				oldest, victim = u, k
			}
		}
		if oldest < 0 {
			return
		}
		delete(m, victim)
	}
}

func evictBiomes(m map[[2]int32]*biomeEntry, limit int) {
	for len(m) > limit {
		var victim [2]int32
		oldest := int64(-1)
		for k, e := range m {
			select {
			case <-e.done:
			default:
				continue
			}
			if u := e.used.Load(); oldest < 0 || u < oldest {
				oldest, victim = u, k
			}
		}
		if oldest < 0 {
			return
		}
		delete(m, victim)
	}
}

func (g *Generator) generate(cx, cz int32) *TerrainChunk {
	var region BiomeRegion
	region.cx, region.cz = cx, cz
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			region.chunks[dx+1][dz+1] = g.Biomes(cx+dx, cz+dz)
		}
	}
	w := <-g.workers
	blocks, _ := w.filler.Fill(cx, cz)
	c := w.filler.NewGenChunk(cx, cz, blocks)
	zoom := g.rs.ZoomSeed()
	w.filler.BuildSurface(c, region.Block(zoom))
	// Carvers read biomes straight from the biome source: the starting
	// chunk's corner quart, and block biomes zoomed from it.
	direct := func(qx, qy, qz int32) string {
		return biomeNameTable[g.source.biomeIndex(g.rs, w.searcher, qx, qy, qz)]
	}
	w.filler.Carve(c, func(sx, sz int32) string { return direct(sx*4, 0, sz*4) },
		func(x, y, z int32) string { return ZoomBiome(zoom, x, y, z, direct) })
	g.workers <- w
	out := &TerrainChunk{CX: cx, CZ: cz, MinY: c.MinY, Biomes: region.Center()}
	for sy := int32(0); sy < c.Height/16; sy++ {
		sec := c.States[sy*4096 : (sy+1)*4096]
		uniform := true
		for _, s := range sec[1:] {
			if s != sec[0] {
				uniform = false
				break
			}
		}
		if uniform {
			out.sections = append(out.sections, []StateID{sec[0]})
		} else {
			out.sections = append(out.sections, append([]StateID(nil), sec...))
		}
	}
	return out
}

// Ready reports whether chunk (cx, cz) is generated and cached.
func (g *Generator) Ready(cx, cz int32) bool {
	sh := g.shard(cx, cz)
	sh.mu.Lock()
	e, ok := sh.chunks[[2]int32{cx, cz}]
	sh.mu.Unlock()
	if !ok {
		return false
	}
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

// Prefetch queues chunk (cx, cz) for background generation, unless it
// is ready, already queued, or the queue is full (the caller asks again
// later).
func (g *Generator) Prefetch(cx, cz int32) {
	if g.Ready(cx, cz) {
		return
	}
	g.prefetchOnce.Do(func() {
		g.prefetchQ = make(chan [2]int32, 4096)
		g.prefetching = map[[2]int32]bool{}
		for i := 0; i < cap(g.workers); i++ {
			go func() {
				for k := range g.prefetchQ {
					g.Chunk(k[0], k[1])
					g.prefetchMu.Lock()
					delete(g.prefetching, k)
					g.prefetchMu.Unlock()
				}
			}()
		}
	})
	k := [2]int32{cx, cz}
	g.prefetchMu.Lock()
	defer g.prefetchMu.Unlock()
	if g.prefetching[k] {
		return
	}
	select {
	case g.prefetchQ <- k:
		g.prefetching[k] = true
	default:
	}
}

// BlockAt is the generated state at a block.
func (g *Generator) BlockAt(x, y, z int32) StateID {
	return g.Chunk(x>>4, z>>4).Block(x&15, y, z&15)
}

// BiomeAt is the stored biome at a quart.
func (g *Generator) BiomeAt(qx, qy, qz int32) string {
	return g.Biomes(qx>>2, qz>>2).At(qx, qy, qz)
}
