package worldgen

import (
	"fmt"
	"os"
	"sync"
)

// The features stage behind the generator. A chunk's terrain stays a
// pure function of the seed and its position, so any chunk may be
// dropped from the caches and rebuilt the same:
//
//   - Each chunk's features run once, against its 3×3 neighbourhood as
//     the carvers left it (the region the probe pins them against), and
//     the run's writes are kept per neighbour.
//   - A finished chunk is its carved blocks with the writes of the nine
//     runs that reach it laid over, its own run first and then the
//     others in a fixed order, the first run to write a block keeping
//     it.
//
// Vanilla runs neighbouring chunks' features in whatever order the
// chunks happen to generate, each over the blocks the earlier ones left,
// so its blocks along chunk borders depend on that order; the fixed
// order here stands in for it.

// carvedChunk is a chunk as the carvers leave it, its sections
// compressed as a TerrainChunk's are.
type carvedChunk struct {
	cx, cz       int32
	minY, height int32
	sections     [][]StateID
	surfaceTop   [256]int32
	wg           [2][256]int32
	// fluidMarks are the chunk-relative positions whose aquifer fluid the
	// noise stage marked for an update once the chunk is complete.
	fluidMarks []Point
}

// genChunk expands a carved chunk into a fresh, writable copy.
func (c *carvedChunk) genChunk() *GenChunk {
	g := &GenChunk{CX: c.cx, CZ: c.cz, MinY: c.minY, Height: c.height, States: make([]StateID, c.height*256)}
	for sy, sec := range c.sections {
		dst := g.States[sy*4096 : (sy+1)*4096]
		if len(sec) == 1 {
			for i := range dst {
				dst[i] = sec[0]
			}
		} else {
			copy(dst, sec)
		}
	}
	g.surfaceTop = c.surfaceTop
	g.heights = &finalHeights{wg: c.wg, frozen: true}
	return g
}

func compressSections(states []StateID, height int32) [][]StateID {
	var out [][]StateID
	for sy := int32(0); sy < height/16; sy++ {
		sec := states[sy*4096 : (sy+1)*4096]
		uniform := true
		for _, s := range sec[1:] {
			if s != sec[0] {
				uniform = false
				break
			}
		}
		if uniform {
			out = append(out, []StateID{sec[0]})
		} else {
			out = append(out, append([]StateID(nil), sec...))
		}
	}
	return out
}

// blockChange is one block a feature run changed, by chunk index.
type blockChange struct {
	index int32
	state StateID
}

// featureRun is what one chunk's features stage wrote into each chunk
// of its 3×3, indexed (dx+1)*3 + (dz+1).
type featureRun struct {
	changes       [9][]blockChange
	blockEntities [9]map[Point]*GenBlockEntity
	entities      [9][]GenEntity
	fluidTicks    [9][]GenFluidTick
}

// stageCache is a bounded, least-recently-used cache of one stage's
// results, each computed once by whichever caller first asks.
type stageCache[T any] struct {
	mu      sync.Mutex
	entries map[[2]int32]*stageEntry[T]
	limit   int
	clock   int64
}

type stageEntry[T any] struct {
	done  chan struct{}
	value T
	used  int64
}

func newStageCache[T any](limit int) *stageCache[T] {
	return &stageCache[T]{entries: map[[2]int32]*stageEntry[T]{}, limit: limit}
}

func (c *stageCache[T]) get(cx, cz int32, compute func() T) T {
	key := [2]int32{cx, cz}
	c.mu.Lock()
	c.clock++
	e, ok := c.entries[key]
	if !ok {
		e = &stageEntry[T]{done: make(chan struct{})}
		c.entries[key] = e
		c.evict()
	}
	e.used = c.clock
	c.mu.Unlock()
	if !ok {
		e.value = compute()
		close(e.done)
	}
	<-e.done
	return e.value
}

// evict drops least recently used finished entries over the limit.
// Caller holds mu.
func (c *stageCache[T]) evict() {
	for len(c.entries) > c.limit {
		var victim [2]int32
		oldest := int64(-1)
		for k, e := range c.entries {
			select {
			case <-e.done:
			default:
				continue
			}
			if oldest < 0 || e.used < oldest {
				oldest, victim = e.used, k
			}
		}
		if oldest < 0 {
			return
		}
		delete(c.entries, victim)
	}
}

// carved returns chunk (cx, cz) after the carvers.
func (g *Generator) carved(cx, cz int32) *carvedChunk {
	return g.carvedCache.get(cx, cz, func() *carvedChunk { return g.carve(cx, cz) })
}

func (g *Generator) carve(cx, cz int32) *carvedChunk {
	var region BiomeRegion
	region.cx, region.cz = cx, cz
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			region.chunks[dx+1][dz+1] = g.Biomes(cx+dx, cz+dz)
		}
	}
	w := <-g.workers
	blocks, marks := w.filler.Fill(cx, cz)
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
	c.freezeWorldgenHeights()
	return &carvedChunk{cx: cx, cz: cz, minY: c.MinY, height: c.Height,
		sections: compressSections(c.States, c.Height), surfaceTop: c.surfaceTop, wg: c.heights.wg, fluidMarks: marks}
}

// featureSteps is the generator's per-step feature order, built once.
func (g *Generator) featureSteps() [][]*placedFeature {
	g.stepsOnce.Do(func() { g.steps = featureSteps(possibleBiomes(g.source)) })
	return g.steps
}

// runFeatures returns chunk (cx, cz)'s features run.
func (g *Generator) runFeatures(cx, cz int32) *featureRun {
	return g.runCache.get(cx, cz, func() *featureRun { return g.decorateChunk(cx, cz) })
}

var featurePanicOnce sync.Map

func (g *Generator) decorateChunk(cx, cz int32) *featureRun {
	rs := g.rs
	r := &decoRegion{rs: rs, seed: rs.Seed, cx: cx, cz: cz, zoomSeed: rs.ZoomSeed(),
		genMinY: int32(rs.Settings.MinY), genDepth: int32(rs.Settings.Height), seaLevel: int32(rs.Settings.SeaLevel)}
	for dx := int32(-2); dx <= 2; dx++ {
		for dz := int32(-2); dz <= 2; dz++ {
			r.biomes[dx+2][dz+2] = g.Biomes(cx+dx, cz+dz)
		}
	}
	// The neighbourhood is carved in parallel: a chunk asked for on its
	// own would otherwise build its whole dependency cone on one core.
	var carved [9]*carvedChunk
	var wg sync.WaitGroup
	for i := range carved {
		wg.Add(1)
		go func() {
			defer wg.Done()
			carved[i] = g.carved(cx+int32(i/3)-1, cz+int32(i%3)-1)
		}()
	}
	wg.Wait()
	for i := range carved {
		r.chunks[i/3][i%3] = carved[i].genChunk()
	}
	r.minY, r.height = r.chunks[1][1].MinY, r.chunks[1][1].Height
	steps := g.featureSteps()
	w := <-g.workers
	r.decorate(steps, func(step, index int, f *placedFeature, place func() bool) {
		defer func() {
			if e := recover(); e != nil {
				if _, ok := e.(unsupportedFeature); ok {
					return
				}
				// A feature that fails leaves what it placed; report each
				// failing feature once rather than lose the chunk.
				if _, seen := featurePanicOnce.LoadOrStore(f.name, true); !seen {
					fmt.Fprintf(os.Stderr, "worldgen: feature %s failed in chunk %d,%d: %v\n", f.name, cx, cz, e)
				}
			}
		}()
		place()
	})
	g.workers <- w
	run := &featureRun{}
	for i := range carved {
		c := r.chunks[i/3][i%3]
		before := carved[i].genChunk().States
		for j, s := range c.States {
			if s != before[j] {
				run.changes[i] = append(run.changes[i], blockChange{int32(j), s})
			}
		}
		run.blockEntities[i] = c.BlockEntities
		run.entities[i] = c.Entities
		run.fluidTicks[i] = c.FluidTicks
	}
	return run
}

// finish builds chunk (cx, cz) from its carved blocks and the features
// runs reaching it.
func (g *Generator) finish(cx, cz int32) *TerrainChunk {
	base := g.carved(cx, cz)
	c := base.genChunk()
	won := make([]int8, len(c.States))
	out := &TerrainChunk{CX: cx, CZ: cz, MinY: c.MinY, Biomes: g.Biomes(cx, cz)}
	// The chunk's own run first, then its neighbours'.
	order := [9][2]int32{{0, 0}, {-1, -1}, {-1, 0}, {-1, 1}, {0, -1}, {0, 1}, {1, -1}, {1, 0}, {1, 1}}
	var runs [9]*featureRun
	var wg sync.WaitGroup
	for n, o := range order {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runs[n] = g.runFeatures(cx+o[0], cz+o[1])
		}()
	}
	wg.Wait()
	for n, o := range order {
		run := runs[n]
		// This chunk sits at -o from the run's chunk.
		k := (1-o[0])*3 + (1 - o[1])
		for _, ch := range run.changes[k] {
			if won[ch.index] == 0 {
				won[ch.index] = int8(n + 1)
				c.States[ch.index] = ch.state
			}
		}
		for p, be := range run.blockEntities[k] {
			if won[c.index(p.X&15, p.Y, p.Z&15)] == int8(n+1) {
				if out.BlockEntities == nil {
					out.BlockEntities = map[Point]*GenBlockEntity{}
				}
				out.BlockEntities[p] = be
			}
		}
		out.Entities = append(out.Entities, run.entities[k]...)
		out.FluidTicks = append(out.FluidTicks, run.fluidTicks[k]...)
	}
	// A completed chunk ticks the fluids its aquifers marked, as they
	// stand after the features.
	for _, m := range base.fluidMarks {
		if st := c.States[c.index(m.X, m.Y, m.Z)]; st.hasFluid() {
			out.FluidTicks = append(out.FluidTicks, GenFluidTick{Point{cx*16 + m.X, m.Y, cz*16 + m.Z}, st.fluidName()})
		}
	}
	out.sections = compressSections(c.States, c.Height)
	return out
}
