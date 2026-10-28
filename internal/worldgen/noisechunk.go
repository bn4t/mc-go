package worldgen

import (
	"fmt"
	"math"
	"sync"
)

// NoiseBlock is a block the noise stage can place.
type NoiseBlock uint8

const (
	NBAir NoiseBlock = iota
	NBDefault
	NBWater
	NBLava
	NBCopperOre
	NBRawCopperBlock
	NBGranite
	NBDeepslateIronOre
	NBRawIronBlock
	NBTuff
)

// Fluid levels and limits of the noise stage.
const (
	lavaSeaLevel = -54
	// wayBelowMinY is the fluid level of an aquifer with no fluid: the
	// lowest packable Y (-2032) shifted up four bits.
	wayBelowMinY = -2032 << 4
)

// fluidStatus is a fluid surface: below level the fluid, at and above
// it air.
type fluidStatus struct {
	level int32
	fluid NoiseBlock
}

func (f fluidStatus) at(y int32) NoiseBlock {
	if y < f.level {
		return f.fluid
	}
	return NBAir
}

// NoiseChunkFiller runs the noise stage of a noise-based generator for
// one chunk at a time: the final density, interpolated across 4×8×4
// cells, decides solid or not; open space is filled by the aquifers and
// solid ground may become an ore vein. A filler is not safe for
// concurrent use; give each worker its own.
type NoiseChunkFiller struct {
	rs                       *RandomState
	cellW, cellH             int32
	cellMinY, cellCountY     int32
	minY, height             int32
	seaLevel                 int32
	defaultFluid             NoiseBlock
	aquifers, veins          bool
	router                   BoundRouter
	final                    Func
	interps                  []*interpolator
	flats                    []*flatCache
	firstQuartX, firstQuartZ int32
	// flatSize is the side of the flat caches' quart window, cornersXZ
	// the interpolators' corner count per horizontal axis.
	flatSize, cornersXZ int32

	// fill state: while filling, interpolators answer for cur from the
	// corners of its cell.
	filling             bool
	cur                 Point
	cellX, cellY, cellZ int
	tx, ty, tz          float64

	pslCache map[[2]int32]int32
	aq       aquifer
	// shared, if set, are caches shared with the generator's other
	// fillers.
	shared *fillerCaches

	// carverTags caches carver replaceable-tag membership by state.
	carverTags map[string]map[StateID]bool
}

// NewNoiseChunkFiller binds the generator's router with per-chunk caches.
func NewNoiseChunkFiller(rs *RandomState) *NoiseChunkFiller {
	s := rs.Settings
	f := &NoiseChunkFiller{
		rs:       rs,
		cellW:    int32(s.SizeHorizontal) * 4,
		cellH:    int32(s.SizeVertical) * 4,
		minY:     int32(s.MinY),
		height:   int32(s.Height),
		seaLevel: int32(s.SeaLevel),
		aquifers: s.AquifersEnabled,
		veins:    s.OreVeinsEnabled,
		pslCache: map[[2]int32]int32{},

		carverTags: map[string]map[StateID]bool{},
	}
	f.cellMinY = floorDiv32(f.minY, f.cellH)
	f.cellCountY = floorDiv32(f.height, f.cellH)
	switch s.DefaultFluid.Name {
	case "minecraft:water":
		f.defaultFluid = NBWater
	case "minecraft:lava":
		f.defaultFluid = NBLava
	case "minecraft:air":
		f.defaultFluid = NBAir
	default:
		panic(fmt.Sprintf("worldgen: unsupported default fluid %s", s.DefaultFluid.Name))
	}
	f.router = rs.bindRouter(f.wrapMarker)
	// The filled density is the final density plus the structure
	// beardifier, which is zero for a chunk without structures.
	f.final = newBinary(KAdd, f.router.FinalDensity, beardifier{})
	return f
}

// beardifier stands in for the structure terrain adaptation; with no
// structures it adds nothing, but it is not a constant, so the sum is
// a real addition (turning a negative zero positive).
type beardifier struct{}

func (beardifier) Compute(Point) float64 { return 0 }
func (beardifier) Min() float64          { return 0 }
func (beardifier) Max() float64          { return 0 }

func (f *NoiseChunkFiller) wrapMarker(k Kind, in Func) Func {
	switch k {
	case KInterpolated:
		ip := &interpolator{f: f, in: in}
		f.interps = append(f.interps, ip)
		return ip
	case KFlatCache:
		fc := &flatCache{f: f, in: in}
		f.flats = append(f.flats, fc)
		return fc
	}
	// The other caches only memoize; evaluated afresh they give the
	// same values.
	return &marker{kind: k, in: in}
}

func floorDiv32(a, b int32) int32 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// flatCache holds a 2D function's values at the noise chunk's quart
// columns (sampled at y 0); a lookup anywhere inside that quart window —
// 5×5 for a chunk, at any y, by any caller — answers from the quart
// column it falls in.
type flatCache struct {
	f      *NoiseChunkFiller
	in     Func
	values [5 * 5]float64
	have   [5 * 5]bool
}

func (c *flatCache) reset() { c.have = [25]bool{} }

func (c *flatCache) Compute(p Point) float64 {
	qx := (p.X >> 2) - c.f.firstQuartX
	qz := (p.Z >> 2) - c.f.firstQuartZ
	if qx < 0 || qz < 0 || qx >= c.f.flatSize || qz >= c.f.flatSize {
		return c.in.Compute(p)
	}
	i := qx + qz*c.f.flatSize
	if !c.have[i] {
		c.values[i] = c.in.Compute(Point{(qx + c.f.firstQuartX) << 2, 0, (qz + c.f.firstQuartZ) << 2})
		c.have[i] = true
	}
	return c.values[i]
}
func (c *flatCache) Min() float64 { return c.in.Min() }
func (c *flatCache) Max() float64 { return c.in.Max() }

// interpolator samples its function at the chunk's cell corners and,
// for the block being filled, blends the eight corners of its cell:
// along y first, then x, then z.
type interpolator struct {
	f       *NoiseChunkFiller
	in      Func
	corners []float64 // [x][z][y] over (cells+1) per axis
}

func (ip *interpolator) corner(x, y, z int) float64 {
	nY := int(ip.f.cellCountY) + 1
	return ip.corners[(x*int(ip.f.cornersXZ)+z)*nY+y]
}

func (ip *interpolator) Compute(p Point) float64 {
	f := ip.f
	if !f.filling || p != f.cur {
		return ip.in.Compute(p)
	}
	x, y, z := f.cellX, f.cellY, f.cellZ
	v00 := lerp(f.ty, ip.corner(x, y, z), ip.corner(x, y+1, z))
	v10 := lerp(f.ty, ip.corner(x+1, y, z), ip.corner(x+1, y+1, z))
	v01 := lerp(f.ty, ip.corner(x, y, z+1), ip.corner(x, y+1, z+1))
	v11 := lerp(f.ty, ip.corner(x+1, y, z+1), ip.corner(x+1, y+1, z+1))
	z0 := lerp(f.tx, v00, v10)
	z1 := lerp(f.tx, v01, v11)
	return lerp(f.tz, z0, z1)
}
func (ip *interpolator) Min() float64 { return ip.in.Min() }
func (ip *interpolator) Max() float64 { return ip.in.Max() }

// NoiseColumnIndex is the index of block (x, y, z) — x and z within the
// chunk, y absolute — in a filled chunk's block slice.
func (f *NoiseChunkFiller) NoiseColumnIndex(x, y, z int32) int {
	return int((y-f.minY)*256 + z*16 + x)
}

// Fill runs the noise stage for chunk (cx, cz) and returns its blocks,
// indexed by NoiseColumnIndex. fluidUpdates lists the chunk-relative
// positions whose fluid needs a post-generation update.
func (f *NoiseChunkFiller) Fill(cx, cz int32) (blocks []NoiseBlock, fluidUpdates []Point) {
	minX, minZ := cx*16, cz*16
	f.begin(minX, minZ, 16/f.cellW)

	blocks = make([]NoiseBlock, 256*int(f.height))
	cellsXZ := int(16 / f.cellW)
	for cellX := 0; cellX < cellsXZ; cellX++ {
		for cellZ := 0; cellZ < cellsXZ; cellZ++ {
			for cellY := int(f.cellCountY) - 1; cellY >= 0; cellY-- {
				for yIn := f.cellH - 1; yIn >= 0; yIn-- {
					y := (f.cellMinY+int32(cellY))*f.cellH + yIn
					for xIn := int32(0); xIn < f.cellW; xIn++ {
						x := int32(cellX)*f.cellW + xIn
						for zIn := int32(0); zIn < f.cellW; zIn++ {
							z := int32(cellZ)*f.cellW + zIn
							f.filling = true
							f.cur = Point{minX + x, y, minZ + z}
							f.cellX, f.cellY, f.cellZ = cellX, cellY, cellZ
							f.ty = float64(yIn) / float64(f.cellH)
							f.tx = float64(xIn) / float64(f.cellW)
							f.tz = float64(zIn) / float64(f.cellW)
							b, update := f.blockAt()
							blocks[f.NoiseColumnIndex(x, y, z)] = b
							if update && (b == NBWater || b == NBLava) {
								fluidUpdates = append(fluidUpdates, Point{x, y, z})
							}
						}
					}
				}
			}
		}
	}
	f.filling = false
	return blocks, fluidUpdates
}

// begin readies the caches for a noise chunk of cells×cells cells from
// block (minX, minZ): the flat caches' window, the interpolators' cell
// corners and the aquifer of the chunk holding it.
func (f *NoiseChunkFiller) begin(minX, minZ, cells int32) {
	f.firstQuartX, f.firstQuartZ = minX>>2, minZ>>2
	f.flatSize = (cells*f.cellW)>>2 + 1
	f.cornersXZ = cells + 1
	for _, c := range f.flats {
		c.reset()
	}
	clear(f.pslCache)
	f.filling = false

	nY := int(f.cellCountY) + 1
	n := int(f.cornersXZ)
	for _, ip := range f.interps {
		if len(ip.corners) != n*n*nY {
			ip.corners = make([]float64, n*n*nY)
		}
	}
	for x := 0; x < n; x++ {
		for z := 0; z < n; z++ {
			cx, cz := minX+int32(x)*f.cellW, minZ+int32(z)*f.cellW
			col := f.cornerColumn(cx, cz)
			for i, ip := range f.interps {
				copy(ip.corners[(x*n+z)*nY:(x*n+z+1)*nY], col[i*nY:(i+1)*nY])
			}
		}
	}
	if f.aquifers {
		f.aq.reset(f, minX>>4, minZ>>4)
	}
}

// cornerColumn is every interpolator's values down the cell corners at
// block column (x, z). A corner is sampled where the flat caches answer
// from that very column, whatever the window, so its values depend on
// the column alone and are shared through the corner cache.
func (f *NoiseChunkFiller) cornerColumn(x, z int32) []float64 {
	compute := func() []float64 {
		nY := int(f.cellCountY) + 1
		col := make([]float64, len(f.interps)*nY)
		for i, ip := range f.interps {
			for y := 0; y < nY; y++ {
				col[i*nY+y] = ip.in.Compute(Point{x, (f.cellMinY + int32(y)) * f.cellH, z})
			}
		}
		return col
	}
	if f.shared == nil {
		return compute()
	}
	return f.shared.corners.get(x, z, compute)
}

// sharedCache memoizes a pure function of a block or chunk column for
// the fillers of one generator, keeping a bounded number of entries.
type sharedCache[V any] struct {
	shards [64]struct {
		mu sync.Mutex
		m  map[[2]int32]V
	}
	perShard int
}

func newSharedCache[V any](entries int) *sharedCache[V] {
	c := &sharedCache[V]{perShard: max(1, entries/64)}
	for i := range c.shards {
		c.shards[i].m = map[[2]int32]V{}
	}
	return c
}

func (c *sharedCache[V]) get(x, z int32, compute func() V) V {
	sh := &c.shards[(uint32(x)*0x9E3779B1^uint32(z)*0x85EBCA77)>>26]
	key := [2]int32{x, z}
	sh.mu.Lock()
	v, ok := sh.m[key]
	sh.mu.Unlock()
	if ok {
		return v
	}
	v = compute()
	sh.mu.Lock()
	for k := range sh.m {
		if len(sh.m) < c.perShard {
			break
		}
		delete(sh.m, k)
	}
	sh.m[key] = v
	sh.mu.Unlock()
	return v
}

// fillerCaches are what a generator's fillers share: interpolator corner
// columns, preliminary surface levels by quart column, and each chunk's
// aquifer sampling ceiling.
type fillerCaches struct {
	corners *sharedCache[[]float64]
	surface *sharedCache[int32]
	skip    *sharedCache[int32]
}

func newFillerCaches() *fillerCaches {
	return &fillerCaches{corners: newSharedCache[[]float64](8192), surface: newSharedCache[int32](1 << 16),
		skip: newSharedCache[int32](4096)}
}

// Column runs the noise stage down the column at block (x, z), as the
// generator's base-height and base-column queries do: a noise chunk of
// the one cell holding the column, its caches covering only that cell.
// visit sees each block from the top down until it returns false.
func (f *NoiseChunkFiller) Column(x, z int32, visit func(y int32, b NoiseBlock) bool) {
	minX, minZ := floorDiv32(x, f.cellW)*f.cellW, floorDiv32(z, f.cellW)*f.cellW
	f.begin(minX, minZ, 1)
	defer func() { f.filling = false }()
	tx := float64(x-minX) / float64(f.cellW)
	tz := float64(z-minZ) / float64(f.cellW)
	for cellY := int(f.cellCountY) - 1; cellY >= 0; cellY-- {
		for yIn := f.cellH - 1; yIn >= 0; yIn-- {
			y := (f.cellMinY+int32(cellY))*f.cellH + yIn
			f.filling = true
			f.cur = Point{x, y, z}
			f.cellX, f.cellY, f.cellZ = 0, cellY, 0
			f.ty = float64(yIn) / float64(f.cellH)
			f.tx, f.tz = tx, tz
			b, _ := f.blockAt()
			if !visit(y, b) {
				return
			}
		}
	}
}

// blockAt decides the block being filled: the aquifer's answer for open
// space, else an ore vein, else the default block.
func (f *NoiseChunkFiller) blockAt() (NoiseBlock, bool) {
	density := f.final.Compute(f.cur)
	var b NoiseBlock
	var placed, update bool
	if f.aquifers {
		b, placed, update = f.aq.substance(f.cur, density)
	} else if density <= 0 {
		b, placed = f.globalFluid(f.cur.Y).at(f.cur.Y), true
	}
	if !placed && f.veins {
		b, placed = f.vein(f.cur)
	}
	if !placed {
		return NBDefault, false
	}
	return b, update
}

// globalFluid is the dimension-wide fluid: lava below y -54 (or the sea
// level, if lower), the default fluid up to the sea level above that.
func (f *NoiseChunkFiller) globalFluid(y int32) fluidStatus {
	if y < min(lavaSeaLevel, f.seaLevel) {
		return fluidStatus{lavaSeaLevel, NBLava}
	}
	return fluidStatus{f.seaLevel, f.defaultFluid}
}

// preliminarySurface is the estimated surface height of the quart
// column containing (x, z).
func (f *NoiseChunkFiller) preliminarySurface(x, z int32) int32 {
	key := [2]int32{x >> 2 << 2, z >> 2 << 2}
	if v, ok := f.pslCache[key]; ok {
		return v
	}
	compute := func() int32 { return floorInt(f.router.PreliminarySurfaceLevel.Compute(Point{key[0], 0, key[1]})) }
	var v int32
	if f.shared != nil {
		v = f.shared.surface.get(key[0], key[1], compute)
	} else {
		v = compute()
	}
	f.pslCache[key] = v
	return v
}

// Ore veins: a toggle noise picks copper (positive) or iron (negative)
// and its magnitude the vein strength, fading out within 20 blocks of
// the type's height band; a ridged noise carves the vein's shape.
type veinType struct {
	ore, raw, filler NoiseBlock
	minY, maxY       int32
}

var (
	copperVein = veinType{NBCopperOre, NBRawCopperBlock, NBGranite, 0, 50}
	ironVein   = veinType{NBDeepslateIronOre, NBRawIronBlock, NBTuff, -60, -8}
)

func (f *NoiseChunkFiller) vein(p Point) (NoiseBlock, bool) {
	toggle := f.router.VeinToggle.Compute(p)
	vt := ironVein
	if toggle > 0 {
		vt = copperVein
	}
	strength := math.Abs(toggle)
	above, below := vt.maxY-p.Y, p.Y-vt.minY
	if below < 0 || above < 0 {
		return 0, false
	}
	edge := min(above, below)
	fade := clampedMap(float64(edge), 0, 20, -0.2, 0)
	if strength+fade < 0.4000000059604645 {
		return 0, false
	}
	r := f.rs.Ore.At(p.X, p.Y, p.Z)
	if r.NextFloat() > 0.7 {
		return 0, false
	}
	if f.router.VeinRidged.Compute(p) >= 0 {
		return 0, false
	}
	richness := clampedMap(strength, 0.4000000059604645, 0.6000000238418579, 0.10000000149011612, 0.30000001192092896)
	if float64(r.NextFloat()) < richness && f.router.VeinGap.Compute(p) > -0.30000001192092896 {
		if r.NextFloat() < 0.02 {
			return vt.raw, true
		}
		return vt.ore, true
	}
	return vt.filler, true
}
