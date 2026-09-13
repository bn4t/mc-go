package server

import (
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
func chunkTops(d *dimension, cx, cz int) [256]int32 {
	var tops [256]int32
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			top := int32(d.minY - 1)
			for y := d.minY + d.height - 1; y >= d.minY; y-- {
				if d.w.GetBlock(world.Pos{X: cx*16 + x, Y: y, Z: cz*16 + z}) != world.Air {
					top = int32(y)
					break
				}
			}
			tops[z*16+x] = top
		}
	}
	return tops
}

// columnTop returns one column's highest non-air Y in dimension d.
func columnTop(d *dimension, x, z int) int32 {
	for y := d.minY + d.height - 1; y >= d.minY; y-- {
		if d.w.GetBlock(world.Pos{X: x, Y: y, Z: z}) != world.Air {
			return int32(y)
		}
	}
	return int32(d.minY - 1)
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

// lightUpdateBody builds the Light Data for the given affected sections
// of a chunk in dimension d using the column model: every affected
// all-zero section is marked empty, every other affected section
// carries an explicit sky array. Block light is empty everywhere.
func lightUpdateBody(d *dimension, tops *[256]int32, affected []int) []byte {
	var skyMask, emptySky, emptyBlock []uint64
	var arrays [][]byte
	for _, sec := range affected {
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
			arrays = append(arrays, arr)
		}
		setLongBit(&emptyBlock, bit)
	}
	var b protocol.Buffer
	writeLongMask(&b, skyMask)
	b.VarInt(0) // block light mask: no emitters
	writeLongMask(&b, emptySky)
	writeLongMask(&b, emptyBlock)
	b.VarInt(int32(len(arrays)))
	for _, arr := range arrays {
		writeByteArray(&b, arr)
	}
	b.VarInt(0) // no block light arrays
	return b.Bytes()
}

// sendLightUpdate emits one Update Light packet to every session
// tracking chunk (cx,cz) in dimension d. Dimensions without skylight
// carry no sky data, so the update is a no-op there.
func (s *Server) sendLightUpdate(d *dimension, cx, cz int, affected []int) {
	if len(affected) == 0 || !d.skylight {
		return
	}
	tops := chunkTops(d, cx, cz)
	body := lightUpdateBody(d, &tops, affected)
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
// top moves between oldTop and newTop in dimension d.
func affectedSections(d *dimension, oldTop, newTop int32) []int {
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
