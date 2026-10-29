package worldgen

import "math"

// Substance is what the noise stage puts at a position before surface
// rules run: the settings' default block, a fluid, air or an ore-vein
// block.
type Substance uint8

const (
	SubDefault Substance = iota // density > 0 and no vein: default_block
	SubAir
	SubWater // default_fluid in the overworld
	SubLava
	SubCopperOre
	SubRawCopperBlock
	SubGranite
	SubDeepslateIronOre
	SubRawIronBlock
	SubTuff
)

type interpState struct {
	slice0, slice1                                 [][]float64
	n000, n001, n100, n101, n010, n011, n110, n111 float64
	xz00, xz10, xz01, xz11                         float64
	z0, z1, value                                  float64
}

// NoiseChunk is vanilla's NoiseChunk for one 16x16 column: the
// per-chunk caches and cell interpolators that density functions read
// through their marker nodes, plus the aquifer and ore veinifier.
type NoiseChunk struct {
	rs                                 *RandomState
	cellW, cellH                       int32
	cellCountXZ, cellCountY            int32
	cellNoiseMinY                      int32
	firstCellX, firstCellZ             int32
	firstNoiseX, firstNoiseZ           int32
	noiseSizeXZ                        int32
	interpolating, fillingCell         bool
	cellStartX, cellStartY, cellStartZ int32
	inCellX, inCellY, inCellZ          int32
	interps                            []*interpState // by marker id
	interpList                         []*interpState
	interpArgs                         []DF
	flat                               [][]float64 // by marker id; nil until filled
	cellVals                           []float64
	finalDensity                       DF
	psl                                map[[2]int32]int32
	aq                                 *aquifer
	beard                              func(x, y, z int32) float64
	// lazyCell computes cache_all_in_cell values on demand instead of
	// filling the whole cell (single-column height queries).
	lazyCell bool
}

func (nc *NoiseChunk) ctx() *Ctx {
	return &Ctx{X: nc.cellStartX + nc.inCellX, Y: nc.cellStartY + nc.inCellY, Z: nc.cellStartZ + nc.inCellZ, nc: nc, inChunk: true}
}

func quartFromBlock(v int32) int32 { return v >> 2 }
func quartToBlock(v int32) int32   { return v << 2 }

func floorDiv(a, b int32) int32 {
	q := a / b
	if (a%b != 0) && ((a < 0) != (b < 0)) {
		q--
	}
	return q
}

// newNoiseChunk is NoiseChunk.forChunk for chunk (cx, cz).
func newNoiseChunk(rs *RandomState, cx, cz int32) *NoiseChunk {
	return newNoiseChunkCells(rs, 16/(rs.settings.sizeHorizontal*4), cx*16, cz*16)
}

// newNoiseChunkCells is the NoiseChunk constructor for cellCountXZ
// cells from block (minX, minZ); the aquifer covers the chunk holding
// that corner (NoiseBasedChunkGenerator.iterateNoiseColumn builds a
// one-cell chunk this way).
func newNoiseChunkCells(rs *RandomState, cellCountXZ, minX, minZ int32) *NoiseChunk {
	s := rs.settings
	nc := &NoiseChunk{rs: rs}
	nc.cellW = s.sizeHorizontal * 4
	nc.cellH = s.sizeVertical * 4
	nc.cellCountXZ = cellCountXZ
	nc.cellCountY = floorDiv(s.height, nc.cellH)
	nc.cellNoiseMinY = floorDiv(s.minY, nc.cellH)
	cx, cz := minX>>4, minZ>>4
	nc.firstCellX = floorDiv(minX, nc.cellW)
	nc.firstCellZ = floorDiv(minZ, nc.cellW)
	nc.firstNoiseX = quartFromBlock(minX)
	nc.firstNoiseZ = quartFromBlock(minZ)
	nc.noiseSizeXZ = quartFromBlock(nc.cellCountXZ * nc.cellW)
	nc.psl = map[[2]int32]int32{}

	nm := len(rs.markers)
	nc.interps = make([]*interpState, nm)
	nc.flat = make([][]float64, nm)
	for _, m := range rs.markers {
		switch m.kind {
		case mkInterpolated:
			st := &interpState{slice0: allocSlice(nc.cellCountY, nc.cellCountXZ), slice1: allocSlice(nc.cellCountY, nc.cellCountXZ)}
			nc.interps[m.id] = st
			nc.interpList = append(nc.interpList, st)
			nc.interpArgs = append(nc.interpArgs, m.arg)
		case mkFlatCache:
			// Children are wrapped (and so filled) before their parents;
			// marker ids follow parse order, children first.
			size := nc.noiseSizeXZ + 1
			vals := make([]float64, size*size)
			p := Ctx{nc: nc}
			for i := int32(0); i <= nc.noiseSizeXZ; i++ {
				p.X = quartToBlock(nc.firstNoiseX + i)
				for j := int32(0); j <= nc.noiseSizeXZ; j++ {
					p.Z = quartToBlock(nc.firstNoiseZ + j)
					p.Y = 0
					vals[i+j*size] = m.arg.Compute(&p)
				}
			}
			nc.flat[m.id] = vals
		}
	}
	nc.cellVals = make([]float64, nc.cellW*nc.cellW*nc.cellH)
	nc.finalDensity = rs.router.FinalDensity
	if s.aquifersEnabled {
		nc.aq = newAquifer(nc, cx, cz, rs, s.minY, s.height)
	}
	return nc
}

func allocSlice(cellCountY, cellCountXZ int32) [][]float64 {
	s := make([][]float64, cellCountXZ+1)
	for i := range s {
		s[i] = make([]float64, cellCountY+1)
	}
	return s
}

func (nc *NoiseChunk) markerCompute(m *markerDF, c *Ctx) float64 {
	switch m.kind {
	case mkInterpolated:
		if !c.inChunk {
			return m.arg.Compute(c)
		}
		st := nc.interps[m.id]
		if st == nil || !nc.interpolating {
			return m.arg.Compute(c)
		}
		if nc.fillingCell {
			return lerp3(float64(nc.inCellX)/float64(nc.cellW), float64(nc.inCellY)/float64(nc.cellH), float64(nc.inCellZ)/float64(nc.cellW),
				st.n000, st.n100, st.n010, st.n110, st.n001, st.n101, st.n011, st.n111)
		}
		return st.value
	case mkFlatCache:
		vals := nc.flat[m.id]
		if vals == nil {
			return m.arg.Compute(c)
		}
		size := nc.noiseSizeXZ + 1
		i := quartFromBlock(c.X) - nc.firstNoiseX
		j := quartFromBlock(c.Z) - nc.firstNoiseZ
		if i >= 0 && j >= 0 && i < size && j < size {
			return vals[i+j*size]
		}
		return m.arg.Compute(c)
	default:
		// cache_2d, cache_once: pure caches, the value is the argument's.
		return m.arg.Compute(c)
	}
}

// finalDensityAt is the cache_all_in_cell(final_density + beardifier)
// wrapper NoiseChunk installs over the router's final density.
func (nc *NoiseChunk) finalDensityAt(c *Ctx) float64 {
	if c.inChunk && nc.interpolating {
		x, y, z := nc.inCellX, nc.inCellY, nc.inCellZ
		if x >= 0 && y >= 0 && z >= 0 && x < nc.cellW && y < nc.cellH && z < nc.cellW {
			if nc.lazyCell {
				// The value the cell fill would have cached here.
				nc.fillingCell = true
				v := nc.finalDensity.Compute(c) + beardifierDF{}.Compute(c)
				nc.fillingCell = false
				return v
			}
			return nc.cellVals[((nc.cellH-1-y)*nc.cellW+x)*nc.cellW+z]
		}
	}
	return nc.finalDensity.Compute(c) + beardifierDF{}.Compute(c)
}

func (nc *NoiseChunk) fillSlice(first bool, cellX int32) {
	nc.cellStartX = cellX * nc.cellW
	nc.inCellX = 0
	for z := int32(0); z < nc.cellCountXZ+1; z++ {
		nc.cellStartZ = (nc.firstCellZ + z) * nc.cellW
		nc.inCellZ = 0
		for k, st := range nc.interpList {
			slice := st.slice1
			if first {
				slice = st.slice0
			}
			col := slice[z]
			arg := nc.interpArgs[k]
			for y := int32(0); y < nc.cellCountY+1; y++ {
				nc.cellStartY = (y + nc.cellNoiseMinY) * nc.cellH
				nc.inCellY = 0
				col[y] = arg.Compute(nc.ctx())
			}
		}
	}
}

func (nc *NoiseChunk) initializeForFirstCellX() {
	nc.interpolating = true
	nc.fillSlice(true, nc.firstCellX)
}

func (nc *NoiseChunk) advanceCellX(cellX int32) {
	nc.fillSlice(false, nc.firstCellX+cellX+1)
	nc.cellStartX = (nc.firstCellX + cellX) * nc.cellW
}

func (nc *NoiseChunk) selectCellYZ(cellY, cellZ int32) {
	y, z := cellY, cellZ
	for _, st := range nc.interpList {
		st.n000 = st.slice0[z][y]
		st.n001 = st.slice0[z+1][y]
		st.n100 = st.slice1[z][y]
		st.n101 = st.slice1[z+1][y]
		st.n010 = st.slice0[z][y+1]
		st.n011 = st.slice0[z+1][y+1]
		st.n110 = st.slice1[z][y+1]
		st.n111 = st.slice1[z+1][y+1]
	}
	nc.cellStartY = (cellY + nc.cellNoiseMinY) * nc.cellH
	nc.cellStartZ = (nc.firstCellZ + cellZ) * nc.cellW
	if nc.lazyCell {
		return
	}
	nc.fillingCell = true
	i := 0
	for yy := nc.cellH - 1; yy >= 0; yy-- {
		nc.inCellY = yy
		for xx := int32(0); xx < nc.cellW; xx++ {
			nc.inCellX = xx
			for zz := int32(0); zz < nc.cellW; zz++ {
				nc.inCellZ = zz
				c := nc.ctx()
				nc.cellVals[i] = nc.finalDensity.Compute(c) + beardifierDF{}.Compute(c)
				i++
			}
		}
	}
	nc.fillingCell = false
}

func (nc *NoiseChunk) updateForY(y int32, t float64) {
	nc.inCellY = y - nc.cellStartY
	for _, st := range nc.interpList {
		st.xz00 = lerp(t, st.n000, st.n010)
		st.xz10 = lerp(t, st.n100, st.n110)
		st.xz01 = lerp(t, st.n001, st.n011)
		st.xz11 = lerp(t, st.n101, st.n111)
	}
}

func (nc *NoiseChunk) updateForX(x int32, t float64) {
	nc.inCellX = x - nc.cellStartX
	for _, st := range nc.interpList {
		st.z0 = lerp(t, st.xz00, st.xz10)
		st.z1 = lerp(t, st.xz01, st.xz11)
	}
}

func (nc *NoiseChunk) updateForZ(z int32, t float64) {
	nc.inCellZ = z - nc.cellStartZ
	for _, st := range nc.interpList {
		st.value = lerp(t, st.z0, st.z1)
	}
}

func (nc *NoiseChunk) swapSlices() {
	for _, st := range nc.interpList {
		st.slice0, st.slice1 = st.slice1, st.slice0
	}
}

// preliminarySurfaceLevel is the per-column cached, quart-aligned
// router preliminary_surface_level, floored.
func (nc *NoiseChunk) preliminarySurfaceLevel(x, z int32) int32 {
	qx := quartToBlock(quartFromBlock(x))
	qz := quartToBlock(quartFromBlock(z))
	k := [2]int32{qx, qz}
	if v, ok := nc.psl[k]; ok {
		return v
	}
	v := floorI(nc.rs.router.PreliminarySurfaceLevel.Compute(&Ctx{X: qx, Y: 0, Z: qz, nc: nc}))
	nc.psl[k] = v
	return v
}

func (nc *NoiseChunk) maxPreliminarySurfaceLevel(minX, minZ, maxX, maxZ int32) int32 {
	best := int32(math.MinInt32)
	for z := minZ; z <= maxZ; z += 4 {
		for x := minX; x <= maxX; x += 4 {
			if v := nc.preliminarySurfaceLevel(x, z); v > best {
				best = v
			}
		}
	}
	return best
}

// substance is the BlockStateFiller chain at the current interpolation
// position: aquifer first, then the ore veinifier, else default block.
func (nc *NoiseChunk) substance() (Substance, bool) {
	c := nc.ctx()
	d := nc.finalDensityAt(c)
	var sub Substance
	var ok bool
	sched := false
	if nc.aq != nil {
		sub, ok, sched = nc.aq.computeSubstance(c, d)
	} else if d <= 0 {
		sub, ok = nc.rs.globalFluid(c.Y).at(c.Y), true
	}
	if ok {
		return sub, sched
	}
	if nc.rs.settings.oreVeinsEnabled {
		if v, hit := nc.oreVein(c); hit {
			return v, false
		}
	}
	return SubDefault, false
}

func (nc *NoiseChunk) oreVein(c *Ctx) (Substance, bool) {
	r := nc.rs.router
	toggle := r.VeinToggle.Compute(c)
	copper := toggle > 0
	var minY, maxY int32 = -60, -8
	if copper {
		minY, maxY = 0, 50
	}
	a := math.Abs(toggle)
	up := maxY - c.Y
	down := c.Y - minY
	if down < 0 || up < 0 {
		return 0, false
	}
	edge := min(up, down)
	ro := clampedMap(float64(edge), 0, 20, -0.2, 0)
	if a+ro < 0.4000000059604645 {
		return 0, false
	}
	rnd := nc.rs.oreRandom.At(c.X, c.Y, c.Z)
	if rnd.NextFloat() > 0.7 {
		return 0, false
	}
	if r.VeinRidged.Compute(c) >= 0 {
		return 0, false
	}
	rich := clampedMap(a, 0.4000000059604645, 0.6000000238418579, 0.10000000149011612, 0.30000001192092896)
	if float64(rnd.NextFloat()) < rich && r.VeinGap.Compute(c) > -0.30000001192092896 {
		if rnd.NextFloat() < 0.02 {
			if copper {
				return SubRawCopperBlock, true
			}
			return SubRawIronBlock, true
		}
		if copper {
			return SubCopperOre, true
		}
		return SubDeepslateIronOre, true
	}
	if copper {
		return SubGranite, true
	}
	return SubTuff, true
}

// NoiseColumn holds one chunk's noise-stage substances, indexed
// ((y-minY)*16 + z)*16 + x, and the positions marked for fluid
// post-processing.
type NoiseColumn struct {
	MinY, Height int32
	Cells        []Substance
	FluidMarks   [][3]int32
}

// At returns the substance at chunk-local (x, z) and absolute y.
func (c *NoiseColumn) At(x, y, z int32) Substance {
	return c.Cells[((y-c.MinY)*16+z)*16+x]
}

// FillNoise is NoiseBasedChunkGenerator.doFill for chunk (cx, cz).
func (rs *RandomState) FillNoise(cx, cz int32) *NoiseColumn {
	nc := newNoiseChunk(rs, cx, cz)
	return rs.fillNoise(nc, cx, cz)
}

func (rs *RandomState) fillNoise(nc *NoiseChunk, cx, cz int32) *NoiseColumn {
	s := rs.settings
	col := &NoiseColumn{MinY: s.minY, Height: s.height, Cells: make([]Substance, 16*16*s.height)}
	for i := range col.Cells {
		col.Cells[i] = SubAir
	}
	minX, minZ := cx*16, cz*16
	nc.initializeForFirstCellX()
	w, h := nc.cellW, nc.cellH
	for cellX := int32(0); cellX < 16/w; cellX++ {
		nc.advanceCellX(cellX)
		for cellZ := int32(0); cellZ < 16/w; cellZ++ {
			for cellY := nc.cellCountY - 1; cellY >= 0; cellY-- {
				nc.selectCellYZ(cellY, cellZ)
				for yy := h - 1; yy >= 0; yy-- {
					y := (nc.cellNoiseMinY+cellY)*h + yy
					nc.updateForY(y, float64(yy)/float64(h))
					for xx := int32(0); xx < w; xx++ {
						x := minX + cellX*w + xx
						nc.updateForX(x, float64(xx)/float64(w))
						for zz := int32(0); zz < w; zz++ {
							z := minZ + cellZ*w + zz
							nc.updateForZ(z, float64(zz)/float64(w))
							sub, sched := nc.substance()
							if sub == SubAir {
								continue
							}
							if y < s.minY || y >= s.minY+s.height {
								continue
							}
							col.Cells[((y-s.minY)*16+(z&15))*16+(x&15)] = sub
							if sched && (sub == SubWater || sub == SubLava) {
								col.FluidMarks = append(col.FluidMarks, [3]int32{x, y, z})
							}
						}
					}
				}
			}
		}
		nc.swapSlices()
	}
	nc.interpolating = false
	return col
}
