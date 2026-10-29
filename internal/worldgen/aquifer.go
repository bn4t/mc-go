package worldgen

import "math"

// fluidStatus is Aquifer.FluidStatus: fluid below level, air above.
type fluidStatus struct {
	level int32
	typ   Substance
}

func (f fluidStatus) at(y int32) Substance {
	if y < f.level {
		return f.typ
	}
	return SubAir
}

// wayBelowMinY is DimensionType.WAY_BELOW_MIN_Y.
const wayBelowMinY = -2032 << 4

// globalFluid is NoiseBasedChunkGenerator.createFluidPicker: lava
// below min(-54, sea level), the default fluid up to sea level.
func (rs *RandomState) globalFluid(y int32) fluidStatus {
	sea := rs.settings.seaLevel
	if y < min(-54, sea) {
		return fluidStatus{level: -54, typ: SubLava}
	}
	return fluidStatus{level: sea, typ: rs.settings.defaultFluid}
}

// aquifer is Aquifer.NoiseBasedAquifer: a jittered 16x12x16 grid of
// local fluid bodies whose levels follow the terrain, separated from
// each other by barrier pressure.
type aquifer struct {
	nc                   *NoiseChunk
	rs                   *RandomState
	cache                []*fluidStatus
	loc                  []int64
	locSet               []bool
	skipAboveY           int32
	sched                bool // shouldScheduleFluidUpdate from the last computeSubstance
	minGridX, minGridY   int32
	minGridZ             int32
	gridSizeX, gridSizeZ int32
}

func aqGridX(x int32) int32        { return x >> 4 }
func aqFromGridX(g, o int32) int32 { return g<<4 + o }
func aqGridY(y int32) int32        { return floorDiv(y, 12) }
func aqFromGridY(g, o int32) int32 { return g*12 + o }

var surfaceSamplingOffsets = [13][2]int32{
	{0, 0}, {-2, -1}, {-1, -1}, {0, -1}, {1, -1}, {-3, 0}, {-2, 0}, {-1, 0}, {1, 0}, {-2, 1}, {-1, 1}, {0, 1}, {1, 1},
}

var flowingUpdateSimilarity = similarity(10*10, 12*12)

func similarity(a, b int32) float64 { return 1 - float64(b-a)/25 }

func newAquifer(nc *NoiseChunk, cx, cz int32, rs *RandomState, minY, height int32) *aquifer {
	a := &aquifer{nc: nc, rs: rs}
	minX, maxX := cx*16, cx*16+15
	minZ, maxZ := cz*16, cz*16+15
	a.minGridX = aqGridX(minX - 5)
	maxGridX := aqGridX(maxX-5) + 1
	a.gridSizeX = maxGridX - a.minGridX + 1
	a.minGridY = aqGridY(minY+1) - 1
	maxGridY := aqGridY(minY+height+1) + 1
	sizeY := maxGridY - a.minGridY + 1
	a.minGridZ = aqGridZ(minZ - 5)
	maxGridZ := aqGridZ(maxZ-5) + 1
	a.gridSizeZ = maxGridZ - a.minGridZ + 1
	n := a.gridSizeX * sizeY * a.gridSizeZ
	a.cache = make([]*fluidStatus, n)
	a.loc = make([]int64, n)
	a.locSet = make([]bool, n)
	top := nc.maxPreliminarySurfaceLevel(aqFromGridX(a.minGridX, 0), aqFromGridX(a.minGridZ, 0), aqFromGridX(maxGridX, 9), aqFromGridX(maxGridZ, 9)) + 8
	g := aqGridY(top+12) + 1
	a.skipAboveY = aqFromGridY(g, 11) - 1
	return a
}

func aqGridZ(z int32) int32 { return z >> 4 }

func (a *aquifer) index(x, y, z int32) int32 {
	return ((y-a.minGridY)*a.gridSizeZ+(z-a.minGridZ))*a.gridSizeX + (x - a.minGridX)
}

// BlockPos.asLong packing (26 bits x/z, 12 bits y).
func packPos(x, y, z int32) int64 {
	return (int64(x)&0x3FFFFFF)<<38 | (int64(y) & 0xFFF) | (int64(z)&0x3FFFFFF)<<12
}

func unpackPos(l int64) (int32, int32, int32) {
	x := int32(l << 0 >> 38)
	y := int32(l << 52 >> 52)
	z := int32(l << 26 >> 38)
	return x, y, z
}

func (a *aquifer) status(idx int32) fluidStatus {
	if s := a.cache[idx]; s != nil {
		return *s
	}
	x, y, z := unpackPos(a.loc[idx])
	s := a.computeFluid(x, y, z)
	a.cache[idx] = &s
	return s
}

// computeSubstance returns (substance, true) for a fluid/air decision
// or (_, false) for "solid here" (vanilla's null), plus the
// shouldScheduleFluidUpdate flag.
func (a *aquifer) computeSubstance(c *Ctx, density float64) (Substance, bool, bool) {
	sub, ok, sched := a.compute(c, density)
	a.sched = sched
	return sub, ok, sched
}

func (a *aquifer) compute(c *Ctx, density float64) (Substance, bool, bool) {
	if density > 0 {
		return 0, false, false
	}
	x, y, z := c.X, c.Y, c.Z
	global := a.rs.globalFluid(y)
	if y > a.skipAboveY {
		return global.at(y), true, false
	}
	if global.at(y) == SubLava {
		return SubLava, true, false
	}
	gx := aqGridX(x - 5)
	gy := aqGridY(y + 1)
	gz := aqGridZ(z - 5)
	d1, d2, d3, d4 := int32(math.MaxInt32), int32(math.MaxInt32), int32(math.MaxInt32), int32(math.MaxInt32)
	var i1, i2, i3, i4 int32
	for dx := int32(0); dx <= 1; dx++ {
		for dy := int32(-1); dy <= 1; dy++ {
			for dz := int32(0); dz <= 1; dz++ {
				cx, cy, cz := gx+dx, gy+dy, gz+dz
				idx := a.index(cx, cy, cz)
				var l int64
				if a.locSet[idx] {
					l = a.loc[idx]
				} else {
					r := a.rs.aquiferRandom.At(cx, cy, cz)
					px := aqFromGridX(cx, r.NextIntN(10))
					py := aqFromGridY(cy, r.NextIntN(9))
					pz := aqFromGridX(cz, r.NextIntN(10))
					l = packPos(px, py, pz)
					a.loc[idx] = l
					a.locSet[idx] = true
				}
				px, py, pz := unpackPos(l)
				ex, ey, ez := px-x, py-y, pz-z
				dist := ex*ex + ey*ey + ez*ez
				switch {
				case d1 >= dist:
					i4, i3, i2, i1 = i3, i2, i1, idx
					d4, d3, d2, d1 = d3, d2, d1, dist
				case d2 >= dist:
					i4, i3, i2 = i3, i2, idx
					d4, d3, d2 = d3, d2, dist
				case d3 >= dist:
					i4, i3 = i3, idx
					d4, d3 = d3, dist
				case d4 >= dist:
					i4 = idx
					d4 = dist
				}
			}
		}
	}
	st1 := a.status(i1)
	sim12 := similarity(d1, d2)
	state := st1.at(y)
	if sim12 <= 0 {
		sched := false
		if sim12 >= flowingUpdateSimilarity {
			sched = st1 != a.status(i2)
		}
		return state, true, sched
	}
	if state == SubWater && a.rs.globalFluid(y-1).at(y-1) == SubLava {
		return state, true, true
	}
	barrier := math.NaN()
	st2 := a.status(i2)
	p := float64(sim12 * a.pressure(c, &barrier, st1, st2))
	if density+p > 0 {
		return 0, false, false
	}
	st3 := a.status(i3)
	sim13 := similarity(d1, d3)
	if sim13 > 0 {
		q := float64(float64(sim12*sim13) * a.pressure(c, &barrier, st1, st3))
		if density+q > 0 {
			return 0, false, false
		}
	}
	sim23 := similarity(d2, d3)
	if sim23 > 0 {
		q := float64(float64(sim12*sim23) * a.pressure(c, &barrier, st2, st3))
		if density+q > 0 {
			return 0, false, false
		}
	}
	b1 := st1 != st2
	b2 := sim23 >= flowingUpdateSimilarity && st2 != st3
	b3 := sim13 >= flowingUpdateSimilarity && st1 != st3
	var sched bool
	if b1 || b2 || b3 {
		sched = true
	} else {
		sched = sim13 >= flowingUpdateSimilarity && similarity(d1, d4) >= flowingUpdateSimilarity && st1 != a.status(i4)
	}
	return state, true, sched
}

func (a *aquifer) pressure(c *Ctx, barrier *float64, s1, s2 fluidStatus) float64 {
	y := c.Y
	b1 := s1.at(y)
	b2 := s2.at(y)
	if (b1 == SubLava && b2 == SubWater) || (b1 == SubWater && b2 == SubLava) {
		return 2
	}
	diff := s1.level - s2.level
	if diff < 0 {
		diff = -diff
	}
	if diff == 0 {
		return 0
	}
	mid := float64(0.5 * float64(s1.level+s2.level))
	e := float64(y) + 0.5 - mid
	half := float64(diff) / 2
	o := half - math.Abs(e)
	var q float64
	if e > 0 {
		r := 0 + o
		if r > 0 {
			q = r / 1.5
		} else {
			q = r / 2.5
		}
	} else {
		r := 3 + o
		if r > 0 {
			q = r / 3
		} else {
			q = r / 10
		}
	}
	var noise float64
	if q < -2 || q > 2 {
		noise = 0
	} else {
		if math.IsNaN(*barrier) {
			*barrier = a.rs.router.Barrier.Compute(c)
		}
		noise = *barrier
	}
	return float64(2 * (noise + q))
}

func (a *aquifer) computeFluid(x, y, z int32) fluidStatus {
	global := a.rs.globalFluid(y)
	minPrelim := int32(math.MaxInt32)
	up := y + 12
	down := y - 12
	flag := false
	for _, off := range surfaceSamplingOffsets {
		sx := x + off[0]*16
		sz := z + off[1]*16
		prelim := a.nc.preliminarySurfaceLevel(sx, sz)
		adj := prelim + 8
		center := off[0] == 0 && off[1] == 0
		if center && down > adj {
			return global
		}
		above := up > adj
		if above || center {
			fs := a.rs.globalFluid(adj)
			if fs.at(adj) != SubAir {
				if center {
					flag = true
				}
				if above {
					return fs
				}
			}
		}
		minPrelim = min(minPrelim, prelim)
	}
	level := a.surfaceLevel(x, y, z, global, minPrelim, flag)
	return fluidStatus{level: level, typ: a.fluidType(x, y, z, global, level)}
}

func (a *aquifer) surfaceLevel(x, y, z int32, global fluidStatus, minPrelim int32, flag bool) int32 {
	p := &Ctx{X: x, Y: y, Z: z, nc: a.nc}
	r := a.rs.router
	var floodLo, floodHi float64
	if r.Erosion.Compute(p) < -0.22499999403953552 && r.Depth.Compute(p) > 0.8999999761581421 {
		floodLo, floodHi = -1, -1
	} else {
		dist := minPrelim + 8 - y
		var f float64
		if flag {
			f = clampedMap(float64(dist), 0, 64, 1, 0)
		}
		flood := clampD(r.FluidFloodedness.Compute(p), -1, 1)
		lo := lerp(inverseLerp(f, 1, 0), -0.3, 0.8)
		hi := lerp(inverseLerp(f, 1, 0), -0.8, 0.4)
		floodLo = flood - hi
		floodHi = flood - lo
	}
	if floodHi > 0 {
		return global.level
	}
	if floodLo > 0 {
		return a.randomizedLevel(x, y, z, minPrelim)
	}
	return wayBelowMinY
}

func (a *aquifer) randomizedLevel(x, y, z, minPrelim int32) int32 {
	gx := floorDiv(x, 16)
	gy := floorDiv(y, 40)
	gz := floorDiv(z, 16)
	base := gy*40 + 20
	spread := float64(a.rs.router.FluidSpread.Compute(&Ctx{X: gx, Y: gy, Z: gz, nc: a.nc}) * 10)
	q := floorI(spread/3) * 3
	return min(minPrelim, base+q)
}

func (a *aquifer) fluidType(x, y, z int32, global fluidStatus, level int32) Substance {
	t := global.typ
	if level <= -10 && level != wayBelowMinY && global.typ != SubLava {
		gx := floorDiv(x, 64)
		gy := floorDiv(y, 40)
		gz := floorDiv(z, 64)
		if math.Abs(a.rs.router.Lava.Compute(&Ctx{X: gx, Y: gy, Z: gz, nc: a.nc})) > 0.3 {
			t = SubLava
		}
	}
	return t
}
