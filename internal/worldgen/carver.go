package worldgen

import (
	"math"
	"strings"

	"mc-go/internal/data"
)

// The carvers stage cuts caves and canyons into a chunk after its
// surface is built. Every chunk within eight of it may start carvers:
// each biome's carvers run in order, seeded from the world seed plus the
// carver's index and the starting chunk, and those that pass their
// probability carve a random walk of ellipsoids, of which only the parts
// inside the chunk being generated are cut. A carving mask keeps a block
// from being carved twice.

// FloatProvider is a float value provider: a constant, a uniform draw
// from [min, max), or a trapezoid of two summed uniform draws.
type FloatProvider struct {
	kind       uint8
	a, b, c, d float32
}

const (
	floatConstKind uint8 = iota
	floatUniformKind
	floatTrapezoidKind
	floatClampedNormalKind
)

func floatConst(v float32) FloatProvider { return FloatProvider{kind: floatConstKind, a: v} }
func floatUniform(lo, hi float32) FloatProvider {
	return FloatProvider{kind: floatUniformKind, a: lo, b: hi}
}
func floatTrapezoid(lo, hi, plateau float32) FloatProvider {
	return FloatProvider{kind: floatTrapezoidKind, a: lo, b: hi, c: plateau}
}

// floatClampedNormal is a normal draw of the mean and deviation,
// clamped to [lo, hi].
func floatClampedNormal(mean, deviation, lo, hi float32) FloatProvider {
	return FloatProvider{kind: floatClampedNormalKind, a: mean, b: deviation, c: lo, d: hi}
}

func clampedNormal(r Random, mean, deviation, lo, hi float32) float32 {
	return min(max(mean+float32(float32(r.NextGaussian())*deviation), lo), hi)
}

func (p FloatProvider) sample(r Random) float32 {
	switch p.kind {
	case floatClampedNormalKind:
		return clampedNormal(r, p.a, p.b, p.c, p.d)
	case floatUniformKind:
		return randomBetween(r, p.a, p.b)
	case floatTrapezoidKind:
		span := p.b - p.a
		ramp := (span - p.c) / 2
		flat := span - ramp
		v := p.a + float32(r.NextFloat()*flat)
		return v + float32(r.NextFloat()*ramp)
	}
	return p.a
}

// randomBetween is a uniform float in [lo, hi).
func randomBetween(r Random, lo, hi float32) float32 { return float32(r.NextFloat()*(hi-lo)) + lo }

// HeightProvider picks a Y; carvers only use uniform ones, drawing from
// the inclusive range between two anchors (the lower bound when the
// range is empty).
type HeightProvider struct{ lo, hi VerticalAnchor }

func heightUniform(lo, hi VerticalAnchor) HeightProvider { return HeightProvider{lo, hi} }

func (h HeightProvider) sample(r Random, minY, height int32) int32 {
	lo, hi := h.lo.resolve(minY, height), h.hi.resolve(minY, height)
	if lo > hi {
		return lo
	}
	return r.NextIntn(hi-lo+1) + lo
}

type carverKind uint8

const (
	carverCave carverKind = iota
	carverNetherCave
	carverCanyon
)

// CarverConfig is a configured carver.
type CarverConfig struct {
	Kind        carverKind
	Probability float32
	Y           HeightProvider
	LavaLevel   VerticalAnchor
	Replaceable string // block tag
	YScale      FloatProvider

	// caves
	HorizontalRadius, VerticalRadius, FloorLevel FloatProvider

	// canyons
	VerticalRotation, DistanceFactor, Thickness FloatProvider
	WidthSmoothness                             int32
	HorizontalRadiusFactor                      FloatProvider
	VerticalRadiusDefaultFactor                 float32
	VerticalRadiusCenterFactor                  float32
}

// mthSinTable is the game's 65536-step sine table over one turn.
var mthSinTable = func() *[65536]float32 {
	var t [65536]float32
	for i := range t {
		t[i] = float32(math.Sin(float64(i) / 10430.378350470453))
	}
	return &t
}()

func mthSin(v float64) float32 { return mthSinTable[int64(v*10430.378350470453)&65535] }
func mthCos(v float64) float32 { return mthSinTable[int64(v*10430.378350470453+16384)&65535] }

// SetSeed reseeds the source, scrambling the seed as NewLegacy does.
func (r *Legacy) SetSeed(seed int64) {
	r.seed = (uint64(seed) ^ lcgMult) & lcgMask
	r.g = gaussian{}
}

// setLargeFeatureSeed seeds the source for a structure or carver
// starting in chunk (cx, cz).
func (r *Legacy) setLargeFeatureSeed(seed int64, cx, cz int32) {
	r.SetSeed(seed)
	a, b := r.NextLong(), r.NextLong()
	r.SetSeed(int64(cx)*a ^ int64(cz)*b ^ seed)
}

var (
	stCaveAir = InternState("minecraft:cave_air")
	stVoidAir = InternState("minecraft:void_air")
	stDirt    = InternState("minecraft:dirt")
)

func isAirState(s StateID) bool { return s == stAir || s == stCaveAir || s == stVoidAir }

// blockOf is a state's block identifier without the namespace.
func blockOf(s StateID) string {
	name, _, _ := strings.Cut(StateName(s), "[")
	return strings.TrimPrefix(name, "minecraft:")
}

// carving is one chunk's carvers stage.
type carving struct {
	f                 *NoiseChunkFiller
	chunk             *GenChunk
	minGenY, genDepth int32
	mask              []uint64
	biome             func(x, y, z int32) string
	replaceable       map[string]map[StateID]bool
}

// Carve runs the carvers stage over a chunk the filler has just filled
// and surfaced. carverBiome names the biome whose carvers chunk (cx, cz)
// starts; biome is the block biome the top-material rules read.
func (f *NoiseChunkFiller) Carve(chunk *GenChunk, carverBiome func(cx, cz int32) string, biome func(x, y, z int32) string) {
	minGenY := max(chunk.MinY, f.minY)
	c := &carving{
		f: f, chunk: chunk, minGenY: minGenY,
		genDepth: min(chunk.MinY+chunk.Height, f.minY+f.height) - minGenY,
		mask:     make([]uint64, (256*int(chunk.Height)+63)/64),
		biome:    biome, replaceable: f.carverTags,
	}
	r := NewLegacy(0)
	for dx := int32(-8); dx <= 8; dx++ {
		for dz := int32(-8); dz <= 8; dz++ {
			sx, sz := chunk.CX+dx, chunk.CZ+dz
			for i, name := range biomeCarvers[carverBiome(sx, sz)] {
				cfg := configuredCarvers[name]
				r.setLargeFeatureSeed(f.rs.Seed+int64(i), sx, sz)
				if r.NextFloat() <= cfg.Probability {
					if cfg.Kind == carverCanyon {
						c.canyon(cfg, r, sx, sz)
					} else {
						c.cave(cfg, r, sx, sz)
					}
				}
			}
		}
	}
}

func (c *carving) maskIndex(lx, y, lz int32) int {
	return int(lx | lz<<4 | (y-c.chunk.MinY)<<8)
}

// canReplace reports whether a state is in the carver's replaceable tag.
func (c *carving) canReplace(cfg *CarverConfig, s StateID) bool {
	set := c.replaceable[cfg.Replaceable]
	if set == nil {
		set = map[StateID]bool{}
		c.replaceable[cfg.Replaceable] = set
	}
	ok, known := set[s]
	if !known {
		name := blockOf(s)
		for _, b := range data.BlockTags[cfg.Replaceable] {
			if b == name {
				ok = true
				break
			}
		}
		set[s] = ok
	}
	return ok
}

// canReach reports whether a tunnel at (x, z), with branches left to
// run, can still come within reach of the chunk being carved.
func (c *carving) canReach(x, z float64, branch, branches int32, thickness float32) bool {
	dx := x - float64(c.chunk.CX*16+8)
	dz := z - float64(c.chunk.CZ*16+8)
	left := float64(branches - branch)
	reach := float64(thickness + 2 + 16)
	return float64(dx*dx)+float64(dz*dz)-float64(left*left) <= float64(reach*reach)
}

// carveEllipsoid cuts the part of an ellipsoid inside the chunk, column
// by column and top down, skipping blocks the carver's shape excludes
// and blocks already carved.
func (c *carving) carveEllipsoid(cfg *CarverConfig, x, y, z, hr, vr float64, skip func(dx, dy, dz float64, y int32) bool) {
	minX, minZ := c.chunk.CX*16, c.chunk.CZ*16
	reach := 16 + hr*2
	if math.Abs(x-float64(minX+8)) > reach || math.Abs(z-float64(minZ+8)) > reach {
		return
	}
	floor := func(v float64) int32 { return int32(math.Floor(v)) }
	x0 := max(floor(x-hr)-minX-1, 0)
	x1 := min(floor(x+hr)-minX, 15)
	y0 := max(floor(y-vr)-1, c.minGenY+1)
	y1 := min(floor(y+vr)+1, c.minGenY+c.genDepth-1-7)
	z0 := max(floor(z-hr)-minZ-1, 0)
	z1 := min(floor(z+hr)-minZ, 15)
	for lx := x0; lx <= x1; lx++ {
		dx := (float64(minX+lx) + 0.5 - x) / hr
		for lz := z0; lz <= z1; lz++ {
			dz := (float64(minZ+lz) + 0.5 - z) / hr
			if float64(dx*dx)+float64(dz*dz) >= 1 {
				continue
			}
			surface := false
			for by := y1; by > y0; by-- {
				dy := (float64(by) - 0.5 - y) / vr
				if skip(dx, dy, dz, by) {
					continue
				}
				i := c.maskIndex(lx, by, lz)
				if c.mask[i>>6]&(1<<(i&63)) != 0 {
					continue
				}
				c.mask[i>>6] |= 1 << (i & 63)
				if cfg.Kind == carverNetherCave {
					c.carveNetherBlock(cfg, lx, by, lz)
				} else {
					c.carveBlock(cfg, lx, by, lz, &surface)
				}
			}
		}
	}
}

// carveBlock replaces one replaceable block with what the aquifer holds
// there (lava at and below the lava level). Once a column has cut
// through grass or mycelium, dirt left exposed below gets the biome's
// top material.
func (c *carving) carveBlock(cfg *CarverConfig, lx, y, lz int32, surface *bool) bool {
	st := c.chunk.Get(lx, y, lz)
	if b := blockOf(st); b == "grass_block" || b == "mycelium" {
		*surface = true
	}
	if !c.canReplace(cfg, st) {
		return false
	}
	x, z := c.chunk.CX*16+lx, c.chunk.CZ*16+lz
	carved, ok := c.carveState(cfg, x, y, z)
	if !ok {
		return false
	}
	c.chunk.set(lx, y, lz, carved)
	if *surface && c.chunk.Get(lx, y-1, lz) == stDirt {
		if top, ok := c.topMaterial(x, y-1, z, isFluidState(carved)); ok {
			c.chunk.set(lx, y-1, lz, top)
		}
	}
	return true
}

// carveNetherBlock cuts nether caves: cave air, lava up to 31 above the
// bottom.
func (c *carving) carveNetherBlock(cfg *CarverConfig, lx, y, lz int32) bool {
	if !c.canReplace(cfg, c.chunk.Get(lx, y, lz)) {
		return false
	}
	if y <= c.minGenY+31 {
		c.chunk.set(lx, y, lz, stLava)
	} else {
		c.chunk.set(lx, y, lz, stCaveAir)
	}
	return true
}

func (c *carving) carveState(cfg *CarverConfig, x, y, z int32) (StateID, bool) {
	if y <= cfg.LavaLevel.resolve(c.minGenY, c.genDepth) {
		return stLava, true
	}
	var b NoiseBlock
	if c.f.aquifers {
		var placed bool
		if b, placed, _ = c.f.aq.substance(Point{x, y, z}, 0); !placed {
			return 0, false
		}
	} else {
		b = c.f.globalFluid(y).at(y)
	}
	switch b {
	case NBWater:
		return stWater, true
	case NBLava:
		return stLava, true
	}
	return stAir, true
}

// topMaterial is what the surface rules put on a fresh top block: the
// block one below the surface, with water above it when the carved
// block holds a fluid.
func (c *carving) topMaterial(x, y, z int32, fluid bool) (StateID, bool) {
	ctx := &surfaceContext{ss: c.f.surface(), f: c.f, chunk: c.chunk, biome: c.biome,
		noise2D: map[string]float64{}, noise3D: map[string]float64{}}
	ctx.updateXZ(x, z)
	water := int32(math.MinInt32)
	if fluid {
		water = y + 1
	}
	ctx.updateY(1, 1, water, y)
	return ctx.apply(c.f.rs.Settings.Surface)
}

// cave runs a cave carver (or the nether's) started in chunk (sx, sz):
// up to a skewed count of systems, each an optional room and a few
// tunnels.
func (c *carving) cave(cfg *CarverConfig, r *Legacy, sx, sz int32) {
	const maxDistance = (4*2 - 1) * 16
	bound, yScale := int32(15), 1.0
	if cfg.Kind == carverNetherCave {
		bound, yScale = 10, 5.0
	}
	count := r.NextIntn(r.NextIntn(r.NextIntn(bound)+1) + 1)
	for i := int32(0); i < count; i++ {
		x := float64(sx*16 + r.NextIntn(16))
		y := float64(cfg.Y.sample(r, c.minGenY, c.genDepth))
		z := float64(sz*16 + r.NextIntn(16))
		hMul := float64(cfg.HorizontalRadius.sample(r))
		vMul := float64(cfg.VerticalRadius.sample(r))
		floorLevel := float64(cfg.FloorLevel.sample(r))
		skip := func(dx, dy, dz float64, _ int32) bool {
			return dy <= floorLevel || float64(dx*dx)+float64(dy*dy)+float64(dz*dz) >= 1
		}
		tunnels := int32(1)
		if r.NextIntn(4) == 0 {
			roomScale := float64(cfg.YScale.sample(r))
			thick := 1 + float32(r.NextFloat()*6)
			hr := 1.5 + float64(mthSin(1.5707963705062866)*thick)
			c.carveEllipsoid(cfg, x+1, y, z, hr, hr*roomScale, skip)
			tunnels += r.NextIntn(4)
		}
		for j := int32(0); j < tunnels; j++ {
			yaw := float32(r.NextFloat() * 6.2831855)
			pitch := (r.NextFloat() - 0.5) / 4
			thickness := c.caveThickness(cfg, r)
			branches := maxDistance - r.NextIntn(maxDistance/4)
			c.tunnel(cfg, r.NextLong(), x, y, z, hMul, vMul, thickness, yaw, pitch, 0, branches, yScale, skip)
		}
	}
}

func (c *carving) caveThickness(cfg *CarverConfig, r *Legacy) float32 {
	t := float32(r.NextFloat()*2) + r.NextFloat()
	if cfg.Kind == carverNetherCave {
		return t * 2
	}
	if r.NextIntn(10) == 0 {
		t *= float32(float32(r.NextFloat()*r.NextFloat())*3) + 1
	}
	return t
}

// tunnel walks a cave tunnel from (x, y, z), turning a little each step
// and splitting once into two branches when thick enough.
func (c *carving) tunnel(cfg *CarverConfig, seed int64, x, y, z, hMul, vMul float64, thickness, yaw, pitch float32,
	branch, branches int32, yScale float64, skip func(dx, dy, dz float64, y int32) bool) {
	r := NewLegacy(seed)
	split := r.NextIntn(branches/2) + branches/4
	steep := r.NextIntn(6) == 0
	var yawTurn, pitchTurn float32
	for i := branch; i < branches; i++ {
		hr := 1.5 + float64(mthSin(float64(float32(float32(3.1415927*float32(i))/float32(branches))))*thickness)
		vr := hr * yScale
		cosPitch := mthCos(float64(pitch))
		x += float64(mthCos(float64(yaw)) * cosPitch)
		y += float64(mthSin(float64(pitch)))
		z += float64(mthSin(float64(yaw)) * cosPitch)
		if steep {
			pitch *= 0.92
		} else {
			pitch *= 0.7
		}
		pitch += float32(pitchTurn * 0.1)
		yaw += float32(yawTurn * 0.1)
		pitchTurn *= 0.9
		yawTurn *= 0.75
		pitchTurn += float32(float32((r.NextFloat()-r.NextFloat())*r.NextFloat()) * 2)
		yawTurn += float32(float32((r.NextFloat()-r.NextFloat())*r.NextFloat()) * 4)
		if i == split && thickness > 1 {
			c.tunnel(cfg, r.NextLong(), x, y, z, hMul, vMul, float32(r.NextFloat()*0.5)+0.5, yaw-1.5707964, pitch/3, i, branches, 1, skip)
			c.tunnel(cfg, r.NextLong(), x, y, z, hMul, vMul, float32(r.NextFloat()*0.5)+0.5, yaw+1.5707964, pitch/3, i, branches, 1, skip)
			return
		}
		if r.NextIntn(4) == 0 {
			continue
		}
		if !c.canReach(x, z, i, branches, thickness) {
			return
		}
		c.carveEllipsoid(cfg, x, y, z, hr*hMul, vr*vMul, skip)
	}
}

// canyon runs a canyon carver started in chunk (sx, sz): one long,
// tall, slowly turning cut whose width varies by layer.
func (c *carving) canyon(cfg *CarverConfig, r *Legacy, sx, sz int32) {
	const maxDistance = (4*2 - 1) * 16
	x := float64(sx*16 + r.NextIntn(16))
	y := cfg.Y.sample(r, c.minGenY, c.genDepth)
	z := float64(sz*16 + r.NextIntn(16))
	yaw := float32(r.NextFloat() * 6.2831855)
	pitch := cfg.VerticalRotation.sample(r)
	yScale := float64(cfg.YScale.sample(r))
	thickness := cfg.Thickness.sample(r)
	branches := int32(float32(maxDistance * cfg.DistanceFactor.sample(r)))
	c.canyonWalk(cfg, r.NextLong(), x, float64(y), z, thickness, yaw, pitch, 0, branches, yScale)
}

func (c *carving) canyonWalk(cfg *CarverConfig, seed int64, x, y, z float64, thickness, yaw, pitch float32,
	branch, branches int32, yScale float64) {
	r := NewLegacy(seed)
	widths := make([]float32, c.genDepth)
	w := float32(1)
	for i := range widths {
		if i == 0 || r.NextIntn(cfg.WidthSmoothness) == 0 {
			w = 1 + float32(r.NextFloat()*r.NextFloat())
		}
		widths[i] = w * w
	}
	skip := func(dx, dy, dz float64, by int32) bool {
		return float64(float64(float64(dx*dx)+float64(dz*dz))*float64(widths[by-c.minGenY-1]))+float64(dy*dy)/6 >= 1
	}
	var yawTurn, pitchTurn float32
	for i := branch; i < branches; i++ {
		hr := 1.5 + float64(mthSin(float64(float32(float32(float32(i)*3.1415927)/float32(branches))))*thickness)
		vr := hr * yScale
		hr *= float64(cfg.HorizontalRadiusFactor.sample(r))
		t := 1 - float32(float32(math.Abs(float64(0.5-float32(i)/float32(branches))))*2)
		factor := cfg.VerticalRadiusDefaultFactor + float32(cfg.VerticalRadiusCenterFactor*t)
		vr = float64(float64(factor)*vr) * float64(randomBetween(r, 0.75, 1))
		cosPitch, sinPitch := mthCos(float64(pitch)), mthSin(float64(pitch))
		x += float64(mthCos(float64(yaw)) * cosPitch)
		y += float64(sinPitch)
		z += float64(mthSin(float64(yaw)) * cosPitch)
		pitch *= 0.7
		pitch += float32(pitchTurn * 0.05)
		yaw += float32(yawTurn * 0.05)
		pitchTurn *= 0.8
		yawTurn *= 0.5
		pitchTurn += float32(float32((r.NextFloat()-r.NextFloat())*r.NextFloat()) * 2)
		yawTurn += float32(float32((r.NextFloat()-r.NextFloat())*r.NextFloat()) * 4)
		if r.NextIntn(4) == 0 {
			continue
		}
		if !c.canReach(x, z, i, branches, thickness) {
			return
		}
		c.carveEllipsoid(cfg, x, y, z, hr, vr, skip)
	}
}
