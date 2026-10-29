package worldgen

import "math"

// gradient is the 16-entry gradient table ImprovedNoise and
// SimplexNoise share (indices taken & 15).
var gradient = [16][3]float64{
	{1, 1, 0}, {-1, 1, 0}, {1, -1, 0}, {-1, -1, 0},
	{1, 0, 1}, {-1, 0, 1}, {1, 0, -1}, {-1, 0, -1},
	{0, 1, 1}, {0, -1, 1}, {0, 1, -1}, {0, -1, -1},
	{1, 1, 0}, {0, -1, 1}, {-1, 1, 0}, {0, -1, -1},
}

func gradDot(h int, x, y, z float64) float64 {
	g := &gradient[h&15]
	return float64(g[0]*x) + float64(g[1]*y) + float64(g[2]*z)
}

// lerp is Mth.lerp(double): a + t*(b-a), each step rounded.
func lerp(t, a, b float64) float64 { return a + float64(t*(b-a)) }

func lerp2(dx, dy, a, b, c, d float64) float64 {
	return lerp(dy, lerp(dx, a, b), lerp(dx, c, d))
}

func lerp3(dx, dy, dz, a000, a100, a010, a110, a001, a101, a011, a111 float64) float64 {
	return lerp(dz, lerp2(dx, dy, a000, a100, a010, a110), lerp2(dx, dy, a001, a101, a011, a111))
}

func smoothstep(t float64) float64 {
	return float64(float64(float64(t*t)*t) * (float64(t*(float64(t*6)-15)) + 10))
}

func clampedLerp(t, a, b float64) float64 {
	if t < 0 {
		return a
	}
	if t > 1 {
		return b
	}
	return lerp(t, a, b)
}

func inverseLerp(v, a, b float64) float64 { return (v - a) / (b - a) }

func clampedMap(v, a, b, c, d float64) float64 { return clampedLerp(inverseLerp(v, a, b), c, d) }

func clampD(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// floorI is Mth.floor: (int) Math.floor, saturating like Java's d2i.
func floorI(v float64) int32 { return d2i(math.Floor(v)) }

// d2i is Java's double-to-int narrowing: NaN is 0, out of range clamps.
func d2i(v float64) int32 {
	switch {
	case v != v:
		return 0
	case v >= math.MaxInt32:
		return math.MaxInt32
	case v <= math.MinInt32:
		return math.MinInt32
	}
	return int32(v)
}

// d2l is Java's double-to-long narrowing.
func d2l(v float64) int64 {
	switch {
	case v != v:
		return 0
	case v >= math.MaxInt64:
		return math.MaxInt64
	case v <= math.MinInt64:
		return math.MinInt64
	}
	return int64(v)
}

// ImprovedNoise is one Perlin octave with its random offset and
// permutation table.
type ImprovedNoise struct {
	p          [256]byte
	xo, yo, zo float64
}

// NewImprovedNoise draws the offsets then the Fisher-Yates permutation.
func NewImprovedNoise(r Random) *ImprovedNoise {
	n := &ImprovedNoise{}
	n.xo = float64(r.NextDouble() * 256)
	n.yo = float64(r.NextDouble() * 256)
	n.zo = float64(r.NextDouble() * 256)
	for i := 0; i < 256; i++ {
		n.p[i] = byte(i)
	}
	for i := 0; i < 256; i++ {
		j := int(r.NextIntN(int32(256 - i)))
		n.p[i], n.p[i+j] = n.p[i+j], n.p[i]
	}
	return n
}

func (n *ImprovedNoise) perm(i int32) int32 { return int32(n.p[i&255]) }

// Noise is ImprovedNoise.noise(x, y, z, yScale, yMax).
func (n *ImprovedNoise) Noise(x, y, z, yScale, yMax float64) float64 {
	d := x + n.xo
	e := y + n.yo
	f := z + n.zo
	i := floorI(d)
	j := floorI(e)
	k := floorI(f)
	g := d - float64(i)
	h := e - float64(j)
	l := f - float64(k)
	var shift float64
	if yScale != 0 {
		var top float64
		if yMax >= 0 && yMax < h {
			top = yMax
		} else {
			top = h
		}
		shift = float64(float64(floorI(top/yScale+1.0000000116860974e-7)) * yScale)
	}
	return n.sampleAndLerp(i, j, k, g, h-shift, l, h)
}

func (n *ImprovedNoise) sampleAndLerp(x, y, z int32, dx, dy, dz, dyOrig float64) float64 {
	i := n.perm(x)
	j := n.perm(x + 1)
	k := n.perm(i + y)
	l := n.perm(i + y + 1)
	m := n.perm(j + y)
	o := n.perm(j + y + 1)
	d := gradDot(int(n.perm(k+z)), dx, dy, dz)
	e := gradDot(int(n.perm(m+z)), dx-1, dy, dz)
	f := gradDot(int(n.perm(l+z)), dx, dy-1, dz)
	g := gradDot(int(n.perm(o+z)), dx-1, dy-1, dz)
	h := gradDot(int(n.perm(k+z+1)), dx, dy, dz-1)
	p := gradDot(int(n.perm(m+z+1)), dx-1, dy, dz-1)
	q := gradDot(int(n.perm(l+z+1)), dx, dy-1, dz-1)
	r := gradDot(int(n.perm(o+z+1)), dx-1, dy-1, dz-1)
	sx := smoothstep(dx)
	sy := smoothstep(dyOrig)
	sz := smoothstep(dz)
	return lerp3(sx, sy, sz, d, e, f, g, h, p, q, r)
}

// wrap is PerlinNoise.wrap: fold into ±2^25 to keep precision.
func wrap(v float64) float64 {
	const roundOff = 3.3554432e7
	return v - float64(float64(d2l(math.Floor(v/roundOff+0.5)))*roundOff)
}

// PerlinNoise is a stack of octaves (nil where the amplitude is zero).
type PerlinNoise struct {
	levels          []*ImprovedNoise
	firstOctave     int
	amplitudes      []float64
	lowestValFactor float64
	lowestInFactor  float64
	maxValue        float64
}

// NewPerlinNoise is PerlinNoise.create with the new (positional)
// initialization: octave k seeded from fromHashOf("octave_<k>").
func NewPerlinNoise(r Random, firstOctave int, amplitudes []float64) *PerlinNoise {
	return newPerlin(r, firstOctave, amplitudes, true)
}

// NewPerlinNoiseLegacy is the sequential legacy initialization
// (createLegacyForLegacyNetherBiome / createLegacyForBlendedNoise).
func NewPerlinNoiseLegacy(r Random, firstOctave int, amplitudes []float64) *PerlinNoise {
	return newPerlin(r, firstOctave, amplitudes, false)
}

// amplitudesForOctaves is makeAmplitudes over an inclusive octave range.
func amplitudesForOctaves(lo, hi int) (int, []float64) {
	a := make([]float64, hi-lo+1)
	for i := range a {
		a[i] = 1
	}
	return lo, a
}

func newPerlin(r Random, firstOctave int, amplitudes []float64, newInit bool) *PerlinNoise {
	n := len(amplitudes)
	zero := -firstOctave
	p := &PerlinNoise{levels: make([]*ImprovedNoise, n), firstOctave: firstOctave, amplitudes: amplitudes}
	if newInit {
		pos := r.ForkPositional()
		for i := 0; i < n; i++ {
			if amplitudes[i] != 0 {
				p.levels[i] = NewImprovedNoise(pos.FromHashOf("octave_" + itoa(firstOctave+i)))
			}
		}
	} else {
		first := NewImprovedNoise(r)
		if zero >= 0 && zero < n && amplitudes[zero] != 0 {
			p.levels[zero] = first
		}
		for i := zero - 1; i >= 0; i-- {
			if i < n {
				if amplitudes[i] != 0 {
					p.levels[i] = NewImprovedNoise(r)
				} else {
					r.Consume(262)
				}
			} else {
				r.Consume(262)
			}
		}
	}
	p.lowestInFactor = math.Pow(2, float64(-zero))
	p.lowestValFactor = math.Pow(2, float64(n-1)) / (math.Pow(2, float64(n)) - 1)
	p.maxValue = p.edgeValue(2)
	return p
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// Value is PerlinNoise.getValue(x, y, z).
func (p *PerlinNoise) Value(x, y, z float64) float64 { return p.ValueY(x, y, z, 0, 0) }

// ValueY is getValue(x, y, z, yScale, yMax).
func (p *PerlinNoise) ValueY(x, y, z, yScale, yMax float64) float64 {
	var sum float64
	in := p.lowestInFactor
	val := p.lowestValFactor
	for i, lvl := range p.levels {
		if lvl != nil {
			v := lvl.Noise(wrap(float64(x*in)), wrap(float64(y*in)), wrap(float64(z*in)), float64(yScale*in), float64(yMax*in))
			sum += float64(float64(p.amplitudes[i]*v) * val)
		}
		in *= 2
		val /= 2
	}
	return sum
}

func (p *PerlinNoise) edgeValue(v float64) float64 {
	var sum float64
	val := p.lowestValFactor
	for i, lvl := range p.levels {
		if lvl != nil {
			sum += float64(float64(p.amplitudes[i]*v) * val)
		}
		val /= 2
	}
	return sum
}

// octave is getOctaveNoise(i): levels counted from the highest.
func (p *PerlinNoise) octave(i int) *ImprovedNoise { return p.levels[len(p.levels)-1-i] }

// NoiseParams is a worldgen/noise entry.
type NoiseParams struct {
	FirstOctave int       `json:"firstOctave"`
	Amplitudes  []float64 `json:"amplitudes"`
}

// NormalNoise sums two PerlinNoise stacks, the second sampled at a
// slightly stretched input, normalized toward unit deviation.
type NormalNoise struct {
	first, second *PerlinNoise
	valueFactor   float64
}

const normalInputFactor = 1.0181268882175227

// NewNormalNoise is NormalNoise.create (new initialization).
func NewNormalNoise(r Random, p NoiseParams) *NormalNoise {
	return newNormal(r, p, true)
}

// NewNormalNoiseLegacyNether is createLegacyNetherBiome.
func NewNormalNoiseLegacyNether(r Random, p NoiseParams) *NormalNoise {
	return newNormal(r, p, false)
}

func newNormal(r Random, p NoiseParams, newInit bool) *NormalNoise {
	n := &NormalNoise{}
	n.first = newPerlin(r, p.FirstOctave, p.Amplitudes, newInit)
	n.second = newPerlin(r, p.FirstOctave, p.Amplitudes, newInit)
	lo, hi := math.MaxInt32, math.MinInt32
	for i, a := range p.Amplitudes {
		if a != 0 {
			lo = min(lo, i)
			hi = max(hi, i)
		}
	}
	n.valueFactor = 0.16666666666666666 / (0.1 * (1 + 1/float64(hi-lo+1)))
	return n
}

// Value is NormalNoise.getValue.
func (n *NormalNoise) Value(x, y, z float64) float64 {
	a := float64(x * normalInputFactor)
	b := float64(y * normalInputFactor)
	c := float64(z * normalInputFactor)
	return float64((n.first.Value(x, y, z) + n.second.Value(a, b, c)) * n.valueFactor)
}

// BlendedNoise is the old_blended_noise terrain sampler.
type BlendedNoise struct {
	minLimit, maxLimit, main                          *PerlinNoise
	xzScale, yScale, xzFactor, yFactor, smearScaleMul float64
	xzMul, yMul                                       float64
}

// NewBlendedNoise draws the three legacy stacks in order.
func NewBlendedNoise(r Random, xzScale, yScale, xzFactor, yFactor, smear float64) *BlendedNoise {
	lo, a16 := amplitudesForOctaves(-15, 0)
	b := &BlendedNoise{
		minLimit: NewPerlinNoiseLegacy(r, lo, a16),
		maxLimit: NewPerlinNoiseLegacy(r, lo, a16),
	}
	lo8, a8 := amplitudesForOctaves(-7, 0)
	b.main = NewPerlinNoiseLegacy(r, lo8, a8)
	b.xzScale, b.yScale, b.xzFactor, b.yFactor, b.smearScaleMul = xzScale, yScale, xzFactor, yFactor, smear
	b.xzMul = float64(684.412 * xzScale)
	b.yMul = float64(684.412 * yScale)
	return b
}

// Compute is BlendedNoise.compute at a block position.
func (b *BlendedNoise) Compute(bx, by, bz int32) float64 {
	d := float64(float64(bx) * b.xzMul)
	e := float64(float64(by) * b.yMul)
	f := float64(float64(bz) * b.xzMul)
	g := d / b.xzFactor
	h := e / b.yFactor
	i := f / b.xzFactor
	j := float64(b.yMul * b.smearScaleMul)
	k := j / b.yFactor
	var n float64
	o := 1.0
	for p := 0; p < 8; p++ {
		if lvl := b.main.octave(p); lvl != nil {
			n += lvl.Noise(wrap(float64(g*o)), wrap(float64(h*o)), wrap(float64(i*o)), float64(k*o), float64(h*o)) / o
		}
		o /= 2
	}
	q := (n/10 + 1) / 2
	hiSat := q >= 1
	loSat := q <= 0
	o = 1
	var l, m float64
	for r := 0; r < 16; r++ {
		s := wrap(float64(d * o))
		t := wrap(float64(e * o))
		u := wrap(float64(f * o))
		v := float64(j * o)
		if !hiSat {
			if lvl := b.minLimit.octave(r); lvl != nil {
				l += lvl.Noise(s, t, u, v, float64(e*o)) / o
			}
		}
		if !loSat {
			if lvl := b.maxLimit.octave(r); lvl != nil {
				m += lvl.Noise(s, t, u, v, float64(e*o)) / o
			}
		}
		o /= 2
	}
	return clampedLerp(q, l/512, m/512) / 128
}

// SimplexNoise is the 2D/3D simplex sampler (end islands).
type SimplexNoise struct {
	p          [512]int32
	xo, yo, zo float64
}

// NewSimplexNoise draws offsets then the permutation.
func NewSimplexNoise(r Random) *SimplexNoise {
	n := &SimplexNoise{}
	n.xo = float64(r.NextDouble() * 256)
	n.yo = float64(r.NextDouble() * 256)
	n.zo = float64(r.NextDouble() * 256)
	for i := 0; i < 256; i++ {
		n.p[i] = int32(i)
	}
	for i := 0; i < 256; i++ {
		j := r.NextIntN(int32(256 - i))
		n.p[i], n.p[int32(i)+j] = n.p[int32(i)+j], n.p[i]
	}
	return n
}

func (n *SimplexNoise) perm(i int32) int32 { return n.p[i&255] }

func simplexCorner(g int32, x, y, z, base float64) float64 {
	d := base - float64(x*x) - float64(y*y) - float64(z*z)
	if d < 0 {
		return 0
	}
	d *= d
	return float64(float64(d*d) * gradDot(int(g), x, y, z))
}

var (
	simplexF2 = 0.5 * (math.Sqrt(3) - 1)
	simplexG2 = (3 - math.Sqrt(3)) / 6
)

// Value2D is SimplexNoise.getValue(x, y).
func (n *SimplexNoise) Value2D(x, y float64) float64 {
	d := float64((x + y) * simplexF2)
	i := floorI(x + d)
	j := floorI(y + d)
	e := float64(float64(i+j) * simplexG2)
	f := float64(i) - e
	g := float64(j) - e
	h := x - f
	k := y - g
	var l, m int32
	if h > k {
		l, m = 1, 0
	} else {
		l, m = 0, 1
	}
	o := h - float64(l) + simplexG2
	p := k - float64(m) + simplexG2
	q := h - 1 + float64(2*simplexG2)
	r := k - 1 + float64(2*simplexG2)
	s := i & 255
	t := j & 255
	u := n.perm(s+n.perm(t)) % 12
	v := n.perm(s+l+n.perm(t+m)) % 12
	w := n.perm(s+1+n.perm(t+1)) % 12
	z := simplexCorner(u, h, k, 0, 0.5)
	aa := simplexCorner(v, o, p, 0, 0.5)
	ab := simplexCorner(w, q, r, 0, 0.5)
	return float64(70 * (z + aa + ab))
}

// sinJava and cosJava stand in for Java's Math.sin/cos. The spawn
// search only truncates their products to block coordinates, and the
// resulting world spawn is pinned by TestNormalWorldChunkParity.
func sinJava(v float64) float64 { return math.Sin(v) }
func cosJava(v float64) float64 { return math.Cos(v) }
