package server

import (
	"sort"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Column-model sky lighting. Every column's highest non-air block (its
// top) determines sky light: blocks above the top get 15, blocks at or
// below it get 0. This matches the flat world vanilla generates and,
// unlike a hardcoded flat assumption, follows block changes up and
// down. Horizontal sky propagation (walls shading neighbors) and block
// light sources arrive with the full light engine.
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
				if lightOpaque(buf[row+x]) && tops[z*16+x] == int32(d.minY-1) {
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
		if lightOpaque(buf[y]) {
			return int32(d.minY + y)
		}
	}
	return int32(d.minY - 1)
}

// lightOpaque reports whether a block state stops the column-model
// sky heightmap — vanilla's isSuffocating/canOcclude set, the same
// table Entity.isInWall uses.
func lightOpaque(b world.Block) bool {
	name, ok := world.StateOwner(b)
	return ok && suffocatingBlocks[name]
}

// lightOpacity is the cost light pays entering a cell — vanilla's
// getOpacity: opaque cubes absorb everything, water dims by 3, and
// every other block costs 1 (the max(1, opacity) floor).
func lightOpacity(b world.Block) int {
	name, ok := world.StateOwner(b)
	if !ok {
		return 1
	}
	switch name {
	case "water":
		return 3
	case "lava":
		return 255
	}
	if suffocatingBlocks[name] {
		return 255
	}
	return 1
}

// lightDirs6 are the propagation directions — vanilla spreads in the
// six face directions only.
var lightDirs6 = [6][3]int{
	{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}, {0, 1, 0}, {0, -1, 0},
}

// lightGridSet raises one cell's block light to v when it exceeds the
// stored value, recording the pre-change value in pre the first time
// a cell is touched (so emitLightUpdate can report real changes).
func lightGridSet(d *dimension, p world.Pos, v int, pre map[world.Pos]int) bool {
	if _, seen := pre[p]; !seen {
		pre[p] = int(d.blockLight[p])
	}
	if int(d.blockLight[p]) >= v {
		return false
	}
	if d.blockLight == nil {
		d.blockLight = map[world.Pos]uint8{}
	}
	d.blockLight[p] = uint8(v)
	return true
}

// lightGridDel zeroes one cell, with the same pre-change capture.
func lightGridDel(d *dimension, p world.Pos, pre map[world.Pos]int) {
	if _, seen := pre[p]; !seen {
		pre[p] = int(d.blockLight[p])
	}
	if d.blockLight != nil {
		delete(d.blockLight, p)
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
			nv := int(d.blockLight[n])
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
	oe, ne := lightEmit(old), lightEmit(blk)
	if oe == ne && lightOpacity(old) == lightOpacity(blk) {
		return
	}
	if len(d.blockLight) == 0 && ne <= 0 {
		return
	}
	pre := map[world.Pos]int{}
	// Remove with the value the cell actually held — the bigger of
	// the old emission and what neighbours had fed it.
	v := oe
	if ov := int(d.blockLight[pos]); ov > v {
		v = ov
	}
	if v > ne {
		s.blockLightRemove(d, pos, v, pre)
	}
	if ne > 0 {
		s.blockLightSpread(d, pos, ne, pre)
	}
	for p, ov := range pre {
		if int(d.blockLight[p]) == ov {
			continue
		}
		ck := [2]int{p.X >> 4, p.Z >> 4}
		sec := (p.Y - d.minY) / 16
		if d.lightDirty == nil {
			d.lightDirty = map[[2]int]map[int]bool{}
		}
		if d.lightDirty[ck] == nil {
			d.lightDirty[ck] = map[int]bool{}
		}
		d.lightDirty[ck][sec] = true
	}
}

// emitLightUpdate is the emit point every write path shares: the
// accumulated block-light changes drain into one update_light per
// touched chunk, and the sky side of pos's own column tags along on
// pos's chunk — matching vanilla, which sends only what changed of
// each type per section.
func (s *Server) emitLightUpdate(d *dimension, pos world.Pos, skySecs []int) {
	if len(d.lightDirty) == 0 && len(skySecs) == 0 {
		return
	}
	key := [2]int{pos.X >> 4, pos.Z >> 4}
	if !d.skylight {
		// Update light in no-skylight dimensions is unmeasured;
		// keep the previous behaviour of sending nothing.
		d.lightDirty = nil
		return
	}
	dirty := d.lightDirty
	d.lightDirty = nil
	for ck, secs := range dirty {
		sky := []int(nil)
		if ck == key {
			sky = skySecs
			skySecs = nil
		}
		secList := make([]int, 0, len(secs))
		for sec := range secs {
			secList = append(secList, sec)
		}
		sort.Ints(secList)
		s.sendLightUpdate(d, ck[0], ck[1], sky, secList)
	}
	if len(skySecs) > 0 {
		s.sendLightUpdate(d, key[0], key[1], skySecs, nil)
	}
}

// skySectionArray packs one section's 4096 sky-light nibbles (x fastest,
// then z, then y) from the column tops.
func skySectionArray(d *dimension, tops *[256]int32, section int) []byte {
	y0 := d.minY + section*16
	out := make([]byte, 2048)
	idx := 0
	for y := 0; y < 16; y++ {
		for z := 0; z < 16; z++ {
			for x := 0; x < 16; x++ {
				var v byte
				if int32(y0+y) > tops[z*16+x] {
					v = 15
				}
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
				v := d.blockLight[world.Pos{X: cx*16 + x, Y: y0 + y, Z: cz*16 + z}]
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

// lightUpdateBody builds the Light Data for a chunk: skySecs carry
// the column-model sky arrays (all-zero sections mark the empty
// mask, measured) and blkSecs carry the grid's block arrays — which
// vanilla ships as arrays even when every nibble is zero (the
// section object survives a removal).
func lightUpdateBody(d *dimension, tops *[256]int32, cx, cz int, skySecs, blkSecs []int) []byte {
	var skyMask, blockMask, emptySky, emptyBlock []uint64
	var skyArrays, blockArrays [][]byte
	for _, sec := range skySecs {
		if sec < 0 || sec >= d.sections() {
			continue
		}
		arr := skySectionArray(d, tops, sec)
		allZero := true
		for _, v := range arr {
			if v != 0 {
				allZero = false
				break
			}
		}
		bit := sec + 1 // light masks reserve bit 0 for below the world
		if allZero {
			setLongBit(&emptySky, bit)
		} else {
			setLongBit(&skyMask, bit)
			skyArrays = append(skyArrays, arr)
		}
	}
	for _, sec := range blkSecs {
		if sec < 0 || sec >= d.sections() {
			continue
		}
		setLongBit(&blockMask, sec+1)
		blockArrays = append(blockArrays, blockSectionArray(d, cx, cz, sec))
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

// sendLightUpdate emits one Update Light packet to every session
// tracking chunk (cx,cz) in dimension d.
func (s *Server) sendLightUpdate(d *dimension, cx, cz int, skySecs, blkSecs []int) {
	if len(skySecs) == 0 && len(blkSecs) == 0 {
		return
	}
	tops := chunkTops(d, cx, cz)
	body := lightUpdateBody(d, &tops, cx, cz, skySecs, blkSecs)
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dimName() != d.name || !sess.hasChunk(cx, cz) {
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

// affectedSections returns the section range touched when a column's
// top moves between oldTop and newTop in dimension d — an unmoved top
// touches nothing.
func affectedSections(d *dimension, oldTop, newTop int32) []int {
	if oldTop == newTop {
		return nil
	}
	lo, hi := oldTop, newTop
	if lo > hi {
		lo, hi = hi, lo
	}
	if lo < int32(d.minY) {
		lo = int32(d.minY)
	}
	if hi > int32(d.minY+d.height-1) {
		hi = int32(d.minY + d.height - 1)
	}
	first := int((lo - int32(d.minY)) / 16)
	last := int((hi - int32(d.minY)) / 16)
	if first < 0 {
		first = 0
	}
	if last >= d.sections() {
		last = d.sections() - 1
	}
	out := make([]int, 0, last-first+1)
	for s := first; s <= last; s++ {
		out = append(out, s)
	}
	return out
}
