package blackbox

import (
	"bytes"
	"encoding/hex"
	"io"
	"testing"

	"mc-go/internal/nbt"

	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

// chunkView is a decoded Chunk Data and Update Light body with light
// canonicalized per section (explicit arrays, empty masks, or the
// implicit full/zero defaults).
type chunkView struct {
	x, z          int32
	hms           map[int32][]int64
	data          []byte
	blockEntities []byte
	sky           [24][]byte
	block         [24][]byte
}

// splitHeightmaps parses a Chunk Data body into the chunk coords, a
// heightmap map keyed by type, and the remaining bytes.
func splitHeightmaps(t *testing.T, tag string, body []byte) (int32, int32, map[int32][]int64, []byte) {
	t.Helper()
	r := protocol.NewPacketReader(body)
	x, err := r.Int()
	if err != nil {
		t.Fatalf("%s chunk x: %v", tag, err)
	}
	z, err := r.Int()
	if err != nil {
		t.Fatalf("%s chunk z: %v", tag, err)
	}
	n, err := r.VarInt()
	if err != nil {
		t.Fatalf("%s heightmap count: %v", tag, err)
	}
	hms := make(map[int32][]int64, n)
	for i := int32(0); i < n; i++ {
		typ, err := r.VarInt()
		if err != nil {
			t.Fatalf("%s heightmap type: %v", tag, err)
		}
		longs, err := r.VarInt()
		if err != nil {
			t.Fatalf("%s heightmap longs: %v", tag, err)
		}
		vals := make([]int64, longs)
		for j := range vals {
			vals[j], err = r.Long()
			if err != nil {
				t.Fatalf("%s heightmap data: %v", tag, err)
			}
		}
		hms[typ] = vals
	}
	rest, err := r.Read(r.Remaining())
	if err != nil {
		t.Fatalf("%s remaining: %v", tag, err)
	}
	return x, z, hms, rest
}

// readLightMask reads one BitSet.
func readLightMask(t *testing.T, tag string, r *protocol.PacketReader) []uint64 {
	t.Helper()
	n, err := r.VarInt()
	if err != nil || n < 0 || n > 64 {
		t.Fatalf("%s light mask longs = %d, err %v", tag, n, err)
	}
	out := make([]uint64, n)
	for i := range out {
		v, err := r.Long()
		if err != nil {
			t.Fatalf("%s light mask: %v", tag, err)
		}
		out[i] = uint64(v)
	}
	return out
}

// parseChunkView decodes a full chunk body into a comparable view.
func parseChunkView(t *testing.T, tag string, body []byte) chunkView {
	t.Helper()
	x, z, hms, rest := splitHeightmaps(t, tag, body)
	var v chunkView
	v.x, v.z, v.hms = x, z, hms
	r := protocol.NewPacketReader(rest)
	dataLen, err := r.VarInt()
	if err != nil {
		t.Fatalf("%s data length: %v", tag, err)
	}
	v.data, err = r.Read(int(dataLen))
	if err != nil {
		t.Fatalf("%s data: %v", tag, err)
	}
	beCount, err := r.VarInt()
	if err != nil {
		t.Fatalf("%s block entities: %v", tag, err)
	}
	// Block entities are compared separately (parseChunkBlockEntities);
	// skip past them to the light data.
	if beCount != 0 {
		rest := rest[len(rest)-r.Remaining():]
		br := bytes.NewReader(rest)
		for i := int32(0); i < beCount; i++ {
			var hdr [3]byte
			if _, err := io.ReadFull(br, hdr[:]); err != nil {
				t.Fatalf("%s block entity %d: %v", tag, i, err)
			}
			if _, err := readChunkBEVarInt(br); err != nil {
				t.Fatalf("%s block entity %d type: %v", tag, i, err)
			}
			peek, err := readChunkBEByte(br)
			if err != nil {
				t.Fatalf("%s block entity %d nbt: %v", tag, i, err)
			}
			if peek != 0 {
				_ = br.UnreadByte()
				if _, err := nbt.ReadUnnamed(br); err != nil {
					t.Fatalf("%s block entity %d nbt: %v", tag, i, err)
				}
			}
		}
		r = protocol.NewPacketReader(rest[len(rest)-br.Len():])
	}
	v.blockEntities = nil

	skyMask := readLightMask(t, tag, r)
	blockMask := readLightMask(t, tag, r)
	emptySky := readLightMask(t, tag, r)
	emptyBlock := readLightMask(t, tag, r)

	// Implicit defaults: missing sky sections are full (0xFF), missing
	// block sections are dark (0x00).
	for i := range v.sky {
		v.sky[i] = bytes.Repeat([]byte{0xFF}, 2048)
	}
	hasBit := func(mask []uint64, bit int) bool {
		li := bit / 64
		return li < len(mask) && mask[li]&(1<<uint(bit%64)) != 0
	}
	readArrays := func(dst *[24][]byte, mask []uint64) {
		t.Helper()
		var bits []int
		for bit := 0; bit < 64; bit++ {
			if hasBit(mask, bit) {
				bits = append(bits, bit)
			}
		}
		count, err := r.VarInt()
		if err != nil || int(count) != len(bits) {
			t.Fatalf("%s light arrays = %d, err %v (mask bits %v)", tag, count, err, bits)
		}
		for _, bit := range bits {
			n, err := r.VarInt()
			if err != nil || n != 2048 {
				t.Fatalf("%s light array %d length = %d, err %v", tag, bit, n, err)
			}
			arr, err := r.Read(2048)
			if err != nil {
				t.Fatalf("%s light array %d: %v", tag, bit, err)
			}
			sec := bit - 1
			if sec >= 0 && sec < len(dst) {
				dst[sec] = arr
			}
		}
	}
	readArrays(&v.sky, skyMask)
	for bit := 0; bit < 64; bit++ {
		if hasBit(emptySky, bit) {
			sec := bit - 1
			if sec >= 0 && sec < len(v.sky) {
				v.sky[sec] = make([]byte, 2048)
			}
		}
	}
	readArrays(&v.block, blockMask)
	for bit := 0; bit < 64; bit++ {
		if hasBit(emptyBlock, bit) {
			sec := bit - 1
			if sec >= 0 && sec < len(v.block) {
				v.block[sec] = make([]byte, 2048)
			}
		}
	}
	if r.Remaining() != 0 {
		t.Fatalf("%s trailing light bytes: %d", tag, r.Remaining())
	}
	return v
}

// TestChunkParity compares the Chunk Data and Update Light body for
// chunk (0,0) between vanilla and mc-go. Heightmaps, section data, and
// block entities must match byte for byte; light is compared per
// section after canonicalizing vanilla's nondeterministic mask coverage
// (explicit arrays vs. implicit full/empty defaults).
func TestChunkParity(t *testing.T) {
	tg := requireBlackbox(t)
	defer tg.cleanup()

	fetch := func(tag, addr string) []byte {
		c, pr, _ := joinPlayBlackbox(t, addr, "ChunkProbe")
		defer c.Close()
		for i := 0; i < 400; i++ {
			p, err := pr.ReadPacket()
			if err != nil {
				t.Fatalf("%s stream: %v", tag, err)
			}
			if p.ID != server.IDPlayChunkData {
				continue
			}
			r := protocol.NewPacketReader(p.Data)
			x, err1 := r.Int()
			z, err2 := r.Int()
			if err1 != nil || err2 != nil {
				t.Fatalf("%s chunk header: %v/%v", tag, err1, err2)
			}
			if x == 0 && z == 0 {
				return p.Data
			}
		}
		t.Fatalf("%s: chunk (0,0) never streamed", tag)
		return nil
	}

	vBody := fetch("vanilla", tg.vanilla)
	mBody := fetch("mc-go", tg.mcgo)
	t.Logf("chunk (0,0): vanilla %d bytes, mc-go %d bytes", len(vBody), len(mBody))

	v := parseChunkView(t, "vanilla", vBody)
	m := parseChunkView(t, "mc-go", mBody)
	if v.x != m.x || v.z != m.z {
		t.Fatalf("coords: vanilla %d,%d mc-go %d,%d", v.x, v.z, m.x, m.z)
	}
	if len(v.hms) != len(m.hms) {
		t.Fatalf("heightmap count: vanilla %d mc-go %d", len(v.hms), len(m.hms))
	}
	for typ, vv := range v.hms {
		mv, ok := m.hms[typ]
		if !ok {
			t.Fatalf("mc-go lacks heightmap type %d", typ)
		}
		if len(vv) != len(mv) {
			t.Fatalf("heightmap %d longs: vanilla %d mc-go %d", typ, len(vv), len(mv))
		}
		for i := range vv {
			if vv[i] != mv[i] {
				t.Fatalf("heightmap %d long %d: vanilla %016x mc-go %016x", typ, i, uint64(vv[i]), uint64(mv[i]))
			}
		}
	}
	if diff := firstDiff(v.data, m.data); diff >= 0 {
		t.Fatalf("section data differs at %d: vanilla ...%s... mc-go ...%s...",
			diff, hexAround(v.data, diff), hexAround(m.data, diff))
	}
	if !bytes.Equal(v.blockEntities, m.blockEntities) {
		t.Fatalf("block entities differ: vanilla %x mc-go %x", v.blockEntities, m.blockEntities)
	}
	for sec := 0; sec < 24; sec++ {
		if !bytes.Equal(v.sky[sec], m.sky[sec]) {
			diff := firstDiff(v.sky[sec], m.sky[sec])
			t.Fatalf("sky section %d differs at %d: vanilla %s mc-go %s",
				sec, diff, hexAround(v.sky[sec], diff), hexAround(m.sky[sec], diff))
		}
		if !bytes.Equal(v.block[sec], m.block[sec]) {
			t.Fatalf("block section %d differs", sec)
		}
	}
}

// firstDiff returns the first differing index, or -1.
func firstDiff(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return len(a)
	}
	return -1
}

func hexAround(b []byte, i int) string {
	lo := i - 8
	if lo < 0 {
		lo = 0
	}
	hi := i + 8
	if hi > len(b) {
		hi = len(b)
	}
	return hex.EncodeToString(b[lo:hi])
}
