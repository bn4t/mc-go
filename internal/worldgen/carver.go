package worldgen

import (
	"encoding/json"
	"math"
	"strings"
	"sync"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// sinTable is Mth's 65536-entry float sine table.
var sinTable = sync.OnceValue(func() *[65536]float32 {
	var t [65536]float32
	for i := range t {
		t[i] = float32(math.Sin(float64(i) / 10430.378350470453))
	}
	return &t
})

func mthSin(d float64) float32 { return sinTable()[d2l(float64(d*10430.378350470453))&65535] }
func mthCos(d float64) float32 {
	return sinTable()[d2l(float64(d*10430.378350470453)+16384)&65535]
}

// carverConfig is a configured carver (cave, cave_extra_underground,
// canyon, nether_cave).
type carverConfig struct {
	kind        string
	probability float32
	y           heightProvider
	yScale      floatProvider
	lavaLevel   json.RawMessage
	replaceable map[world.Block]bool
	// cave
	hRadius, vRadius, floorLevel floatProvider
	// canyon
	vertRot                                  floatProvider
	thickness, distanceFactor, hRadiusFactor floatProvider
	vRadiusDefault, vRadiusCenter            float32
	widthSmoothness                          int32
}

func blockSet(spec json.RawMessage) map[world.Block]bool {
	var names []string
	var one string
	if json.Unmarshal(spec, &one) == nil {
		if strings.HasPrefix(one, "#") {
			names = data.BlockTags[trimNS(one[1:])]
		} else {
			names = []string{trimNS(one)}
		}
	} else {
		jsonUnmarshal(spec, &names)
	}
	set := map[world.Block]bool{}
	for _, n := range names {
		for _, s := range world.StatesOfBlock(trimNS(n)) {
			set[s] = true
		}
	}
	return set
}

func (rs *RandomState) carver(name string) *carverConfig {
	rs.carverMu.Lock()
	defer rs.carverMu.Unlock()
	if rs.carvers == nil {
		rs.carvers = map[string]*carverConfig{}
	}
	if c, ok := rs.carvers[name]; ok {
		return c
	}
	raw, ok := rs.data.Get("configured_carver/" + name)
	if !ok {
		panic("worldgen: unknown carver " + name)
	}
	var o obj
	jsonUnmarshal(raw, &o)
	var cf obj
	jsonUnmarshal(o["config"], &cf)
	c := &carverConfig{kind: trimNS(o.str("type"))}
	c.probability = parseF32(cf["probability"])
	c.y = parseHeightProvider(cf["y"])
	c.yScale = parseFloatProvider(cf["yScale"])
	c.lavaLevel = cf["lava_level"]
	c.replaceable = blockSet(cf["replaceable"])
	switch c.kind {
	case "cave", "nether_cave":
		c.hRadius = parseFloatProvider(cf["horizontal_radius_multiplier"])
		c.vRadius = parseFloatProvider(cf["vertical_radius_multiplier"])
		c.floorLevel = parseFloatProvider(cf["floor_level"])
	case "canyon":
		c.vertRot = parseFloatProvider(cf["vertical_rotation"])
		var sh obj
		jsonUnmarshal(cf["shape"], &sh)
		c.thickness = parseFloatProvider(sh["thickness"])
		c.distanceFactor = parseFloatProvider(sh["distance_factor"])
		c.hRadiusFactor = parseFloatProvider(sh["horizontal_radius_factor"])
		c.vRadiusDefault = parseF32(sh["vertical_radius_default_factor"])
		c.vRadiusCenter = parseF32(sh["vertical_radius_center_factor"])
		c.widthSmoothness = sh.i32("width_smoothness")
	default:
		panic("worldgen: unsupported carver type " + c.kind)
	}
	rs.carvers[name] = c
	return c
}

// carveCtx is CarvingContext plus the target chunk's state.
type carveCtx struct {
	g     *Generator
	p     *ProtoChunk
	nc    *NoiseChunk
	gc    genCtx
	mask  []bool // ((y-minY)*16+z)*16+x
	cave  world.Block
	air   world.Block
	water world.Block
	lava  world.Block
	grass world.Block
	myc   world.Block
	dirt  world.Block
	// aqSched is Aquifer.shouldScheduleFluidUpdate as read after the
	// carve state: stale (the last aquifer answer) on the lava-level path.
	aqSched bool
}

type skipFn func(x, y, z float64, yy int32) bool

func (c *carveCtx) canReplace(cfg *carverConfig, b world.Block) bool { return cfg.replaceable[b] }

func (c *carveCtx) carveEllipsoid(cfg *carverConfig, x, y, z, hr, vr float64, skip skipFn) bool {
	cx, cz := c.p.CX, c.p.CZ
	midX := float64(cx*16 + 8)
	midZ := float64(cz*16 + 8)
	reach := 16 + hr*2
	if math.Abs(x-midX) > reach || math.Abs(z-midZ) > reach {
		return false
	}
	minX, minZ := cx*16, cz*16
	x0 := max(floorI(x-hr)-minX-1, 0)
	x1 := min(floorI(x+hr)-minX, 15)
	y0 := max(floorI(y-vr)-1, c.gc.minY+1)
	y1 := min(floorI(y+vr)+1, c.gc.minY+c.gc.genDepth-1-7)
	z0 := max(floorI(z-hr)-minZ-1, 0)
	z1 := min(floorI(z+hr)-minZ, 15)
	carved := false
	for lx := x0; lx <= x1; lx++ {
		bx := minX + lx
		dx := (float64(bx) + 0.5 - x) / hr
		for lz := z0; lz <= z1; lz++ {
			bz := minZ + lz
			dz := (float64(bz) + 0.5 - z) / hr
			if float64(dx*dx)+float64(dz*dz) >= 1 {
				continue
			}
			surface := false
			for yy := y1; yy > y0; yy-- {
				dy := (float64(yy) - 0.5 - y) / vr
				if skip(dx, dy, dz, yy) {
					continue
				}
				mi := ((yy-c.p.MinY)*16+lz)*16 + lx
				if c.mask[mi] {
					continue
				}
				c.mask[mi] = true
				if c.carveBlock(cfg, lx, yy, lz, bx, bz, &surface) {
					carved = true
				}
			}
		}
	}
	return carved
}

var caveAirBlock = sync.OnceValue(func() world.Block { return blockNamed("cave_air") })

func (c *carveCtx) carveBlock(cfg *carverConfig, lx, y, lz, bx, bz int32, surface *bool) bool {
	st := c.p.Get(lx, y, lz)
	if cfg.kind == "nether_cave" {
		// NetherWorldCarver.carveBlock: no aquifer and no surface
		// repair — lava up to 31 above the generation floor, cave air
		// above.
		if !c.canReplace(cfg, st) {
			return false
		}
		ns := caveAirBlock()
		if y <= c.gc.minY+31 {
			ns = c.lava
		}
		c.p.Set(lx, y, lz, ns)
		return true
	}
	if st == c.grass || st == c.myc || sameBlock(st, c.grass) || sameBlock(st, c.myc) {
		*surface = true
	}
	if !c.canReplace(cfg, st) {
		return false
	}
	ns, ok := c.carveState(cfg, bx, y, bz)
	if !ok {
		return false
	}
	c.p.Set(lx, y, lz, ns)
	if c.aqSched && ns != c.air {
		c.p.FluidMarks = append(c.p.FluidMarks, [3]int32{bx, y, bz})
	}
	if *surface {
		if below := c.p.Get(lx, y-1, lz); sameBlock(below, c.dirt) {
			hasFluid := ns != c.air
			if b, ok := c.g.RS.topMaterial(c, bx, y-1, bz, hasFluid); ok {
				c.p.Set(lx, y-1, lz, b)
			}
		}
	}
	return true
}

func (c *carveCtx) carveState(cfg *carverConfig, x, y, z int32) (world.Block, bool) {
	if c.nc.aq != nil {
		c.aqSched = c.nc.aq.sched
	}
	if y <= c.gc.anchor(cfg.lavaLevel) {
		return c.lava, true
	}
	if c.nc.aq == nil {
		s := c.g.RS.globalFluid(y).at(y)
		return substanceBlock(s, c), true
	}
	s, ok, sched := c.nc.aq.computeSubstance(&Ctx{X: x, Y: y, Z: z, nc: c.nc}, 0)
	c.aqSched = sched
	if !ok {
		return 0, false
	}
	return substanceBlock(s, c), true
}

func substanceBlock(s Substance, c *carveCtx) world.Block {
	switch s {
	case SubWater:
		return c.water
	case SubLava:
		return c.lava
	}
	return c.air
}

// topMaterial is SurfaceSystem.topMaterial: the surface rule's pick for
// a freshly exposed dirt block, with stone depths of one.
func (rs *RandomState) topMaterial(c *carveCtx, x, y, z int32, hasFluid bool) (world.Block, bool) {
	s := rs.surfaceSys()
	sc := &surfCtx{s: s, p: c.p, nc: c.nc, biomeAt: c.g.biomeAtUncached, minGenY: rs.settings.minY, genDepth: rs.settings.height}
	sc.updateXZ(x, z)
	water := int32(math.MinInt32)
	if hasFluid {
		water = y + 1
	}
	sc.updateY(1, 1, water, y)
	return s.rule.apply(sc)
}

func (c *carveCtx) caveCarve(cfg *carverConfig, r Random, startCX, startCZ int32) {
	bound := int32((4*2 - 1) * 16)
	caveBound := int32(15) // CaveWorldCarver.getCaveBound
	if cfg.kind == "nether_cave" {
		caveBound = 10 // NetherWorldCarver.getCaveBound
	}
	n := r.NextIntN(r.NextIntN(r.NextIntN(caveBound)+1) + 1)
	for i := int32(0); i < n; i++ {
		x := float64(startCX*16 + r.NextIntN(16))
		y := float64(cfg.y(r, c.gc))
		z := float64(startCZ*16 + r.NextIntN(16))
		hr := float64(cfg.hRadius(r))
		vr := float64(cfg.vRadius(r))
		floor := float64(cfg.floorLevel(r))
		skip := func(dx, dy, dz float64, _ int32) bool {
			if dy <= floor {
				return true
			}
			return float64(dx*dx)+float64(dy*dy)+float64(dz*dz) >= 1
		}
		tunnels := int32(1)
		if r.NextIntN(4) == 0 {
			ys := float64(cfg.yScale(r))
			f := 1 + float32(r.NextFloat()*6)
			c.caveRoom(cfg, x, y, z, f, ys, skip)
			tunnels += r.NextIntN(4)
		}
		for j := int32(0); j < tunnels; j++ {
			yaw := float32(r.NextFloat() * 6.2831855)
			pitch := (r.NextFloat() - 0.5) / 4
			thick := caveThickness(r, cfg.kind)
			branch := bound - r.NextIntN(bound/4)
			ys := 1.0
			if cfg.kind == "nether_cave" {
				ys = 5
			}
			c.caveTunnel(cfg, r.NextLong(), x, y, z, hr, vr, thick, yaw, pitch, 0, branch, ys, skip)
		}
	}
}

func caveThickness(r Random, kind string) float32 {
	if kind == "nether_cave" {
		return (r.NextFloat()*2 + r.NextFloat()) * 2
	}
	f := r.NextFloat()*2 + r.NextFloat()
	if r.NextIntN(10) == 0 {
		f *= r.NextFloat()*r.NextFloat()*3 + 1
	}
	return f
}

func (c *carveCtx) caveRoom(cfg *carverConfig, x, y, z float64, radius float32, yScale float64, skip skipFn) {
	d := 1.5 + float64(mthSin(1.5707963705062866)*radius)
	e := float64(d * yScale)
	c.carveEllipsoid(cfg, x+1, y, z, d, e, skip)
}

func canReach(cx, cz int32, x, z float64, step, total int32, thickness float32) bool {
	d := x - float64(cx*16+8)
	e := z - float64(cz*16+8)
	f := float64(total - step)
	g := float64(thickness + 2 + 16)
	return float64(d*d)+float64(e*e)-float64(f*f) <= float64(g*g)
}

func (c *carveCtx) caveTunnel(cfg *carverConfig, seed int64, x, y, z, hr, vr float64, thickness, yaw, pitch float32, step, total int32, yScale float64, skip skipFn) {
	r := NewLegacy(seed)
	branchAt := r.NextIntN(total/2) + total/4
	steep := r.NextIntN(6) == 0
	var dYaw, dPitch float32
	for i := step; i < total; i++ {
		d := 1.5 + float64(mthSin(float64(float32(3.1415927*float32(i))/float32(total)))*thickness)
		e := float64(d * yScale)
		cosP := mthCos(float64(pitch))
		x += float64(mthCos(float64(yaw)) * cosP)
		y += float64(mthSin(float64(pitch)))
		z += float64(mthSin(float64(yaw)) * cosP)
		if steep {
			pitch *= 0.92
		} else {
			pitch *= 0.7
		}
		pitch += dPitch * 0.1
		yaw += dYaw * 0.1
		dPitch *= 0.9
		dYaw *= 0.75
		dPitch += float32((r.NextFloat()-r.NextFloat())*r.NextFloat()) * 2
		dYaw += float32((r.NextFloat()-r.NextFloat())*r.NextFloat()) * 4
		if i == branchAt && thickness > 1 {
			c.caveTunnel(cfg, r.NextLong(), x, y, z, hr, vr, r.NextFloat()*0.5+0.5, yaw-1.5707964, pitch/3, i, total, 1, skip)
			c.caveTunnel(cfg, r.NextLong(), x, y, z, hr, vr, r.NextFloat()*0.5+0.5, yaw+1.5707964, pitch/3, i, total, 1, skip)
			return
		}
		if r.NextIntN(4) == 0 {
			continue
		}
		if !canReach(c.p.CX, c.p.CZ, x, z, i, total, thickness) {
			return
		}
		c.carveEllipsoid(cfg, x, y, z, float64(d*hr), float64(e*vr), skip)
	}
}

func (c *carveCtx) canyonCarve(cfg *carverConfig, r Random, startCX, startCZ int32) {
	bound := int32((4*2 - 1) * 16)
	x := float64(startCX*16 + r.NextIntN(16))
	y := cfg.y(r, c.gc)
	z := float64(startCZ*16 + r.NextIntN(16))
	yaw := float32(r.NextFloat() * 6.2831855)
	pitch := cfg.vertRot(r)
	ys := float64(cfg.yScale(r))
	thick := cfg.thickness(r)
	total := int32(float32(bound) * cfg.distanceFactor(r))
	c.canyonDo(cfg, r.NextLong(), x, float64(y), z, thick, yaw, pitch, 0, total, ys)
}

func (c *carveCtx) canyonDo(cfg *carverConfig, seed int64, x, y, z float64, thickness, yaw, pitch float32, step, total int32, yScale float64) {
	r := NewLegacy(seed)
	depth := c.gc.genDepth
	widths := make([]float32, depth)
	f := float32(1)
	for i := int32(0); i < depth; i++ {
		if i == 0 || r.NextIntN(cfg.widthSmoothness) == 0 {
			f = 1 + float32(r.NextFloat()*r.NextFloat())
		}
		widths[i] = f * f
	}
	skip := func(dx, dy, dz float64, yy int32) bool {
		k := yy - c.gc.minY
		return float64(float64(float64(dx*dx)+float64(dz*dz))*float64(widths[k-1]))+float64(dy*dy)/6 >= 1
	}
	var dYaw, dPitch float32
	for i := step; i < total; i++ {
		d := 1.5 + float64(mthSin(float64(float32(float32(i)*3.1415927)/float32(total)))*thickness)
		e := float64(d * yScale)
		d = float64(d * float64(cfg.hRadiusFactor(r)))
		e = c.canyonVertical(cfg, r, e, float32(total), float32(i))
		cosP := mthCos(float64(pitch))
		sinP := mthSin(float64(pitch))
		x += float64(mthCos(float64(yaw)) * cosP)
		y += float64(sinP)
		z += float64(mthSin(float64(yaw)) * cosP)
		pitch *= 0.7
		pitch += dPitch * 0.05
		yaw += dYaw * 0.05
		dPitch *= 0.8
		dYaw *= 0.5
		dPitch += float32((r.NextFloat()-r.NextFloat())*r.NextFloat()) * 2
		dYaw += float32((r.NextFloat()-r.NextFloat())*r.NextFloat()) * 4
		if r.NextIntN(4) == 0 {
			continue
		}
		if !canReach(c.p.CX, c.p.CZ, x, z, i, total, thickness) {
			return
		}
		c.carveEllipsoid(cfg, x, y, z, d, e, skip)
	}
}

func (c *carveCtx) canyonVertical(cfg *carverConfig, r Random, e float64, total, i float32) float64 {
	f := 1 - abs32(0.5-i/total)*2
	g := cfg.vRadiusDefault + cfg.vRadiusCenter*f
	return float64(float64(g)*e) * float64(r.NextFloat()*(1-0.75)+0.75)
}

// carverBiome is ChunkAccess.carverBiome: the biome at the chunk's
// minimum corner quart, y quart 0, from the uncached source.
func (g *Generator) carverBiome(cx, cz int32) string {
	return g.Biomes.Biome(g.RS, nil, quartFromBlock(cx*16), 0, quartFromBlock(cz*16))
}

// biomeAtUncached zooms over the biome source directly (the carver's
// BiomeManager.withDifferentSource view).
func (g *Generator) biomeAtUncached(x, y, z int32) string {
	return zoomBiome(g.zoomSeed, x, y, z, func(qx, qy, qz int32) string { return g.Biomes.Biome(g.RS, nil, qx, qy, qz) })
}

// ApplyCarvers is NoiseBasedChunkGenerator.applyCarvers for p.
func (g *Generator) ApplyCarvers(p *ProtoChunk, nc *NoiseChunk) {
	rs := g.RS
	c := &carveCtx{g: g, p: p, nc: nc, gc: genCtx{rs.settings.minY, rs.settings.height},
		mask: make([]bool, len(p.Blocks)), air: world.Air, cave: blockNamed("cave_air"),
		water: blockNamed("water"), lava: blockNamed("lava"), grass: blockNamed("grass_block"),
		myc: blockNamed("mycelium"), dirt: blockNamed("dirt")}
	r := NewLegacy(0)
	for dx := int32(-8); dx <= 8; dx++ {
		for dz := int32(-8); dz <= 8; dz++ {
			sx, sz := p.CX+dx, p.CZ+dz
			biome := rs.BiomeInfo(g.carverBiome(sx, sz))
			for idx, name := range biome.carverNames() {
				cfg := rs.carver(trimNS(name))
				setLargeFeatureSeed(r, rs.Seed+int64(idx), sx, sz)
				if r.NextFloat() > cfg.probability {
					continue
				}
				switch cfg.kind {
				case "canyon":
					c.canyonCarve(cfg, r, sx, sz)
				default:
					c.caveCarve(cfg, r, sx, sz)
				}
			}
		}
	}
}

// setLargeFeatureSeed is WorldgenRandom.setLargeFeatureSeed.
func setLargeFeatureSeed(r Random, seed int64, x, z int32) {
	r.SetSeed(seed)
	l := r.NextLong()
	m := r.NextLong()
	r.SetSeed(int64(x)*l ^ int64(z)*m ^ seed)
}

func (b *BiomeInfo) carverNames() []string {
	raw, ok := b.raw["carvers"]
	if !ok {
		return nil
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	var list []string
	jsonUnmarshal(raw, &list)
	return list
}
