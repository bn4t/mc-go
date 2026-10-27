package worldgen

// blendedNoise is the pre-1.18 terrain noise kept as base_3d_noise: a
// selector noise (8 octaves) blends between two limit noises (16
// octaves each), with the vertical axis smeared by the legacy y
// quantization. All three stacks are seeded in sequence from one source.
type blendedNoise struct {
	minLimit, maxLimit, main *Octaves
	xzMul, yMul              float64
	xzFactor, yFactor        float64
	smear                    float64
	maxValue                 float64
}

// The legacy noise input scale every blended coordinate is multiplied by.
const blendedBaseScale = 684.412

func allOnes(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = 1
	}
	return out
}

func newBlendedNoise(r Random, xzScale, yScale, xzFactor, yFactor, smear float64) *blendedNoise {
	b := &blendedNoise{
		minLimit: NewLegacyOctaves(r, -15, allOnes(16)),
		maxLimit: NewLegacyOctaves(r, -15, allOnes(16)),
		main:     NewLegacyOctaves(r, -7, allOnes(8)),
		xzMul:    blendedBaseScale * xzScale,
		yMul:     blendedBaseScale * yScale,
		xzFactor: xzFactor,
		yFactor:  yFactor,
		smear:    smear,
	}
	b.maxValue = b.minLimit.edgeValue(b.yMul + 2)
	return b
}

func (b *blendedNoise) Compute(p Point) float64 {
	x := float64(p.X) * b.xzMul
	y := float64(p.Y) * b.yMul
	z := float64(p.Z) * b.xzMul
	sx, sy, sz := x/b.xzFactor, y/b.yFactor, z/b.xzFactor
	smearY := b.yMul * b.smear
	smearSel := smearY / b.yFactor

	// The selector picks how much of each limit noise to take; at either
	// end only one limit is sampled.
	var sel float64
	f := 1.0
	for i := 0; i < 8; i++ {
		if o := b.main.Octave(i); o != nil {
			sel += o.NoiseY(wrapCoord(sx*f), wrapCoord(sy*f), wrapCoord(sz*f), smearSel*f, sy*f) / f
		}
		f /= 2
	}
	t := (sel/10 + 1) / 2
	onlyMax, onlyMin := t >= 1, t <= 0

	var lo, hi float64
	f = 1
	for i := 0; i < 16; i++ {
		wx, wy, wz := wrapCoord(x*f), wrapCoord(y*f), wrapCoord(z*f)
		ys := smearY * f
		if !onlyMax {
			if o := b.minLimit.Octave(i); o != nil {
				lo += o.NoiseY(wx, wy, wz, ys, y*f) / f
			}
		}
		if !onlyMin {
			if o := b.maxLimit.Octave(i); o != nil {
				hi += o.NoiseY(wx, wy, wz, ys, y*f) / f
			}
		}
		f /= 2
	}
	return clampedLerp(t, lo/512, hi/512) / 128
}

func (b *blendedNoise) Min() float64 { return -b.maxValue }
func (b *blendedNoise) Max() float64 { return b.maxValue }
