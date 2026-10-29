package server

import (
	"sort"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Sky lighting: the column heightmap decides which cells see the sun
// (15 intrinsically) and BFS propagation carries the value around
// overhangs at cost max(1, opacity(dest)) per step — measured on the
// 26.2 wire: a single opaque block shadows its own column to 14, the
// centre of a 9x9 platform to 10 (five steps to open sky), a water
// cell counts toward the heightmap (its cell reads 14 = 15-1) while
// glass does not. Above-top cells keep an implicit 15 and are never
// stored; the grid below top is d.skyLight.
//
// Chunk loads keep the captured flat-world light layout (see
// encodeLightData) for byte parity; block changes emit Update Light
// packets computed from this model.

// chunkTops returns the highest non-air Y for each of the 256 columns
// (index z*16+x) of chunk (cx,cz) in dimension d, or minY-1 when empty.
// The chunk is read with one FillBox — a column-batched shard pass —
// rather than 98k per-cell reads, which measured ~50ms per write.
func chunkTops(d *dimension, cx, cz int) [256]int32 {
	var tops [256]int32
	for i := range tops {
		tops[i] = int32(d.minY - 1)
	}
	buf := make([]world.Block, 16*16*d.height)
	d.w.FillBox(
		world.Pos{X: cx * 16, Y: d.minY, Z: cz * 16},
		world.Pos{X: cx*16 + 15, Y: d.minY + d.height - 1, Z: cz*16 + 15},
		buf,
	)
	// FillBox order: x fastest, then z, then y.
	for y := d.height - 1; y >= 0; y-- {
		for z := 0; z < 16; z++ {
			row := (y*16 + z) * 16
			for x := 0; x < 16; x++ {
				if blocksSky(buf[row+x]) && tops[z*16+x] == int32(d.minY-1) {
					tops[z*16+x] = int32(d.minY + y)
				}
			}
		}
	}
	return tops
}

// columnTop returns one column's highest light-occluding Y in
// dimension d (vanilla's sky heightmap looks through transparent
// cells, so a torch or pane sitting above ground moves nothing).
// Reads the column in one FillBox round trip: a column is one shard
// op while per-cell reads are one shard op apiece — over a 384-cell
// column that difference lands inside every setSilentBlock.
func columnTop(d *dimension, x, z int) int32 {
	buf := make([]world.Block, d.height)
	d.w.FillBox(
		world.Pos{X: x, Y: d.minY, Z: z},
		world.Pos{X: x, Y: d.minY + d.height - 1, Z: z},
		buf,
	)
	for y := len(buf) - 1; y >= 0; y-- {
		if blocksSky(buf[y]) {
			return int32(d.minY + y)
		}
	}
	return int32(d.minY - 1)
}

// blocksSky reports whether a cell counts toward the sky top —
// vanilla's SkyLightEngine stops its downward beam at the first cell
// that does not propagatesSkylightDown (per state, from the 26.2 jar
// oracle table: a waterlogged plant stops it, glass does not).
func blocksSky(b world.Block) bool {
	if _, ok := world.StateOwner(b); !ok || b == world.Air {
		return false
	}
	return !worldgen.PropagatesSkylightDown(b)
}

// lightOpacity is the cost light pays entering a cell — vanilla's
// getOpacity = max(1, getLightDampening), the dampening per state from
// the 26.2 jar oracle table (15 for solid-render blocks, 1 for leaves,
// fluids and the rest, 0 for skylight-propagating cells).
func lightOpacity(b world.Block) int {
	if _, ok := world.StateOwner(b); !ok {
		return 1
	}
	return max(1, worldgen.LightDampening(b))
}

// lightDirs6 are the propagation directions — vanilla spreads in the
// six face directions only.
var lightDirs6 = [6][3]int{
	{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}, {0, 1, 0}, {0, -1, 0},
}

// blkStoring is block light's storingLightForSection: only sections
// sharing sky's eligibility set hold a layer (vanilla tracks the same
// LayerLightSectionStorage state for both).
func blkStoring(d *dimension, p world.Pos) bool {
	sec := skySecIdx(d, p.Y)
	return sec >= 0 && sec < d.sections() && d.blkLayered[[2]int{p.X >> 4, p.Z >> 4}][sec]
}

// lightGridSet raises one cell's block light to v when it exceeds the
// stored value, gated on the cell's layer existing; each landed write
// marks its aroundAndAtBlockPos neighbourhood.
func lightGridSet(d *dimension, p world.Pos, v int, pre map[world.Pos]int) bool {
	if !blkStoring(d, p) {
		return false
	}
	if _, seen := pre[p]; !seen {
		pre[p] = int(d.blockLight.get(p))
	}
	if int(d.blockLight.get(p)) >= v {
		return false
	}
	d.blockLight.set(p, uint8(v))
	ck := [2]int{p.X >> 4, p.Z >> 4}
	if d.blkWritten == nil {
		d.blkWritten = map[[2]int]map[int]bool{}
	}
	if d.blkWritten[ck] == nil {
		d.blkWritten[ck] = map[int]bool{}
	}
	d.blkWritten[ck][skySecIdx(d, p.Y)] = true
	lightMarkAround(d, p)
	return true
}

// lightGridDel zeroes one cell, with the same storing gate and mark.
func lightGridDel(d *dimension, p world.Pos, pre map[world.Pos]int) {
	if !blkStoring(d, p) {
		return
	}
	if _, seen := pre[p]; !seen {
		pre[p] = int(d.blockLight.get(p))
	}
	if d.blockLight.del(p) {
		lightMarkAround(d, p)
	}
}

type lightItem struct {
	p world.Pos
	v uint8
}

// blockLightSpread is vanilla's add-source BFS: a cell passes its
// value minus the neighbour's opacity to each face neighbour that
// ends up brighter, queueing the wins as new sources.
func (s *Server) blockLightSpread(d *dimension, pos world.Pos, level int, pre map[world.Pos]int) {
	spreadBlockLight(d, pos, level, pre)
}

func spreadBlockLight(d *dimension, pos world.Pos, level int, pre map[world.Pos]int) {
	if level <= 0 {
		return
	}
	lightGridSet(d, pos, level, pre)
	q := []lightItem{{pos, uint8(level)}}
	for len(q) > 0 {
		it := q[0]
		q = q[1:]
		for _, dp := range lightDirs6 {
			n := world.Pos{X: it.p.X + dp[0], Y: it.p.Y + dp[1], Z: it.p.Z + dp[2]}
			if n.Y < d.minY || n.Y >= d.minY+d.height {
				continue
			}
			nv := int(it.v) - lightOpacity(d.w.GetBlock(n))
			if nv <= 0 {
				continue
			}
			if lightGridSet(d, n, nv, pre) {
				q = append(q, lightItem{n, uint8(nv)})
			}
		}
	}
}

// blockLightRemove is vanilla's remove-source pass: the cell's former
// value floods out, erasing every cell that could only have been lit
// by it (value <= the value it would have fed them) and re-seeding
// the brighter boundary cells — plus any emitters the flood ate —
// back into the spread pass.
func (s *Server) blockLightRemove(d *dimension, pos world.Pos, level int, pre map[world.Pos]int) {
	if level <= 0 {
		return
	}
	lightGridDel(d, pos, pre)
	var readd []lightItem
	q := []lightItem{{pos, uint8(level)}}
	for len(q) > 0 {
		it := q[0]
		q = q[1:]
		for _, dp := range lightDirs6 {
			n := world.Pos{X: it.p.X + dp[0], Y: it.p.Y + dp[1], Z: it.p.Z + dp[2]}
			if n.Y < d.minY || n.Y >= d.minY+d.height {
				continue
			}
			nv := int(d.blockLight.get(n))
			if nv == 0 {
				continue
			}
			exp := int(it.v) - lightOpacity(d.w.GetBlock(n))
			if nv <= exp {
				// n was lit by the erased contribution at most this
				// much — anything dimmer still is definitely ours.
				lightGridDel(d, n, pre)
				q = append(q, lightItem{n, uint8(nv)})
				// An emitter eaten by the flood respreads at its own
				// level — its cell's light is the emission itself.
				if e := lightEmit(d.w.GetBlock(n)); e > 0 {
					readd = append(readd, lightItem{n, uint8(e)})
				}
			} else {
				readd = append(readd, lightItem{n, uint8(nv)})
			}
		}
	}
	for _, it := range readd {
		s.blockLightSpread(d, it.p, int(it.v), pre)
	}
}

// blockLightChanged runs the block-light half of a finished write at
// pos: emission and opacity decide between a pure spread (a new or
// stronger emitter), a remove (a killed emitter or a cell that went
// opaque under light), or both. Touched cells fold into lightDirty
// for the next update_light flush. Returns false when the write
// could not change any light.
func (s *Server) blockLightChanged(d *dimension, pos world.Pos, old, blk world.Block) {
	d.lightMu.Lock()
	defer d.lightMu.Unlock()
	genLightAround(d, pos)
	oe, ne := lightEmit(old), lightEmit(blk)
	if oe == ne && lightOpacity(old) == lightOpacity(blk) {
		return
	}
	if d.blockLight.len() == 0 && ne <= 0 {
		return
	}
	pre := map[world.Pos]int{}
	// Vanilla order: the section's emptiness flip reconciles light
	// layers before either engine's checkNode runs.
	s.lightSectionFlipped(d, pos, old, blk)
	ensureSkyChunk(d, pos.X>>4, pos.Z>>4)
	v := oe
	if ov := int(d.blockLight.get(pos)); ov > v {
		v = ov
	}
	if v > ne {
		s.blockLightRemove(d, pos, v, pre)
	}
	if ne > 0 {
		s.blockLightSpread(d, pos, ne, pre)
	}
	// Vanilla still marks pos's sections when the cell reads
	// inconsistent — recomputed from neighbours it disagrees with
	// what it holds — even when the write touched nothing.
	if len(pre) == 0 && blkStoring(d, pos) && int(d.blockLight.get(pos)) != blkComputedAt(d, pos) {
		lightMarkAround(d, pos)
	}
}

// ---------- sky propagation ----------
//
// Sky light keeps vanilla's sectioned model (SkyLightEngine +
// SkyLightSectionStorage): a section owns a light layer only while it
// or one of its 26 neighbours — the 3x3x3 section box around it,
// across chunk borders — holds a non-empty block section
// (updateSectionStatus's hasData + neighbour-count state). Cells in
// sections without a layer never store a value: reads virtualize —
// everything at or above the column's data top reads 15, a gap below
// it repeats the first layer-above's bottom row — and every write and
// queue entry into them is skipped outright (storingLightForSection).
//
// Marks are the wire's sectionsAffectedByLightUpdates: each stored
// write marks the cell's SectionPos.aroundAndAtBlockPos neighbourhood
// (±1 cell per axis, up to eight sections), and a newly created layer
// marks its own 27-section neighbourhood. Removed layers mark
// nothing — vanilla's toRemove path only rewrites the data map.

// topsFor caches one chunk's 256 sky-heightmap columns (absolute Y,
// index z*16+x). The cache is filled lazily and kept current by
// skyLightChanged, which rewrites the touched column on every write.
func topsFor(d *dimension, cx, cz int) *[256]int32 {
	ck := [2]int{cx, cz}
	if d.skyTops == nil {
		d.skyTops = map[[2]int]*[256]int32{}
	}
	t, ok := d.skyTops[ck]
	if !ok {
		t2 := chunkTops(d, cx, cz)
		t = &t2
		d.skyTops[ck] = t
	}
	return t
}

// skyTop returns the cached sky heightmap Y of one column.
func skyTop(d *dimension, x, z int) int32 {
	return topsFor(d, x>>4, z>>4)[(z&15)*16+(x&15)]
}

// skySecIdx is the 0-based section index of a block Y.
func skySecIdx(d *dimension, y int) int {
	return (y - d.minY) >> 4
}

// nonEmptyCnts returns the chunk's per-section non-air block counts,
// scanned from the world once and then kept current by writes through
// the light engine. Vanilla's version is LevelChunkSection's
// nonEmptyBlockCount, consulted by hasOnlyAir.
func nonEmptyCnts(d *dimension, cx, cz int) map[int]int {
	ck := [2]int{cx, cz}
	if d.nonEmptyCnt == nil {
		d.nonEmptyCnt = map[[2]int]map[int]int{}
	}
	if m, ok := d.nonEmptyCnt[ck]; ok {
		return m
	}
	m := map[int]int{}
	buf := make([]world.Block, 256*d.height)
	d.w.FillBox(
		world.Pos{X: cx * 16, Y: d.minY, Z: cz * 16},
		world.Pos{X: cx*16 + 15, Y: d.minY + d.height - 1, Z: cz*16 + 15},
		buf,
	)
	for sec := 0; sec < d.sections(); sec++ {
		cnt := 0
		base := sec * 16 * 256
		for i := 0; i < 4096; i++ {
			if buf[base+i] != world.Air {
				cnt++
			}
		}
		m[sec] = cnt
	}
	d.nonEmptyCnt[ck] = m
	return m
}

// nonEmptySec reports whether one real section holds any non-air
// block — vanilla's LevelChunkSection.hasOnlyAir.
func nonEmptySec(d *dimension, ck [2]int, sec int) bool {
	if sec < 0 || sec >= d.sections() {
		return false
	}
	return nonEmptyCnts(d, ck[0], ck[1])[sec] > 0
}

// lightEligNow is the live eligibility predicate — vanilla's
// sectionStates != 0 — recomputed from non-empty block sections: a
// light section stores light iff itself or any of the 26 surrounding
// sections is non-empty.
func lightEligNow(d *dimension, ck [2]int, sec int) bool {
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			m := nonEmptyCnts(d, ck[0]+dx, ck[1]+dz)
			for ds := -1; ds <= 1; ds++ {
				s := sec + ds
				if s < 0 || s >= d.sections() {
					continue
				}
				if m[s] > 0 {
					return true
				}
			}
		}
	}
	return false
}

// lightLayered reports whether the eligibility map currently holds a
// layer for the section.
func lightLayered(d *dimension, ck [2]int, sec int) bool {
	if d.lightLayered == nil {
		return false
	}
	return d.lightLayered[ck][sec]
}

// topSkySec is vanilla's per-column topSections value: one past the
// highest materialized sky layer, or the dimension's bottom when the
// column holds none — either way the "above data" predicate below is
// true for every section then.
func topSkySec(d *dimension, ck [2]int) int {
	top := -1
	for sec := range d.skyLayers[ck] {
		if sec > top {
			top = sec
		}
	}
	return top + 1
}

// skyLowestSec is vanilla's storage-wide lowestY: the lowest
// materialized sky section anywhere in the dimension (the bound of
// hasLightDataAtOrBelow). Recomputed lazily and cached.
func skyLowestSec(d *dimension) int {
	if d.skyLowestSet {
		return d.skyLowestSec
	}
	low := d.sections()
	for _, m := range d.skyLayers {
		for sec := range m {
			if sec < low {
				low = sec
			}
		}
	}
	d.skyLowestSec = low
	d.skyLowestSet = true
	return low
}

// skyHasDataAtOrBelow is vanilla's hasLightDataAtOrBelow: sections at
// or below the storage's lowest layer are inside the data range.
func skyHasDataAtOrBelow(d *dimension, sec int) bool {
	return sec >= skyLowestSec(d)
}

// materializeLightSec creates the section's light layers — vanilla's
// initializeSection: the sky array's base is full-15 above the data
// top, else a copy of the first layer above's bottom row
// (repeatFirstLayer); the block side just gains a (zero) layer. The
// section's whole 27-neighbourhood lands on the affected mask.
func materializeLightSec(d *dimension, ck [2]int, sec int) {
	if d.lightLayered == nil {
		d.lightLayered = map[[2]int]map[int]bool{}
	}
	if d.lightLayered[ck] == nil {
		d.lightLayered[ck] = map[int]bool{}
	}
	d.lightLayered[ck][sec] = true
	if d.skyLayers == nil {
		d.skyLayers = map[[2]int]map[int]*[4096]byte{}
	}
	if d.skyLayers[ck] == nil {
		d.skyLayers[ck] = map[int]*[4096]byte{}
	}
	arr := &[4096]byte{}
	written := false
	top := topSkySec(d, ck)
	if sec >= top {
		// vanilla's DataLayer(15) default — isEmpty() is false, so
		// the section always ships its array.
		for i := range arr {
			arr[i] = 15
		}
		written = true
	} else {
		for s := sec + 1; s < top; s++ {
			if ab := d.skyLayers[ck][s]; ab != nil {
				// repeatFirstLayer — a real write pass in vanilla,
				// the layer counts as allocated even when the row
				// it copies is all zero.
				written = true
				for z := 0; z < 16; z++ {
					for x := 0; x < 16; x++ {
						v := ab[z*16+x]
						for y := 0; y < 16; y++ {
							arr[y*256+z*16+x] = v
						}
					}
				}
				break
			}
		}
	}
	d.skyLayers[ck][sec] = arr
	if written {
		markSkyWritten(d, ck, sec)
	}
	if d.blkLayered == nil {
		d.blkLayered = map[[2]int]map[int]bool{}
	}
	if d.blkLayered[ck] == nil {
		d.blkLayered[ck] = map[int]bool{}
	}
	d.blkLayered[ck][sec] = true
	d.skyLowestSet = false
	// initializeSection ends with markSectionAndNeighborsAsAffected:
	// every materialization — the flipped section and neighbour-count
	// transitions alike — marks its own 27-box on both engines, which
	// is how light updates propagate up to two chunks from a write.
	skyMark27(d, ck, sec)
	lightMark27(d, ck, sec)
}

// dematerializeLightSec drops the section's layers — vanilla's
// removeSection/toRemove. It marks nothing.
func dematerializeLightSec(d *dimension, ck [2]int, sec int) {
	if m := d.lightLayered[ck]; m != nil {
		delete(m, sec)
	}
	if m := d.skyLayers[ck]; m != nil {
		delete(m, sec)
	}
	if m := d.blkLayered[ck]; m != nil {
		delete(m, sec)
	}
	// A rematerialized section starts as a fresh DataLayer — back to
	// the never-written state.
	if m := d.blkWritten[ck]; m != nil {
		delete(m, sec)
	}
	if m := d.skyWritten[ck]; m != nil {
		delete(m, sec)
	}
	d.skyLowestSet = false
}

// syncLightSecsAround reconciles the 27-section neighbourhood of a
// block section whose emptiness just flipped — vanilla's
// updateSectionStatus state change. Runs before the light write like
// vanilla's setBlockState ordering.
func syncLightSecsAround(d *dimension, ck [2]int, sec int) {
	for dx := -1; dx <= 1; dx++ {
		for ds := -1; ds <= 1; ds++ {
			ns := sec + ds
			if ns < 0 || ns >= d.sections() {
				continue
			}
			for dz := -1; dz <= 1; dz++ {
				nck := [2]int{ck[0] + dx, ck[1] + dz}
				now := lightEligNow(d, nck, ns)
				was := lightLayered(d, nck, ns)
				switch {
				case now && !was:
					materializeLightSec(d, nck, ns)
				case !now && was:
					dematerializeLightSec(d, nck, ns)
				}
			}
		}
	}
}

// lightSectionFlipped updates the per-section non-air count for a
// write and reconciles layer eligibility around it — vanilla's
// LevelChunkSection empty-flag transition. Returns early when the
// write cannot flip the count.
func (s *Server) lightSectionFlipped(d *dimension, pos world.Pos, old, blk world.Block) {
	oldAir := old == world.Air
	newAir := blk == world.Air
	if oldAir == newAir {
		return
	}
	ck := [2]int{pos.X >> 4, pos.Z >> 4}
	sec := skySecIdx(d, pos.Y)
	if sec < 0 || sec >= d.sections() {
		return
	}
	// The count map is the authoritative running tally: when it was
	// already materialized it still holds the pre-write count — apply
	// the delta. On a fresh scan it already reflects the write — undo
	// the delta to get the pre-write count.
	m, cached := d.nonEmptyCnt[ck]
	post := nonEmptyCnts(d, ck[0], ck[1])[sec]
	var pre int
	if cached {
		pre = post
		if newAir {
			m[sec] = post - 1
		} else {
			m[sec] = post + 1
		}
		post = m[sec]
	} else {
		if newAir {
			pre = post + 1
		} else {
			pre = post - 1
		}
	}
	if (pre > 0) == (post > 0) {
		return
	}
	// putSectionState: the flipped section and its 26 neighbours all
	// take state transitions; materializations mark their own 27-box
	// while removals mark nothing — all handled inside the sync.
	syncLightSecsAround(d, ck, sec)
}

// ensureSkyChunk materializes the chunk's generated sky state the
// first time the engine reads it: eligible sections get their layers
// and the generation-time source pass (propagateLightSources + the
// increase flood) fills them, so below-top cells already hold their
// propagated values before any write touches them. A provided
// (generated) chunk also pulls in light its lit neighbours produced
// before it had layers, and a skylight-less one gets block layers only.
func ensureSkyChunk(d *dimension, cx, cz int) {
	if d.w.Provided() && !d.skyGen[[2]int{cx, cz}] {
		if d.skylight {
			genSkyChunk(d, cx, cz)
		} else {
			genBlockLayers(d, cx, cz)
		}
		return
	}
	ensureSkyChunkCore(d, cx, cz)
}

func ensureSkyChunkCore(d *dimension, cx, cz int) {
	ck := [2]int{cx, cz}
	if d.skyGen == nil {
		d.skyGen = map[[2]int]bool{}
	}
	if d.skyGen[ck] {
		return
	}
	d.skyGen[ck] = true
	if !d.skylight {
		return
	}
	for sec := 0; sec < d.sections(); sec++ {
		if lightEligNow(d, ck, sec) && !lightLayered(d, ck, sec) {
			if d.lightLayered == nil {
				d.lightLayered = map[[2]int]map[int]bool{}
			}
			if d.lightLayered[ck] == nil {
				d.lightLayered[ck] = map[int]bool{}
			}
			d.lightLayered[ck][sec] = true
			if d.skyLayers == nil {
				d.skyLayers = map[[2]int]map[int]*[4096]byte{}
			}
			if d.skyLayers[ck] == nil {
				d.skyLayers[ck] = map[int]*[4096]byte{}
			}
			// Gen layers start empty: the source pass below writes
			// the 15s and the flood does the rest.
			d.skyLayers[ck][sec] = &[4096]byte{}
			if d.blkLayered == nil {
				d.blkLayered = map[[2]int]map[int]bool{}
			}
			if d.blkLayered[ck] == nil {
				d.blkLayered[ck] = map[int]bool{}
			}
			d.blkLayered[ck][sec] = true
			d.skyLowestSet = false
			skyMark27(d, ck, sec)
			lightMark27(d, ck, sec)
		}
	}
	// propagateLightSources: write source 15s into each column's band
	// [lowestSourceY, sectionTop] of every layered section, then run
	// the increase flood. Sources enqueue downward at their band edge
	// and sideways into columns whose band starts lower.
	tops := topsFor(d, cx, cz)
	var inc []lqItem
	var dec []lqItem
	for sec := d.sections() - 1; sec >= 0; sec-- {
		L := d.skyLayers[ck][sec]
		if L == nil {
			continue
		}
		secMinY := d.minY + sec*16
		sourcesBelow := false
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				low := int(tops[z*16+x]) + 1
				if low > secMinY+15 {
					continue
				}
				nLow, sLow, wLow, eLow := low, low, low, low
				if z > 0 {
					nLow = int(tops[(z-1)*16+x]) + 1
				} else {
					nLow = int(skyTop(d, cx*16+x, cz*16-1)) + 1
				}
				if z < 15 {
					sLow = int(tops[(z+1)*16+x]) + 1
				} else {
					sLow = int(skyTop(d, cx*16+x, cz*16+16)) + 1
				}
				if x > 0 {
					wLow = int(tops[z*16+x-1]) + 1
				} else {
					wLow = int(skyTop(d, cx*16-1, cz*16+z)) + 1
				}
				if x < 15 {
					eLow = int(tops[z*16+x+1]) + 1
				} else {
					eLow = int(skyTop(d, cx*16+16, cz*16+z)) + 1
				}
				for y := secMinY + 15; y >= secMinY && y >= low; y-- {
					L[(y&15)*256+z*16+x] = 15
					markSkyWritten(d, ck, sec)
					var dirs uint8
					if y == low {
						dirs |= 1 << skyDirD
					}
					if y < nLow {
						dirs |= 1 << skyDirN
					}
					if y < sLow {
						dirs |= 1 << skyDirS
					}
					if y < wLow {
						dirs |= 1 << skyDirW
					}
					if y < eLow {
						dirs |= 1 << skyDirE
					}
					if dirs != 0 {
						inc = append(inc, lqItem{p: world.Pos{X: cx*16 + x, Y: y, Z: cz*16 + z}, v: 15, dirs: dirs})
					}
				}
				if low < secMinY {
					sourcesBelow = true
				}
			}
		}
		if !sourcesBelow {
			break
		}
	}
	skyDrainInc(d, &inc, &dec, false)
	for len(dec) > 0 {
		skyDrainDec(d, &dec, &inc, false)
		skyDrainInc(d, &inc, &dec, false)
	}
}

// skyStoring is vanilla's storingLightForSection — true while the
// section owns a materialized sky layer.
func skyStoring(d *dimension, ck [2]int, sec int) bool {
	if sec < 0 || sec >= d.sections() {
		return false
	}
	return d.skyLayers[ck][sec] != nil
}

// skyStored is a direct read of a stored cell — callers hold the
// storing guarantee, so the layer is never nil here.
func skyStored(d *dimension, p world.Pos) int {
	return int(d.skyLayers[[2]int{p.X >> 4, p.Z >> 4}][skySecIdx(d, p.Y)][(p.Y&15)*256+(p.Z&15)*16+(p.X&15)])
}

// skySetStored is vanilla's setStoredLevel: write plus the cell's
// aroundAndAtBlockPos neighbourhood on the affected mask.
func skySetStored(d *dimension, p world.Pos, v int) {
	d.skyLayers[[2]int{p.X >> 4, p.Z >> 4}][skySecIdx(d, p.Y)][(p.Y&15)*256+(p.Z&15)*16+(p.X&15)] = byte(v)
	skyMarkAround(d, p)
}

// skyEffective is one cell's live sky value: intrinsic 15 above the
// column top; the stored value in a layered section; and for the
// unlayered gap the first-layer-above's bottom row (or 15 above the
// data top) — SkyLightSectionStorage.getLightValue.
func skyEffective(d *dimension, p world.Pos) int {
	if int32(p.Y) > skyTop(d, p.X, p.Z) {
		return 15
	}
	ck := [2]int{p.X >> 4, p.Z >> 4}
	ensureSkyChunk(d, ck[0], ck[1])
	sec := skySecIdx(d, p.Y)
	if L := d.skyLayers[ck][sec]; L != nil {
		return int(L[(p.Y&15)*256+(p.Z&15)*16+(p.X&15)])
	}
	top := topSkySec(d, ck)
	if sec >= top {
		return 15
	}
	for s := sec + 1; s < top; s++ {
		if L := d.skyLayers[ck][s]; L != nil {
			return int(L[(p.Z&15)*16+(p.X&15)])
		}
	}
	return 15
}

// skyComputedAt re-derives pos's sky value purely from its six
// neighbours — vanilla's computeLevelFromNeighbors.
func skyComputedAt(d *dimension, p world.Pos) int {
	best := 0
	for _, dp := range lightDirs6 {
		n := world.Pos{X: p.X + dp[0], Y: p.Y + dp[1], Z: p.Z + dp[2]}
		if n.Y < d.minY || n.Y >= d.minY+d.height {
			continue
		}
		if v := skyEffective(d, n); v > best {
			best = v
		}
	}
	best -= lightOpacity(d.w.GetBlock(p))
	if best < 0 {
		best = 0
	}
	return best
}

// markSkyWritten records that a sky layer's data was allocated at
// least once — vanilla's DataLayer data!=null state, which decides
// the data mask over the empty mask at emit time.
func markSkyWritten(d *dimension, ck [2]int, sec int) {
	if d.skyWritten == nil {
		d.skyWritten = map[[2]int]map[int]bool{}
	}
	if d.skyWritten[ck] == nil {
		d.skyWritten[ck] = map[int]bool{}
	}
	d.skyWritten[ck][sec] = true
}

// skyMarkSec adds one section to the sky affected set; marks outside
// the real range never ship, same as vanilla.
func skyMarkSec(d *dimension, ck [2]int, sec int) {
	if sec < 0 || sec >= d.sections() {
		return
	}

	if d.skyDirty == nil {
		d.skyDirty = map[[2]int]map[int]bool{}
	}
	if d.skyDirty[ck] == nil {
		d.skyDirty[ck] = map[int]bool{}
	}
	d.skyDirty[ck][sec] = true
}

// skyMarkAround is setStoredLevel's mark: the sections the cell's
// 3x3x3 neighbourhood touches — vanilla's aroundAndAtBlockPos.
func skyMarkAround(d *dimension, p world.Pos) {
	sy0, sy1 := skySecIdx(d, p.Y-1), skySecIdx(d, p.Y+1)
	for sx := (p.X - 1) >> 4; sx <= (p.X+1)>>4; sx++ {
		for sz := (p.Z - 1) >> 4; sz <= (p.Z+1)>>4; sz++ {
			for sy := sy0; sy <= sy1; sy++ {
				skyMarkSec(d, [2]int{sx, sz}, sy)
			}
		}
	}
}

// skyMark27 is initializeSection's mark: the whole 3x3x3 section box
// around the materialized one.
func skyMark27(d *dimension, ck [2]int, sec int) {
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			for ds := -1; ds <= 1; ds++ {
				skyMarkSec(d, [2]int{ck[0] + dx, ck[1] + dz}, sec+ds)
			}
		}
	}
}

// lightMarkSec/lightMark27 are the same two marks on the block-light
// side — vanilla keeps an affected set per storage.
func lightMarkSec(d *dimension, ck [2]int, sec int) {
	if sec < 0 || sec >= d.sections() {
		return
	}
	if d.lightDirty == nil {
		d.lightDirty = map[[2]int]map[int]bool{}
	}
	if d.lightDirty[ck] == nil {
		d.lightDirty[ck] = map[int]bool{}
	}
	d.lightDirty[ck][sec] = true
}

func lightMark27(d *dimension, ck [2]int, sec int) {
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			for ds := -1; ds <= 1; ds++ {
				lightMarkSec(d, [2]int{ck[0] + dx, ck[1] + dz}, sec+ds)
			}
		}
	}
}

// blkComputedAt re-derives pos's block light from its six
// neighbours — the same computeLevelFromNeighbors shape as sky.
func blkComputedAt(d *dimension, p world.Pos) int {
	best := 0
	for _, dp := range lightDirs6 {
		n := world.Pos{X: p.X + dp[0], Y: p.Y + dp[1], Z: p.Z + dp[2]}
		if n.Y < d.minY || n.Y >= d.minY+d.height {
			continue
		}
		if v := int(d.blockLight.get(n)); v > best {
			best = v
		}
	}
	best -= lightOpacity(d.w.GetBlock(p))
	if best < 0 {
		best = 0
	}
	return best
}

func lightMarkAround(d *dimension, p world.Pos) {
	sy0, sy1 := skySecIdx(d, p.Y-1), skySecIdx(d, p.Y+1)
	for sx := (p.X - 1) >> 4; sx <= (p.X+1)>>4; sx++ {
		for sz := (p.Z - 1) >> 4; sz <= (p.Z+1)>>4; sz++ {
			for sy := sy0; sy <= sy1; sy++ {
				lightMarkSec(d, [2]int{sx, sz}, sy)
			}
		}
	}
}

// Queue directions: the six face directions an entry propagates into.
const (
	skyDirE = iota // +x
	skyDirW        // -x
	skyDirS        // +z
	skyDirN        // -z
	skyDirU        // +y
	skyDirD        // -y
)

const skyAllDirs = 0x3f

// lqItem is a QueueEntry: the cell, its level, the directions it
// propagates into, and the emission flag (setStoredLevel-through when
// the stored value trails the target).
type lqItem struct {
	p    world.Pos
	v    int
	dirs uint8
	emit bool
}

func skyOpp(dir int) int { return dir ^ 1 }

// skyCrossedEdge is vanilla's crossedSectionEdge: the direction the
// propagation travelled lands on the named local edge — the gate for
// the downward projection across empty sections.
func skyCrossedEdge(dir int, p world.Pos) bool {
	switch dir {
	case skyDirN:
		return p.Z&15 == 15
	case skyDirS:
		return p.Z&15 == 0
	case skyDirE:
		return p.X&15 == 0
	case skyDirW:
		return p.X&15 == 15
	}
	return false
}

// skyEmptyBelow is countEmptySectionsBelowIfAtBorder: when the cell
// sits on a section's bottom row at its horizontal edge, count the
// non-storing sections directly below it (up to the first storing
// one). Zero everywhere else.
func skyEmptyBelow(d *dimension, p world.Pos) int {
	if p.Y&15 != 0 {
		return 0
	}
	if p.X&15 != 0 && p.X&15 != 15 && p.Z&15 != 0 && p.Z&15 != 15 {
		return 0
	}
	ck := [2]int{p.X >> 4, p.Z >> 4}
	sec := skySecIdx(d, p.Y)
	cnt := 0
	for skyHasDataAtOrBelow(d, sec-cnt-1) && !skyStoring(d, ck, sec-cnt-1) {
		cnt++
	}
	return cnt
}

// skyProjectDown is propagateFromEmptySections: the write's level is
// pushed straight down through the empty-section gap into the first
// layered section below it (filling that column), each carrying the
// opposite-direction skip.
func skyProjectDown(d *dimension, to world.Pos, dir, level int, increase bool, emptyBelow int, dec, inc *[]lqItem) {
	if emptyBelow == 0 || !skyCrossedEdge(dir, to) {
		return
	}
	ck := [2]int{to.X >> 4, to.Z >> 4}
	sec := skySecIdx(d, to.Y) - 1
	bottom := sec - emptyBelow + 1
	for ; sec >= bottom; sec-- {
		if !skyStoring(d, ck, sec) {
			continue
		}
		secMin := d.minY + sec*16
		for ly := 15; ly >= 0; ly-- {
			p2 := world.Pos{X: to.X, Y: secMin + ly, Z: to.Z}
			if increase {
				skySetStored(d, p2, level)
				if level > 1 {
					*inc = append(*inc, lqItem{p: p2, v: level, dirs: skyAllDirs &^ (1 << skyOpp(dir))})
				}
			} else {
				skySetStored(d, p2, 0)
				*dec = append(*dec, lqItem{p: p2, v: level, dirs: skyAllDirs &^ (1 << skyOpp(dir))})
			}
		}
	}
}

// skyPropagateDec is vanilla's propagateDecrease: the target's stored
// level decides between erase-and-reflood (darker or equal) and readd
// (still brighter than the fading light).
func skyPropagateDec(d *dimension, it lqItem, dec, inc *[]lqItem, mark bool) {
	emptyBelow := skyEmptyBelow(d, it.p)
	for dir := 0; dir < 6; dir++ {
		if it.dirs>>dir&1 == 0 {
			continue
		}
		n := world.Pos{X: it.p.X + lightDirs6[dir][0], Y: it.p.Y + lightDirs6[dir][1], Z: it.p.Z + lightDirs6[dir][2]}
		if n.Y < d.minY || n.Y >= d.minY+d.height {
			continue
		}
		ck := [2]int{n.X >> 4, n.Z >> 4}
		if !skyStoring(d, ck, skySecIdx(d, n.Y)) {
			continue
		}
		tl := skyStored(d, n)
		if tl == 0 {
			continue
		}
		if tl <= it.v-1 {
			if mark {
				skySetStored(d, n, 0)
			} else {
				skyStoredRaw(d, n, 0)
			}
			*dec = append(*dec, lqItem{p: n, v: tl, dirs: skyAllDirs &^ (1 << skyOpp(dir))})
			skyProjectDown(d, n, dir, tl, false, emptyBelow, dec, inc)
		} else {
			*inc = append(*inc, lqItem{p: n, v: tl, dirs: 1 << skyOpp(dir)})
		}
	}
}

// skyPropagateInc is vanilla's propagateIncrease plus the emission
// through-write the queue drain performs on stale source entries.
func skyPropagateInc(d *dimension, it lqItem, inc, dec *[]lqItem, mark bool) {
	emptyBelow := skyEmptyBelow(d, it.p)
	ck0 := [2]int{it.p.X >> 4, it.p.Z >> 4}
	fromL := 0
	if skyStoring(d, ck0, skySecIdx(d, it.p.Y)) {
		fromL = skyStored(d, it.p)
	}
	if it.emit && fromL < it.v {
		if mark {
			skySetStored(d, it.p, it.v)
		} else {
			skyStoredRaw(d, it.p, it.v)
		}
		fromL = it.v
	}
	if fromL != it.v {
		return
	}
	for dir := 0; dir < 6; dir++ {
		if it.dirs>>dir&1 == 0 {
			continue
		}
		n := world.Pos{X: it.p.X + lightDirs6[dir][0], Y: it.p.Y + lightDirs6[dir][1], Z: it.p.Z + lightDirs6[dir][2]}
		if n.Y < d.minY || n.Y >= d.minY+d.height {
			continue
		}
		ck := [2]int{n.X >> 4, n.Z >> 4}
		if !skyStoring(d, ck, skySecIdx(d, n.Y)) {
			continue
		}
		tl := skyStored(d, n)
		if fromL-1 <= tl {
			continue
		}
		nv := fromL - lightOpacity(d.w.GetBlock(n))
		if nv <= tl {
			continue
		}
		if mark {
			skySetStored(d, n, nv)
		} else {
			skyStoredRaw(d, n, nv)
		}
		if nv > 1 {
			*inc = append(*inc, lqItem{p: n, v: nv, dirs: skyAllDirs &^ (1 << skyOpp(dir))})
		}
		skyProjectDown(d, n, dir, nv, true, emptyBelow, dec, inc)
	}
}

// skyStoredRaw writes without the affected mark — the source-flood
// and replica fills that vanilla routes around setStoredLevel.
func skyStoredRaw(d *dimension, p world.Pos, v int) {
	d.skyLayers[[2]int{p.X >> 4, p.Z >> 4}][skySecIdx(d, p.Y)][(p.Y&15)*256+(p.Z&15)*16+(p.X&15)] = byte(v)
	markSkyWritten(d, [2]int{p.X >> 4, p.Z >> 4}, skySecIdx(d, p.Y))
}

// skyDrainDec/skyDrainInc drain the two queues — vanilla's
// propagateDecreases runs to empty before propagateIncreases each
// runLightUpdates.
func skyDrainDec(d *dimension, dec, inc *[]lqItem, mark bool) {
	for len(*dec) > 0 {
		it := (*dec)[0]
		*dec = (*dec)[1:]
		skyPropagateDec(d, it, dec, inc, mark)
	}
}

func skyDrainInc(d *dimension, inc, dec *[]lqItem, mark bool) {
	for len(*inc) > 0 {
		it := (*inc)[0]
		*inc = (*inc)[1:]
		skyPropagateInc(d, it, inc, dec, mark)
	}
}

// skyRemoveSourcesBelow is vanilla's removeSourcesBelow: walk sections
// downward from the one under the new lowest source, clearing stored
// source 15s — only inside layered sections, and stopping the whole
// walk at the first non-source cell.
func skyRemoveSourcesBelow(d *dimension, x, z, lowest, worldBottom int, dec *[]lqItem, mark bool) {
	if lowest <= worldBottom {
		return
	}
	ck := [2]int{x >> 4, z >> 4}
	for sec := skySecIdx(d, lowest-1); skyHasDataAtOrBelow(d, sec); sec-- {
		if !skyStoring(d, ck, sec) {
			continue
		}
		secMin := d.minY + sec*16
		for y := min(secMin+15, lowest-1); y >= secMin; y-- {
			p := world.Pos{X: x, Y: y, Z: z}
			if skyStored(d, p) != 15 {
				return
			}
			if mark {
				skySetStored(d, p, 0)
			} else {
				skyStoredRaw(d, p, 0)
			}
			dirs := uint8(skyAllDirs &^ (1 << skyDirU))
			if y == lowest-1 {
				dirs = skyAllDirs
			}
			*dec = append(*dec, lqItem{p: p, v: 15, dirs: dirs})
		}
	}
}

// skyAddSourcesAbove is vanilla's addSourcesAbove: fill the column's
// source band into layered sections up to the data top, enqueueing
// increase entries only at the band edges that still outshine the
// neighbours.
func skyAddSourcesAbove(d *dimension, x, z, lowest, worldBottom int, inc *[]lqItem, mark bool) {
	ck := [2]int{x >> 4, z >> 4}
	nLow := int(skyTop(d, x-1, z)) + 1
	if v := int(skyTop(d, x+1, z)) + 1; v > nLow {
		nLow = v
	}
	if v := int(skyTop(d, x, z-1)) + 1; v > nLow {
		nLow = v
	}
	if v := int(skyTop(d, x, z+1)) + 1; v > nLow {
		nLow = v
	}
	start := lowest
	if worldBottom > start {
		start = worldBottom
	}
	for sec := skySecIdx(d, start); sec < topSkySec(d, ck); sec++ {
		if !skyStoring(d, ck, sec) {
			continue
		}
		secMin := d.minY + sec*16
		for y := max(secMin, start); y <= secMin+15; y++ {
			p := world.Pos{X: x, Y: y, Z: z}
			if skyStored(d, p) == 15 {
				return
			}
			if mark {
				skySetStored(d, p, 15)
			} else {
				skyStoredRaw(d, p, 15)
			}
			if y < nLow || y == lowest {
				*inc = append(*inc, lqItem{p: p, v: 15, dirs: skyAllDirs &^ (1 << skyDirU)})
			}
		}
	}
}

// skyLightChanged is vanilla's checkNode for a finished block write:
// the column's source band first slides to the new heightmap top
// (removeSourcesBelow / addSourcesAbove), then the written cell itself
// queues its remove+add entries, and the two floods run to
// completion. The marks land in skyDirty for emitLightUpdate.
func (s *Server) skyLightChanged(d *dimension, pos world.Pos, old, blk world.Block, oldTop, newTop int32) {
	d.lightMu.Lock()
	defer d.lightMu.Unlock()
	genLightAround(d, pos)
	if !d.skylight {
		return
	}
	ck := [2]int{pos.X >> 4, pos.Z >> 4}
	// The heightmap moves first — vanilla updates it inside
	// setBlockState before the light hooks run.
	topsFor(d, pos.X>>4, pos.Z>>4)[(pos.Z&15)*16+(pos.X&15)] = newTop
	// The emptiness flip normally already ran inside
	// blockLightChanged — vanilla's setBlockState order — but this
	// site may have skipped it (a no-op write for block light still
	// flips layers), so reconcile once more; it is a no-op the
	// second time.
	s.lightSectionFlipped(d, pos, old, blk)
	ensureSkyChunk(d, pos.X>>4, pos.Z>>4)
	newLowest := int(newTop) + 1
	var dec, inc []lqItem
	skyRemoveSourcesBelow(d, pos.X, pos.Z, newLowest, d.minY, &dec, true)
	skyAddSourcesAbove(d, pos.X, pos.Z, newLowest, d.minY, &inc, true)
	if skyStoring(d, ck, skySecIdx(d, pos.Y)) {
		if pos.Y >= newLowest {
			dec = append(dec, lqItem{p: pos, v: 15, dirs: skyAllDirs &^ (1 << skyDirU)})
			inc = append(inc, lqItem{p: pos, v: 15, dirs: skyAllDirs &^ (1 << skyDirU)})
		} else {
			if oldL := skyStored(d, pos); oldL > 0 {
				skySetStored(d, pos, 0)
				dec = append(dec, lqItem{p: pos, v: oldL, dirs: skyAllDirs})
			} else {
				dec = append(dec, lqItem{p: pos, v: 0, dirs: skyAllDirs})
			}
		}
	}
	// runLightUpdates: the decrease queue drains fully before the
	// increase queue; entries queued during the increase pass run on
	// the next pass — same tick, same broadcast.
	for len(dec) > 0 || len(inc) > 0 {
		skyDrainDec(d, &dec, &inc, true)
		skyDrainInc(d, &inc, &dec, true)
	}
}

// emitLightUpdate is the emit point every write path shares. Writes
// only accumulate marks; the drain runs at the end of the tick —
// vanilla's ChunkHolder.broadcastChanges, which batches a tick's
// section changes into one update_light per chunk so a section
// materialized and removed inside the same tick leaves no trace on
// the wire. With no game loop running (unit tests driving the server
// without Serve) it flushes right away.
func (s *Server) emitLightUpdate(d *dimension, pos world.Pos) {
	if s.ticking.Load() {
		return
	}
	s.lightChangesFlushStep()
}

// lightChangesFlushStep drains every dimension's accumulated sky and
// block-light marks into one update_light per touched chunk —
// vanilla's ChunkHolder.broadcastChanges: the light filter runs before
// the block updates, so this flushes just before blockChangesFlushStep.
func (s *Server) lightChangesFlushStep() {
	for _, d := range s.dims {
		s.emitLightUpdateFor(d)
	}
}

// emitLightUpdateFor drains one dimension's marks into packets.
func (s *Server) emitLightUpdateFor(d *dimension) {
	d.lightMu.Lock()
	defer d.lightMu.Unlock()
	if len(d.lightDirty) == 0 && len(d.skyDirty) == 0 {
		return
	}
	dirty := d.lightDirty
	d.lightDirty = nil
	sdirty := d.skyDirty
	d.skyDirty = nil
	keys := map[[2]int]bool{}
	for ck := range dirty {
		keys[ck] = true
	}
	for ck := range sdirty {
		keys[ck] = true
	}
	for ck := range keys {
		sky := []int(nil)
		if d.skylight {
			for sec := range sdirty[ck] {
				sky = append(sky, sec)
			}
			sort.Ints(sky)
		}
		blk := make([]int, 0, len(dirty[ck]))
		for sec := range dirty[ck] {
			blk = append(blk, sec)
		}
		sort.Ints(blk)
		s.sendLightUpdate(d, ck[0], ck[1], sky, blk)
	}
}

// skySectionArray packs one materialized sky layer's 4096 nibbles (x
// fastest, then z, then y) straight from the stored array.
func skySectionArray(d *dimension, cx, cz, sec int) []byte {
	L := d.skyLayers[[2]int{cx, cz}][sec]
	out := make([]byte, 2048)
	for i, v := range L {
		b := i / 2
		if i%2 == 0 {
			out[b] |= v
		} else {
			out[b] |= v << 4
		}
	}
	return out
}

// skyLayerNonZero reports whether a materialized layer holds any lit
// cell — vanilla's maybeNonEmpty: an all-zero layer ships on the
// empty mask, not as an array.
func skyLayerNonZero(d *dimension, cx, cz, sec int) bool {
	L := d.skyLayers[[2]int{cx, cz}][sec]
	if L == nil {
		return false
	}
	for _, v := range L {
		if v != 0 {
			return true
		}
	}
	return false
}

// sectionHasSky reports whether one marked section owns a sky layer:
// vanilla ships getDataLayerData — the array when the section's layer
// exists and holds any non-zero cell, the empty mask otherwise.
func sectionHasSky(d *dimension, cx, cz, sec int) bool {
	return skyLayerNonZero(d, cx, cz, sec)
}

// setBits sets a section bit in a long mask.
func setLongBit(mask *[]uint64, bit int) {
	li := bit / 64
	for len(*mask) <= li {
		*mask = append(*mask, 0)
	}
	(*mask)[li] |= 1 << uint(bit%64)
}

// writeLongMask writes a BitSet: VarInt long count then longs.
func writeLongMask(b *protocol.Buffer, mask []uint64) {
	b.VarInt(int32(len(mask)))
	for _, m := range mask {
		b.Long(int64(m))
	}
}

// blockSectionArray packs one section's 4096 block-light nibbles (x
// fastest, then z, then y) from the per-dimension grid.
func blockSectionArray(d *dimension, cx, cz, sec int) []byte {
	y0 := d.minY + sec*16
	out := make([]byte, 2048)
	idx := 0
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				v := d.blockLight.get(world.Pos{X: cx*16 + x, Y: y0 + y, Z: cz*16 + z})
				b := idx / 2
				if idx%2 == 0 {
					out[b] |= v
				} else {
					out[b] |= v << 4
				}
				idx++
			}
		}
	}
	return out
}

// blkSectionNonZero reports whether a marked section has a layered,
// non-zero block-light content — the same maybeNonEmpty rule as sky.
func blkSectionNonZero(d *dimension, cx, cz, sec int) bool {
	if !d.blkLayered[[2]int{cx, cz}][sec] {
		return false
	}
	y0 := d.minY + sec*16
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				if d.blockLight.get(world.Pos{X: cx*16 + x, Y: y0 + y, Z: cz*16 + z}) != 0 {
					return true
				}
			}
		}
	}
	return false
}

// lightUpdateBody builds the Light Data for a chunk: marked sky
// sections ship their layer array when they own one and land on the
// empty mask without it (a fully-underground marked section), and
// blkSecs carry the grid's block arrays — which vanilla ships as
// arrays even when every nibble is zero (the section object survives
// a removal).
func lightUpdateBody(d *dimension, cx, cz int, skySecs, blkSecs []int) []byte {
	var skyMask, blockMask, emptySky, emptyBlock []uint64
	var skyArrays, blockArrays [][]byte
	for _, sec := range skySecs {
		if sec < 0 || sec >= d.sections() {
			continue
		}
		// prepareSectionData: unlayered marks are dropped silently;
		// a never-written layer lands on the empty mask; anything
		// with allocated data ships its array — even all-zero.
		ck := [2]int{cx, cz}
		bit := sec + 1 // light masks reserve bit 0 for below the world
		if d.skyLayers[ck][sec] == nil {
			continue
		}
		if d.skyWritten[ck][sec] {
			setLongBit(&skyMask, bit)
			skyArrays = append(skyArrays, skySectionArray(d, cx, cz, sec))
		} else {
			setLongBit(&emptySky, bit)
		}
	}
	for _, sec := range blkSecs {
		if sec < 0 || sec >= d.sections() {
			continue
		}
		ck := [2]int{cx, cz}
		if !d.blkLayered[ck][sec] {
			continue
		}
		if d.blkWritten[ck][sec] {
			setLongBit(&blockMask, sec+1)
			blockArrays = append(blockArrays, blockSectionArray(d, cx, cz, sec))
		} else {
			setLongBit(&emptyBlock, sec+1)
		}
	}
	var b protocol.Buffer
	writeLongMask(&b, skyMask)
	writeLongMask(&b, blockMask)
	writeLongMask(&b, emptySky)
	writeLongMask(&b, emptyBlock)
	b.VarInt(int32(len(skyArrays)))
	for _, arr := range skyArrays {
		writeByteArray(&b, arr)
	}
	b.VarInt(int32(len(blockArrays)))
	for _, arr := range blockArrays {
		writeByteArray(&b, arr)
	}
	return b.Bytes()
}

// sendLightUpdate emits one Update Light packet to every session for
// which chunk (cx,cz) sits on the tracked border of its view: the
// column must be seen (in the window and already sent), and at least
// one of its eight neighbours must not be. Vanilla's
// ChunkMap.getPlayers(pos, true) gates light updates to that border -
// a client relights interior changes itself off the block updates it
// already has, so an interior light write reaches nobody.
func (s *Server) sendLightUpdate(d *dimension, cx, cz int, skySecs, blkSecs []int) {
	if len(skySecs) == 0 && len(blkSecs) == 0 {
		return
	}
	body := lightUpdateBody(d, cx, cz, skySecs, blkSecs)
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dimName() != d.name || !sess.chunkSeen(cx, cz) {
			return true
		}
		border := false
		for dx := -1; dx <= 1 && !border; dx++ {
			for dz := -1; dz <= 1 && !border; dz++ {
				if dx == 0 && dz == 0 {
					continue
				}
				if !sess.chunkSeen(cx+dx, cz+dz) {
					border = true
				}
			}
		}
		if !border {
			return true
		}
		var b protocol.Buffer
		b.VarInt(int32(cx))
		b.VarInt(int32(cz))
		b.Raw(body)
		_ = sess.write(sess.ids().PlayUpdateLight, b.Bytes())
		return true
	})
}

// savedLight is the dimension's ChunkLight provider for a save: a sky
// array where a sky layer materialized (skyLayers entry), a block
// array where the block engine layered the section (blkLayered) — the
// same existence rules vanilla's DataLayer serialization follows.
func (d *dimension) savedLight(cx, cz int) *world.ChunkLight {
	ck := [2]int{cx, cz}
	out := &world.ChunkLight{Sky: map[int][]byte{}, Block: map[int][]byte{}}
	for sec := range d.skyLayers[ck] {
		out.Sky[sec] = skySectionArray(d, cx, cz, sec)
	}
	// Vanilla stores a BlockLight array iff the section's DataLayer
	// holds data — a layer that was only materialized never gains a
	// key, so the predicate is the data!=null mirror blkWritten, not
	// mere eligibility.
	for sec := range d.blkWritten[ck] {
		out.Block[sec] = blockSectionArray(d, cx, cz, sec)
	}
	return out
}
