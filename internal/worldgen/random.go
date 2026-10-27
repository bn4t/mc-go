// Package worldgen implements vanilla 26.2 world generation: the random
// sources, the noise primitives, density functions and the stages built
// on them. Everything here is written independently from public
// descriptions of the algorithms; values are pinned against goldens the
// official server produced at runtime (see testdata and tools/worldgenprobe).
package worldgen

import (
	"crypto/md5"
	"encoding/binary"
	"math"
	"math/bits"
)

// Seed constants: the 64-bit golden and silver ratios used to expand a
// 64-bit seed into the 128-bit Xoroshiro state, and the two odd
// multipliers of Stafford's variant-13 64-bit finalizer.
const (
	goldenRatio64 = 0x9E3779B97F4A7C15
	silverRatio64 = 0x6A09E667F3BCC909
	stafford13a   = 0xBF58476D1CE4E5B9
	stafford13b   = 0x94D049BB133111EB
)

// mixStafford13 is Stafford's variant-13 64-bit mixer.
func mixStafford13(z uint64) uint64 {
	z = (z ^ z>>30) * stafford13a
	z = (z ^ z>>27) * stafford13b
	return z ^ z>>31
}

// Seed128 is a 128-bit Xoroshiro seed.
type Seed128 struct{ Lo, Hi uint64 }

// UpgradeSeed expands a 64-bit world seed into a mixed 128-bit seed.
func UpgradeSeed(seed int64) Seed128 {
	lo := uint64(seed) ^ silverRatio64
	hi := lo + goldenRatio64
	return Seed128{mixStafford13(lo), mixStafford13(hi)}
}

// SeedFromHash derives a 128-bit seed from a name: the MD5 digest of its
// UTF-8 bytes, read as two big-endian halves.
func SeedFromHash(name string) Seed128 {
	sum := md5.Sum([]byte(name))
	return Seed128{binary.BigEndian.Uint64(sum[:8]), binary.BigEndian.Uint64(sum[8:])}
}

// BlockSeed is the position hash both positional factories feed into
// their per-position sources. The x factor multiplies in 32 bits before
// widening.
func BlockSeed(x, y, z int32) int64 {
	l := int64(x*3129871) ^ int64(z)*116129781 ^ int64(y)
	l = l*l*42317861 + l*11
	return l >> 16
}

// Random is the subset of a vanilla random source world generation uses.
type Random interface {
	NextLong() int64
	NextInt() int32
	NextIntn(bound int32) int32
	NextFloat() float32
	NextDouble() float64
	NextBool() bool
	NextGaussian() float64
	ConsumeCount(n int)
	Fork() Random
	ForkPositional() Positional
}

// Positional hands out sources keyed by position or by name.
type Positional interface {
	At(x, y, z int32) Random
	FromHash(name string) Random
}

// gaussian is the Marsaglia polar method with its cached second value.
type gaussian struct {
	next float64
	have bool
}

func (g *gaussian) draw(nextDouble func() float64) float64 {
	if g.have {
		g.have = false
		return g.next
	}
	for {
		x := float64(2*nextDouble()) - 1
		y := float64(2*nextDouble()) - 1
		s := float64(x*x) + float64(y*y)
		if s >= 1 || s == 0 {
			continue
		}
		m := math.Sqrt(-2 * math.Log(s) / s)
		g.next, g.have = y*m, true
		return x * m
	}
}

// Xoroshiro is the Xoroshiro128++ random source.
type Xoroshiro struct {
	lo, hi uint64
	g      gaussian
}

// NewXoroshiro seeds a source from a 64-bit seed.
func NewXoroshiro(seed int64) *Xoroshiro {
	return NewXoroshiro128(UpgradeSeed(seed))
}

// NewXoroshiro128 seeds a source from a 128-bit seed; an all-zero seed
// is replaced by the two ratio constants.
func NewXoroshiro128(s Seed128) *Xoroshiro {
	if s.Lo|s.Hi == 0 {
		s = Seed128{goldenRatio64, silverRatio64}
	}
	return &Xoroshiro{lo: s.Lo, hi: s.Hi}
}

func (r *Xoroshiro) next() uint64 {
	lo, hi := r.lo, r.hi
	out := bits.RotateLeft64(lo+hi, 17) + lo
	hi ^= lo
	r.lo = bits.RotateLeft64(lo, 49) ^ hi ^ hi<<21
	r.hi = bits.RotateLeft64(hi, 28)
	return out
}

func (r *Xoroshiro) nextBits(n uint) uint64 { return r.next() >> (64 - n) }

func (r *Xoroshiro) NextLong() int64 { return int64(r.next()) }
func (r *Xoroshiro) NextInt() int32  { return int32(r.next()) }

// NextIntn draws uniformly from [0, bound) by widening multiplication
// with rejection of the biased low band.
func (r *Xoroshiro) NextIntn(bound int32) int32 {
	if bound <= 0 {
		panic("worldgen: bound must be positive")
	}
	m := uint64(uint32(r.NextInt())) * uint64(bound)
	low := uint32(m)
	if low < uint32(bound) {
		threshold := uint32(-bound) % uint32(bound)
		for low < threshold {
			m = uint64(uint32(r.NextInt())) * uint64(bound)
			low = uint32(m)
		}
	}
	return int32(m >> 32)
}

func (r *Xoroshiro) NextFloat() float32    { return float32(r.nextBits(24)) * 5.9604645e-8 }
func (r *Xoroshiro) NextDouble() float64   { return float64(r.nextBits(53)) * 1.1102230246251565e-16 }
func (r *Xoroshiro) NextBool() bool        { return r.next()&1 != 0 }
func (r *Xoroshiro) NextGaussian() float64 { return r.g.draw(r.NextDouble) }

func (r *Xoroshiro) ConsumeCount(n int) {
	for i := 0; i < n; i++ {
		r.next()
	}
}

func (r *Xoroshiro) Fork() Random {
	lo := r.next()
	hi := r.next()
	return NewXoroshiro128(Seed128{lo, hi})
}

func (r *Xoroshiro) ForkPositional() Positional {
	lo := r.next()
	hi := r.next()
	return xoroshiroPositional{lo, hi}
}

type xoroshiroPositional struct{ lo, hi uint64 }

func (p xoroshiroPositional) At(x, y, z int32) Random {
	return NewXoroshiro128(Seed128{uint64(BlockSeed(x, y, z)) ^ p.lo, p.hi})
}

func (p xoroshiroPositional) FromHash(name string) Random {
	s := SeedFromHash(name)
	return NewXoroshiro128(Seed128{s.Lo ^ p.lo, s.Hi ^ p.hi})
}

// Legacy is the 48-bit linear congruential source (java.util.Random's
// generator) that older world-generation paths keep using.
type Legacy struct {
	seed uint64
	g    gaussian
}

const (
	lcgMult = 0x5DEECE66D
	lcgAdd  = 0xB
	lcgMask = 1<<48 - 1
)

// NewLegacy seeds the generator, scrambling the seed with the multiplier.
func NewLegacy(seed int64) *Legacy { return &Legacy{seed: (uint64(seed) ^ lcgMult) & lcgMask} }

func (r *Legacy) bitsN(n uint) int32 {
	r.seed = (r.seed*lcgMult + lcgAdd) & lcgMask
	return int32(int64(r.seed) >> (48 - n))
}

func (r *Legacy) NextLong() int64 { return int64(r.bitsN(32))<<32 + int64(r.bitsN(32)) }
func (r *Legacy) NextInt() int32  { return r.bitsN(32) }

// NextIntn: a power-of-two bound takes the high bits; otherwise draws
// are rejected while they fall in the incomplete last band.
func (r *Legacy) NextIntn(bound int32) int32 {
	if bound <= 0 {
		panic("worldgen: bound must be positive")
	}
	if bound&-bound == bound {
		return int32((int64(bound) * int64(r.bitsN(31))) >> 31)
	}
	for {
		b := r.bitsN(31)
		v := b % bound
		if b-v+(bound-1) >= 0 {
			return v
		}
	}
}

func (r *Legacy) NextFloat() float32 { return float32(r.bitsN(24)) * 5.9604645e-8 }

func (r *Legacy) NextDouble() float64 {
	hi := int64(r.bitsN(26))
	lo := int64(r.bitsN(27))
	return float64(hi<<27+lo) * 1.1102230246251565e-16
}

func (r *Legacy) NextBool() bool        { return r.bitsN(1) != 0 }
func (r *Legacy) NextGaussian() float64 { return r.g.draw(r.NextDouble) }

func (r *Legacy) ConsumeCount(n int) {
	for i := 0; i < n; i++ {
		r.bitsN(32)
	}
}

func (r *Legacy) Fork() Random { return NewLegacy(r.NextLong()) }

func (r *Legacy) ForkPositional() Positional { return legacyPositional{r.NextLong()} }

type legacyPositional struct{ seed int64 }

func (p legacyPositional) At(x, y, z int32) Random { return NewLegacy(BlockSeed(x, y, z) ^ p.seed) }

func (p legacyPositional) FromHash(name string) Random {
	return NewLegacy(int64(javaStringHash(name)) ^ p.seed)
}

// javaStringHash is the 31-multiplier string hash over UTF-16 units.
func javaStringHash(s string) int32 {
	var h int32
	for _, u := range utf16Units(s) {
		h = 31*h + int32(u)
	}
	return h
}

func utf16Units(s string) []uint16 {
	out := make([]uint16, 0, len(s))
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			out = append(out, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
			continue
		}
		out = append(out, uint16(r))
	}
	return out
}
