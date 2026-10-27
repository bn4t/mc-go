package worldgen

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sync"
)

// Climate parameters are compared as longs: a value times 10000 in
// float32, truncated.
func quantizeClimate(v float32) int64 { return int64(v * 10000) }

// ClimateTarget is a quantized climate sample: temperature, humidity,
// continentalness, erosion, depth, weirdness.
type ClimateTarget [6]int64

// climatePoint is one entry of a multi-noise parameter list: a range per
// climate axis, an offset, and the biome it selects.
type climatePoint struct {
	lo, hi [6]int64
	offset int64
	biome  uint8
}

// MultiNoiseBiomes picks a biome per climate sample: the parameter point
// nearest to it, distance being the sum of squared per-axis gaps to the
// point's ranges plus its squared offset.
type MultiNoiseBiomes struct {
	points   []climatePoint
	treeOnce sync.Once
	root     *rnode
}

var (
	climateListsOnce sync.Once
	climateParsed    map[string]*MultiNoiseBiomes
)

// BiomeParameters returns a registered multi-noise parameter list, e.g.
// "minecraft:overworld".
func BiomeParameters(id string) (*MultiNoiseBiomes, bool) {
	climateListsOnce.Do(func() {
		climateParsed = map[string]*MultiNoiseBiomes{}
		for name, blob := range climateLists {
			raw, err := base64.StdEncoding.DecodeString(blob)
			if err != nil {
				panic(err)
			}
			zr, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				panic(err)
			}
			packed, err := io.ReadAll(zr)
			if err != nil {
				panic(err)
			}
			const stride = 14
			if len(packed)%stride != 0 {
				panic(fmt.Sprintf("worldgen: parameter list %s is %d bytes", name, len(packed)))
			}
			m := &MultiNoiseBiomes{}
			for i := 0; i < len(packed); i += stride {
				e := packed[i : i+stride]
				var p climatePoint
				for axis := 0; axis < 6; axis++ {
					p.lo[axis] = quantizeClimate(climateValues[e[axis*2]])
					p.hi[axis] = quantizeClimate(climateValues[e[axis*2+1]])
				}
				p.offset = quantizeClimate(climateValues[e[12]])
				p.biome = e[13]
				m.points = append(m.points, p)
			}
			climateParsed[name] = m
		}
	})
	m, ok := climateParsed[id]
	return m, ok
}

// axisGap is how far v lies outside [lo, hi].
func axisGap(v, lo, hi int64) int64 {
	if d := v - hi; d > 0 {
		return d
	}
	return max(lo-v, 0)
}

// Lookup returns the biome for a climate sample by exhaustive search,
// the first nearest point in list order; vanilla's own search (see
// BiomeSearcher) can break exact ties differently.
func (m *MultiNoiseBiomes) Lookup(t ClimateTarget) string {
	best, bestDist := 0, int64(math.MaxInt64)
	for i := range m.points {
		p := &m.points[i]
		var d int64
		for axis := 0; axis < 6; axis++ {
			g := axisGap(t[axis], p.lo[axis], p.hi[axis])
			d += g * g
			if d >= bestDist {
				break
			}
		}
		d += p.offset * p.offset
		if d < bestDist {
			best, bestDist = i, d
		}
	}
	return climateBiomeNames[m.points[best].biome]
}

// Climate samples the climate at quart (qx, qy, qz): the router's
// temperature, vegetation, continents, erosion, depth and ridges at the
// quart's corner block, each narrowed to float32 and quantized.
func (rs *RandomState) Climate(qx, qy, qz int32) ClimateTarget {
	p := Point{qx << 2, qy << 2, qz << 2}
	r := &rs.Router
	fns := [6]Func{r.Temperature, r.Vegetation, r.Continents, r.Erosion, r.Depth, r.Ridges}
	var t ClimateTarget
	for i, f := range fns {
		t[i] = quantizeClimate(float32(f.Compute(p)))
	}
	return t
}

// ZoomSeed is the seed of the block-level biome zoom: the first eight
// bytes, little-endian, of the SHA-256 of the world seed's eight
// little-endian bytes.
func ZoomSeed(seed int64) int64 {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], uint64(seed))
	sum := sha256.Sum256(b[:])
	return int64(binary.LittleEndian.Uint64(sum[:8]))
}

// zoomStep is one step of the zoom's 64-bit linear congruential mix.
func zoomStep(s, add int64) int64 {
	return s*(s*6364136223846793005+1442695040888963407) + add
}

// zoomJitter maps a mixed seed to an offset in [-0.45, 0.45).
func zoomJitter(s int64) float64 {
	m := (s >> 24) % 1024
	if m < 0 {
		m += 1024
	}
	return (float64(m)/1024 - 0.5) * 0.9
}

// zoomDistance is the squared distance from a block's position within
// its quart cell to the jittered center of the candidate quart.
func zoomDistance(seed int64, qx, qy, qz int32, fx, fy, fz float64) float64 {
	s := seed
	for _, v := range [6]int32{qx, qy, qz, qx, qy, qz} {
		s = zoomStep(s, int64(v))
	}
	jx := zoomJitter(s)
	s = zoomStep(s, seed)
	jy := zoomJitter(s)
	s = zoomStep(s, seed)
	jz := zoomJitter(s)
	dz, dy, dx := fz+jz, fy+jy, fx+jx
	return float64(dz*dz) + float64(dy*dy) + float64(dx*dx)
}

// ZoomBiome resolves the biome at a block from per-quart biomes: of the
// eight quarts around the block (offset by half a quart), the one whose
// jittered center is nearest.
func ZoomBiome(zoomSeed int64, x, y, z int32, quart func(qx, qy, qz int32) string) string {
	x, y, z = x-2, y-2, z-2
	qx, qy, qz := x>>2, y>>2, z>>2
	fx, fy, fz := float64(x&3)/4, float64(y&3)/4, float64(z&3)/4
	best, bestDist := 0, math.Inf(1)
	for i := 0; i < 8; i++ {
		cx, cy, cz := qx, qy, qz
		dx, dy, dz := fx, fy, fz
		if i&4 != 0 {
			cx, dx = qx+1, fx-1
		}
		if i&2 != 0 {
			cy, dy = qy+1, fy-1
		}
		if i&1 != 0 {
			cz, dz = qz+1, fz-1
		}
		if d := zoomDistance(zoomSeed, cx, cy, cz, dx, dy, dz); bestDist > d {
			best, bestDist = i, d
		}
	}
	cx, cy, cz := qx, qy, qz
	if best&4 != 0 {
		cx++
	}
	if best&2 != 0 {
		cy++
	}
	if best&1 != 0 {
		cz++
	}
	return quart(cx, cy, cz)
}

// ChunkBiomes is a chunk's biome storage: one biome per quart, filled
// section by section from the bottom, each section x, then y, then z —
// the order vanilla asks its climate search in.
type ChunkBiomes struct {
	CX, CZ       int32
	minQY, quart int32   // lowest quart Y and quart count up
	biomes       []uint8 // biomeNameTable indices
}

// biomeNameTable is every biome a source can produce: the climate
// lists' biomes, then the End's.
var biomeNameTable = append(append([]string(nil), climateBiomeNames...), endBiomes...)

// BiomeSource resolves quart biomes for one dimension.
type BiomeSource interface {
	// biomeIndex is the biomeNameTable index of quart (qx, qy, qz).
	biomeIndex(rs *RandomState, s *BiomeSearcher, qx, qy, qz int32) uint8
}

func (m *MultiNoiseBiomes) biomeIndex(rs *RandomState, s *BiomeSearcher, qx, qy, qz int32) uint8 {
	return s.find(rs.Climate(qx, qy, qz))
}

// EndBiomeSource is the End's distance-and-erosion biome source.
type EndBiomeSource struct{}

func (EndBiomeSource) biomeIndex(rs *RandomState, _ *BiomeSearcher, qx, qy, qz int32) uint8 {
	name := rs.endBiome(qx, qy, qz)
	for i, n := range endBiomes {
		if n == name {
			return uint8(len(climateBiomeNames) + i)
		}
	}
	panic("worldgen: unknown end biome")
}

// FillChunkBiomes resolves chunk (cx, cz)'s biomes through a searcher.
func FillChunkBiomes(rs *RandomState, s *BiomeSearcher, cx, cz int32) *ChunkBiomes {
	return FillChunkBiomesFrom(rs, s.m, s, cx, cz)
}

// FillChunkBiomesFrom resolves chunk (cx, cz)'s biomes from a source; s
// is the multi-noise searcher (unused by other sources).
func FillChunkBiomesFrom(rs *RandomState, src BiomeSource, s *BiomeSearcher, cx, cz int32) *ChunkBiomes {
	set := rs.Settings
	c := &ChunkBiomes{CX: cx, CZ: cz, minQY: int32(set.MinY) >> 2, quart: int32(set.Height) >> 2}
	c.biomes = make([]uint8, 16*c.quart)
	for sec := int32(0); sec < c.quart/4; sec++ {
		for x := int32(0); x < 4; x++ {
			for y := int32(0); y < 4; y++ {
				for z := int32(0); z < 4; z++ {
					qy := c.minQY + sec*4 + y
					c.biomes[c.index(x, qy, z)] = src.biomeIndex(rs, s, cx*4+x, qy, cz*4+z)
				}
			}
		}
	}
	return c
}

func (c *ChunkBiomes) index(qx, qy, qz int32) int {
	return int(((qy-c.minQY)*4+qz)*4 + qx)
}

// At is the stored biome at absolute quart (qx, qy, qz) of this chunk,
// the quart Y clamped to the chunk's height.
func (c *ChunkBiomes) At(qx, qy, qz int32) string {
	qy = min(max(qy, c.minQY), c.minQY+c.quart-1)
	return biomeNameTable[c.biomes[c.index(qx-c.CX*4, qy, qz-c.CZ*4)]]
}

// names lists the biomes stored anywhere in the chunk.
func (c *ChunkBiomes) names() []string {
	var seen [256]bool
	var out []string
	for _, b := range c.biomes {
		if !seen[b] {
			seen[b] = true
			out = append(out, biomeNameTable[b])
		}
	}
	return out
}

// BiomeRegion answers quart biomes from the stored biomes of a 3×3 block
// of chunks around a center chunk, as a generation region does.
type BiomeRegion struct {
	cx, cz int32
	chunks [3][3]*ChunkBiomes
}

// NewBiomeRegion fills the 3×3 chunks around (cx, cz), x outer, z inner.
func NewBiomeRegion(rs *RandomState, s *BiomeSearcher, cx, cz int32) *BiomeRegion {
	return NewBiomeRegionFrom(rs, s.m, s, cx, cz)
}

// NewBiomeRegionFrom is NewBiomeRegion over any biome source.
func NewBiomeRegionFrom(rs *RandomState, src BiomeSource, s *BiomeSearcher, cx, cz int32) *BiomeRegion {
	r := &BiomeRegion{cx: cx, cz: cz}
	for dx := int32(-1); dx <= 1; dx++ {
		for dz := int32(-1); dz <= 1; dz++ {
			r.chunks[dx+1][dz+1] = FillChunkBiomesFrom(rs, src, s, cx+dx, cz+dz)
		}
	}
	return r
}

// Center is the region's center chunk storage.
func (r *BiomeRegion) Center() *ChunkBiomes { return r.chunks[1][1] }

// Quart is the biome at quart (qx, qy, qz), which must fall in the region.
func (r *BiomeRegion) Quart(qx, qy, qz int32) string {
	return r.chunks[qx>>2-r.cx+1][qz>>2-r.cz+1].At(qx, qy, qz)
}

// Block is the zoomed biome at a block of the center chunk.
func (r *BiomeRegion) Block(zoomSeed int64) func(x, y, z int32) string {
	return func(x, y, z int32) string { return ZoomBiome(zoomSeed, x, y, z, r.Quart) }
}
