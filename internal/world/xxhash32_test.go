package world

import (
	"testing"
)

// javaPattern regenerates the deterministic inputs the vector dump
// used: java.util.Random(seed).nextInt(94) + 33, one byte per element.
// The xxHash32 vectors below were measured against the exact library
// vanilla 26.2 ships - vanilla/libraries/at/yawk/lz4/lz4-java 1.10.1,
// XXHashFactory.fastestInstance().hash32() - over these inputs under
// both the LZ4 stream's DEFAULT_SEED 0x9747b28c and seed 0 (whose
// empty-input value, 02cc5d05, is also the public specification's
// sanity vector). The lengths cover every band: no stripes, stripes
// with a 4-byte-lane remainder, stripes with a byte-only remainder,
// stripes with both, and a zero-remainder multi-stripe input.
func javaPattern(n int, seed int64) []byte {
	scratch := int64(seed ^ 0x5DEECE66D)
	next := func(bits int) int64 {
		scratch = (scratch*0x5DEECE66D + 0xB) & ((1 << 48) - 1)
		return int64(uint64(scratch) >> (48 - uint(bits)))
	}
	out := make([]byte, n)
	for i := range out {
		r := int(next(31)) % 94
		if r < 0 {
			r += 94
		}
		out[i] = byte(r + 33)
	}
	return out
}

func javaChunky(n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(i / 4096)
	}
	return out
}

func TestXXHash32Vectors(t *testing.T) {
	for _, tc := range []struct {
		input []byte
		seed  uint32
		want  uint32
	}{
		{nil, 0, 0x02cc5d05},
		{nil, 0x9747b28c, 0x8d3b42d8},
		{[]byte("a"), 0, 0x550d7456},
		{[]byte("a"), 0x9747b28c, 0x12b7e114},
		{[]byte("lz4block stream"), 0, 0x8678e10f},
		{[]byte("lz4block stream"), 0x9747b28c, 0x9e98dc44},
		{javaPattern(31, 1), 0, 0xa48b3e10},
		{javaPattern(31, 1), 0x9747b28c, 0x902f5a71},
		{javaPattern(100, 2), 0, 0x6bd74d42},
		{javaPattern(100, 2), 0x9747b28c, 0x5dc3c864},
		{javaPattern(163, 3), 0, 0x6d2da24b},
		{javaPattern(163, 3), 0x9747b28c, 0x77283160},
		{javaChunky(70000), 0, 0x4eeed58a},
		{javaChunky(70000), 0x9747b28c, 0x22afa333},
		{javaPattern(200000, 4), 0, 0x9afabf0f},
		{javaPattern(200000, 4), 0x9747b28c, 0x42c521a5},
	} {
		got := xxHash32(tc.input, tc.seed)
		if got != tc.want {
			t.Errorf("xxHash32(len=%d, seed=%#x) = %#08x, want %#08x", len(tc.input), tc.seed, got, tc.want)
		}
	}
}
