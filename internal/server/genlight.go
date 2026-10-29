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
// emitters spread, and so do the emitters of already-lit neighbours,
// whose light could not enter this chunk before its layers existed.
// Spreading only ever raises cells, so re-seeding is idempotent.

// genEmitter is one generated light source.
type genEmitter struct {
	p     world.Pos
	level int
}

// genEmitters lists a provided chunk's emitters (cached), or nil when
// the chunk has not been generated yet.
func genEmitters(d *dimension, cx, cz int) []genEmitter {
	ck := [2]int{cx, cz}
	if e, ok := d.genEmit[ck]; ok {
		return e
	}
	c := d.w.PeekBaseChunk(cx, cz)
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
	for dz := -1; dz <= 1; dz++ {
		for dx := -1; dx <= 1; dx++ {
			nk := [2]int{cx + dx, cz + dz}
			if nk != ck && !d.genLit[nk] {
				continue
			}
			for _, e := range genEmitters(d, nk[0], nk[1]) {
				// A runtime edit may have replaced the source since.
				if lv := lightEmit(d.w.GetBlock(e.p)); lv > 0 {
					spreadBlockLight(d, e.p, lv, pre)
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
	ensureSkyChunk(d, cx, cz)
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
