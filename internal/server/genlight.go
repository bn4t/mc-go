package server

import (
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Generated terrain carries its own light sources (lava, magma, glow
// lichen, ...). Vanilla's LIGHT status runs propagateLightSources over
// every emitter a new chunk holds, spreading into whatever neighbouring
// sections already have a light layer. mc-go lights a provided chunk
// lazily when it is first encoded: its layers are created, its own
// emitters spread, and the light of already-lit neighbours, which could
// not enter this chunk before its layers existed, re-floods in from
// their border cells. Spreading only ever raises cells.

// genEmitter is one generated light source.
type genEmitter struct {
	p     world.Pos
	level int
}

// genEmitters lists a provided chunk's emitters (cached), generating
// the chunk if needed.
func genEmitters(d *dimension, cx, cz int) []genEmitter {
	ck := [2]int{cx, cz}
	if e, ok := d.genEmit[ck]; ok {
		return e
	}
	c := d.w.BaseChunk(cx, cz)
	if c == nil {
		return nil
	}
	var out []genEmitter
	for si, sec := range c.Sections {
		if sec == nil {
			continue
		}
		for i, b := range sec {
			if b == world.Air {
				continue
			}
			if lv := worldgen.LightEmission(b); lv > 0 {
				out = append(out, genEmitter{world.Pos{X: cx*16 + i&15, Y: c.MinY + si*16 + i>>8, Z: cz*16 + i>>4&15}, lv})
			}
		}
	}
	if d.genEmit == nil {
		d.genEmit = map[[2]int][]genEmitter{}
	}
	d.genEmit[ck] = out
	return out
}

// seedGenBlockLight runs the generated-emitter pass for chunk (cx, cz)
// once, after its light layers exist. Caller holds d.lightMu.
func seedGenBlockLight(d *dimension, cx, cz int) {
	if !d.w.Provided() {
		return
	}
	ck := [2]int{cx, cz}
	if d.genLit[ck] {
		return
	}
	if d.genLit == nil {
		d.genLit = map[[2]int]bool{}
	}
	d.genLit[ck] = true
	pre := map[world.Pos]int{}
	for _, e := range genEmitters(d, cx, cz) {
		// A runtime edit may have replaced the source since.
		if lv := lightEmit(d.w.GetBlock(e.p)); lv > 0 {
			spreadBlockLight(d, e.p, lv, pre)
		}
	}
	// Light the lit neighbours spread before this chunk had layers
	// stopped at the border: re-flood from their border cells.
	for _, side := range [4][2]int{{-1, 0}, {1, 0}, {0, -1}, {0, 1}} {
		nk := [2]int{cx + side[0], cz + side[1]}
		if !d.genLit[nk] {
			continue
		}
		for y := d.minY; y < d.minY+d.height; y++ {
			for i := 0; i < 16; i++ {
				var p world.Pos
				switch {
				case side[0] < 0:
					p = world.Pos{X: nk[0]*16 + 15, Y: y, Z: cz*16 + i}
				case side[0] > 0:
					p = world.Pos{X: nk[0] * 16, Y: y, Z: cz*16 + i}
				case side[1] < 0:
					p = world.Pos{X: cx*16 + i, Y: y, Z: nk[1]*16 + 15}
				default:
					p = world.Pos{X: cx*16 + i, Y: y, Z: nk[1] * 16}
				}
				if v := int(d.blockLight.get(p)); v > 1 {
					spreadBlockLight(d, p, v, pre)
				}
			}
		}
	}
	// The chunk's own packet carries this light; only the neighbours
	// it reached need an update.
	delete(d.lightDirty, ck)
}

// genSkyChunk is ensureSkyChunk for a provided world. The chunk's own
// sources flood into its lit neighbours, but light those neighbours
// produced before this chunk had layers stopped at the border; vanilla
// creates every layer (INITIALIZE_LIGHT) before any neighbour lights,
// so it is pulled in here: each lit neighbour's border cells propagate
// into this chunk. Caller holds d.lightMu.
func genSkyChunk(d *dimension, cx, cz int) {
	ck := [2]int{cx, cz}
	if d.skyGen[ck] {
		return
	}
	ensureSkyChunkCore(d, cx, cz)
	var inc, dec []lqItem
	for _, side := range [4]struct {
		dx, dz int
		dir    uint8
	}{{-1, 0, skyDirE}, {1, 0, skyDirW}, {0, -1, skyDirS}, {0, 1, skyDirN}} {
		nk := [2]int{cx + side.dx, cz + side.dz}
		if !d.skyGen[nk] {
			continue
		}
		for sec := 0; sec < d.sections(); sec++ {
			if !skyStoring(d, nk, sec) || !skyStoring(d, ck, sec) {
				continue
			}
			y0 := d.minY + sec*16
			for y := y0; y < y0+16; y++ {
				for i := 0; i < 16; i++ {
					var p world.Pos
					switch {
					case side.dx < 0:
						p = world.Pos{X: nk[0]*16 + 15, Y: y, Z: cz*16 + i}
					case side.dx > 0:
						p = world.Pos{X: nk[0] * 16, Y: y, Z: cz*16 + i}
					case side.dz < 0:
						p = world.Pos{X: cx*16 + i, Y: y, Z: nk[1]*16 + 15}
					default:
						p = world.Pos{X: cx*16 + i, Y: y, Z: nk[1] * 16}
					}
					if v := skyStored(d, p); v > 1 {
						inc = append(inc, lqItem{p: p, v: v, dirs: 1 << side.dir})
					}
				}
			}
		}
	}
	skyDrainInc(d, &inc, &dec, false)
	for len(dec) > 0 {
		skyDrainDec(d, &dec, &inc, false)
		skyDrainInc(d, &inc, &dec, false)
	}
}

// genBlockLayers creates a skylight-less provided chunk's block-light
// layers: the sections the shared eligibility rule (a non-empty section
// in the 3x3x3 box) gives a layer. Caller holds d.lightMu.
func genBlockLayers(d *dimension, cx, cz int) {
	ck := [2]int{cx, cz}
	if d.skyGen[ck] {
		return
	}
	if d.skyGen == nil {
		d.skyGen = map[[2]int]bool{}
	}
	d.skyGen[ck] = true
	for sec := 0; sec < d.sections(); sec++ {
		if !lightEligNow(d, ck, sec) {
			continue
		}
		if d.lightLayered == nil {
			d.lightLayered = map[[2]int]map[int]bool{}
		}
		if d.lightLayered[ck] == nil {
			d.lightLayered[ck] = map[int]bool{}
		}
		d.lightLayered[ck][sec] = true
		if d.blkLayered == nil {
			d.blkLayered = map[[2]int]map[int]bool{}
		}
		if d.blkLayered[ck] == nil {
			d.blkLayered[ck] = map[int]bool{}
		}
		d.blkLayered[ck][sec] = true
	}
}

// genBelowWorldSky is the sky layer of a generated column's below-world
// slot (section -1): nil unless a bottom section in the 3x3 columns is
// non-empty (the layer eligibility rule). Its cells read 15 under open
// columns (nothing above the floor) and fall off by one per horizontal
// step from them — light only reaches the slot sideways, never through
// a column's blocks. Caller holds d.lightMu.
func genBelowWorldSky(d *dimension, cx, cz int) []byte {
	if !belowWorldLayered(d, cx, cz) {
		return nil
	}
	// Distance field over the 48x48 block area of the 3x3 columns.
	const n = 48
	var dist [n * n]int
	var q []int
	for i := range dist {
		x, z := cx*16-16+i%n, cz*16-16+i/n
		if skyTop(d, x, z) < int32(d.minY) {
			dist[i] = 0
			q = append(q, i)
		} else {
			dist[i] = 99
		}
	}
	for len(q) > 0 {
		i := q[0]
		q = q[1:]
		x, z := i%n, i/n
		for _, o := range [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nx, nz := x+o[0], z+o[1]
			if nx < 0 || nz < 0 || nx >= n || nz >= n {
				continue
			}
			if j := nz*n + nx; dist[j] > dist[i]+1 {
				dist[j] = dist[i] + 1
				q = append(q, j)
			}
		}
	}
	out := make([]byte, 2048)
	for idx := 0; idx < 4096; idx++ {
		x, z := idx&15, idx>>4&15
		v := 15 - dist[(z+16)*n+x+16]
		if v < 0 {
			v = 0
		}
		if idx%2 == 0 {
			out[idx/2] |= byte(v)
		} else {
			out[idx/2] |= byte(v) << 4
		}
	}
	return out
}

func nonZero(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return true
		}
	}
	return false
}

// belowWorldLayered is the layer eligibility of a column's below-world
// slot: a non-empty bottom section in one of the 3x3 columns.
func belowWorldLayered(d *dimension, cx, cz int) bool {
	for dz := -1; dz <= 1; dz++ {
		for dx := -1; dx <= 1; dx++ {
			if nonEmptyCnts(d, cx+dx, cz+dz)[0] > 0 {
				return true
			}
		}
	}
	return false
}

// genLightAround lights the generated chunks around a write before the
// write's own light update runs: in vanilla every chunk a write can
// reach is already lit, and a write here must not materialize an unlit
// neighbour's layers from virtual values. Caller holds d.lightMu.
func genLightAround(d *dimension, pos world.Pos) {
	if !d.w.Provided() {
		return
	}
	cx, cz := pos.X>>4, pos.Z>>4
	for dz := -1; dz <= 1; dz++ {
		for dx := -1; dx <= 1; dx++ {
			ensureSkyChunk(d, cx+dx, cz+dz)
			seedGenBlockLight(d, cx+dx, cz+dz)
		}
	}
}
