package server

// javaRand is java.util.Random's 48-bit LCG — the generator behind
// vanilla's LegacyRandomSource (RandomSource.create()), which the
// enchanting menu uses for offer rolls. Pinned bit-for-bit so a given
// seed produces vanilla's exact draws.
//
// The sequence: seed = (seed ^ 0x5DEECE66D) & ((1<<48)-1) at setSeed;
// each next(bits) advance is seed = (seed*0x5DEECE66D + 0xB) & mask,
// returning the top `bits` of the new seed.
type javaRand struct {
	seed uint64
}

const (
	jrMult     = 0x5DEECE66D
	jrAdd      = 0xB
	jrMask48   = (1 << 48) - 1
	jrFloatDiv = 1 << 24
)

func newJavaRand(seed int64) *javaRand {
	r := &javaRand{}
	r.setSeed(seed)
	return r
}

func (r *javaRand) setSeed(seed int64) {
	r.seed = (uint64(seed) ^ jrMult) & jrMask48
}

// next advances the LCG and returns the top bits of the new state.
func (r *javaRand) next(bits uint) int32 {
	r.seed = (r.seed*jrMult + jrAdd) & jrMask48
	return int32(r.seed >> (48 - bits))
}

// nextInt is java.util.Random.nextInt(): a full signed 32-bit draw.
func (r *javaRand) nextInt() int32 {
	return r.next(32)
}

// nextIntN is nextInt(bound) for bound > 0: the power-of-2 fast path
// or the rejection loop otherwise.
func (r *javaRand) nextIntN(bound int32) int32 {
	if bound <= 0 {
		panic("bound must be positive")
	}
	if bound&(bound-1) == 0 {
		return int32((int64(bound) * int64(r.next(31))) >> 31)
	}
	for {
		bits := r.next(31)
		val := bits % bound
		if bits-val+(bound-1) >= 0 {
			return val
		}
	}
}

// nextFloat is (next(24) as float) / 2^24 in float32.
func (r *javaRand) nextFloat() float32 {
	return float32(r.next(24)) / float32(jrFloatDiv)
}
