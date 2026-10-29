package worldgen

import (
	"encoding/json"
	"math"
	"strings"

	"mc-go/internal/world"
)

// surfaceSystem is vanilla's SurfaceSystem for one RandomState.
type surfaceSystem struct {
	rs                                                  *RandomState
	defaultBlock                                        world.Block
	seaLevel                                            int32
	clayBands                                           [192]world.Block
	clayBandsOffset, surface, surfaceSecondary          *NormalNoise
	badlandsPillar, badlandsPillarRoof, badlandsSurface *NormalNoise
	icebergPillar, icebergPillarRoof, icebergSurface    *NormalNoise
	rule                                                surfaceRule
	packedIce, snowBlock, water                         world.Block
}

func (rs *RandomState) surfaceSys() *surfaceSystem {
	rs.surfOnce.Do(func() {
		b := rs.b
		s := &surfaceSystem{rs: rs, seaLevel: rs.settings.seaLevel, defaultBlock: blockNamed(rs.settings.defaultBlock)}
		s.clayBandsOffset = b.noise("minecraft:clay_bands_offset")
		s.clayBands = generateBands(rs.random.FromHashOf("minecraft:clay_bands"))
		s.surface = b.noise("minecraft:surface")
		s.surfaceSecondary = b.noise("minecraft:surface_secondary")
		s.badlandsPillar = b.noise("minecraft:badlands_pillar")
		s.badlandsPillarRoof = b.noise("minecraft:badlands_pillar_roof")
		s.badlandsSurface = b.noise("minecraft:badlands_surface")
		s.icebergPillar = b.noise("minecraft:iceberg_pillar")
		s.icebergPillarRoof = b.noise("minecraft:iceberg_pillar_roof")
		s.icebergSurface = b.noise("minecraft:iceberg_surface")
		s.packedIce = blockNamed("packed_ice")
		s.snowBlock = blockNamed("snow_block")
		s.water = blockNamed("water")
		s.rule = s.compileRule(rs.settings.raw["surface_rule"])
		rs.surf = s
	})
	return rs.surf
}

func generateBands(r Random) [192]world.Block {
	var bands [192]world.Block
	tc := blockNamed("terracotta")
	for i := range bands {
		bands[i] = tc
	}
	orange := blockNamed("orange_terracotta")
	for i := int32(0); i < 192; i++ {
		i += r.NextIntN(5) + 1
		if i < 192 {
			bands[i] = orange
		}
	}
	makeBands(r, &bands, 1, blockNamed("yellow_terracotta"))
	makeBands(r, &bands, 2, blockNamed("brown_terracotta"))
	makeBands(r, &bands, 1, blockNamed("red_terracotta"))
	n := r.NextIntN(15-9+1) + 9
	white := blockNamed("white_terracotta")
	lgray := blockNamed("light_gray_terracotta")
	j := int32(0)
	for c := int32(0); c < n && j < 192; c++ {
		bands[j] = white
		if j-1 > 0 && r.NextBool() {
			bands[j-1] = lgray
		}
		if j+1 < 192 && r.NextBool() {
			bands[j+1] = lgray
		}
		j += r.NextIntN(16) + 4
	}
	return bands
}

func makeBands(r Random, bands *[192]world.Block, base int32, state world.Block) {
	n := r.NextIntN(15-6+1) + 6
	for i := int32(0); i < n; i++ {
		w := base + r.NextIntN(3)
		start := r.NextIntN(192)
		for k := int32(0); start+k < 192 && k < w; k++ {
			bands[start+k] = state
		}
	}
}

func javaRound(v float64) int64 {
	f := math.Floor(v)
	if v-f >= 0.5 {
		f++
	}
	return d2l(f)
}

func (s *surfaceSystem) band(x, y, z int32) world.Block {
	i := int32(javaRound(float64(s.clayBandsOffset.Value(float64(x), 0, float64(z)) * 4)))
	return s.clayBands[(y+i+192)%192]
}

func (s *surfaceSystem) surfaceDepth(x, z int32) int32 {
	d := s.surface.Value(float64(x), 0, float64(z))
	r := s.rs.random.At(x, 0, z)
	return d2i(float64(d*2.75) + 3 + float64(r.NextDouble()*0.25))
}

// surfCtx is SurfaceRules.Context: the per-column and per-y state the
// conditions read.
type surfCtx struct {
	s                                             *surfaceSystem
	p                                             *ProtoChunk
	nc                                            *NoiseChunk
	biomeAt                                       func(x, y, z int32) string
	blockX, blockZ, blockY                        int32
	surfaceDepth                                  int32
	secondarySet                                  bool
	secondary                                     float64
	minSurfSet                                    bool
	minSurface                                    int32
	cellOrigin                                    [2]int32
	cellSet                                       bool
	cell                                          [4]int32
	waterHeight, stoneDepthBelow, stoneDepthAbove int32
	biome                                         string
	steepSet, steep                               bool
	minGenY, genDepth                             int32
}

func (c *surfCtx) updateXZ(x, z int32) {
	c.blockX, c.blockZ = x, z
	c.surfaceDepth = c.s.surfaceDepth(x, z)
	c.secondarySet = false
	c.minSurfSet = false
	c.steepSet = false
}

func (c *surfCtx) updateY(above, below, water, y int32) {
	c.biome = ""
	c.blockY = y
	c.waterHeight = water
	c.stoneDepthBelow = below
	c.stoneDepthAbove = above
}

func (c *surfCtx) getBiome() string {
	if c.biome == "" {
		c.biome = c.biomeAt(c.blockX, c.blockY, c.blockZ)
	}
	return c.biome
}

func (c *surfCtx) surfaceSecondary() float64 {
	if !c.secondarySet {
		c.secondary = c.s.surfaceSecondary.Value(float64(c.blockX), 0, float64(c.blockZ))
		c.secondarySet = true
	}
	return c.secondary
}

func (c *surfCtx) minSurfaceLevel() int32 {
	if !c.minSurfSet {
		c.minSurfSet = true
		cx, cz := c.blockX>>4, c.blockZ>>4
		if !c.cellSet || c.cellOrigin != [2]int32{cx, cz} {
			c.cellSet = true
			c.cellOrigin = [2]int32{cx, cz}
			c.cell[0] = c.nc.preliminarySurfaceLevel(cx<<4, cz<<4)
			c.cell[1] = c.nc.preliminarySurfaceLevel((cx+1)<<4, cz<<4)
			c.cell[2] = c.nc.preliminarySurfaceLevel(cx<<4, (cz+1)<<4)
			c.cell[3] = c.nc.preliminarySurfaceLevel((cx+1)<<4, (cz+1)<<4)
		}
		fx := float64(float32(c.blockX&15) / 16)
		fz := float64(float32(c.blockZ&15) / 16)
		i := floorI(lerp2(fx, fz, float64(c.cell[0]), float64(c.cell[1]), float64(c.cell[2]), float64(c.cell[3])))
		c.minSurface = i + c.surfaceDepth - 8
	}
	return c.minSurface
}

func (c *surfCtx) isSteep() bool {
	if !c.steepSet {
		c.steepSet = true
		x := c.blockX & 15
		z := c.blockZ & 15
		zl := max(z-1, 0)
		zh := min(z+1, 15)
		a := c.p.WorldSurfaceWG(x, zl)
		b := c.p.WorldSurfaceWG(x, zh)
		if b >= a+4 {
			c.steep = true
		} else {
			xl := max(x-1, 0)
			xh := min(x+1, 15)
			d := c.p.WorldSurfaceWG(xl, z)
			e := c.p.WorldSurfaceWG(xh, z)
			c.steep = d >= e+4
		}
	}
	return c.steep
}

type surfaceRule interface {
	apply(c *surfCtx) (world.Block, bool)
}

type surfCond interface {
	test(c *surfCtx) bool
}

type seqRule []surfaceRule

func (r seqRule) apply(c *surfCtx) (world.Block, bool) {
	for _, s := range r {
		if b, ok := s.apply(c); ok {
			return b, true
		}
	}
	return 0, false
}

type stateRule world.Block

func (r stateRule) apply(*surfCtx) (world.Block, bool) { return world.Block(r), true }

type testRule struct {
	cond surfCond
	then surfaceRule
}

func (r *testRule) apply(c *surfCtx) (world.Block, bool) {
	if !r.cond.test(c) {
		return 0, false
	}
	return r.then.apply(c)
}

type bandlandsRule struct{}

func (bandlandsRule) apply(c *surfCtx) (world.Block, bool) {
	return c.s.band(c.blockX, c.blockY, c.blockZ), true
}

type condFunc func(c *surfCtx) bool

func (f condFunc) test(c *surfCtx) bool { return f(c) }

func (s *surfaceSystem) compileRule(raw json.RawMessage) surfaceRule {
	var o obj
	jsonUnmarshal(raw, &o)
	switch trimNS(o.str("type")) {
	case "sequence":
		var items []json.RawMessage
		jsonUnmarshal(o["sequence"], &items)
		r := seqRule{}
		for _, it := range items {
			r = append(r, s.compileRule(it))
		}
		return r
	case "block":
		var st obj
		jsonUnmarshal(o["result_state"], &st)
		return stateRule(stateFromJSON(st))
	case "condition":
		return &testRule{cond: s.compileCond(o["if_true"]), then: s.compileRule(o["then_run"])}
	case "bandlands":
		return bandlandsRule{}
	}
	panic("worldgen: unknown surface rule " + o.str("type"))
}

// resolveAnchor is VerticalAnchor.resolveY over the generation context.
func resolveAnchor(raw json.RawMessage, minGenY, genDepth int32) int32 {
	var o obj
	jsonUnmarshal(raw, &o)
	if v, ok := o["absolute"]; ok {
		return int32(obj{"v": v}.f64("v"))
	}
	if v, ok := o["above_bottom"]; ok {
		return minGenY + int32(obj{"v": v}.f64("v"))
	}
	if v, ok := o["below_top"]; ok {
		return genDepth - 1 + minGenY - int32(obj{"v": v}.f64("v"))
	}
	panic("worldgen: bad vertical anchor")
}

func (s *surfaceSystem) compileCond(raw json.RawMessage) surfCond {
	var o obj
	jsonUnmarshal(raw, &o)
	minY, depth := s.rs.settings.minY, s.rs.settings.height
	boolField := func(k string) bool {
		var b bool
		if v, ok := o[k]; ok {
			jsonUnmarshal(v, &b)
		}
		return b
	}
	switch trimNS(o.str("type")) {
	case "biome":
		set := map[string]bool{}
		var one string
		if json.Unmarshal(o["biome_is"], &one) == nil {
			set[trimNS(one)] = true
		} else {
			var many []string
			jsonUnmarshal(o["biome_is"], &many)
			for _, b := range many {
				set[trimNS(b)] = true
			}
		}
		return condFunc(func(c *surfCtx) bool { return set[c.getBiome()] })
	case "noise_threshold":
		n := s.rs.b.noise(o.str("noise"))
		lo, hi := o.f64("min_threshold"), o.f64("max_threshold")
		is3d := boolField("is_3d")
		return condFunc(func(c *surfCtx) bool {
			var v float64
			if is3d {
				v = n.Value(float64(c.blockX), float64(c.blockY), float64(c.blockZ))
			} else {
				v = n.Value(float64(c.blockX), 0, float64(c.blockZ))
			}
			return v >= lo && v <= hi
		})
	case "vertical_gradient":
		lo := resolveAnchor(o["true_at_and_below"], minY, depth)
		hi := resolveAnchor(o["false_at_and_above"], minY, depth)
		rf := s.rs.random.FromHashOf(qualify(o.str("random_name"))).ForkPositional()
		return condFunc(func(c *surfCtx) bool {
			y := c.blockY
			if y <= lo {
				return true
			}
			if y >= hi {
				return false
			}
			d := lerp(inverseLerp(float64(y), float64(lo), float64(hi)), 1, 0)
			return float64(rf.At(c.blockX, y, c.blockZ).NextFloat()) < d
		})
	case "y_above":
		anchor := resolveAnchor(o["anchor"], minY, depth)
		mul := o.i32("surface_depth_multiplier")
		add := boolField("add_stone_depth")
		return condFunc(func(c *surfCtx) bool {
			y := c.blockY
			if add {
				y += c.stoneDepthAbove
			}
			return y >= anchor+c.surfaceDepth*mul
		})
	case "water":
		off := o.i32("offset")
		mul := o.i32("surface_depth_multiplier")
		add := boolField("add_stone_depth")
		return condFunc(func(c *surfCtx) bool {
			if c.waterHeight == math.MinInt32 {
				return true
			}
			y := c.blockY
			if add {
				y += c.stoneDepthAbove
			}
			return y >= c.waterHeight+off+c.surfaceDepth*mul
		})
	case "temperature":
		return condFunc(func(c *surfCtx) bool {
			return s.rs.BiomeInfo(c.getBiome()).ColdEnoughToSnow(c.blockX, c.blockY, c.blockZ, s.seaLevel)
		})
	case "steep":
		return condFunc(func(c *surfCtx) bool { return c.isSteep() })
	case "not":
		inner := s.compileCond(o["invert"])
		return condFunc(func(c *surfCtx) bool { return !inner.test(c) })
	case "hole":
		return condFunc(func(c *surfCtx) bool { return c.surfaceDepth <= 0 })
	case "above_preliminary_surface":
		return condFunc(func(c *surfCtx) bool { return c.blockY >= c.minSurfaceLevel() })
	case "stone_depth":
		off := o.i32("offset")
		addDepth := boolField("add_surface_depth")
		rng := o.i32("secondary_depth_range")
		ceiling := strings.TrimSpace(string(o["surface_type"])) == `"ceiling"`
		return condFunc(func(c *surfCtx) bool {
			d := c.stoneDepthAbove
			if ceiling {
				d = c.stoneDepthBelow
			}
			var sd int32
			if addDepth {
				sd = c.surfaceDepth
			}
			var sec int32
			if rng != 0 {
				sec = d2i(lerp(inverseLerp(c.surfaceSecondary(), -1, 1), 0, float64(rng)))
			}
			return d <= 1+off+sd+sec
		})
	}
	panic("worldgen: unknown surface condition " + o.str("type"))
}

func (s *surfaceSystem) isStone(b world.Block) bool {
	if !notAir(b) {
		return false
	}
	return !isFluidState(b)
}

func isFluidState(b world.Block) bool {
	n, _ := world.StateOwner(b)
	if n == "water" || n == "lava" || n == "bubble_column" {
		return true
	}
	if wl, ok := world.StateProperty(b, "waterlogged"); ok && wl == "true" {
		return true
	}
	return false
}

// BuildSurface is SurfaceSystem.buildSurface over a proto chunk whose
// noise stage is done. biomeAt is the zoomed BiomeManager lookup.
func (rs *RandomState) BuildSurface(p *ProtoChunk, nc *NoiseChunk, biomeAt func(x, y, z int32) string) {
	s := rs.surfaceSys()
	c := &surfCtx{s: s, p: p, nc: nc, biomeAt: biomeAt, minGenY: rs.settings.minY, genDepth: rs.settings.height}
	minX, minZ := p.CX*16, p.CZ*16
	for x := int32(0); x < 16; x++ {
		for z := int32(0); z < 16; z++ {
			bx, bz := minX+x, minZ+z
			top := p.WorldSurfaceWG(x, z) + 1
			biome := biomeAt(bx, top, bz)
			if biome == "eroded_badlands" {
				s.erodedBadlands(p, x, z, bx, bz, top)
			}
			top2 := p.WorldSurfaceWG(x, z) + 1
			c.updateXZ(bx, bz)
			var above int32
			water := int32(math.MinInt32)
			nextNonStone := int32(math.MaxInt32)
			for y := top2; y >= p.MinY; y-- {
				st := p.Get(x, y, z)
				if !notAir(st) {
					above = 0
					water = math.MinInt32
					continue
				}
				if isFluidState(st) {
					if water == math.MinInt32 {
						water = y + 1
					}
					continue
				}
				if nextNonStone >= y {
					nextNonStone = wayBelowMinY
					for yy := y - 1; yy >= p.MinY-1; yy-- {
						if !s.isStone(p.Get(x, yy, z)) {
							nextNonStone = yy + 1
							break
						}
					}
				}
				above++
				below := y - nextNonStone + 1
				c.updateY(above, below, water, y)
				if st == s.defaultBlock {
					if b, ok := s.rule.apply(c); ok {
						p.Set(x, y, z, b)
					}
				}
			}
			if biome == "frozen_ocean" || biome == "deep_frozen_ocean" {
				s.frozenOcean(p, c.minSurfaceLevel(), rs.BiomeInfo(biome), x, z, bx, bz, top)
			}
		}
	}
}

func (s *surfaceSystem) erodedBadlands(p *ProtoChunk, x, z, bx, bz, top int32) {
	e := math.Min(math.Abs(float64(s.badlandsSurface.Value(float64(bx), 0, float64(bz))*8.25)),
		float64(s.badlandsPillar.Value(float64(float64(bx)*0.2), 0, float64(float64(bz)*0.2))*15))
	if e <= 0 {
		return
	}
	h := math.Abs(float64(s.badlandsPillarRoof.Value(float64(float64(bx)*0.75), 0, float64(float64(bz)*0.75)) * 1.5))
	g := 64 + math.Min(float64(float64(e*e)*2.5), math.Ceil(float64(h*50))+24)
	gy := floorI(g)
	if top > gy {
		return
	}
	for y := gy; y >= p.MinY; y-- {
		st := p.Get(x, y, z)
		if sameBlock(st, s.defaultBlock) {
			break
		}
		if n, _ := world.StateOwner(st); n == "water" {
			return
		}
	}
	for y := gy; y >= p.MinY && !notAir(p.Get(x, y, z)); y-- {
		p.Set(x, y, z, s.defaultBlock)
	}
}

func sameBlock(a, b world.Block) bool {
	na, _ := world.StateOwner(a)
	nb, _ := world.StateOwner(b)
	return na == nb
}

func (s *surfaceSystem) frozenOcean(p *ProtoChunk, minSurface int32, biome *BiomeInfo, x, z, bx, bz, top int32) {
	e := math.Min(math.Abs(float64(s.icebergSurface.Value(float64(bx), 0, float64(bz))*8.25)),
		float64(s.icebergPillar.Value(float64(float64(bx)*1.28), 0, float64(float64(bz)*1.28))*15))
	if e <= 1.8 {
		return
	}
	h := math.Abs(float64(s.icebergPillarRoof.Value(float64(float64(bx)*1.17), 0, float64(float64(bz)*1.17)) * 1.5))
	i := math.Min(float64(float64(e*e)*1.2), math.Ceil(float64(h*40))+14)
	if biome.TemperatureAt(bx, s.seaLevel, bz, s.seaLevel) > 0.1 {
		i -= 2
	}
	var j float64
	if i > 2 {
		j = float64(s.seaLevel) - i - 7
		i += float64(s.seaLevel)
	} else {
		i, j = 0, 0
	}
	k := i
	r := s.rs.random.At(bx, 0, bz)
	snowMax := 2 + r.NextIntN(4)
	snowMin := s.seaLevel + 18 + r.NextIntN(10)
	snow := int32(0)
	for y := max(top, d2i(k)+1); y >= minSurface; y-- {
		st := p.Get(x, y, z)
		place := false
		if !notAir(st) && y < d2i(k) && r.NextDouble() > 0.01 {
			place = true
		} else if n, _ := world.StateOwner(st); n == "water" && y > d2i(j) && y < s.seaLevel && j != 0 && r.NextDouble() > 0.15 {
			place = true
		}
		if !place {
			continue
		}
		if snow <= snowMax && y > snowMin {
			p.Set(x, y, z, s.snowBlock)
			snow++
		} else {
			p.Set(x, y, z, s.packedIce)
		}
	}
}
