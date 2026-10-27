package worldgen

import (
	"math"
	"sync"
)

// StateID names a block state interned in this package's state table:
// canonical "namespace:name[key=value,...]" strings, properties sorted.
type StateID uint16

var (
	stateMu    sync.RWMutex
	stateNames []string
	stateIndex = map[string]StateID{}
)

// InternState returns the id of a canonical block state string.
func InternState(s string) StateID {
	stateMu.RLock()
	id, ok := stateIndex[s]
	stateMu.RUnlock()
	if ok {
		return id
	}
	stateMu.Lock()
	defer stateMu.Unlock()
	if id, ok := stateIndex[s]; ok {
		return id
	}
	id = StateID(len(stateNames))
	stateNames = append(stateNames, s)
	stateIndex[s] = id
	return id
}

// StateName is the canonical string of an interned state.
func StateName(id StateID) string {
	stateMu.RLock()
	defer stateMu.RUnlock()
	return stateNames[id]
}

var (
	stAir         = InternState("minecraft:air")
	stWater       = InternState("minecraft:water[level=0]")
	stLava        = InternState("minecraft:lava[level=0]")
	stSnowBlock   = InternState("minecraft:snow_block")
	stPackedIce   = InternState("minecraft:packed_ice")
	stTerracotta  = InternState("minecraft:terracotta")
	stWhiteTc     = InternState("minecraft:white_terracotta")
	stOrangeTc    = InternState("minecraft:orange_terracotta")
	stYellowTc    = InternState("minecraft:yellow_terracotta")
	stBrownTc     = InternState("minecraft:brown_terracotta")
	stRedTc       = InternState("minecraft:red_terracotta")
	stLightGrayTc = InternState("minecraft:light_gray_terracotta")
)

// isFluidState reports whether a state carries a fluid.
func isFluidState(id StateID) bool { return id == stWater || id == stLava }

// GenChunk is a chunk being generated: its blocks as interned states
// and its WORLD_SURFACE_WG heightmap.
type GenChunk struct {
	CX, CZ       int32
	MinY, Height int32
	States       []StateID // ((y-MinY)*16 + z)*16 + x
	// surfaceTop is per column the lowest Y with only air at and above
	// it (MinY when the column is empty).
	surfaceTop [256]int32
}

func (c *GenChunk) index(x, y, z int32) int { return int((y-c.MinY)*256 + z*16 + x) }

// Get returns the state at chunk-relative x, z and absolute y; outside
// the build height it reads air.
func (c *GenChunk) Get(x, y, z int32) StateID {
	if y < c.MinY || y >= c.MinY+c.Height {
		return stAir
	}
	return c.States[c.index(x, y, z)]
}

// set writes a state and keeps the heightmap: a non-air write at or
// above the top raises it, an air write over the top block lowers it to
// the next non-air block down.
func (c *GenChunk) set(x, y, z int32, s StateID) {
	if y < c.MinY || y >= c.MinY+c.Height {
		return
	}
	c.States[c.index(x, y, z)] = s
	top := &c.surfaceTop[z*16+x]
	switch {
	case !isAirState(s) && y >= *top:
		*top = y + 1
	case isAirState(s) && y == *top-1:
		for *top = y; *top > c.MinY && isAirState(c.Get(x, *top-1, z)); *top-- {
		}
	}
}

// Height is the WORLD_SURFACE_WG heightmap at chunk-relative (x, z).
func (c *GenChunk) HeightAt(x, z int32) int32 { return c.surfaceTop[z*16+x] }

func (c *GenChunk) primeHeights() {
	for z := int32(0); z < 16; z++ {
		for x := int32(0); x < 16; x++ {
			top := c.MinY
			for y := c.MinY + c.Height - 1; y >= c.MinY; y-- {
				if c.Get(x, y, z) != stAir {
					top = y + 1
					break
				}
			}
			c.surfaceTop[z*16+x] = top
		}
	}
}

// surfaceSystem is a generator's per-seed surface state.
type surfaceSystem struct {
	rs            *RandomState
	defaultState  StateID
	seaLevel      int32
	clayBands     [192]StateID
	surface       *Normal
	secondary     *Normal
	clayOffset    *Normal
	badlandsPill  *Normal
	badlandsRoof  *Normal
	badlandsSurf  *Normal
	icebergPill   *Normal
	icebergRoof   *Normal
	icebergSurf   *Normal
	gradientRands map[string]Positional
	mu            sync.Mutex
}

func newSurfaceSystem(rs *RandomState) *surfaceSystem {
	s := rs.Settings
	ss := &surfaceSystem{
		rs:            rs,
		defaultState:  InternState(canonicalBlockState(s.DefaultBlock)),
		seaLevel:      int32(s.SeaLevel),
		surface:       rs.Noise("minecraft:surface"),
		secondary:     rs.Noise("minecraft:surface_secondary"),
		clayOffset:    rs.Noise("minecraft:clay_bands_offset"),
		badlandsPill:  rs.Noise("minecraft:badlands_pillar"),
		badlandsRoof:  rs.Noise("minecraft:badlands_pillar_roof"),
		badlandsSurf:  rs.Noise("minecraft:badlands_surface"),
		icebergPill:   rs.Noise("minecraft:iceberg_pillar"),
		icebergRoof:   rs.Noise("minecraft:iceberg_pillar_roof"),
		icebergSurf:   rs.Noise("minecraft:iceberg_surface"),
		gradientRands: map[string]Positional{},
	}
	ss.generateBands(rs.Random.FromHash("minecraft:clay_bands"))
	return ss
}

// canonicalBlockState renders a BlockState with sorted properties.
func canonicalBlockState(b BlockState) string {
	if len(b.Props) == 0 {
		return b.Name
	}
	keys := make([]string, 0, len(b.Props))
	for k := range b.Props {
		keys = append(keys, k)
	}
	sortStrings(keys)
	out := b.Name + "["
	for i, k := range keys {
		if i > 0 {
			out += ","
		}
		out += k + "=" + b.Props[k]
	}
	return out + "]"
}

func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

// nextIntBetween draws from [lo, hi].
func nextIntBetween(r Random, lo, hi int32) int32 { return r.NextIntn(hi-lo+1) + lo }

// generateBands lays out the badlands clay bands: terracotta with
// orange stripes, runs of yellow, brown and red, and white bands edged
// in light gray.
func (ss *surfaceSystem) generateBands(r Random) {
	b := &ss.clayBands
	for i := range b {
		b[i] = stTerracotta
	}
	for i := int32(0); i < int32(len(b)); i++ {
		i += r.NextIntn(5) + 1
		if i < int32(len(b)) {
			b[i] = stOrangeTc
		}
	}
	ss.makeBands(r, 1, stYellowTc)
	ss.makeBands(r, 2, stBrownTc)
	ss.makeBands(r, 1, stRedTc)
	whites := nextIntBetween(r, 9, 15)
	for n, i := int32(0), int32(0); n < whites && i < int32(len(b)); n++ {
		b[i] = stWhiteTc
		if i-1 > 0 && r.NextBool() {
			b[i-1] = stLightGrayTc
		}
		if i+1 < int32(len(b)) && r.NextBool() {
			b[i+1] = stLightGrayTc
		}
		i += r.NextIntn(16) + 4
	}
}

func (ss *surfaceSystem) makeBands(r Random, base int32, s StateID) {
	b := &ss.clayBands
	count := nextIntBetween(r, 6, 15)
	for n := int32(0); n < count; n++ {
		width := base + r.NextIntn(3)
		start := r.NextIntn(int32(len(b)))
		for k := int32(0); start+k < int32(len(b)) && k < width; k++ {
			b[start+k] = s
		}
	}
}

// javaRound is Math.round: nearest, ties toward positive infinity.
func javaRound(v float64) int64 {
	f := math.Floor(v)
	if v-f >= 0.5 {
		f++
	}
	return int64(f)
}

func (ss *surfaceSystem) band(x, y, z int32) StateID {
	off := int32(javaRound(ss.clayOffset.Value(float64(x), 0, float64(z)) * 4))
	n := int32(len(ss.clayBands))
	return ss.clayBands[(y+off+n)%n]
}

// surfaceDepth is the column's surface layer thickness: a noise around
// 3 plus a quarter-block random nudge.
func (ss *surfaceSystem) surfaceDepth(x, z int32) int32 {
	v := ss.surface.Value(float64(x), 0, float64(z))
	r := ss.rs.Random.At(x, 0, z).NextDouble()
	return int32(float64(v*2.75) + 3 + float64(r*0.25))
}

func (ss *surfaceSystem) gradientRandom(name string) Positional {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	p, ok := ss.gradientRands[name]
	if !ok {
		p = ss.rs.Random.FromHash(name).ForkPositional()
		ss.gradientRands[name] = p
	}
	return p
}
