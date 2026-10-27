package worldgen

import "math"

// surfaceContext is the column state surface rules read, refreshed per
// column and per block like vanilla's SurfaceRules.Context, with the
// expensive values (biome, noises) computed on first use.
type surfaceContext struct {
	ss    *surfaceSystem
	f     *NoiseChunkFiller
	chunk *GenChunk
	biome func(x, y, z int32) string

	// column
	x, z           int32
	surfaceDepth   int32
	haveSecondary  bool
	secondary      float64
	haveMinSurface bool
	minSurface     int32
	noise2D        map[string]float64

	// block
	y               int32
	waterHeight     int32
	stoneDepthBelow int32
	stoneDepthAbove int32
	haveBiome       bool
	blockBiome      string
	noise3D         map[string]float64
}

func (c *surfaceContext) updateXZ(x, z int32) {
	c.x, c.z = x, z
	c.surfaceDepth = c.ss.surfaceDepth(x, z)
	c.haveSecondary, c.haveMinSurface = false, false
	clear(c.noise2D)
	c.haveBiome = false
	clear(c.noise3D)
}

func (c *surfaceContext) updateY(stoneDepthAbove, stoneDepthBelow, waterHeight, y int32) {
	c.y = y
	c.waterHeight = waterHeight
	c.stoneDepthBelow = stoneDepthBelow
	c.stoneDepthAbove = stoneDepthAbove
	c.haveBiome = false
	clear(c.noise3D)
}

func (c *surfaceContext) getBiome() string {
	if !c.haveBiome {
		c.blockBiome = c.biome(c.x, c.y, c.z)
		c.haveBiome = true
	}
	return c.blockBiome
}

func (c *surfaceContext) surfaceSecondary() float64 {
	if !c.haveSecondary {
		c.secondary = c.ss.secondary.Value(float64(c.x), 0, float64(c.z))
		c.haveSecondary = true
	}
	return c.secondary
}

// minSurfaceLevel is the preliminary surface interpolated across the
// 16-block cell around the column, lowered by 8 and raised by the
// surface depth: blocks below it are deep underground.
func (c *surfaceContext) minSurfaceLevel() int32 {
	if !c.haveMinSurface {
		cx, cz := c.x>>4, c.z>>4
		s00 := float64(c.f.preliminarySurface(cx<<4, cz<<4))
		s10 := float64(c.f.preliminarySurface((cx+1)<<4, cz<<4))
		s01 := float64(c.f.preliminarySurface(cx<<4, (cz+1)<<4))
		s11 := float64(c.f.preliminarySurface((cx+1)<<4, (cz+1)<<4))
		tx := float64(float32(c.x&15) / 16)
		tz := float64(float32(c.z&15) / 16)
		c.minSurface = floorInt(lerp2(tx, tz, s00, s10, s01, s11)) + c.surfaceDepth - 8
		c.haveMinSurface = true
	}
	return c.minSurface
}

func (c *surfaceContext) noiseValue(id string, is3D bool) float64 {
	if is3D {
		if v, ok := c.noise3D[id]; ok {
			return v
		}
		v := c.ss.rs.Noise(id).Value(float64(c.x), float64(c.y), float64(c.z))
		c.noise3D[id] = v
		return v
	}
	if v, ok := c.noise2D[id]; ok {
		return v
	}
	v := c.ss.rs.Noise(id).Value(float64(c.x), 0, float64(c.z))
	c.noise2D[id] = v
	return v
}

func (c *surfaceContext) test(sc *SurfaceCondition) bool {
	switch sc.kind {
	case scBiome:
		return sc.biomes[c.getBiome()]
	case scNoiseThreshold:
		v := c.noiseValue(sc.noise, sc.is3D)
		return v >= sc.min && v <= sc.max
	case scVerticalGradient:
		lo := sc.a.resolve(c.f.minY, c.f.height)
		hi := sc.b.resolve(c.f.minY, c.f.height)
		if c.y <= lo {
			return true
		}
		if c.y >= hi {
			return false
		}
		chance := mapRange(float64(c.y), float64(lo), float64(hi), 1, 0)
		return float64(c.ss.gradientRandom(sc.name).At(c.x, c.y, c.z).NextFloat()) < chance
	case scYAbove:
		var add int32
		if sc.add {
			add = c.stoneDepthAbove
		}
		return c.y+add >= sc.a.resolve(c.f.minY, c.f.height)+c.surfaceDepth*sc.mult
	case scWater:
		if c.waterHeight == math.MinInt32 {
			return true
		}
		var add int32
		if sc.add {
			add = c.stoneDepthAbove
		}
		return c.y+add >= c.waterHeight+sc.offset+c.surfaceDepth*sc.mult
	case scTemperature:
		return ColdEnoughToSnow(c.getBiome(), c.x, c.y, c.z, c.ss.seaLevel)
	case scSteep:
		x, z := c.x&15, c.z&15
		north := c.chunk.HeightAt(x, max(z-1, 0))
		south := c.chunk.HeightAt(x, min(z+1, 15))
		if south >= north+4 {
			return true
		}
		west := c.chunk.HeightAt(max(x-1, 0), z)
		east := c.chunk.HeightAt(min(x+1, 15), z)
		return west >= east+4
	case scNot:
		return !c.test(sc.inner)
	case scHole:
		return c.surfaceDepth <= 0
	case scAbovePreliminarySurface:
		return c.y >= c.minSurfaceLevel()
	case scStoneDepth:
		depth := c.stoneDepthAbove
		if sc.ceiling {
			depth = c.stoneDepthBelow
		}
		var surface, secondary int32
		if sc.add {
			surface = c.surfaceDepth
		}
		if sc.secRange != 0 {
			secondary = int32(mapRange(c.surfaceSecondary(), -1, 1, 0, float64(sc.secRange)))
		}
		return depth <= 1+sc.offset+surface+secondary
	}
	return false
}

// apply runs a rule for the current block: the state to place, or false.
func (c *surfaceContext) apply(r *SurfaceRule) (StateID, bool) {
	switch r.kind {
	case srBlockRule:
		return InternState(r.state), true
	case srSequenceRule:
		for _, sub := range r.rules {
			if s, ok := c.apply(sub); ok {
				return s, true
			}
		}
	case srConditionRule:
		if c.test(r.cond) {
			return c.apply(r.rules[0])
		}
	case srBandlandsRule:
		return c.ss.band(c.x, c.y, c.z), true
	}
	return 0, false
}

// isStoneState is SurfaceSystem.isStone: neither air nor a fluid.
func isStoneState(s StateID) bool { return s != stAir && !isFluidState(s) }

// BuildSurface runs the surface stage over a chunk just filled by this
// filler: column by column (x outer, z inner), the eroded badlands and
// frozen ocean column extensions and the surface rules over every
// default block, scanning down from just above the surface.
func (f *NoiseChunkFiller) BuildSurface(chunk *GenChunk, biome func(x, y, z int32) string) {
	ss := f.surface()
	rule := f.rs.Settings.Surface
	ctx := &surfaceContext{ss: ss, f: f, chunk: chunk, biome: biome,
		noise2D: map[string]float64{}, noise3D: map[string]float64{}}
	minX, minZ := chunk.CX*16, chunk.CZ*16
	for lx := int32(0); lx < 16; lx++ {
		for lz := int32(0); lz < 16; lz++ {
			x, z := minX+lx, minZ+lz
			start := chunk.HeightAt(lx, lz) + 1
			colBiome := biome(x, start, z)
			if colBiome == "minecraft:eroded_badlands" {
				ss.erodedBadlands(chunk, lx, lz, x, z, start)
			}
			start = chunk.HeightAt(lx, lz) + 1
			ctx.updateXZ(x, z)
			stoneAbove := int32(0)
			water := int32(math.MinInt32)
			stoneBelowTop := int32(math.MaxInt32)
			for y := start; y >= chunk.MinY; y-- {
				b := chunk.Get(lx, y, lz)
				if b == stAir {
					stoneAbove, water = 0, math.MinInt32
					continue
				}
				if isFluidState(b) {
					if water == math.MinInt32 {
						water = y + 1
					}
					continue
				}
				if stoneBelowTop >= y {
					stoneBelowTop = wayBelowMinY
					for yy := y - 1; yy >= chunk.MinY-1; yy-- {
						if !isStoneState(chunk.Get(lx, yy, lz)) {
							stoneBelowTop = yy + 1
							break
						}
					}
				}
				stoneAbove++
				ctx.updateY(stoneAbove, y-stoneBelowTop+1, water, y)
				if b != ss.defaultState {
					continue
				}
				if s, ok := ctx.apply(rule); ok && y >= chunk.MinY && y < chunk.MinY+chunk.Height {
					chunk.set(lx, y, lz, s)
				}
			}
			if colBiome == "minecraft:frozen_ocean" || colBiome == "minecraft:deep_frozen_ocean" {
				ss.frozenOcean(chunk, ctx.minSurfaceLevel(), colBiome, lx, lz, x, z, start)
			}
		}
	}
}

// erodedBadlands raises terracotta-capped stone pillars out of eroded
// badlands columns.
func (ss *surfaceSystem) erodedBadlands(chunk *GenChunk, lx, lz, x, z, start int32) {
	e := math.Min(math.Abs(ss.badlandsSurf.Value(float64(x), 0, float64(z))*8.25),
		ss.badlandsPill.Value(float64(x)*0.2, 0, float64(z)*0.2)*15)
	if e <= 0 {
		return
	}
	roof := math.Abs(ss.badlandsRoof.Value(float64(x)*0.75, 0, float64(z)*0.75) * 1.5)
	top := floorInt(64 + math.Min(e*e*2.5, math.Ceil(roof*50)+24))
	if start > top {
		return
	}
	for y := top; y >= chunk.MinY; y-- {
		b := chunk.Get(lx, y, lz)
		if b == ss.defaultState {
			break
		}
		if b == stWater {
			return
		}
	}
	for y := top; y >= chunk.MinY && chunk.Get(lx, y, lz) == stAir; y-- {
		chunk.set(lx, y, lz, ss.defaultState)
	}
}

// frozenOcean builds packed-ice icebergs (snow-capped above the sea)
// in frozen ocean columns.
func (ss *surfaceSystem) frozenOcean(chunk *GenChunk, minSurface int32, biome string, lx, lz, x, z, start int32) {
	e := math.Min(math.Abs(ss.icebergSurf.Value(float64(x), 0, float64(z))*8.25),
		ss.icebergPill.Value(float64(x)*1.28, 0, float64(z)*1.28)*15)
	if e <= 1.8 {
		return
	}
	roof := math.Abs(ss.icebergRoof.Value(float64(x)*1.17, 0, float64(z)*1.17) * 1.5)
	top := math.Min(e*e*1.2, math.Ceil(roof*40)+14)
	if BiomeTemperature(biome, x, ss.seaLevel, z, ss.seaLevel) > 0.1 {
		top -= 2
	}
	var bottom float64
	if top > 2 {
		bottom = float64(ss.seaLevel) - top - 7
		top += float64(ss.seaLevel)
	} else {
		top, bottom = 0, 0
	}
	r := ss.rs.Random.At(x, 0, z)
	maxSnow := 2 + r.NextIntn(4)
	minSnowY := ss.seaLevel + 18 + r.NextIntn(10)
	snow := int32(0)
	topI, bottomI := int32(top), int32(bottom)
	for y := max(start, topI+1); y >= minSurface; y-- {
		b := chunk.Get(lx, y, lz)
		place := b == stAir && y < topI && r.NextDouble() > 0.01
		if !place {
			place = b == stWater && y > bottomI && y < ss.seaLevel && bottom != 0 && r.NextDouble() > 0.15
		}
		if !place {
			continue
		}
		if snow <= maxSnow && y > minSnowY {
			chunk.set(lx, y, lz, stSnowBlock)
			snow++
		} else {
			chunk.set(lx, y, lz, stPackedIce)
		}
	}
}

func (f *NoiseChunkFiller) surface() *surfaceSystem { return f.rs.surface() }

// noiseStates maps the noise stage's blocks to interned states.
func (f *NoiseChunkFiller) noiseStates() [NBTuff + 1]StateID {
	return [NBTuff + 1]StateID{
		NBAir:              stAir,
		NBDefault:          f.surface().defaultState,
		NBWater:            stWater,
		NBLava:             stLava,
		NBCopperOre:        InternState("minecraft:copper_ore"),
		NBRawCopperBlock:   InternState("minecraft:raw_copper_block"),
		NBGranite:          InternState("minecraft:granite"),
		NBDeepslateIronOre: InternState("minecraft:deepslate_iron_ore"),
		NBRawIronBlock:     InternState("minecraft:raw_iron_block"),
		NBTuff:             InternState("minecraft:tuff"),
	}
}

// NewGenChunk wraps a noise fill of chunk (cx, cz) as states with its
// heightmap primed.
func (f *NoiseChunkFiller) NewGenChunk(cx, cz int32, blocks []NoiseBlock) *GenChunk {
	m := f.noiseStates()
	c := &GenChunk{CX: cx, CZ: cz, MinY: f.minY, Height: f.height, States: make([]StateID, len(blocks))}
	for i, b := range blocks {
		c.States[i] = m[b]
	}
	c.primeHeights()
	return c
}
