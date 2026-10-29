package worldgen

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"

	"mc-go/internal/world"
)

// wgRandom is WorldgenRandom over a non-legacy source: every draw goes
// through next(bits) = nextLong() >>> (64-bits) and BitRandomSource's
// default derivations, which differ from Xoroshiro's own nextInt/
// nextFloat; fork and forkPositional are LegacyRandomSource's.
type wgRandom struct {
	src *Xoroshiro
	g   gaussian
}

func newWGRandom(seed int64) *wgRandom { return &wgRandom{src: NewXoroshiro(seed)} }

func (r *wgRandom) next(bits uint) int32 { return int32(uint64(r.src.NextLong()) >> (64 - bits)) }

func (r *wgRandom) NextInt() int32             { return r.next(32) }
func (r *wgRandom) NextIntN(bound int32) int32 { return bitNextIntN(r.next, bound) }
func (r *wgRandom) NextLong() int64 {
	hi := r.next(32)
	lo := r.next(32)
	return int64(hi)<<32 + int64(lo)
}
func (r *wgRandom) NextBool() bool     { return r.next(1) != 0 }
func (r *wgRandom) NextFloat() float32 { return float32(r.next(24)) * 5.9604645e-8 }
func (r *wgRandom) NextDouble() float64 {
	hi := int64(r.next(26))
	lo := int64(r.next(27))
	return float64(hi<<27+lo) * 1.1102230246251565e-16
}
func (r *wgRandom) NextGaussian() float64 { return r.g.sample(r.NextDouble) }
func (r *wgRandom) Consume(n int) {
	for i := 0; i < n; i++ {
		r.next(32)
	}
}
func (r *wgRandom) Fork() Random                     { return NewLegacy(r.NextLong()) }
func (r *wgRandom) ForkPositional() PositionalRandom { return &LegacyPositional{seed: r.NextLong()} }

// SetSeed reseeds the wrapped source only: WorldgenRandom.setSeed does
// not reach LegacyRandomSource.setSeed, so a cached second Gaussian
// survives into the next feature's seed.
func (r *wgRandom) SetSeed(seed int64) {
	r.src.SetSeed(seed)
}

// setDecorationSeed is WorldgenRandom.setDecorationSeed.
func setDecorationSeed(r Random, seed int64, x, z int32) int64 {
	r.SetSeed(seed)
	a := r.NextLong() | 1
	b := r.NextLong() | 1
	n := int64(x)*a + int64(z)*b ^ seed
	r.SetSeed(n)
	return n
}

// setFeatureSeed is WorldgenRandom.setFeatureSeed.
func setFeatureSeed(r Random, decoration int64, index, step int32) {
	r.SetSeed(decoration + int64(index) + int64(10000*step))
}

// stepFeatures is FeatureSorter.StepFeatureData: one step's placed
// features in their global order.
type stepFeatures struct {
	names []string
	index map[string]int32
}

// buildFeaturesPerStep is FeatureSorter.buildFeaturesPerStep: a
// depth-first topological order over every biome's feature sequence,
// nodes ordered by (step, first-seen index).
func buildFeaturesPerStep(biomeFeatures [][][]string) []stepFeatures {
	type node struct{ step, idx int32 }
	ids := map[string]int32{}
	names := []string{}
	key := func(n node) int64 { return int64(n.step)<<32 | int64(n.idx) }
	edges := map[int64]map[int64]bool{}
	nodes := map[int64]node{}
	maxSteps := 0
	for _, steps := range biomeFeatures {
		maxSteps = max(maxSteps, len(steps))
		var seq []node
		for si, step := range steps {
			for _, f := range step {
				id, ok := ids[f]
				if !ok {
					id = int32(len(names))
					ids[f] = id
					names = append(names, f)
				}
				seq = append(seq, node{int32(si), id})
			}
		}
		for i, n := range seq {
			k := key(n)
			nodes[k] = n
			if edges[k] == nil {
				edges[k] = map[int64]bool{}
			}
			if i < len(seq)-1 {
				nk := key(seq[i+1])
				edges[k][nk] = true
				nodes[nk] = seq[i+1]
			}
		}
	}
	sortedKeys := func(m map[int64]bool) []int64 {
		out := make([]int64, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}
	all := map[int64]bool{}
	for k := range edges {
		all[k] = true
	}
	visited := map[int64]bool{}
	onPath := map[int64]bool{}
	var order []node
	var dfs func(k int64) bool
	dfs = func(k int64) bool {
		if visited[k] {
			return false
		}
		if onPath[k] {
			return true
		}
		onPath[k] = true
		for _, nk := range sortedKeys(edges[k]) {
			if dfs(nk) {
				return true
			}
		}
		delete(onPath, k)
		visited[k] = true
		order = append(order, nodes[k])
		return false
	}
	for _, k := range sortedKeys(all) {
		if visited[k] {
			continue
		}
		if dfs(k) {
			panic("worldgen: feature order cycle")
		}
	}
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	out := make([]stepFeatures, maxSteps)
	for s := range out {
		sf := stepFeatures{index: map[string]int32{}}
		for _, n := range order {
			if int(n.step) == s {
				sf.index[names[n.idx]] = int32(len(sf.names))
				sf.names = append(sf.names, names[n.idx])
			}
		}
		out[s] = sf
	}
	return out
}

// biomeFeatureSteps reads a biome's features: per step, placed feature
// names (namespace trimmed).
func (b *BiomeInfo) featureSteps() [][]string {
	b.featOnce.Do(func() {
		raw, ok := b.raw["features"]
		if !ok {
			return
		}
		var steps []json.RawMessage
		jsonUnmarshal(raw, &steps)
		for _, st := range steps {
			var names []string
			var one string
			if json.Unmarshal(st, &one) == nil {
				names = []string{one}
			} else {
				jsonUnmarshal(st, &names)
			}
			for i := range names {
				names[i] = trimNS(names[i])
			}
			b.features = append(b.features, names)
		}
	})
	return b.features
}

func (b *BiomeInfo) hasFeature(name string) bool {
	b.featSetOnce.Do(func() {
		b.featSet = map[string]bool{}
		for _, st := range b.featureSteps() {
			for _, f := range st {
				b.featSet[f] = true
			}
		}
	})
	return b.featSet[name]
}

// possibleBiomes is BiomeSource.possibleBiomes for the multi-noise
// source: distinct biomes in parameter-list order.
func (bs *BiomeSource) possibleBiomes() []string {
	seen := map[string]bool{}
	var out []string
	for _, e := range bs.entries {
		if !seen[e.biome] {
			seen[e.biome] = true
			out = append(out, e.biome)
		}
	}
	return out
}

func (g *Generator) featuresPerStep() []stepFeatures {
	g.featOnce.Do(func() {
		var all [][][]string
		for _, b := range g.Biomes.possibleBiomes() {
			all = append(all, g.RS.BiomeInfo(b).featureSteps())
		}
		g.steps = buildFeaturesPerStep(all)
	})
	return g.steps
}

var featureOnceMu sync.Mutex

// Decorate is ChunkGenerator.applyBiomeDecoration for the region's
// center chunk.
func (g *Generator) Decorate(rg *Region) {
	steps := g.featuresPerStep()
	center := rg.center
	minY := g.RS.settings.minY
	_ = minY
	ox, oz := center.CX*16, center.CZ*16
	r := newWGRandom(0)
	deco := setDecorationSeed(r, g.RS.Seed, ox, oz)
	biomeSet := map[string]bool{}
	for dz := int32(-1); dz <= 1; dz++ {
		for dx := int32(-1); dx <= 1; dx++ {
			if c := rg.chunk(center.CX+dx, center.CZ+dz); c != nil {
				for _, b := range c.Biomes {
					biomeSet[b] = true
				}
			}
		}
	}
	// GenerationStep.Decoration has 11 steps; each places its
	// structures before its features.
	for j := 0; j < max(11, len(steps)); j++ {
		g.decorateStructures(rg, r, deco, j)
		if j >= len(steps) {
			continue
		}
		idxSet := map[int32]bool{}
		for b := range biomeSet {
			fs := g.RS.BiomeInfo(b).featureSteps()
			if j >= len(fs) {
				continue
			}
			for _, f := range fs[j] {
				idxSet[steps[j].index[f]] = true
			}
		}
		idx := make([]int32, 0, len(idxSet))
		for i := range idxSet {
			idx = append(idx, i)
		}
		sort.Slice(idx, func(a, b int) bool { return idx[a] < idx[b] })
		for _, i := range idx {
			name := steps[j].names[i]
			setFeatureSeed(r, deco, i, int32(j))
			pf := g.placed(name)
			pf.place(&placeCtx{g: g, rg: rg, top: name}, r, [3]int32{ox, minY, oz})
		}
	}
}

func stripNS(s string) string { return strings.TrimPrefix(s, "minecraft:") }

// Region is WorldGenRegion around a chunk being decorated: the center
// and its eight neighbours, writable within one chunk of the center.
type Region struct {
	g      *Generator
	center *ProtoChunk
	chunks map[[2]int32]*ProtoChunk
}

// NewRegion builds a decoration region; chunks must hold the 3x3 area.
func (g *Generator) NewRegion(center *ProtoChunk, chunks map[[2]int32]*ProtoChunk) *Region {
	center.primeHeightmaps()
	return &Region{g: g, center: center, chunks: chunks}
}

func (rg *Region) chunk(cx, cz int32) *ProtoChunk { return rg.chunks[[2]int32{cx, cz}] }

func (rg *Region) chunkAt(x, z int32) *ProtoChunk { return rg.chunk(x>>4, z>>4) }

// Block is WorldGenRegion.getBlockState (air outside the region).
func (rg *Region) Block(x, y, z int32) world.Block {
	c := rg.chunkAt(x, z)
	if c == nil {
		return world.Air
	}
	return c.Get(x&15, y, z&15)
}

// SetBlock is WorldGenRegion.setBlock: refused outside the write zone.
func (rg *Region) SetBlock(x, y, z int32, b world.Block) bool {
	cx, cz := x>>4, z>>4
	if cx < rg.center.CX-1 || cx > rg.center.CX+1 || cz < rg.center.CZ-1 || cz > rg.center.CZ+1 {
		return false
	}
	c := rg.chunk(cx, cz)
	if c == nil || y < c.MinY || y >= c.MinY+c.Height {
		return false
	}
	c.primeHeightmaps()
	if rg.g.WriteLog != nil {
		rg.g.WriteLog(BlockPos{x, y, z}, [2]int32{rg.center.CX, rg.center.CZ}, b)
	}
	c.Set(x&15, y, z&15, b)
	if hasPostProcess(b) {
		c.PostProcess = append(c.PostProcess, [3]int32{x, y, z})
	}
	return true
}

// Height is WorldGenRegion.getHeight: the chunk's height plus one.
func (rg *Region) Height(t int, x, z int32) int32 {
	c := rg.chunkAt(x, z)
	if c == nil {
		return rg.g.RS.lvMinY
	}
	// ProtoChunk.getHeight primes a missing heightmap from the blocks
	// on first read (a neighbour no feature has written yet).
	c.primeHeightmaps()
	return c.HeightAt(t, x&15, z&15) + 1
}

// Biome is the region's zoomed biome lookup.
func (rg *Region) Biome(x, y, z int32) string { return rg.g.BiomeAt(x, y, z) }

// MinY is the dimension floor.
func (rg *Region) MinY() int32 { return rg.g.RS.lvMinY }

// MaxY is the highest buildable y.
func (rg *Region) MaxY() int32 { return rg.g.RS.lvMinY + rg.g.RS.lvHeight - 1 }

// setRaw writes straight into the chunk section, as BulkSectionAccess
// does: no heightmap or post-processing bookkeeping.
func (rg *Region) setRaw(x, y, z int32, b world.Block) {
	c := rg.chunkAt(x, z)
	if c == nil || y < c.MinY || y >= c.MinY+c.Height {
		return
	}
	if rg.g.WriteLog != nil {
		rg.g.WriteLog(BlockPos{x, y, z}, [2]int32{rg.center.CX, rg.center.CZ}, b)
	}
	c.Blocks[c.idx(x&15, y, z&15)] = b
}
