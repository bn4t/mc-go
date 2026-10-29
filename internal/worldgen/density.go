package worldgen

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Ctx is a density-function sampling point. nc is the noise chunk whose
// cache/interpolation wrappers are in force (nil for an unwrapped,
// single-point evaluation); inChunk marks the chunk's own context — the
// position vanilla's NoiseChunk reports while it is filling cells and
// slices — as opposed to a SinglePointContext built inside a wrapped
// graph (flat-cache fill, find_top_surface probes).
type Ctx struct {
	X, Y, Z int32
	nc      *NoiseChunk
	inChunk bool
}

// DF is one density-function node.
type DF interface {
	Compute(c *Ctx) float64
}

type constDF float64

func (d constDF) Compute(*Ctx) float64 { return float64(d) }

type binOp int

const (
	opAdd binOp = iota
	opMul
	opMin
	opMax
)

type binDF struct {
	op   binOp
	a, b DF
}

func (d *binDF) Compute(c *Ctx) float64 {
	x := d.a.Compute(c)
	switch d.op {
	case opAdd:
		return x + d.b.Compute(c)
	case opMul:
		if x == 0 {
			return 0
		}
		return float64(x * d.b.Compute(c))
	case opMin:
		return math.Min(x, d.b.Compute(c))
	default:
		return math.Max(x, d.b.Compute(c))
	}
}

type mapOp int

const (
	mapAbs mapOp = iota
	mapSquare
	mapCube
	mapHalfNeg
	mapQuarterNeg
	mapInvert
	mapSqueeze
)

type mappedDF struct {
	op mapOp
	a  DF
}

func (d *mappedDF) Compute(c *Ctx) float64 {
	v := d.a.Compute(c)
	switch d.op {
	case mapAbs:
		return math.Abs(v)
	case mapSquare:
		return float64(v * v)
	case mapCube:
		return float64(float64(v*v) * v)
	case mapHalfNeg:
		if v > 0 {
			return v
		}
		return float64(v * 0.5)
	case mapQuarterNeg:
		if v > 0 {
			return v
		}
		return float64(v * 0.25)
	case mapInvert:
		return 1 / v
	default:
		e := clampD(v, -1, 1)
		return e/2 - float64(float64(e*e)*e)/24
	}
}

type clampDF struct {
	in     DF
	lo, hi float64
}

func (d *clampDF) Compute(c *Ctx) float64 { return clampD(d.in.Compute(c), d.lo, d.hi) }

type yGradDF struct {
	fromY, toY       int32
	fromVal, toValue float64
}

func (d *yGradDF) Compute(c *Ctx) float64 {
	return clampedMap(float64(c.Y), float64(d.fromY), float64(d.toY), d.fromVal, d.toValue)
}

type noiseDF struct {
	n               *NormalNoise
	xzScale, yScale float64
}

func (d *noiseDF) Compute(c *Ctx) float64 {
	return d.n.Value(float64(float64(c.X)*d.xzScale), float64(float64(c.Y)*d.yScale), float64(float64(c.Z)*d.xzScale))
}

type shiftedNoiseDF struct {
	n               *NormalNoise
	xzScale, yScale float64
	sx, sy, sz      DF
}

func (d *shiftedNoiseDF) Compute(c *Ctx) float64 {
	x := float64(float64(c.X)*d.xzScale) + d.sx.Compute(c)
	y := float64(float64(c.Y)*d.yScale) + d.sy.Compute(c)
	z := float64(float64(c.Z)*d.xzScale) + d.sz.Compute(c)
	return d.n.Value(x, y, z)
}

type shiftKind int

const (
	shiftA shiftKind = iota
	shiftB
	shiftXYZ
)

type shiftDF struct {
	kind shiftKind
	n    *NormalNoise
}

func (d *shiftDF) Compute(c *Ctx) float64 {
	x, y, z := float64(c.X), float64(c.Y), float64(c.Z)
	switch d.kind {
	case shiftA:
		return float64(d.n.Value(float64(x*0.25), 0, float64(z*0.25)) * 4)
	case shiftB:
		return float64(d.n.Value(float64(z*0.25), float64(x*0.25), 0) * 4)
	default:
		return float64(d.n.Value(float64(x*0.25), float64(y*0.25), float64(z*0.25)) * 4)
	}
}

type rangeChoiceDF struct {
	in       DF
	lo, hi   float64
	inR, out DF
}

func (d *rangeChoiceDF) Compute(c *Ctx) float64 {
	v := d.in.Compute(c)
	if v >= d.lo && v < d.hi {
		return d.inR.Compute(c)
	}
	return d.out.Compute(c)
}

type intervalSelectDF struct {
	in         DF
	thresholds []float64
	fns        []DF
}

func (d *intervalSelectDF) Compute(c *Ctx) float64 {
	v := d.in.Compute(c)
	for i, t := range d.thresholds {
		if v < t {
			return d.fns[i].Compute(c)
		}
	}
	return d.fns[len(d.fns)-1].Compute(c)
}

type blendedDF struct{ b *BlendedNoise }

func (d *blendedDF) Compute(c *Ctx) float64 { return d.b.Compute(c.X, c.Y, c.Z) }

type findTopSurfaceDF struct {
	density, upper DF
	lower, cellH   int32
}

func (d *findTopSurfaceDF) Compute(c *Ctx) float64 {
	top := floorI(d.upper.Compute(c)/float64(d.cellH)) * d.cellH
	if top <= d.lower {
		return float64(d.lower)
	}
	p := Ctx{X: c.X, Z: c.Z, nc: c.nc}
	for y := top; y >= d.lower; y -= d.cellH {
		p.Y = y
		if d.density.Compute(&p) > 0 {
			return float64(y)
		}
	}
	return float64(d.lower)
}

// endIslandsDF is DensityFunctions.EndIslandDensityFunction.
type endIslandsDF struct{ n *SimplexNoise }

func newEndIslands(seed int64) *endIslandsDF {
	r := NewLegacy(seed)
	r.Consume(17292)
	return &endIslandsDF{n: NewSimplexNoise(r)}
}

func (d *endIslandsDF) Compute(c *Ctx) float64 {
	return (float64(d.heightValue(c.X/8, c.Z/8)) - 8) / 128
}

func (d *endIslandsDF) heightValue(x, z int32) float32 {
	i := x / 2
	j := z / 2
	k := x % 2
	l := z % 2
	f := 100 - float32(float32(math.Sqrt(float64(float32(x*x+z*z))))*8)
	f = clampF(f, -100, 80)
	for m := int32(-12); m <= 12; m++ {
		for n := int32(-12); n <= 12; n++ {
			o := int64(i + m)
			p := int64(j + n)
			if o*o+p*p > 4096 && d.n.Value2D(float64(o), float64(p)) < -0.8999999761581421 {
				g := float32(float32(abs32(float32(o))*3439) + float32(abs32(float32(p))*147))
				g = float32(math.Mod(float64(g), 13)) + 9
				h := float32(k - m*2)
				q := float32(l - n*2)
				r := 100 - float32(float32(math.Sqrt(float64(float32(h*h)+float32(q*q))))*g)
				r = clampF(r, -100, 80)
				f = max(f, r)
			}
		}
	}
	return f
}

func abs32(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func clampF(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

type splinePoint struct {
	loc, deriv float32
	val        spline
}

type spline interface {
	sample(c *Ctx) float32
}

type splineConst float32

func (s splineConst) sample(*Ctx) float32 { return float32(s) }

type splineMulti struct {
	coord  DF
	locs   []float32
	vals   []spline
	derivs []float32
}

func linearExtend(f float32, locs []float32, v float32, derivs []float32, i int) float32 {
	g := derivs[i]
	if g == 0 {
		return v
	}
	return v + float32(g*float32(f-locs[i]))
}

func (s *splineMulti) sample(c *Ctx) float32 {
	f := float32(s.coord.Compute(c))
	// largest i with locs[i] <= f, or -1 (Mth.binarySearch for f < loc).
	lo, n := 0, len(s.locs)
	for n > 0 {
		h := n / 2
		if f < s.locs[lo+h] {
			n = h
		} else {
			lo += h + 1
			n -= h + 1
		}
	}
	i := lo - 1
	last := len(s.locs) - 1
	if i < 0 {
		return linearExtend(f, s.locs, s.vals[0].sample(c), s.derivs, 0)
	}
	if i == last {
		return linearExtend(f, s.locs, s.vals[last].sample(c), s.derivs, last)
	}
	g := s.locs[i]
	h := s.locs[i+1]
	k := (f - g) / (h - g)
	l := s.vals[i].sample(c)
	m := s.vals[i+1].sample(c)
	n0 := s.derivs[i]
	o := s.derivs[i+1]
	p := float32(n0*float32(h-g)) - (m - l)
	q := float32(-o*float32(h-g)) + (m - l)
	lerpF := func(t, a, b float32) float32 { return a + float32(t*(b-a)) }
	return lerpF(k, l, m) + float32(float32(k*float32(1-k))*lerpF(k, p, q))
}

type splineDF struct{ s spline }

func (d *splineDF) Compute(c *Ctx) float64 { return float64(d.s.sample(c)) }

// markerKind enumerates the NoiseChunk wrapper markers.
type markerKind int

const (
	mkInterpolated markerKind = iota
	mkFlatCache
	mkCache2D
	mkCacheOnce
	mkCacheAllInCell
)

// markerDF passes through outside a noise chunk; inside one it defers
// to the chunk's per-marker state (see NoiseChunk.markerCompute).
type markerDF struct {
	kind markerKind
	id   int
	arg  DF
}

func (d *markerDF) Compute(c *Ctx) float64 {
	if c.nc == nil {
		return d.arg.Compute(c)
	}
	return c.nc.markerCompute(d, c)
}

// blendDensity, blendAlpha and blendOffset have no blending data in a
// freshly generated world: identity, 1 and 0.
type beardifierDF struct{}

func (beardifierDF) Compute(c *Ctx) float64 {
	if c.nc != nil && c.nc.beard != nil {
		return c.nc.beard(c.X, c.Y, c.Z)
	}
	return 0
}

// Router is the noise router after seeding.
type Router struct {
	Barrier, FluidFloodedness, FluidSpread, Lava        DF
	Temperature, Vegetation, Continents, Erosion, Depth DF
	Ridges, PreliminarySurfaceLevel, FinalDensity       DF
	VeinToggle, VeinRidged, VeinGap                     DF
}

// builder parses density functions for one seed.
type builder struct {
	data    Data
	random  PositionalRandom
	seed    int64
	legacy  bool
	named   map[string]DF
	noises  map[string]*NormalNoise
	markers []*markerDF
}

func (b *builder) noise(name string) *NormalNoise {
	name = qualify(name)
	if n, ok := b.noises[name]; ok {
		return n
	}
	raw, ok := b.data.Get("noise/" + strings.TrimPrefix(name, "minecraft:"))
	if !ok {
		panic("worldgen: unknown noise " + name)
	}
	var p NoiseParams
	if err := json.Unmarshal(raw, &p); err != nil {
		panic(err)
	}
	var n *NormalNoise
	switch name {
	case "minecraft:temperature_nether":
		n = NewNormalNoiseLegacyNether(NewLegacy(b.seed+0), p)
	case "minecraft:vegetation_nether":
		n = NewNormalNoiseLegacyNether(NewLegacy(b.seed+1), p)
	default:
		n = NewNormalNoise(b.random.FromHashOf(name), p)
	}
	b.noises[name] = n
	return n
}

func qualify(name string) string {
	if strings.Contains(name, ":") {
		return name
	}
	return "minecraft:" + name
}

func (b *builder) named_(name string) DF {
	name = qualify(name)
	if d, ok := b.named[name]; ok {
		return d
	}
	raw, ok := b.data.Get("density_function/" + strings.TrimPrefix(name, "minecraft:"))
	if !ok {
		panic("worldgen: unknown density function " + name)
	}
	d := b.parse(raw)
	b.named[name] = d
	return d
}

type obj map[string]json.RawMessage

func (o obj) f64(k string) float64 {
	v, err := strconv.ParseFloat(strings.TrimSpace(string(o[k])), 64)
	if err != nil {
		panic(fmt.Sprintf("worldgen: field %s: %v", k, err))
	}
	return v
}

func (o obj) i32(k string) int32 { return int32(o.f64(k)) }

func (o obj) str(k string) string {
	var s string
	if err := json.Unmarshal(o[k], &s); err != nil {
		panic(fmt.Sprintf("worldgen: field %s: %v", k, err))
	}
	return s
}

func parseF32(raw json.RawMessage) float32 {
	v, err := strconv.ParseFloat(strings.TrimSpace(string(raw)), 32)
	if err != nil {
		panic(err)
	}
	return float32(v)
}

func (b *builder) parse(raw json.RawMessage) DF {
	s := strings.TrimSpace(string(raw))
	switch {
	case s == "":
		panic("worldgen: empty density function")
	case s[0] == '"':
		var name string
		json.Unmarshal(raw, &name)
		return b.named_(name)
	case s[0] != '{':
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			panic(err)
		}
		return constDF(v)
	}
	var o obj
	if err := json.Unmarshal(raw, &o); err != nil {
		panic(err)
	}
	t := strings.TrimPrefix(o.str("type"), "minecraft:")
	switch t {
	case "constant":
		return constDF(o.f64("argument"))
	case "add", "mul", "min", "max":
		op := map[string]binOp{"add": opAdd, "mul": opMul, "min": opMin, "max": opMax}[t]
		return &binDF{op: op, a: b.parse(o["argument1"]), b: b.parse(o["argument2"])}
	case "abs", "square", "cube", "half_negative", "quarter_negative", "invert", "squeeze":
		op := map[string]mapOp{"abs": mapAbs, "square": mapSquare, "cube": mapCube, "half_negative": mapHalfNeg,
			"quarter_negative": mapQuarterNeg, "invert": mapInvert, "squeeze": mapSqueeze}[t]
		return &mappedDF{op: op, a: b.parse(o["argument"])}
	case "clamp":
		return &clampDF{in: b.parse(o["input"]), lo: o.f64("min"), hi: o.f64("max")}
	case "y_clamped_gradient":
		return &yGradDF{fromY: o.i32("from_y"), toY: o.i32("to_y"), fromVal: o.f64("from_value"), toValue: o.f64("to_value")}
	case "noise":
		return &noiseDF{n: b.noise(o.str("noise")), xzScale: o.f64("xz_scale"), yScale: o.f64("y_scale")}
	case "shifted_noise":
		return &shiftedNoiseDF{n: b.noise(o.str("noise")), xzScale: o.f64("xz_scale"), yScale: o.f64("y_scale"),
			sx: b.parse(o["shift_x"]), sy: b.parse(o["shift_y"]), sz: b.parse(o["shift_z"])}
	case "shift_a":
		return &shiftDF{kind: shiftA, n: b.noise(o.str("argument"))}
	case "shift_b":
		return &shiftDF{kind: shiftB, n: b.noise(o.str("argument"))}
	case "shift":
		return &shiftDF{kind: shiftXYZ, n: b.noise(o.str("argument"))}
	case "range_choice":
		return &rangeChoiceDF{in: b.parse(o["input"]), lo: o.f64("min_inclusive"), hi: o.f64("max_exclusive"),
			inR: b.parse(o["when_in_range"]), out: b.parse(o["when_out_of_range"])}
	case "interval_select":
		var th []json.RawMessage
		var fns []json.RawMessage
		json.Unmarshal(o["thresholds"], &th)
		json.Unmarshal(o["functions"], &fns)
		d := &intervalSelectDF{in: b.parse(o["input"])}
		for _, t := range th {
			v, _ := strconv.ParseFloat(strings.TrimSpace(string(t)), 64)
			d.thresholds = append(d.thresholds, v)
		}
		for _, f := range fns {
			d.fns = append(d.fns, b.parse(f))
		}
		return d
	case "spline":
		return &splineDF{s: b.spline(o["spline"])}
	case "interpolated", "flat_cache", "cache_2d", "cache_once", "cache_all_in_cell":
		k := map[string]markerKind{"interpolated": mkInterpolated, "flat_cache": mkFlatCache, "cache_2d": mkCache2D,
			"cache_once": mkCacheOnce, "cache_all_in_cell": mkCacheAllInCell}[t]
		return b.marker(k, b.parse(o["argument"]))
	case "blend_alpha":
		return constDF(1)
	case "blend_offset":
		return constDF(0)
	case "blend_density":
		return b.parse(o["argument"])
	case "beardifier":
		return beardifierDF{}
	case "old_blended_noise":
		var r Random
		if b.legacy {
			r = NewLegacy(b.seed)
		} else {
			r = b.random.FromHashOf("minecraft:terrain")
		}
		return &blendedDF{b: NewBlendedNoise(r, o.f64("xz_scale"), o.f64("y_scale"), o.f64("xz_factor"), o.f64("y_factor"), o.f64("smear_scale_multiplier"))}
	case "end_islands":
		return newEndIslands(b.seed)
	case "find_top_surface":
		return &findTopSurfaceDF{density: b.parse(o["density"]), upper: b.parse(o["upper_bound"]), lower: o.i32("lower_bound"), cellH: o.i32("cell_height")}
	}
	panic("worldgen: unsupported density function type " + t)
}

func (b *builder) marker(k markerKind, arg DF) DF {
	m := &markerDF{kind: k, id: len(b.markers), arg: arg}
	b.markers = append(b.markers, m)
	return m
}

func (b *builder) spline(raw json.RawMessage) spline {
	s := strings.TrimSpace(string(raw))
	if s[0] != '{' {
		return splineConst(parseF32(raw))
	}
	var o obj
	json.Unmarshal(raw, &o)
	m := &splineMulti{coord: b.parse(o["coordinate"])}
	var pts []obj
	json.Unmarshal(o["points"], &pts)
	for _, p := range pts {
		m.locs = append(m.locs, parseF32(p["location"]))
		m.derivs = append(m.derivs, parseF32(p["derivative"]))
		m.vals = append(m.vals, b.spline(p["value"]))
	}
	return m
}
