package worldgen

import "math"

// The aquifer grid: one randomly placed aquifer center per 16×12×16
// grid cell, jittered within the first 10×9×10 blocks of it. A block
// takes the fluid of its nearest center and is walled off from the
// next-nearest ones by barriers where their fluids disagree.
const (
	aqSpacingXZ = 16
	aqSpacingY  = 12
	aqRangeXZ   = 10
	aqRangeY    = 9
	// The grid is sampled from a point offset by (-5, +1, -5).
	aqOffsetXZ = -5
	aqOffsetY  = 1
)

// aqSurfaceOffsets are the chunk offsets around an aquifer center whose
// surface heights bound its fluid level, the center itself first.
var aqSurfaceOffsets = [13][2]int32{
	{0, 0}, {-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {-3, 0}, {-2, 0},
	{-1, 0}, {1, 0}, {-2, 1}, {-1, 1}, {0, 1}, {1, 1},
}

// aqSimilarity is 1 at equal squared distances to two centers and
// falls by 1 per 25 of difference.
func aqSimilarity(d1, d2 int32) float64 { return 1 - float64(d2-d1)/25 }

// aqFlowingSimilarity marks centers close enough that their fluids
// meeting needs a fluid update: distances 10 and 12.
var aqFlowingSimilarity = aqSimilarity(10*10, 12*12)

type aqCenter struct {
	x, y, z int32
	status  fluidStatus
	located bool
	known   bool
}

type aquifer struct {
	f                   *NoiseChunkFiller
	minGX, minGY, minGZ int32
	sizeX, sizeY, sizeZ int32
	centers             []aqCenter
	skipAboveY          int32
}

func (a *aquifer) reset(f *NoiseChunkFiller, cx, cz int32) {
	a.f = f
	minX, maxX := cx*16, cx*16+15
	minZ, maxZ := cz*16, cz*16+15
	a.minGX = (minX + aqOffsetXZ) >> 4
	maxGX := (maxX+aqOffsetXZ)>>4 + 1
	a.sizeX = maxGX - a.minGX + 1
	a.minGY = floorDiv32(f.minY+aqOffsetY, aqSpacingY) - 1
	maxGY := floorDiv32(f.minY+f.height+aqOffsetY, aqSpacingY) + 1
	a.sizeY = maxGY - a.minGY + 1
	a.minGZ = (minZ + aqOffsetXZ) >> 4
	maxGZ := (maxZ+aqOffsetXZ)>>4 + 1
	a.sizeZ = maxGZ - a.minGZ + 1
	n := int(a.sizeX * a.sizeY * a.sizeZ)
	if cap(a.centers) < n {
		a.centers = make([]aqCenter, n)
	}
	a.centers = a.centers[:n]
	clear(a.centers)

	// Above the highest surface any center of this chunk could see (plus
	// a grid layer of margin), only the global fluid applies.
	skip := func() int32 {
		top := int32(math.MinInt32)
		for z := a.minGZ << 4; z <= maxGZ<<4+aqRangeXZ-1; z += 4 {
			for x := a.minGX << 4; x <= maxGX<<4+aqRangeXZ-1; x += 4 {
				top = max(top, f.preliminarySurface(x, z))
			}
		}
		layer := floorDiv32(top+8+aqSpacingY, aqSpacingY) + 1
		return layer*aqSpacingY + (aqSpacingY - 1) - 1
	}
	if f.shared != nil {
		a.skipAboveY = f.shared.skip.get(cx, cz, skip)
	} else {
		a.skipAboveY = skip()
	}
}

func (a *aquifer) index(gx, gy, gz int32) int {
	return int(((gy-a.minGY)*a.sizeZ+(gz-a.minGZ))*a.sizeX + (gx - a.minGX))
}

// center returns grid cell (gx, gy, gz)'s center, placing it on first use.
func (a *aquifer) center(gx, gy, gz int32) *aqCenter {
	c := &a.centers[a.index(gx, gy, gz)]
	if !c.located {
		r := a.f.rs.Aquifer.At(gx, gy, gz)
		c.x = gx*aqSpacingXZ + r.NextIntn(aqRangeXZ)
		c.y = gy*aqSpacingY + r.NextIntn(aqRangeY)
		c.z = gz*aqSpacingXZ + r.NextIntn(aqRangeXZ)
		c.located = true
	}
	return c
}

func (a *aquifer) statusOf(c *aqCenter) fluidStatus {
	if !c.known {
		c.status = a.computeFluid(c.x, c.y, c.z)
		c.known = true
	}
	return c.status
}

// substance answers for an open (or barrier-closed) block: placed is
// false when the block stays solid, and update reports a fluid that
// needs a post-generation update.
func (a *aquifer) substance(p Point, density float64) (b NoiseBlock, placed, update bool) {
	if density > 0 {
		return 0, false, false
	}
	f := a.f
	global := f.globalFluid(p.Y)
	if p.Y > a.skipAboveY {
		return global.at(p.Y), true, false
	}
	if global.at(p.Y) == NBLava {
		return NBLava, true, false
	}

	gx0 := (p.X + aqOffsetXZ) >> 4
	gy0 := floorDiv32(p.Y+aqOffsetY, aqSpacingY)
	gz0 := (p.Z + aqOffsetXZ) >> 4
	// The four nearest centers by squared distance; a later center at an
	// equal distance goes ahead of an earlier one.
	var dist [4]int32
	var near [4]*aqCenter
	for i := range dist {
		dist[i] = math.MaxInt32
	}
	for dx := int32(0); dx <= 1; dx++ {
		for dy := int32(-1); dy <= 1; dy++ {
			for dz := int32(0); dz <= 1; dz++ {
				c := a.center(gx0+dx, gy0+dy, gz0+dz)
				ex, ey, ez := c.x-p.X, c.y-p.Y, c.z-p.Z
				d := ex*ex + ey*ey + ez*ez
				for i := 0; i < 4; i++ {
					if dist[i] >= d {
						copy(dist[i+1:], dist[i:3])
						copy(near[i+1:], near[i:3])
						dist[i], near[i] = d, c
						break
					}
				}
			}
		}
	}

	s1 := a.statusOf(near[0])
	sim12 := aqSimilarity(dist[0], dist[1])
	block := s1.at(p.Y)
	if sim12 <= 0 {
		update = sim12 >= aqFlowingSimilarity && s1 != a.statusOf(near[1])
		return block, true, update
	}
	if block == NBWater && f.globalFluid(p.Y-1).at(p.Y-1) == NBLava {
		return block, true, true
	}
	barrier := math.NaN()
	s2 := a.statusOf(near[1])
	if density+sim12*a.pressure(p, &barrier, s1, s2) > 0 {
		return 0, false, false
	}
	s3 := a.statusOf(near[2])
	sim13 := aqSimilarity(dist[0], dist[2])
	if sim13 > 0 && density+sim12*sim13*a.pressure(p, &barrier, s1, s3) > 0 {
		return 0, false, false
	}
	sim23 := aqSimilarity(dist[1], dist[2])
	if sim23 > 0 && density+sim12*sim23*a.pressure(p, &barrier, s2, s3) > 0 {
		return 0, false, false
	}
	switch {
	case s1 != s2, sim23 >= aqFlowingSimilarity && s2 != s3, sim13 >= aqFlowingSimilarity && s1 != s3:
		update = true
	default:
		update = sim13 >= aqFlowingSimilarity && aqSimilarity(dist[0], dist[3]) >= aqFlowingSimilarity &&
			s1 != a.statusOf(near[3])
	}
	return block, true, update
}

// pressure is how strongly two neighbouring aquifers push a barrier
// into the block between them: always high between water and lava,
// otherwise peaking around the mean of their levels and modulated by
// the barrier noise (sampled at most once per block).
func (a *aquifer) pressure(p Point, barrier *float64, s1, s2 fluidStatus) float64 {
	b1, b2 := s1.at(p.Y), s2.at(p.Y)
	if (b1 == NBLava && b2 == NBWater) || (b1 == NBWater && b2 == NBLava) {
		return 2
	}
	diff := s1.level - s2.level
	if diff < 0 {
		diff = -diff
	}
	if diff == 0 {
		return 0
	}
	mid := 0.5 * float64(s1.level+s2.level)
	off := float64(p.Y) + 0.5 - mid
	half := float64(diff) / 2
	rest := half - math.Abs(off)
	var q float64
	if off > 0 {
		v := 0 + rest
		if v > 0 {
			q = v / 1.5
		} else {
			q = v / 2.5
		}
	} else {
		v := 3 + rest
		if v > 0 {
			q = v / 3
		} else {
			q = v / 10
		}
	}
	var noise float64
	if q >= -2 && q <= 2 {
		if math.IsNaN(*barrier) {
			*barrier = a.f.router.Barrier.Compute(p)
		}
		noise = *barrier
	}
	return 2 * (noise + q)
}

// computeFluid decides an aquifer center's fluid surface from the
// surface heights around it.
func (a *aquifer) computeFluid(x, y, z int32) fluidStatus {
	f := a.f
	global := f.globalFluid(y)
	minSurface := int32(math.MaxInt32)
	up, down := y+12, y-12
	underGlobal := false
	for _, o := range aqSurfaceOffsets {
		sx, sz := x+o[0]<<4, z+o[1]<<4
		s := f.preliminarySurface(sx, sz)
		adj := s + 8
		isCenter := o[0] == 0 && o[1] == 0
		if isCenter && down > adj {
			return global
		}
		above := up > adj
		if above || isCenter {
			st := f.globalFluid(adj)
			if st.at(adj) != NBAir {
				if isCenter {
					underGlobal = true
				}
				if above {
					return st
				}
			}
		}
		minSurface = min(minSurface, s)
	}
	level := a.surfaceLevel(x, y, z, global, minSurface, underGlobal)
	return fluidStatus{level, a.fluidType(x, y, z, global, level)}
}

func (a *aquifer) surfaceLevel(x, y, z int32, global fluidStatus, minSurface int32, underGlobal bool) int32 {
	f := a.f
	p := Point{x, y, z}
	var partial, full float64
	if f.router.Erosion.Compute(p) < -0.22499999403953552 && f.router.Depth.Compute(p) > 0.8999999761581421 {
		// Deep dark regions stay dry.
		partial, full = -1, -1
	} else {
		var nearSurface float64
		if underGlobal {
			nearSurface = clampedMap(float64(minSurface+8-y), 0, 64, 1, 0)
		}
		flood := clampD(f.router.FluidLevelFloodedness.Compute(p), -1, 1)
		fullAt := mapRange(nearSurface, 1, 0, -0.3, 0.8)
		partialAt := mapRange(nearSurface, 1, 0, -0.8, 0.4)
		partial = flood - partialAt
		full = flood - fullAt
	}
	switch {
	case full > 0:
		return global.level
	case partial > 0:
		return a.randomizedLevel(x, y, z, minSurface)
	}
	return wayBelowMinY
}

// randomizedLevel picks a fluid level per 16×40×16 region, spread
// around the region's middle in steps of 3 and capped at the surface.
func (a *aquifer) randomizedLevel(x, y, z, minSurface int32) int32 {
	gx, gy, gz := floorDiv32(x, 16), floorDiv32(y, 40), floorDiv32(z, 16)
	mid := gy*40 + 20
	off := a.f.router.FluidLevelSpread.Compute(Point{gx, gy, gz}) * 10
	q := floorInt(off/3) * 3
	return min(minSurface, mid+q)
}

// fluidType turns deep aquifers to lava in patches of a 64×40×64 noise.
func (a *aquifer) fluidType(x, y, z int32, global fluidStatus, level int32) NoiseBlock {
	t := global.fluid
	if level <= -10 && level != wayBelowMinY && global.fluid != NBLava {
		v := a.f.router.Lava.Compute(Point{floorDiv32(x, 64), floorDiv32(y, 40), floorDiv32(z, 64)})
		if math.Abs(v) > 0.3 {
			t = NBLava
		}
	}
	return t
}
