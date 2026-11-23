package world

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// lz4TestBlock frames one jpountz block: the "LZ4Block" magic, a
// token carrying the method and log2(blockSize)-10, little-endian
// compressed and original lengths and checksum, then the data.
func lz4TestBlock(method byte, level int, compressed, original []byte, check uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte("LZ4Block"))
	b.WriteByte(method | byte(level-10))
	var u [4]byte
	binary.LittleEndian.PutUint32(u[:], uint32(len(compressed)))
	b.Write(u[:])
	binary.LittleEndian.PutUint32(u[:], uint32(len(original)))
	b.Write(u[:])
	binary.LittleEndian.PutUint32(u[:], check)
	b.Write(u[:])
	b.Write(compressed)
	return b.Bytes()
}

// lz4TestStream frames blocks the way LZ4BlockOutputStream does, with
// the writer's closing empty block.
func lz4TestStream(blocks ...[]byte) []byte {
	var b bytes.Buffer
	for _, blk := range blocks {
		b.Write(blk)
	}
	b.Write(lz4TestBlock(0x10, 16, nil, nil, 0))
	return b.Bytes()
}

func TestDecodeLZ4BlockStreamRawAndEnd(t *testing.T) {
	// A raw block passes its bytes straight through, and the
	// closing empty block stops the stream.
	plain := javaPattern(5000, 5)
	stream := lz4TestStream(lz4TestBlock(0x10, 16, plain, plain, xxHash32(plain, 0x9747b28c)&0x0FFFFFFF))
	got, err := decodeLZ4BlockStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("raw block round trip: %d bytes, want %d", len(got), len(plain))
	}
}

func TestDecodeLZ4BlockStreamCompressedVector(t *testing.T) {
	// The measured fastCompressor output for 500 zero bytes: one
	// literal, an offset-1 match of 494, then five trailing literals.
	// A hand-verified vector from the same library jar vanilla ships.
	comp := []byte{0x1f, 0x00, 0x01, 0x00, 0xff, 0xdc, 0x50, 0x00, 0x00, 0x00, 0x00, 0x00}
	plain := make([]byte, 500)
	dst := make([]byte, len(plain))
	consumed, err := lz4BlockDecompress(comp, dst)
	if err != nil || consumed != len(comp) || !bytes.Equal(dst, plain) {
		t.Fatalf("compressed vector: err=%v consumed=%d", err, consumed)
	}
	check := xxHash32(plain, 0x9747b28c) & 0x0FFFFFFF
	stream := lz4TestStream(lz4TestBlock(0x20, 16, comp, plain, check))
	got, err := decodeLZ4BlockStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("stream vector content mismatch")
	}
}

func TestDecodeLZ4BlockStreamMultiBlock(t *testing.T) {
	// Blocks concatenate in order until the closing empty block. A
	// token bounds its own block: the real writer emits 64KiB blocks,
	// so a second block continues a larger record.
	a := javaPattern(100, 2)
	b := javaChunky(65000)
	c := javaChunky(20000)
	var stream []byte
	stream = append(stream, lz4TestBlock(0x10, 16, a, a, xxHash32(a, 0x9747b28c)&0x0FFFFFFF)...)
	stream = append(stream, lz4TestBlock(0x10, 16, b, b, xxHash32(b, 0x9747b28c)&0x0FFFFFFF)...)
	stream = append(stream, lz4TestBlock(0x10, 16, c, c, xxHash32(c, 0x9747b28c)&0x0FFFFFFF)...)
	stream = append(stream, lz4TestBlock(0x10, 16, nil, nil, 0)...)
	got, err := decodeLZ4BlockStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, append(append(append([]byte{}, a...), b...), c...)) {
		t.Fatal("multi-block concatenation mismatch")
	}
}

func TestDecodeLZ4BlockStreamChecksumMask(t *testing.T) {
	// jpountz's StreamingXXHash32.asChecksum() masks the 32-bit hash
	// to its low 28 bits (measured: getValue() is i2l, ldc2_w
	// 268435455, land), and both the writer and the reader compare
	// through that adapter. A stream that stores the full 32-bit hash
	// - the natural reading of the public xxHash spec - is therefore
	// rejected whenever the hash's high nibble is nonzero, and a
	// masked one is accepted. Pin both sides with a block whose hash
	// actually has high bits set.
	plain := []byte("lz4block stream")
	full := xxHash32(plain, 0x9747b28c)
	if full&0xF0000000 == 0 {
		t.Fatal("vector's hash has no high bits; pick another input")
	}
	if _, err := decodeLZ4BlockStream(lz4TestStream(lz4TestBlock(0x10, 16, plain, plain, full))); err == nil {
		t.Fatal("full 32-bit checksum accepted; reader must compare through the 28-bit mask")
	}
	if got, err := decodeLZ4BlockStream(lz4TestStream(lz4TestBlock(0x10, 16, plain, plain, full&0x0FFFFFFF))); err != nil {
		t.Fatalf("masked checksum rejected: %v", err)
	} else if !bytes.Equal(got, plain) {
		t.Fatal("masked checksum content mismatch")
	}
}

func TestDecodeLZ4BlockStreamRejectsMalformed(t *testing.T) {
	plain := []byte("lz4block stream")
	check := xxHash32(plain, 0x9747b28c) & 0x0FFFFFFF
	badMagic := lz4TestBlock(0x10, 16, plain, plain, check)
	badMagic[0] = 'X'
	badEnd := lz4TestBlock(0x10, 16, nil, nil, 1)
	halfEmpty := lz4TestBlock(0x20, 16, []byte{0}, nil, check)
	rawMismatch := lz4TestBlock(0x10, 16, plain, plain[:3], check)
	badMethod := lz4TestBlock(0x30, 16, plain, plain, check)
	// A token declares its block's power-of-two bound: this original
	// length exceeds 1<<3.
	oversize := lz4TestBlock(0x10, 3, plain, plain, check)
	wrongCheck := lz4TestBlock(0x10, 16, plain, plain, check^0x55)
	trailing := lz4TestBlock(0x20, 16, append(append([]byte{0xf0, 0x00}, plain...), 0x30), plain, check)
	offsetZero := lz4TestBlock(0x20, 16, []byte{0x10, 'l', 0x00, 0x00}, plain, check)
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"bad magic", badMagic},
		{"nonzero end block", lz4TestStream(badEnd)},
		{"half-empty block", lz4TestStream(halfEmpty)},
		{"raw length mismatch", lz4TestStream(rawMismatch)},
		{"unknown method", lz4TestStream(badMethod)},
		{"block above declared size", lz4TestStream(oversize)},
		{"wrong checksum", lz4TestStream(wrongCheck)},
		{"trailing bytes parsed as token", lz4TestStream(trailing)},
		{"zero match offset", lz4TestStream(offsetZero)},
		{"truncated header", append([]byte("LZ4Bloc"), 0x10)},
		{"truncated data", lz4TestStream(lz4TestBlock(0x10, 16, plain, plain, check))[:30]},
		// The real reader stops on the closing empty block and throws
		// "Stream ended prematurely" when it cannot read a full header
		// after the last data block: a payload that just ends, whole
		// or cut mid-header, is a truncated record.
		{"missing end block", lz4TestBlock(0x10, 16, plain, plain, check)},
		{"truncated end header", append(lz4TestBlock(0x10, 16, plain, plain, check), []byte("LZ4Bloc")...)},
	} {
		if got, err := decodeLZ4BlockStream(tc.data); err == nil {
			t.Errorf("%s: accepted %d bytes", tc.name, len(got))
		}
	}
}

func TestLZ4BlockDecompressRejectsMalformed(t *testing.T) {
	// Literals longer than the destination, a match reaching before
	// the start of the output, a match running past the end, and a
	// match offset of zero are all rejected.
	for _, tc := range []struct {
		name string
		src  []byte
		dst  int
	}{
		{"literal overrun", []byte{0x40, 'l', 'z', '4'}, 2},
		{"truncated match offset", []byte{0x40, 'l', 'z', '4', 0x01, 0x00}, 4},
		{"match past end", []byte{0x10, 'l', 0x01, 0x00}, 3},
		{"zero offset", []byte{0x40, 'l', 'z', '4', 0x00, 0x00}, 6},
		{"literal extension overrun", []byte{0xff, 'l', 'z', '4', 0xff}, 8},
	} {
		if _, err := lz4BlockDecompress(tc.src, make([]byte, tc.dst)); err == nil {
			t.Errorf("%s: accepted", tc.name)
		}
	}
}

func TestEncodeLZ4BlockStreamShape(t *testing.T) {
	// The writer's own header: magic, method|level token, LE
	// compressed and original lengths, the masked checksum, and the
	// 21-byte closing block finish() adds (measured: token level
	// nibble 6 for the 64-KiB block size, raw fallback when LZ4 does
	// not shorten the block).
	plain := bytes.Repeat([]byte("0123456789abcdef"), 250)
	stream := encodeLZ4BlockStream(plain)
	if !bytes.Equal(stream[:8], []byte("LZ4Block")) {
		t.Fatal("stream missing magic")
	}
	if token := stream[8]; token&0xF0 != 0x20 || token&0x0F != 6 {
		t.Fatalf("data block token %#x, want method 0x20 level 6", token)
	}
	if got := binary.LittleEndian.Uint32(stream[13:]); got != uint32(len(plain)) {
		t.Fatalf("original length %d, want %d", got, len(plain))
	}
	if check := binary.LittleEndian.Uint32(stream[17:]); check != xxHash32(plain, 0x9747b28c)&0x0FFFFFFF {
		t.Fatalf("checksum %#x, want masked xxHash32 %#x", check, xxHash32(plain, 0x9747b28c)&0x0FFFFFFF)
	}
	end := stream[len(stream)-21:]
	if !bytes.Equal(end[:8], []byte("LZ4Block")) || end[8] != 0x16 {
		t.Fatalf("closing block %#x", end)
	}
	for _, v := range [][]byte{end[9:13], end[13:17], end[17:21]} {
		if !bytes.Equal(v, []byte{0, 0, 0, 0}) {
			t.Fatal("closing block lengths or checksum nonzero")
		}
	}
	got, err := decodeLZ4BlockStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("round trip content mismatch")
	}
}

func TestEncodeLZ4BlockStreamRawFallback(t *testing.T) {
	// flushBufferedData stores a block raw when its compressed length
	// is not shorter (measured: compressedLen >= originalLen -> raw
	// block, lengths equal).
	plain := javaPattern(60000, 3)
	stream := encodeLZ4BlockStream(plain)
	if token := stream[8]; token&0xF0 != 0x10 {
		t.Fatalf("incompressible token method %#x, want raw 0x10", token>>4)
	}
	if got := binary.LittleEndian.Uint32(stream[9:]); got != uint32(len(plain)) {
		t.Fatalf("raw compressed length %d, want %d", got, len(plain))
	}
	if !bytes.Equal(stream[21:21+len(plain)], plain) {
		t.Fatal("raw block content mismatch")
	}
	got, err := decodeLZ4BlockStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("raw fallback round trip mismatch")
	}
}

func TestEncodeLZ4BlockStreamEmptyPayloadIsEndBlock(t *testing.T) {
	// An empty record is exactly finish()'s closing block (measured:
	// no data block precedes it).
	stream := encodeLZ4BlockStream(nil)
	if len(stream) != 21 {
		t.Fatalf("empty payload stream %d bytes, want the 21-byte end block", len(stream))
	}
	if got, err := decodeLZ4BlockStream(stream); err != nil || len(got) != 0 {
		t.Fatalf("empty stream decode: got %d err %v", len(got), err)
	}
}

func TestEncodeLZ4BlockStreamSplitsAt64KiB(t *testing.T) {
	// Blocks split at the writer's 64-KiB block size, each with its
	// own header and checksum, ending with the one closing block.
	payload := javaPattern(lz4StreamBlockSize+11, 5)
	stream := encodeLZ4BlockStream(payload)
	blocks, i := 0, 0
	for i < len(stream)-21 {
		blocks++
		i += lz4BlockFrameHeader + int(binary.LittleEndian.Uint32(stream[i+9:]))
	}
	if blocks != 2 || i != len(stream)-21 {
		t.Fatalf("stream has %d blocks ending at %d of %d", blocks, i, len(stream))
	}
	got, err := decodeLZ4BlockStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("multi-block round trip mismatch")
	}
}

func TestLZ4BlockCompressRoundTripsThroughStrictReader(t *testing.T) {
	// The compressor's own output must satisfy the strict reader:
	// every match offset is in range, lengths decode exactly, and the
	// block ends with a literals-only sequence - and compressible
	// input actually compresses.
	shapes := [][]byte{
		{},
		[]byte("ab"),
		bytes.Repeat([]byte{0}, 500),
		javaPattern(12, 1),
		javaPattern(13, 1),
		javaPattern(60000, 9),
		bytes.Repeat(javaPattern(100, 2), 4000),
		javaPattern(70000, 4),
	}
	for _, src := range shapes {
		comp := lz4BlockCompress(src, nil)
		dst := make([]byte, len(src))
		consumed, err := lz4BlockDecompress(comp, dst)
		if err != nil {
			t.Fatalf("len %d: %v", len(src), err)
		}
		if consumed != len(comp) {
			t.Fatalf("len %d: consumed %d of %d", len(src), consumed, len(comp))
		}
		if !bytes.Equal(dst, src) {
			t.Fatalf("len %d: content mismatch", len(src))
		}
	}
	src := bytes.Repeat([]byte("0123456789abcdef"), 400)
	if comp := lz4BlockCompress(src, nil); len(comp) >= len(src) {
		t.Fatalf("repetitive input did not compress: %d -> %d", len(src), len(comp))
	}
	if comp := lz4BlockCompress([]byte{}, nil); len(comp) != 0 {
		t.Fatalf("empty input compressed to %d bytes", len(comp))
	}
}
