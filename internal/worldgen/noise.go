package worldgen

import (
	"math"
	"strconv"
)

// floorInt is floor to a 32-bit int, saturating like the JVM's d2i.
func floorInt(d float64) int32 {
	f := math.Floor(d)
	switch {
	case f != f:
		return 0
	case f >= math.MaxInt32:
		return math.MaxInt32
	case f <= math.MinInt32:
		return math.MinInt32
	}
	return int32(f)
}

// floorLong is floor to a 64-bit int, saturating like the JVM's d2l.
func floorLong(d float64) int64 {
	f := math.Floor(d)
	switch {
	case f != f:
		return 0
	case f >= math.MaxInt64:
		return math.MaxInt64
	case f <= math.MinInt64:
		return math.MinInt64
	}
	return int64(f)
}

// Products that feed an addition are wrapped in explicit float64
// conversions throughout this package: the conversion forces rounding,
// so no platform may fuse them into one multiply-add and the results
// stay bit-identical to the JVM's.

// smoothstep is Perlin's quintic fade 6t^5 - 15t^4 + 10t^3, evaluated
// as t^3 * (t*(6t - 15) + 10).
func smoothstep(t float64) float64 {
	return t * t * t * (float64(t*(float64(t*6)-15)) + 10)
}

func lerp(t, a, b float64) float64 { return a + float64(t*(b-a)) }

func lerp2(tx, ty, v00, v10, v01, v11 float64) float64 {
	return lerp(ty, lerp(tx, v00, v10), lerp(tx, v01, v11))
}

func lerp3(tx, ty, tz, v000, v100, v010, v110, v001, v101, v011, v111 float64) float64 {
	return lerp(tz, lerp2(tx, ty, v000, v100, v010, v110), lerp2(tx, ty, v001, v101, v011, v111))
}

func inverseLerp(v, a, b float64) float64 { return (v - a) / (b - a) }

func clampedLerp(t, a, b float64) float64 {
	switch {
	case t < 0:
		return a
	case t > 1:
		return b
	}
	return lerp(t, a, b)
}

func clampedMap(v, a, b, lo, hi float64) float64 { return clampedLerp(inverseLerp(v, a, b), lo, hi) }

func mapRange(v, a, b, lo, hi float64) float64 { return lerp(inverseLerp(v, a, b), lo, hi) }

// gradients are the sixteen improved-noise gradient directions: the
// twelve cube edge midpoints, then four of them again so a 4-bit hash
// indexes the table directly.
var gradients = [16][3]float64{
	{1, 1, 0}, {-1, 1, 0}, {1, -1, 0}, {-1, -1, 0},
	{1, 0, 1}, {-1, 0, 1}, {1, 0, -1}, {-1, 0, -1},
	{0, 1, 1}, {0, -1, 1}, {0, 1, -1}, {0, -1, -1},
	{1, 1, 0}, {0, -1, 1}, {-1, 1, 0}, {0, -1, -1},
}

func gradDot(hash int, x, y, z float64) float64 {
	g := &gradients[hash&15]
	return float64(g[0]*x) + float64(g[1]*y) + float64(g[2]*z)
}

// Improved is one octave of Perlin's improved gradient noise with a
// random origin offset and a shuffled 256-entry permutation.
type Improved struct {
	perm       [256]uint8
	Xo, Yo, Zo float64
}

// NewImproved draws the offset (three doubles scaled to [0, 256)) and
// then shuffles the identity permutation, slot i swapping with a slot
// drawn from [i, 256).
func NewImproved(r Random) *Improved {
	n := &Improved{}
	n.Xo = r.NextDouble() * 256
	n.Yo = r.NextDouble() * 256
	n.Zo = r.NextDouble() * 256
	for i := range n.perm {
		n.perm[i] = uint8(i)
	}
	for i := 0; i < 256; i++ {
		j := i + int(r.NextIntn(int32(256-i)))
		n.perm[i], n.perm[j] = n.perm[j], n.perm[i]
	}
	return n
}

func (n *Improved) p(i int) int { return int(n.perm[i&255]) }

// Noise samples the octave at a point.
func (n *Improved) Noise(x, y, z float64) float64 { return n.NoiseY(x, y, z, 0, 0) }

// NoiseY samples with the legacy vertical quantization: with a non-zero
// yScale, the y fraction used for the gradients (not for the fade) is
// pulled down to a multiple of yScale, using at most yMax of it.
func (n *Improved) NoiseY(x, y, z, yScale, yMax float64) float64 {
	x += n.Xo
	y += n.Yo
	z += n.Zo
	xi, yi, zi := floorInt(x), floorInt(y), floorInt(z)
	xf, yf, zf := x-float64(xi), y-float64(yi), z-float64(zi)
	var shift float64
	if yScale != 0 {
		limit := yf
		if yMax >= 0 && yMax < yf {
			limit = yMax
		}
		shift = float64(float64(floorInt(limit/yScale+1.0000000116860974e-7)) * yScale)
	}
	return n.sample(int(xi), int(yi), int(zi), xf, yf-shift, zf, yf)
}

func (n *Improved) sample(xi, yi, zi int, x, y, z, fadeY float64) float64 {
	a := n.p(xi)
	b := n.p(xi + 1)
	aa := n.p(a + yi)
	ab := n.p(a + yi + 1)
	ba := n.p(b + yi)
	bb := n.p(b + yi + 1)
	v000 := gradDot(n.p(aa+zi), x, y, z)
	v100 := gradDot(n.p(ba+zi), x-1, y, z)
	v010 := gradDot(n.p(ab+zi), x, y-1, z)
	v110 := gradDot(n.p(bb+zi), x-1, y-1, z)
	v001 := gradDot(n.p(aa+zi+1), x, y, z-1)
	v101 := gradDot(n.p(ba+zi+1), x-1, y, z-1)
	v011 := gradDot(n.p(ab+zi+1), x, y-1, z-1)
	v111 := gradDot(n.p(bb+zi+1), x-1, y-1, z-1)
	return lerp3(smoothstep(x), smoothstep(fadeY), smoothstep(z), v000, v100, v010, v110, v001, v101, v011, v111)
}

// wrapCoord folds a coordinate into ±2^24 so far-out samples keep their
// fractional precision.
func wrapCoord(d float64) float64 {
	const period = 3.3554432e7
	return d - float64(float64(floorLong(d/period+0.5))*period)
}

// Octaves sums improved-noise octaves: octave i runs at frequency
// 2^(firstOctave+i) and its amplitude weight halves each step up, scaled
// so the weights of all octaves sum to one.
type Octaves struct {
	levels      []*Improved
	amplitudes  []float64
	firstOctave int
	inputFactor float64
	valueFactor float64
	maxValue    float64
}

// NewOctaves builds the octave stack from a source's positional fork:
// each octave with a non-zero amplitude gets its own source hashed from
// "octave_<n>".
func NewOctaves(r Random, firstOctave int, amplitudes []float64) *Octaves {
	o := newOctaveShell(firstOctave, amplitudes)
	pos := r.ForkPositional()
	for i, a := range amplitudes {
		if a != 0 {
			o.levels[i] = NewImproved(pos.FromHash("octave_" + strconv.Itoa(firstOctave+i)))
		}
	}
	o.finish()
	return o
}

// legacyOctaveSkip is how many draws one unused octave consumes when
// octaves are seeded in sequence: three offset doubles (two draws each)
// and 256 shuffle draws.
const legacyOctaveSkip = 262

// NewLegacyOctaves builds the octave stack the pre-positional way, drawing
// every octave in sequence from one source: the octave at frequency 1
// first, then each lower one, skipping the draws of unused octaves.
func NewLegacyOctaves(r Random, firstOctave int, amplitudes []float64) *Octaves {
	o := newOctaveShell(firstOctave, amplitudes)
	n := len(amplitudes)
	top := -firstOctave
	if top < n-1 {
		panic("worldgen: positive octaves are not supported")
	}
	zero := NewImproved(r)
	if top >= 0 && top < n && amplitudes[top] != 0 {
		o.levels[top] = zero
	}
	for i := top - 1; i >= 0; i-- {
		if i < n && amplitudes[i] != 0 {
			o.levels[i] = NewImproved(r)
		} else {
			r.ConsumeCount(legacyOctaveSkip)
		}
	}
	o.finish()
	return o
}

func newOctaveShell(firstOctave int, amplitudes []float64) *Octaves {
	n := len(amplitudes)
	if n == 0 {
		panic("worldgen: no octaves")
	}
	return &Octaves{
		levels:      make([]*Improved, n),
		amplitudes:  amplitudes,
		firstOctave: firstOctave,
		inputFactor: math.Pow(2, float64(firstOctave)),
		valueFactor: math.Pow(2, float64(n-1)) / (math.Pow(2, float64(n)) - 1),
	}
}

func (o *Octaves) finish() { o.maxValue = o.edgeValue(2) }

func (o *Octaves) edgeValue(v float64) float64 {
	var sum float64
	vf := o.valueFactor
	for i, l := range o.levels {
		if l != nil {
			sum += float64(o.amplitudes[i] * v * vf)
		}
		vf /= 2
	}
	return sum
}

// MaxValue bounds |Value|.
func (o *Octaves) MaxValue() float64 { return o.maxValue }

// Value samples the sum at a point.
func (o *Octaves) Value(x, y, z float64) float64 { return o.ValueY(x, y, z, 0, 0) }

// ValueY samples with the legacy vertical quantization passed down to
// every octave at its own frequency.
func (o *Octaves) ValueY(x, y, z, yScale, yMax float64) float64 {
	var sum float64
	f, vf := o.inputFactor, o.valueFactor
	for i, l := range o.levels {
		if l != nil {
			v := l.NoiseY(wrapCoord(x*f), wrapCoord(y*f), wrapCoord(z*f), yScale*f, yMax*f)
			sum += float64(o.amplitudes[i] * v * vf)
		}
		f *= 2
		vf /= 2
	}
	return sum
}

// Octave returns the octave counted down from the highest frequency
// (0 is the last), or nil when that octave is unused.
func (o *Octaves) Octave(i int) *Improved { return o.levels[len(o.levels)-1-i] }

// NoiseParams is a noise definition: the lowest octave and the
// per-octave amplitudes from there up.
type NoiseParams struct {
	FirstOctave int
	Amplitudes  []float64
}

// Normal adds two independently seeded octave stacks, the second
// sampled at a slightly stretched input, and scales the sum so its
// expected deviation is about one third.
type Normal struct {
	first, second *Octaves
	valueFactor   float64
	maxValue      float64
	Params        NoiseParams
}

const normalInputFactor = 1.0181268882175227

// NewNormal builds a normal noise from a source; legacy selects the
// sequential octave seeding the legacy nether biome source keeps.
func NewNormal(r Random, p NoiseParams, legacy bool) *Normal {
	n := &Normal{Params: p}
	if legacy {
		n.first = NewLegacyOctaves(r, p.FirstOctave, p.Amplitudes)
		n.second = NewLegacyOctaves(r, p.FirstOctave, p.Amplitudes)
	} else {
		n.first = NewOctaves(r, p.FirstOctave, p.Amplitudes)
		n.second = NewOctaves(r, p.FirstOctave, p.Amplitudes)
	}
	lo, hi := math.MaxInt32, math.MinInt32
	for i, a := range p.Amplitudes {
		if a != 0 {
			lo, hi = min(lo, i), max(hi, i)
		}
	}
	span := hi - lo
	n.valueFactor = (1.0 / 6.0) / (0.1 * (1 + 1/float64(span+1)))
	n.maxValue = (n.first.MaxValue() + n.second.MaxValue()) * n.valueFactor
	return n
}

// MaxValue bounds |Value|.
func (n *Normal) MaxValue() float64 { return n.maxValue }

// Value samples the noise at a point.
func (n *Normal) Value(x, y, z float64) float64 {
	x2, y2, z2 := x*normalInputFactor, y*normalInputFactor, z*normalInputFactor
	return (n.first.Value(x, y, z) + n.second.Value(x2, y2, z2)) * n.valueFactor
}
