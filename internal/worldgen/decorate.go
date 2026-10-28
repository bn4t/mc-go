package worldgen

import "sort"

// The features stage. A chunk is decorated inside its generation
// region: it may write the 3×3 chunks around it and read a ring further
// out. Placed features run step by step in the dimension's global
// feature order, each seeded from the chunk's decoration seed, its index
// and its step, restricted to the features of the biomes present in the
// 3×3.

// Heightmap kinds.
const (
	hmWorldSurfaceWG = iota
	hmOceanFloorWG
	hmWorldSurface
	hmOceanFloor
	hmMotionBlocking
	hmMotionBlockingNoLeaves
)

var heightmapNames = map[string]int{
	"WORLD_SURFACE_WG": hmWorldSurfaceWG, "OCEAN_FLOOR_WG": hmOceanFloorWG,
	"WORLD_SURFACE": hmWorldSurface, "OCEAN_FLOOR": hmOceanFloor,
	"MOTION_BLOCKING": hmMotionBlocking, "MOTION_BLOCKING_NO_LEAVES": hmMotionBlockingNoLeaves,
}

func heightmapMatches(kind int, s StateID) bool {
	switch kind {
	case hmWorldSurfaceWG, hmWorldSurface:
		return !s.isAir()
	case hmOceanFloorWG, hmOceanFloor:
		return s.blocksMotion()
	case hmMotionBlocking:
		return s.blocksMotion() || s.hasFluid()
	}
	return (s.blocksMotion() || s.hasFluid()) && !s.isLeaves()
}

// finalHeights are a chunk's live heightmaps from the carvers stage on,
// primed on first use and kept exact through writes.
type finalHeights struct {
	maps [4]*[256]int32 // WORLD_SURFACE, OCEAN_FLOOR, MOTION_BLOCKING, MOTION_BLOCKING_NO_LEAVES
	// wg are the worldgen heightmaps frozen as the carvers left them.
	wg     [2][256]int32
	frozen bool
}

// freezeWorldgenHeights records the worldgen heightmaps as they stand,
// as a chunk finishing its carvers stage keeps them.
func (c *GenChunk) freezeWorldgenHeights() {
	if c.heights == nil {
		c.heights = &finalHeights{}
	}
	h := c.heights
	h.wg[0] = c.surfaceTop
	for z := int32(0); z < 16; z++ {
		for x := int32(0); x < 16; x++ {
			h.wg[1][z*16+x] = c.scanHeight(hmOceanFloorWG, x, z)
		}
	}
	h.frozen = true
}

func (c *GenChunk) scanHeight(kind int, x, z int32) int32 {
	for y := c.MinY + c.Height - 1; y >= c.MinY; y-- {
		if heightmapMatches(kind, c.Get(x, y, z)) {
			return y + 1
		}
	}
	return c.MinY
}

// heightAt is a heightmap's first available Y at chunk-relative (x, z).
func (c *GenChunk) heightAt(kind int, x, z int32) int32 {
	if kind <= hmOceanFloorWG {
		if c.heights == nil || !c.heights.frozen {
			if kind == hmWorldSurfaceWG {
				return c.surfaceTop[z*16+x]
			}
			return c.scanHeight(kind, x, z)
		}
		return c.heights.wg[kind][z*16+x]
	}
	return c.finalMap(kind)[z*16+x]
}

func (c *GenChunk) finalMap(kind int) *[256]int32 {
	if c.heights == nil {
		c.heights = &finalHeights{}
	}
	m := c.heights.maps[kind-hmWorldSurface]
	if m == nil {
		m = new([256]int32)
		for z := int32(0); z < 16; z++ {
			for x := int32(0); x < 16; x++ {
				m[z*16+x] = c.scanHeight(kind, x, z)
			}
		}
		c.heights.maps[kind-hmWorldSurface] = m
	}
	return m
}

// setLive writes a state as a chunk past its carvers stage does: the
// final heightmaps (primed if missing) follow the write.
func (c *GenChunk) setLive(x, y, z int32, s StateID) {
	if y < c.MinY || y >= c.MinY+c.Height {
		return
	}
	c.States[c.index(x, y, z)] = s
	for kind := hmWorldSurface; kind <= hmMotionBlockingNoLeaves; kind++ {
		m := c.finalMap(kind)
		top := m[z*16+x]
		if y <= top-2 {
			continue
		}
		if heightmapMatches(kind, s) {
			if y >= top {
				m[z*16+x] = y + 1
			}
		} else if top-1 == y {
			m[z*16+x] = c.MinY
			for yy := y - 1; yy >= c.MinY; yy-- {
				if heightmapMatches(kind, c.Get(x, yy, z)) {
					m[z*16+x] = yy + 1
					break
				}
			}
		}
	}
}

// setRaw writes a state into its section without touching heightmaps,
// as bulk section access does.
func (c *GenChunk) setRaw(x, y, z int32, s StateID) {
	if y < c.MinY || y >= c.MinY+c.Height {
		return
	}
	c.States[c.index(x, y, z)] = s
}

// decoRegion is the generation region of the chunk being decorated.
type decoRegion struct {
	rs                *RandomState
	seed              int64
	cx, cz            int32
	chunks            [3][3]*GenChunk
	biomes            [5][5]*ChunkBiomes
	zoomSeed          int64
	minY, height      int32
	genMinY, genDepth int32
	seaLevel          int32
	// biomeFeatures lists, per biome, the placed features it has.
	random Random
	// deco is the decoration source while decorate runs.
	deco *decoRandom
}

func (r *decoRegion) chunk(x, z int32) *GenChunk {
	gx, gz := x>>4-r.cx+1, z>>4-r.cz+1
	if gx < 0 || gx > 2 || gz < 0 || gz > 2 {
		return nil
	}
	return r.chunks[gx][gz]
}

func (r *decoRegion) outside(y int32) bool { return y < r.minY || y >= r.minY+r.height }

func (r *decoRegion) get(p Point) StateID {
	if r.outside(p.Y) {
		return stVoidAir
	}
	c := r.chunk(p.X, p.Z)
	if c == nil {
		panic("worldgen: read outside the generation region")
	}
	return c.Get(p.X&15, p.Y, p.Z&15)
}

func (r *decoRegion) canWrite(p Point) bool {
	dx, dz := p.X>>4-r.cx, p.Z>>4-r.cz
	return dx >= -1 && dx <= 1 && dz >= -1 && dz <= 1
}

// set is the region's setBlock: refused outside the write zone.
func (r *decoRegion) set(p Point, s StateID) bool {
	if !r.canWrite(p) {
		return false
	}
	c := r.chunk(p.X, p.Z)
	c.setLive(p.X&15, p.Y, p.Z&15, s)
	// The block that kept a block entity is gone.
	delete(c.BlockEntities, p)
	return true
}

// scheduleFluidTick queues a fluid update at p, in the chunk holding it.
func (r *decoRegion) scheduleFluidTick(p Point, fluid string) {
	c := r.chunk(p.X, p.Z)
	c.FluidTicks = append(c.FluidTicks, GenFluidTick{p, fluid})
}

// addEntity spawns an entity into the chunk holding its block position.
func (r *decoRegion) addEntity(e GenEntity) {
	c := r.chunk(mthFloorD(e.X), mthFloorD(e.Z))
	c.Entities = append(c.Entities, e)
}

// setBlockEntity records a block entity at p, in the chunk holding it.
func (r *decoRegion) setBlockEntity(p Point, be *GenBlockEntity) {
	c := r.chunk(p.X, p.Z)
	if c.BlockEntities == nil {
		c.BlockEntities = map[Point]*GenBlockEntity{}
	}
	c.BlockEntities[p] = be
}

// blockEntity is the block entity at p, nil for none.
func (r *decoRegion) blockEntity(p Point) *GenBlockEntity {
	if c := r.chunk(p.X, p.Z); c != nil {
		return c.BlockEntities[p]
	}
	return nil
}

// heightAt is the region's getHeight: a heightmap's first free Y.
func (r *decoRegion) heightAt(kind int, x, z int32) int32 {
	c := r.chunk(x, z)
	if c == nil {
		panic("worldgen: height read outside the generation region")
	}
	return c.heightAt(kind, x&15, z&15)
}

// biome is the block biome at p, zoomed from the region's stored biomes.
func (r *decoRegion) biome(p Point) string {
	return ZoomBiome(r.zoomSeed, p.X, p.Y, p.Z, r.quartBiome)
}

func (r *decoRegion) quartBiome(qx, qy, qz int32) string {
	gx, gz := qx>>2-r.cx+2, qz>>2-r.cz+2
	if gx < 0 || gx > 4 || gz < 0 || gz > 4 {
		panic("worldgen: biome read outside the generation region")
	}
	return r.biomes[gx][gz].At(qx, qy, qz)
}

// decorate runs the features stage of the region's middle chunk. wrap,
// when set, runs each placed feature in its place (it may or may not
// call place); otherwise a feature that is not implemented is skipped.
func (r *decoRegion) decorate(steps [][]*placedFeature, wrap func(step, index int, f *placedFeature, place func() bool)) {
	random := newDecoRandom()
	r.deco = random
	originX, originZ := r.cx*16, r.cz*16
	deco := setDecorationSeed(random, r.seed, originX, originZ)
	present := map[string]bool{}
	for gx := 1; gx <= 3; gx++ {
		for gz := 1; gz <= 3; gz++ {
			for _, b := range r.biomes[gx][gz].names() {
				present[b] = true
			}
		}
	}
	origin := Point{originX, r.minY, originZ}
	for step, features := range steps {
		var indexes []int
		seen := map[int]bool{}
		for b := range present {
			lists := biomeFeatures[b]
			if step >= len(lists) {
				continue
			}
			for _, name := range lists[step] {
				if i := featureIndex(steps, step, name); i >= 0 && !seen[i] {
					seen[i] = true
					indexes = append(indexes, i)
				}
			}
		}
		sort.Ints(indexes)
		for _, i := range indexes {
			f := features[i]
			random.SetSeed(deco + int64(i) + 10000*int64(step))
			place := func() bool { return f.placeWithBiomeCheck(r, random, origin) }
			if wrap != nil {
				wrap(step, i, f, place)
				continue
			}
			func() {
				defer func() {
					if e := recover(); e != nil {
						if _, ok := e.(unsupportedFeature); !ok {
							panic(e)
						}
					}
				}()
				place()
			}()
		}
	}
}

// setDecorationSeed seeds the source for a chunk's decoration and
// returns the seed.
func setDecorationSeed(r *decoRandom, seed int64, x, z int32) int64 {
	r.SetSeed(seed)
	a := r.NextLong() | 1
	b := r.NextLong() | 1
	s := (int64(x)*a + int64(z)*b) ^ seed
	r.SetSeed(s)
	return s
}

// SetSeed reseeds the source as NewXoroshiro does.
func (r *Xoroshiro) SetSeed(seed int64) {
	*r = *NewXoroshiro(seed)
}

// decoRandom is the decoration source: the legacy draws (ints, floats,
// doubles, gaussians) built on bits taken from the top of a Xoroshiro
// source's longs. Reseeding reseeds the Xoroshiro source only; a cached
// second gaussian survives it.
type decoRandom struct {
	x *Xoroshiro
	g gaussian
}

func newDecoRandom() *decoRandom { return &decoRandom{x: NewXoroshiro(0)} }

func (r *decoRandom) SetSeed(seed int64) { r.x.SetSeed(seed) }

func (r *decoRandom) next(n uint) int32 { return int32(uint64(r.x.NextLong()) >> (64 - n)) }

func (r *decoRandom) NextLong() int64 { return int64(r.next(32))<<32 + int64(r.next(32)) }
func (r *decoRandom) NextInt() int32  { return r.next(32) }

func (r *decoRandom) NextIntn(bound int32) int32 {
	if bound <= 0 {
		panic("worldgen: bound must be positive")
	}
	if bound&-bound == bound {
		return int32((int64(bound) * int64(r.next(31))) >> 31)
	}
	for {
		b := r.next(31)
		v := b % bound
		if b-v+(bound-1) >= 0 {
			return v
		}
	}
}

func (r *decoRandom) NextFloat() float32 { return float32(r.next(24)) * 5.9604645e-8 }

func (r *decoRandom) NextDouble() float64 {
	hi := int64(r.next(26))
	lo := int64(r.next(27))
	return float64(hi<<27+lo) * 1.1102230246251565e-16
}

func (r *decoRandom) NextBool() bool        { return r.next(1) != 0 }
func (r *decoRandom) NextGaussian() float64 { return r.g.draw(r.NextDouble) }

func (r *decoRandom) ConsumeCount(n int) {
	for i := 0; i < n; i++ {
		r.next(32)
	}
}

func (r *decoRandom) Fork() Random               { return r.x.Fork() }
func (r *decoRandom) ForkPositional() Positional { return r.x.ForkPositional() }
