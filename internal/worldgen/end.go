package worldgen

import "math"

// endIslands is the End's island field: a central island falling off
// with distance, and outer islands wherever a simplex noise dips below
// -0.9 on a grid of 2×2-cell spots, each raised as a cone whose
// steepness is hashed from its position.
type endIslands struct{ noise *Simplex }

func newEndIslands(seed int64) *endIslands {
	r := NewLegacy(seed)
	r.ConsumeCount(17292)
	return &endIslands{noise: NewSimplex(r)}
}

func clampF(v, lo, hi float32) float32 {
	if v < lo {
		return lo
	}
	return min(v, hi)
}

func sqrtF(v float32) float32 { return float32(math.Sqrt(float64(v))) }

// height is the island height value for 8-block cell (x, z).
func (e *endIslands) height(x, z int32) float32 {
	hx, hz := x/2, z/2
	rx, rz := x%2, z%2
	h := clampF(100-float32(sqrtF(float32(x*x+z*z))*8), -100, 80)
	for dx := int32(-12); dx <= 12; dx++ {
		for dz := int32(-12); dz <= 12; dz++ {
			ax, az := int64(hx+dx), int64(hz+dz)
			if ax*ax+az*az <= 4096 || !(e.noise.Value(float64(ax), float64(az)) < -0.8999999761581421) {
				continue
			}
			k := float32(math.Mod(float64(float32(abs32f(float32(ax))*3439)+float32(abs32f(float32(az))*147)), 13)) + 9
			fx, fz := float32(rx-dx*2), float32(rz-dz*2)
			g := clampF(100-float32(sqrtF(float32(fx*fx)+float32(fz*fz))*k), -100, 80)
			h = max(h, g)
		}
	}
	return h
}

func abs32f(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

func (e *endIslands) Compute(p Point) float64 {
	return (float64(e.height(p.X/8, p.Z/8)) - 8) / 128
}
func (e *endIslands) Min() float64 { return -0.84375 }
func (e *endIslands) Max() float64 { return 0.5625 }

// The End's biome source: the main island out to 64 sections, beyond it
// highlands, midlands, small islands or barrens by the erosion sampled
// at the section's middle.
var endBiomes = []string{"minecraft:the_end", "minecraft:end_highlands", "minecraft:end_midlands",
	"minecraft:small_end_islands", "minecraft:end_barrens"}

func (rs *RandomState) endBiome(qx, qy, qz int32) string {
	bx, by, bz := qx<<2, qy<<2, qz<<2
	sx, sz := int64(bx>>4), int64(bz>>4)
	if sx*sx+sz*sz <= 4096 {
		return endBiomes[0]
	}
	e := rs.Router.Erosion.Compute(Point{(bx>>4*2 + 1) * 8, by, (bz>>4*2 + 1) * 8})
	switch {
	case e > 0.25:
		return endBiomes[1]
	case e >= -0.0625:
		return endBiomes[2]
	case e < -0.21875:
		return endBiomes[3]
	}
	return endBiomes[4]
}
