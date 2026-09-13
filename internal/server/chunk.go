package server

import (
	"bytes"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Block-state numeric ids, derived from the official block-state report
// via world's generated constants (see blocks_gen.go). StateAir and
// friends are aliases kept for the existing call sites and tests.
const (
	StateAir   = int32(world.Air)
	StateStone = int32(world.Stone)
	StateDirt  = int32(world.Dirt)
	StateGrass = int32(world.Grass)
)

// World vertical range for the flat generator.
const (
	MinY     = -64
	Height   = 384
	Sections = Height / 16 // 24
)

func blockState(b world.Block) int32 { return int32(b) }

// DescribeBlockState resolves a global state id to its block name and
// property values. It exists for diagnostics and tests that print what
// the server is actually sending; the simulation itself works on ids.
func DescribeBlockState(state int32) (string, map[string]string, bool) {
	return world.DescribeState(world.Block(state))
}

// heightmapTypes are the heightmaps vanilla sends with each chunk, in
// vanilla's order: WORLD_SURFACE, MOTION_BLOCKING_NO_LEAVES,
// MOTION_BLOCKING.
var heightmapTypes = []int32{1, 5, 4}

// heightmapBits is ceil(log2(worldHeight+1)) for Height=384.
const heightmapBits = 9

// packPadded packs values into big-endian longs, least-significant
// entry first, with entries never spanning longs (1.21.5+ format).
func packPadded(vals []int32, bits int) []int64 {
	perLong := 64 / bits
	longs := (len(vals) + perLong - 1) / perLong
	out := make([]int64, longs)
	for i, v := range vals {
		li := i / perLong
		shift := uint(i%perLong) * uint(bits)
		out[li] |= int64(uint64(v) << shift)
	}
	return out
}

func writeLongs(b *protocol.Buffer, longs []int64) {
	for _, l := range longs {
		b.Long(l)
	}
}

// heightmapValues returns the packed heightmap entries for one column:
// for each (z,x) column (x fastest), the highest occupied Y offset
// from the dimension's minY, plus one. Zero means the whole column is
// empty.
func heightmapValues(states []world.Block, height int) []int32 {
	vals := make([]int32, 16*16)
	for z := 0; z < 16; z++ {
		for x := 0; x < 16; x++ {
			top := int32(0)
			for y := height - 1; y >= 0; y-- {
				if states[(y*16+z)*16+x] != world.Air {
					top = int32(y) + 1
					break
				}
			}
			vals[z*16+x] = top
		}
	}
	return vals
}

// encodeChunk builds one Chunk Data and Update Light body for column
// (cx, cz) in the 776 format: chunk coords, prefixed heightmaps, a
// length-prefixed data blob of sections (block count, fluid count,
// block palette, biome palette), block entities, then inline light.
// The dimension supplies the world, vertical bounds, and biome.
func encodeChunk(d *dimension, cx, cz int) []byte {
	var b protocol.Buffer
	b.Int(int32(cx))
	b.Int(int32(cz))

	height := d.height
	states := make([]world.Block, 16*height*16)
	d.w.FillBox(
		world.Pos{X: cx * 16, Y: d.minY, Z: cz * 16},
		world.Pos{X: cx*16 + 15, Y: d.minY + height - 1, Z: cz*16 + 15},
		states,
	)

	b.VarInt(int32(len(heightmapTypes)))
	for _, typ := range heightmapTypes {
		b.VarInt(typ)
		longs := packPadded(heightmapValues(states, height), heightmapBits)
		b.VarInt(int32(len(longs)))
		writeLongs(&b, longs)
	}

	var data protocol.Buffer
	section := make([]world.Block, 4096)
	vals := make([]int32, 4096)
	biomes := make([]int32, 64)
	for i := range biomes {
		biomes[i] = biomeID(d.biome)
	}
	for sy := 0; sy < d.sections(); sy++ {
		for y := 0; y < 16; y++ {
			base := ((sy*16 + y) * 16) * 16
			copy(section[y*256:(y+1)*256], states[base:base+256])
		}
		var blockCount, fluidCount int16
		for i, blk := range section {
			vals[i] = int32(blk)
			if vals[i] != StateAir {
				blockCount++
			}
		}
		data.Short(blockCount)
		data.Short(fluidCount)
		encodePalettedContainer(&data, vals, 4096, 4)
		encodePalettedContainer(&data, biomes, 64, 1)
	}
	b.VarInt(int32(data.Len()))
	b.Raw(data.Bytes())
	b.VarInt(0) // block entities
	b.Raw(encodeLightDataFor(d))
	return b.Bytes()
}

// encodePalettedContainer writes one paletted container: bits per
// entry (Unsigned Byte), palette, and a fixed-size padded data array.
// entries is 4096 for blocks, 64 for biomes; minIndirectBits is the
// smallest legal indirect BPE for that global palette.
func encodePalettedContainer(b *protocol.Buffer, vals []int32, entries, minIndirectBits int) {
	var palette []int32
	index := map[int32]int{}
	for _, v := range vals {
		if _, ok := index[v]; !ok {
			index[v] = len(palette)
			palette = append(palette, v)
		}
	}
	if len(palette) == 1 {
		b.Byte(0)
		b.VarInt(palette[0])
		return
	}
	bpe := minIndirectBits
	for (1 << bpe) < len(palette) {
		bpe++
	}
	if bpe > 8 {
		// Direct: global ids, padded, no palette. Vanilla uses 15 for
		// blocks and the registry-derived width for biomes.
		bits := 15
		if entries == 64 {
			bits = 7
		}
		b.Byte(byte(bits))
		writePackedLongs(b, vals, nil, bits)
		return
	}
	b.Byte(byte(bpe))
	b.VarInt(int32(len(palette)))
	for _, p := range palette {
		b.VarInt(p)
	}
	writePackedLongs(b, vals, index, bpe)
}

// writePackedLongs writes vals as fixed-size padded longs, using index
// to map to palette indices when non-nil.
func writePackedLongs(b *protocol.Buffer, vals []int32, index map[int32]int, bits int) {
	packed := make([]int32, len(vals))
	for i, v := range vals {
		if index != nil {
			packed[i] = int32(index[v])
		} else {
			packed[i] = v
		}
	}
	writeLongs(b, packPadded(packed, bits))
}

// lightSections is Sections plus the below/above edge arrays vanilla
// includes in light data.
const lightSections = Sections + 2

// encodeLightData builds the Light Data portion shared by chunk and
// update-light packets for the flat overworld: sky light dark through
// the four solid layers of the ground section and full above, block
// light empty everywhere.
func encodeLightData() []byte {
	var b protocol.Buffer
	// Sky mask bits 1,2 (ground section and the section above it).
	b.VarInt(1)
	b.Long(0x6)
	b.VarInt(0) // block light mask
	// Empty sky below the world; empty block light below and at ground.
	b.VarInt(1)
	b.Long(0x1)
	b.VarInt(1)
	b.Long(0x7)
	// Sky arrays: ground section (4 dark layers, 12 lit) then all-lit.
	b.VarInt(2)
	ground := make([]byte, 2048)
	for i := 512; i < 2048; i++ {
		ground[i] = 0xFF
	}
	writeByteArray(&b, ground)
	writeByteArray(&b, bytes.Repeat([]byte{0xFF}, 2048))
	b.VarInt(0) // block light arrays
	return b.Bytes()
}

// encodeLightDataFor picks the light profile per dimension: skylight
// dimensions get the flat-overworld profile, skylight-less ones
// (nether, end) mark every section empty so the client relies on the
// dimension's ambient light instead.
func encodeLightDataFor(d *dimension) []byte {
	if d.skylight {
		return encodeLightData()
	}
	// sections+2 light slots, all flagged empty; no arrays follow.
	bits := d.sections() + 2
	var b protocol.Buffer
	b.VarInt(0) // sky light mask: no arrays
	b.VarInt(0) // block light mask: no arrays
	b.VarInt(1) // empty sky light mask
	b.Long(int64(uint64(1)<<uint(bits) - 1))
	b.VarInt(1) // empty block light mask
	b.Long(int64(uint64(1)<<uint(bits) - 1))
	b.VarInt(0) // sky arrays
	b.VarInt(0) // block light arrays
	return b.Bytes()
}

func writeByteArray(b *protocol.Buffer, data []byte) {
	b.VarInt(int32(len(data)))
	b.Raw(data)
}

// encodeLight builds one Update Light body for column (cx, cz).
func encodeLight(cx, cz int) []byte {
	var b protocol.Buffer
	b.VarInt(int32(cx))
	b.VarInt(int32(cz))
	b.Raw(encodeLightData())
	return b.Bytes()
}

// encodeLightFor builds one Update Light body for a dimension column.
func encodeLightFor(d *dimension, cx, cz int) []byte {
	var b protocol.Buffer
	b.VarInt(int32(cx))
	b.VarInt(int32(cz))
	b.Raw(encodeLightDataFor(d))
	return b.Bytes()
}

// pruneBorder is how many columns past the view window the server
// keeps before forgetting them, avoiding churn on small movements.
const pruneBorder = 2

// sendChunkCacheRadius tells the client the radius the server streams.
func (s *Server) sendChunkCacheRadius(sess *Session) {
	radius := sess.viewRadius(s.ViewDistance)
	var b protocol.Buffer
	b.VarInt(int32(radius))
	_ = sess.write(sess.ids().PlayChunkCacheRadius, b.Bytes())
}

// encodeForget builds one Forget Level Chunk body: a single Long with
// x in the low 32 bits and z in the high 32 bits.
func encodeForget(cx, cz int) []byte {
	var b protocol.Buffer
	b.Long(int64(uint64(uint32(int32(cx))) | uint64(uint32(int32(cz)))<<32))
	return b.Bytes()
}

// sendWindow streams every unstreamed column in the effective view
// window around (ccx, ccz) as Chunk Data and Update Light (the chunk
// packet carries light inline), wrapped in a Chunk Batch, then tells
// the client to unload far columns via Forget Level Chunk and drops
// them from the sent-set. Column order is deterministic (dx, dz);
// forgets are sorted ascending (x, z) so tests and captures are stable.
func (s *Server) sendWindow(sess *Session, ccx, ccz int) error {
	radius := sess.viewRadius(s.ViewDistance)
	var fresh [][2]int
	sess.sentMu.Lock()
	if sess.sent == nil {
		sess.sent = make(map[[2]int]bool)
	}
	for dx := -radius; dx <= radius; dx++ {
		for dz := -radius; dz <= radius; dz++ {
			cx, cz := ccx+dx, ccz+dz
			key := [2]int{cx, cz}
			if sess.sent[key] {
				continue
			}
			sess.sent[key] = true
			fresh = append(fresh, key)
		}
	}
	sess.sentMu.Unlock()
	if len(fresh) > 0 {
		var b protocol.Buffer // Chunk Batch Start has an empty body
		if err := sess.write(sess.ids().PlayChunkBatchStart, b.Bytes()); err != nil {
			return err
		}
		for _, key := range fresh {
			if err := sess.write(sess.ids().PlayChunkData, encodeChunk(sess.dimension(), key[0], key[1])); err != nil {
				return err
			}
		}
		var done protocol.Buffer
		done.VarInt(int32(len(fresh)))
		if err := sess.write(sess.ids().PlayChunkBatchDone, done.Bytes()); err != nil {
			return err
		}
	}
	prune := radius + pruneBorder
	var forgot [][2]int
	sess.sentMu.Lock()
	for k := range sess.sent {
		dx, dz := k[0]-ccx, k[1]-ccz
		if dx < -prune || dx > prune || dz < -prune || dz > prune {
			delete(sess.sent, k)
			forgot = append(forgot, k)
		}
	}
	sess.sentMu.Unlock()
	sortPairs(forgot)
	for _, k := range forgot {
		if err := sess.write(sess.ids().PlayForgetChunk, encodeForget(k[0], k[1])); err != nil {
			return err
		}
	}
	return nil
}

// sortPairs orders chunk keys ascending by x then z.
func sortPairs(p [][2]int) {
	for i := 1; i < len(p); i++ {
		for j := i; j > 0 && (p[j][0] < p[j-1][0] ||
			(p[j][0] == p[j-1][0] && p[j][1] < p[j-1][1])); j-- {
			p[j], p[j-1] = p[j-1], p[j]
		}
	}
}

// sendCenterChunk tells the client its new loaded-chunk center, once per
// column: repeats for the same center are dropped. Vanilla uses this to
// evict stale chunks; without it far columns may never render.
func (s *Server) sendCenterChunk(sess *Session, cx, cz int) {
	sess.sentMu.Lock()
	same := sess.centerSet && sess.centerX == cx && sess.centerZ == cz
	sess.centerSet, sess.centerX, sess.centerZ = true, cx, cz
	sess.sentMu.Unlock()
	if same {
		return
	}
	var b protocol.Buffer
	b.VarInt(int32(cx))
	b.VarInt(int32(cz))
	_ = sess.write(sess.ids().PlaySetCenterChunk, b.Bytes())
}
