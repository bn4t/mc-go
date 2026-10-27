package worldgen

import (
	"fmt"
	"math"
)

// Point is a block position a density function is sampled at.
type Point struct{ X, Y, Z int32 }

// Func is a density function bound to a world seed. Min and Max bound
// every value Compute can return; min/max nodes use them to skip an
// argument whose range cannot affect the result.
type Func interface {
	Compute(p Point) float64
	Min() float64
	Max() float64
}

// Kind names a density function type.
type Kind uint8

const (
	KConst Kind = iota
	KRef
	KAdd
	KMul
	KMin
	KMax
	KAbs
	KSquare
	KCube
	KHalfNegative
	KQuarterNegative
	KInvert
	KSqueeze
	KClamp
	KRangeChoice
	KYGradient
	KNoise
	KShiftedNoise
	KShiftA
	KShiftB
	KShift
	KIntervalSelect
	KFindTopSurface
	KSpline
	KInterpolated
	KFlatCache
	KCache2D
	KCacheOnce
	KCacheAllInCell
	KBlendAlpha
	KBlendOffset
	KBlendDensity
	KBeardifier
	KEndIslands
	KOldBlendedNoise
)

// Spec is a seed-independent density function definition, as the
// worldgen datapack describes it. Which fields are used depends on Kind.
type Spec struct {
	Kind  Kind
	Value float64 // constant
	Ref   string  // reference to a registered function
	Args  []*Spec // operands; interval_select's candidates
	Noise string  // noise identifier
	XZ, Y float64 // noise input scales
	// Nums carries the remaining numbers: clamp [min, max]; range_choice
	// [minInclusive, maxExclusive]; y gradient [fromY, toY, fromValue,
	// toValue]; interval_select thresholds; find_top_surface [lowerBound,
	// cellHeight]; old blended noise [xzScale, yScale, xzFactor, yFactor,
	// smear].
	Nums   []float64
	Spline *SplineSpec
}

// SplineSpec is a cubic spline over a coordinate function, or a constant
// when Coord is nil.
type SplineSpec struct {
	Coord  *Spec
	Const  float32
	Points []SplinePoint
}

// SplinePoint is one knot: location, value (itself a spline) and slope.
type SplinePoint struct {
	Loc   float32
	Value *SplineSpec
	Deriv float32
}

// Constructors the generated tables are written with.

func dfConst(v float64) *Spec              { return &Spec{Kind: KConst, Value: v} }
func dfRef(id string) *Spec                { return &Spec{Kind: KRef, Ref: id} }
func dfOp(k Kind, args ...*Spec) *Spec     { return &Spec{Kind: k, Args: args} }
func dfLeaf(k Kind) *Spec                  { return &Spec{Kind: k} }
func dfNum(k Kind, nums ...float64) *Spec  { return &Spec{Kind: k, Nums: nums} }
func dfShiftNoise(k Kind, id string) *Spec { return &Spec{Kind: k, Noise: id} }
func dfNoise(id string, xz, y float64) *Spec {
	return &Spec{Kind: KNoise, Noise: id, XZ: xz, Y: y}
}
func dfShifted(id string, sx, sy, sz *Spec, xz, y float64) *Spec {
	return &Spec{Kind: KShiftedNoise, Noise: id, Args: []*Spec{sx, sy, sz}, XZ: xz, Y: y}
}
func dfClamp(in *Spec, lo, hi float64) *Spec {
	return &Spec{Kind: KClamp, Args: []*Spec{in}, Nums: []float64{lo, hi}}
}
func dfRange(in *Spec, lo, hi float64, inside, outside *Spec) *Spec {
	return &Spec{Kind: KRangeChoice, Args: []*Spec{in, inside, outside}, Nums: []float64{lo, hi}}
}
func dfInterval(in *Spec, thresholds []float64, fns ...*Spec) *Spec {
	return &Spec{Kind: KIntervalSelect, Args: append([]*Spec{in}, fns...), Nums: thresholds}
}
func dfTopSurface(density, upper *Spec, lower, cell float64) *Spec {
	return &Spec{Kind: KFindTopSurface, Args: []*Spec{density, upper}, Nums: []float64{lower, cell}}
}
func dfSpline(s *SplineSpec) *Spec { return &Spec{Kind: KSpline, Spline: s} }
func spl(coord *Spec, pts ...SplinePoint) *SplineSpec {
	return &SplineSpec{Coord: coord, Points: pts}
}
func splConst(v float32) *SplineSpec { return &SplineSpec{Const: v} }
func knot(loc float32, v *SplineSpec, d float32) SplinePoint {
	return SplinePoint{Loc: loc, Value: v, Deriv: d}
}

// NoiseSource hands out a world's normal noises by identifier.
type NoiseSource interface {
	Noise(id string) *Normal
}

// binder instantiates specs for one seed, sharing each referenced
// function and each noise between all its uses.
type binder struct {
	noises NoiseSource
	refs   map[string]*Spec
	bound  map[string]Func
	seed   int64
	// legacy seeds the blended terrain noise from the world seed directly;
	// otherwise it comes from root's "minecraft:terrain" hash.
	legacy bool
	root   Positional
}

func (b *binder) bind(s *Spec) Func {
	switch s.Kind {
	case KConst:
		return constant(s.Value)
	case KRef:
		if f, ok := b.bound[s.Ref]; ok {
			return f
		}
		def, ok := b.refs[s.Ref]
		if !ok {
			panic(fmt.Sprintf("worldgen: unknown density function %s", s.Ref))
		}
		f := b.bind(def)
		b.bound[s.Ref] = f
		return f
	case KAdd, KMul, KMin, KMax:
		return newBinary(s.Kind, b.bind(s.Args[0]), b.bind(s.Args[1]))
	case KAbs, KSquare, KCube, KHalfNegative, KQuarterNegative, KInvert, KSqueeze:
		return newMapped(s.Kind, b.bind(s.Args[0]))
	case KClamp:
		return &clampFn{in: b.bind(s.Args[0]), lo: s.Nums[0], hi: s.Nums[1]}
	case KRangeChoice:
		return &rangeChoice{in: b.bind(s.Args[0]), lo: s.Nums[0], hi: s.Nums[1],
			inside: b.bind(s.Args[1]), outside: b.bind(s.Args[2])}
	case KYGradient:
		return &yGradient{fromY: s.Nums[0], toY: s.Nums[1], from: s.Nums[2], to: s.Nums[3]}
	case KNoise:
		return &noiseFn{n: b.noises.Noise(s.Noise), xz: s.XZ, y: s.Y}
	case KShiftedNoise:
		return &shiftedNoise{n: b.noises.Noise(s.Noise), sx: b.bind(s.Args[0]), sy: b.bind(s.Args[1]),
			sz: b.bind(s.Args[2]), xz: s.XZ, y: s.Y}
	case KShiftA, KShiftB, KShift:
		return &shiftFn{kind: s.Kind, n: b.noises.Noise(s.Noise)}
	case KIntervalSelect:
		fns := make([]Func, len(s.Args)-1)
		for i, a := range s.Args[1:] {
			fns[i] = b.bind(a)
		}
		return newIntervalSelect(b.bind(s.Args[0]), s.Nums, fns)
	case KFindTopSurface:
		return &findTopSurface{density: b.bind(s.Args[0]), upper: b.bind(s.Args[1]),
			lower: int32(s.Nums[0]), cell: int32(s.Nums[1])}
	case KSpline:
		sp := b.bindSpline(s.Spline)
		return &splineFn{s: sp}
	case KInterpolated, KFlatCache, KCache2D, KCacheOnce, KCacheAllInCell:
		return &marker{kind: s.Kind, in: b.bind(s.Args[0])}
	case KBlendAlpha:
		return constant(1)
	case KBlendOffset, KBeardifier:
		return constant(0)
	case KBlendDensity:
		return b.bind(s.Args[0])
	case KOldBlendedNoise:
		var r Random
		if b.legacy {
			r = NewLegacy(b.seed)
		} else {
			r = b.root.FromHash("minecraft:terrain")
		}
		return newBlendedNoise(r, s.Nums[0], s.Nums[1], s.Nums[2], s.Nums[3], s.Nums[4])
	}
	panic(fmt.Sprintf("worldgen: density function kind %d not supported", s.Kind))
}

// constant is a fixed value.
type constant float64

func (c constant) Compute(Point) float64 { return float64(c) }
func (c constant) Min() float64          { return float64(c) }
func (c constant) Max() float64          { return float64(c) }

// binaryFn is add, mul, min or max. A constant operand turns add and mul
// into a plain offset or scale of the other operand; otherwise mul skips
// its second operand when the first is zero, and min/max skip it when
// its bounds cannot change the result.
type binaryFn struct {
	kind     Kind
	a, b     Func
	lo, hi   float64
	constArg bool
}

func newBinary(k Kind, a, b Func) Func {
	f := &binaryFn{kind: k, a: a, b: b}
	amin, amax, bmin, bmax := a.Min(), a.Max(), b.Min(), b.Max()
	switch k {
	case KAdd:
		f.lo, f.hi = amin+bmin, amax+bmax
	case KMul:
		products := [4]float64{amin * bmin, amin * bmax, amax * bmin, amax * bmax}
		f.lo, f.hi = products[0], products[0]
		for _, p := range products[1:] {
			f.lo, f.hi = math.Min(f.lo, p), math.Max(f.hi, p)
		}
	case KMin:
		f.lo, f.hi = math.Min(amin, bmin), math.Min(amax, bmax)
	case KMax:
		f.lo, f.hi = math.Max(amin, bmin), math.Max(amax, bmax)
	}
	if math.IsNaN(f.lo) {
		f.lo = math.Inf(-1)
	}
	if math.IsNaN(f.hi) {
		f.hi = math.Inf(1)
	}
	if k == KAdd || k == KMul {
		if c, ok := a.(constant); ok {
			f.a, f.b, f.constArg = b, c, true
		} else if _, ok := b.(constant); ok {
			f.constArg = true
		}
	}
	return f
}

func (f *binaryFn) Compute(p Point) float64 {
	if f.constArg {
		c := float64(f.b.(constant))
		if f.kind == KAdd {
			return f.a.Compute(p) + c
		}
		return f.a.Compute(p) * c
	}
	v := f.a.Compute(p)
	switch f.kind {
	case KAdd:
		return v + f.b.Compute(p)
	case KMul:
		if v == 0 {
			return 0
		}
		return v * f.b.Compute(p)
	case KMin:
		if v < f.b.Min() {
			return v
		}
		return math.Min(v, f.b.Compute(p))
	default:
		if v > f.b.Max() {
			return v
		}
		return math.Max(v, f.b.Compute(p))
	}
}

func (f *binaryFn) Min() float64 { return f.lo }
func (f *binaryFn) Max() float64 { return f.hi }

// mapped applies a pointwise shape to its input.
type mapped struct {
	kind   Kind
	in     Func
	lo, hi float64
}

func newMapped(k Kind, in Func) Func {
	f := &mapped{kind: k, in: in}
	lo, hi := in.Min(), in.Max()
	a, b := transform(k, lo), transform(k, hi)
	switch k {
	case KAbs, KSquare:
		switch {
		case lo >= 0:
			f.lo, f.hi = a, b
		case hi <= 0:
			f.lo, f.hi = b, a
		default:
			f.lo, f.hi = 0, math.Max(a, b)
		}
	case KInvert:
		if lo < 0 && hi > 0 {
			f.lo, f.hi = math.Inf(-1), math.Inf(1)
		} else {
			f.lo, f.hi = math.Min(a, b), math.Max(a, b)
		}
	default: // monotone non-decreasing
		f.lo, f.hi = a, b
	}
	if math.IsNaN(f.lo) {
		f.lo = math.Inf(-1)
	}
	if math.IsNaN(f.hi) {
		f.hi = math.Inf(1)
	}
	return f
}

func transform(k Kind, v float64) float64 {
	switch k {
	case KAbs:
		return math.Abs(v)
	case KSquare:
		return v * v
	case KCube:
		return v * v * v
	case KHalfNegative:
		if v > 0 {
			return v
		}
		return v * 0.5
	case KQuarterNegative:
		if v > 0 {
			return v
		}
		return v * 0.25
	case KInvert:
		return 1 / v
	case KSqueeze:
		c := clampD(v, -1, 1)
		return c/2 - c*c*c/24
	}
	panic("worldgen: not a mapped kind")
}

func (f *mapped) Compute(p Point) float64 { return transform(f.kind, f.in.Compute(p)) }
func (f *mapped) Min() float64            { return f.lo }
func (f *mapped) Max() float64            { return f.hi }

// clampD is "below lo gives lo, else the smaller of v and hi".
func clampD(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	return math.Min(v, hi)
}

type clampFn struct {
	in     Func
	lo, hi float64
}

func (f *clampFn) Compute(p Point) float64 { return clampD(f.in.Compute(p), f.lo, f.hi) }
func (f *clampFn) Min() float64            { return f.lo }
func (f *clampFn) Max() float64            { return f.hi }

type rangeChoice struct {
	in              Func
	lo, hi          float64
	inside, outside Func
}

func (f *rangeChoice) Compute(p Point) float64 {
	v := f.in.Compute(p)
	if v >= f.lo && v < f.hi {
		return f.inside.Compute(p)
	}
	return f.outside.Compute(p)
}
func (f *rangeChoice) Min() float64 { return math.Min(f.inside.Min(), f.outside.Min()) }
func (f *rangeChoice) Max() float64 { return math.Max(f.inside.Max(), f.outside.Max()) }

type yGradient struct{ fromY, toY, from, to float64 }

func (f *yGradient) Compute(p Point) float64 {
	return clampedMap(float64(p.Y), f.fromY, f.toY, f.from, f.to)
}
func (f *yGradient) Min() float64 { return math.Min(f.from, f.to) }
func (f *yGradient) Max() float64 { return math.Max(f.from, f.to) }

// noiseFn samples a normal noise at the block position, horizontal and
// vertical axes scaled separately. A missing noise reads zero.
type noiseFn struct {
	n     *Normal
	xz, y float64
}

func (f *noiseFn) Compute(p Point) float64 {
	if f.n == nil {
		return 0
	}
	return f.n.Value(float64(p.X)*f.xz, float64(p.Y)*f.y, float64(p.Z)*f.xz)
}
func (f *noiseFn) Min() float64 { return -f.Max() }
func (f *noiseFn) Max() float64 { return noiseMax(f.n) }

func noiseMax(n *Normal) float64 {
	if n == nil {
		return 2
	}
	return n.MaxValue()
}

type shiftedNoise struct {
	n          *Normal
	sx, sy, sz Func
	xz, y      float64
}

func (f *shiftedNoise) Compute(p Point) float64 {
	x := float64(float64(p.X)*f.xz) + f.sx.Compute(p)
	y := float64(float64(p.Y)*f.y) + f.sy.Compute(p)
	z := float64(float64(p.Z)*f.xz) + f.sz.Compute(p)
	if f.n == nil {
		return 0
	}
	return f.n.Value(x, y, z)
}
func (f *shiftedNoise) Min() float64 { return -f.Max() }
func (f *shiftedNoise) Max() float64 { return noiseMax(f.n) }

// shiftFn offsets coordinates by a noise sampled at quarter resolution
// and scaled by four: shift_a reads (x, 0, z), shift_b (z, x, 0), shift
// (x, y, z).
type shiftFn struct {
	kind Kind
	n    *Normal
}

func (f *shiftFn) Compute(p Point) float64 {
	x, y, z := float64(p.X), float64(p.Y), float64(p.Z)
	switch f.kind {
	case KShiftA:
		y = 0
	case KShiftB:
		x, y, z = z, x, 0
	}
	if f.n == nil {
		return 0
	}
	return f.n.Value(x*0.25, y*0.25, z*0.25) * 4
}
func (f *shiftFn) Min() float64 { return -f.Max() }
func (f *shiftFn) Max() float64 { return noiseMax(f.n) * 4 }

// intervalSelect picks the candidate for the first threshold the input
// is below, the last candidate when it is below none.
type intervalSelect struct {
	in         Func
	thresholds []float64
	fns        []Func
	lo, hi     float64
}

func newIntervalSelect(in Func, thresholds []float64, fns []Func) Func {
	f := &intervalSelect{in: in, thresholds: thresholds, fns: fns, lo: math.MaxFloat64, hi: -math.MaxFloat64}
	for _, c := range fns {
		f.lo, f.hi = math.Min(f.lo, c.Min()), math.Max(f.hi, c.Max())
	}
	return f
}

func (f *intervalSelect) Compute(p Point) float64 {
	v := f.in.Compute(p)
	for i, t := range f.thresholds {
		if v < t {
			return f.fns[i].Compute(p)
		}
	}
	return f.fns[len(f.fns)-1].Compute(p)
}
func (f *intervalSelect) Min() float64 { return f.lo }
func (f *intervalSelect) Max() float64 { return f.hi }

// findTopSurface walks down from the upper bound (rounded down to a
// cell boundary) in cell steps and reports the first Y whose density is
// positive, or the lower bound.
type findTopSurface struct {
	density, upper Func
	lower, cell    int32
}

func (f *findTopSurface) Compute(p Point) float64 {
	start := floorInt(f.upper.Compute(p)/float64(f.cell)) * f.cell
	if start <= f.lower {
		return float64(f.lower)
	}
	for y := start; y >= f.lower; y -= f.cell {
		if f.density.Compute(Point{p.X, y, p.Z}) > 0 {
			return float64(y)
		}
	}
	return float64(f.lower)
}
func (f *findTopSurface) Min() float64 { return float64(f.lower) }
func (f *findTopSurface) Max() float64 { return math.Max(float64(f.lower), f.upper.Max()) }

// marker wraps a function a chunk filler caches or interpolates; sampled
// at a single point it is the function itself.
type marker struct {
	kind Kind
	in   Func
}

func (f *marker) Compute(p Point) float64 { return f.in.Compute(p) }
func (f *marker) Min() float64            { return f.in.Min() }
func (f *marker) Max() float64            { return f.in.Max() }

// Spline evaluation runs in float32 throughout, as the knots are stored.

type boundSpline struct {
	coord  Func // nil for a constant
	value  float32
	locs   []float32
	values []*boundSpline
	derivs []float32
}

func (b *binder) bindSpline(s *SplineSpec) *boundSpline {
	if s.Coord == nil {
		return &boundSpline{value: s.Const}
	}
	out := &boundSpline{coord: b.bind(s.Coord)}
	for _, k := range s.Points {
		out.locs = append(out.locs, k.Loc)
		out.values = append(out.values, b.bindSpline(k.Value))
		out.derivs = append(out.derivs, k.Deriv)
	}
	return out
}

func lerpF(t, a, b float32) float32 { return a + float32(t*(b-a)) }

func (s *boundSpline) sample(p Point) float32 {
	if s.coord == nil {
		return s.value
	}
	x := float32(s.coord.Compute(p))
	// The interval is the last knot at or below x: one before the first
	// knot x is strictly below.
	lo, n := 0, len(s.locs)
	for n > 0 {
		half := n / 2
		if x < s.locs[lo+half] {
			n = half
		} else {
			lo += half + 1
			n -= half + 1
		}
	}
	i := lo - 1
	last := len(s.locs) - 1
	if i < 0 {
		return s.extend(x, s.values[0].sample(p), 0)
	}
	if i == last {
		return s.extend(x, s.values[last].sample(p), last)
	}
	l0, l1 := s.locs[i], s.locs[i+1]
	t := (x - l0) / (l1 - l0)
	y0, y1 := s.values[i].sample(p), s.values[i+1].sample(p)
	span := l1 - l0
	a := float32(s.derivs[i]*span) - (y1 - y0)
	b := float32(-s.derivs[i+1]*span) + (y1 - y0)
	return lerpF(t, y0, y1) + float32(float32(t*(1-t))*lerpF(t, a, b))
}

// extend continues the spline past its end knots along the knot's slope.
func (s *boundSpline) extend(x, v float32, i int) float32 {
	d := s.derivs[i]
	if d == 0 {
		return v
	}
	return v + float32(d*(x-s.locs[i]))
}

type splineFn struct{ s *boundSpline }

func (f *splineFn) Compute(p Point) float64 { return float64(f.s.sample(p)) }

// A cubic segment can overshoot its knots, so the spline claims no
// bounds; min/max over a spline always evaluate it.
func (f *splineFn) Min() float64 { return math.Inf(-1) }
func (f *splineFn) Max() float64 { return math.Inf(1) }
