// Package worldgen is mc-go's vanilla-parity terrain generator: the
// random sources, noise samplers, density-function graph, noise chunk
// interpolation, aquifers, ore veins, biome lookup and surface rules
// that the 26.2 server runs to turn a seed into blocks.
//
// The behavior is measured: constants, evaluation order and branch
// structure were read off the 26.2 jar (javap) and the data-driven
// parts come from the bundled worldgen data pack, and the result is
// pinned block for block against chunks the real server generated
// from the same seed (tests/blackbox TestTerrainNoiseParity). The Go
// is independently written; floating-point products that vanilla
// rounds separately are wrapped in explicit float64()/float32()
// conversions so the compiler never fuses them into FMAs.
package worldgen

import (
	"crypto/md5"
	"encoding/binary"
	"math"
)

// Random is the vanilla RandomSource surface the generator uses.
type Random interface {
	NextInt() int32
	NextIntN(bound int32) int32
	NextLong() int64
	NextBool() bool
	NextFloat() float32
	NextDouble() float64
	NextGaussian() float64
	Consume(n int)
	Fork() Random
	ForkPositional() PositionalRandom
	SetSeed(seed int64)
}

// PositionalRandom is vanilla's PositionalRandomFactory.
type PositionalRandom interface {
	At(x, y, z int32) Random
	FromHashOf(name string) Random
	FromSeed(seed int64) Random
}

const (
	goldenRatio64 = -7046029254386353131 // 0x9E3779B97F4A7C15
	silverRatio64 = 7640891576956012809  // 0x6A09E667F3BCC909
)

// mixStafford13 is RandomSupport.mixStafford13.
func mixStafford13(z int64) int64 {
	u := uint64(z)
	u = (u ^ u>>30) * 0xBF58476D1CE4E5B9
	u = (u ^ u>>27) * 0x94D049BB133111EB
	return int64(u ^ u>>31)
}

// upgradeSeed is RandomSupport.upgradeSeedTo128bit.
func upgradeSeed(seed int64) (lo, hi int64) {
	lo = seed ^ silverRatio64
	hi = lo + goldenRatio64
	return mixStafford13(lo), mixStafford13(hi)
}

// seedFromHashOf is RandomSupport.seedFromHashOf: the string's MD5,
// first and second halves read big-endian.
func seedFromHashOf(name string) (lo, hi int64) {
	sum := md5.Sum([]byte(name))
	return int64(binary.BigEndian.Uint64(sum[0:8])), int64(binary.BigEndian.Uint64(sum[8:16]))
}

// BlockSeed is Mth.getSeed(x, y, z).
func BlockSeed(x, y, z int32) int64 {
	l := int64(x*3129871) ^ int64(z)*116129781 ^ int64(y)
	l = l*l*42317861 + l*11
	return l >> 16
}

// javaStringHash is java.lang.String.hashCode over UTF-16 units.
func javaStringHash(s string) int32 {
	var h int32
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			h = 31*h + int32(0xD800+(r>>10))
			h = 31*h + int32(0xDC00+(r&0x3FF))
			continue
		}
		h = 31*h + int32(r)
	}
	return h
}

// gaussian is MarsagliaPolarGaussian over its owning source.
type gaussian struct {
	have bool
	next float64
}

func (g *gaussian) sample(nextDouble func() float64) float64 {
	if g.have {
		g.have = false
		return g.next
	}
	for {
		a := 2*nextDouble() - 1
		b := 2*nextDouble() - 1
		s := float64(a*a) + float64(b*b)
		if s >= 1 || s == 0 {
			continue
		}
		m := math.Sqrt(-2 * math.Log(s) / s)
		g.next = float64(b * m)
		g.have = true
		return float64(a * m)
	}
}

// Xoroshiro is XoroshiroRandomSource (Xoroshiro128++).
type Xoroshiro struct {
	lo, hi int64
	g      gaussian
}

// NewXoroshiro seeds from a 64-bit seed through upgradeSeedTo128bit.
func NewXoroshiro(seed int64) *Xoroshiro {
	lo, hi := upgradeSeed(seed)
	return NewXoroshiro128(lo, hi)
}

// NewXoroshiro128 seeds the raw 128-bit state; an all-zero state is
// replaced by the golden/silver ratio pair, as vanilla does.
func NewXoroshiro128(lo, hi int64) *Xoroshiro {
	if lo == 0 && hi == 0 {
		lo, hi = goldenRatio64, silverRatio64
	}
	return &Xoroshiro{lo: lo, hi: hi}
}

func rotl(x uint64, k uint) uint64 { return x<<k | x>>(64-k) }

func (r *Xoroshiro) NextLong() int64 {
	s0, s1 := uint64(r.lo), uint64(r.hi)
	out := rotl(s0+s1, 17) + s0
	s1 ^= s0
	r.lo = int64(rotl(s0, 49) ^ s1 ^ s1<<21)
	r.hi = int64(rotl(s1, 28))
	return int64(out)
}

func (r *Xoroshiro) SetSeed(seed int64) {
	r.lo, r.hi = upgradeSeed(seed)
	if r.lo == 0 && r.hi == 0 {
		r.lo, r.hi = goldenRatio64, silverRatio64
	}
	r.g = gaussian{}
}

func (r *Xoroshiro) nextBits(n uint) int64 { return int64(uint64(r.NextLong()) >> (64 - n)) }

func (r *Xoroshiro) NextInt() int32 { return int32(r.NextLong()) }

func (r *Xoroshiro) NextIntN(bound int32) int32 {
	if bound <= 0 {
		panic("worldgen: bound must be positive")
	}
	l := uint64(uint32(r.NextInt()))
	m := l * uint64(bound)
	low := m & 0xFFFFFFFF
	if low < uint64(bound) {
		t := uint32(-bound) % uint32(bound)
		for low < uint64(t) {
			l = uint64(uint32(r.NextInt()))
			m = l * uint64(bound)
			low = m & 0xFFFFFFFF
		}
	}
	return int32(m >> 32)
}

func (r *Xoroshiro) NextBool() bool        { return r.NextLong()&1 != 0 }
func (r *Xoroshiro) NextFloat() float32    { return float32(r.nextBits(24)) * 5.9604645e-8 }
func (r *Xoroshiro) NextDouble() float64   { return float64(r.nextBits(53)) * 1.1102230246251565e-16 }
func (r *Xoroshiro) NextGaussian() float64 { return r.g.sample(r.NextDouble) }

func (r *Xoroshiro) Consume(n int) {
	for i := 0; i < n; i++ {
		r.NextLong()
	}
}

func (r *Xoroshiro) Fork() Random {
	lo := r.NextLong()
	hi := r.NextLong()
	return NewXoroshiro128(lo, hi)
}

func (r *Xoroshiro) ForkPositional() PositionalRandom {
	lo := r.NextLong()
	hi := r.NextLong()
	return &XoroshiroPositional{lo: lo, hi: hi}
}

// XoroshiroPositional is XoroshiroPositionalRandomFactory.
type XoroshiroPositional struct{ lo, hi int64 }

func (f *XoroshiroPositional) At(x, y, z int32) Random {
	return NewXoroshiro128(BlockSeed(x, y, z)^f.lo, f.hi)
}

func (f *XoroshiroPositional) FromHashOf(name string) Random {
	lo, hi := seedFromHashOf(name)
	return NewXoroshiro128(lo^f.lo, hi^f.hi)
}

func (f *XoroshiroPositional) FromSeed(seed int64) Random {
	return NewXoroshiro128(seed^f.lo, seed^f.hi)
}

// Legacy is LegacyRandomSource: java.util.Random's 48-bit LCG.
type Legacy struct {
	seed int64
	g    gaussian
}

const (
	lcgMul  = 0x5DEECE66D
	lcgMask = (1 << 48) - 1
)

// NewLegacy seeds the LCG.
func NewLegacy(seed int64) *Legacy {
	r := &Legacy{}
	r.SetSeed(seed)
	return r
}

func (r *Legacy) SetSeed(seed int64) {
	r.seed = (seed ^ lcgMul) & lcgMask
	r.g = gaussian{}
}

// Next is BitRandomSource.next(bits).
func (r *Legacy) Next(bits uint) int32 {
	r.seed = (r.seed*lcgMul + 0xB) & lcgMask
	return int32(r.seed >> (48 - bits))
}

func (r *Legacy) NextInt() int32 { return r.Next(32) }

func (r *Legacy) NextIntN(bound int32) int32 { return bitNextIntN(r.Next, bound) }

func bitNextIntN(next func(uint) int32, bound int32) int32 {
	if bound <= 0 {
		panic("worldgen: bound must be positive")
	}
	if bound&(bound-1) == 0 {
		return int32(int64(bound) * int64(next(31)) >> 31)
	}
	for {
		bits := next(31)
		val := bits % bound
		if bits-val+(bound-1) >= 0 {
			return val
		}
	}
}

func (r *Legacy) NextLong() int64 {
	hi := r.Next(32)
	lo := r.Next(32)
	return int64(hi)<<32 + int64(lo)
}

func (r *Legacy) NextBool() bool     { return r.Next(1) != 0 }
func (r *Legacy) NextFloat() float32 { return float32(r.Next(24)) * 5.9604645e-8 }
func (r *Legacy) NextDouble() float64 {
	hi := int64(r.Next(26))
	lo := int64(r.Next(27))
	return float64(hi<<27+lo) * 1.1102230246251565e-16
}
func (r *Legacy) NextGaussian() float64 { return r.g.sample(r.NextDouble) }

func (r *Legacy) Consume(n int) {
	for i := 0; i < n; i++ {
		r.Next(32)
	}
}

func (r *Legacy) Fork() Random { return NewLegacy(r.NextLong()) }

func (r *Legacy) ForkPositional() PositionalRandom { return &LegacyPositional{seed: r.NextLong()} }

// LegacyPositional is LegacyRandomSource.LegacyPositionalRandomFactory.
type LegacyPositional struct{ seed int64 }

func (f *LegacyPositional) At(x, y, z int32) Random {
	return NewLegacy(BlockSeed(x, y, z) ^ f.seed)
}

func (f *LegacyPositional) FromHashOf(name string) Random {
	return NewLegacy(int64(javaStringHash(name)) ^ f.seed)
}

func (f *LegacyPositional) FromSeed(seed int64) Random { return NewLegacy(seed) }
