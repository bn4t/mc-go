package world

// xxHash32, implemented from the public xxHash specification. The LZ4
// block stream a region file's version-4 records carry stores a 32-bit
// xxHash32 checksum of each block's decompressed bytes, seeded with the
// jpountz library's DEFAULT_SEED 0x9747b28c (measured: the LZ4BlockInputStream
// constructor chain vanilla's RegionFileVersion wires ends at
// XXHashFactory.newStreamingHash32(-1756908916).asChecksum()); a reader
// that skips the checksum accepts corrupt records the real server
// rejects. The constants below are the specification's primes; known
// answers are pinned against the very library jar vanilla 26.2 ships
// (vanilla/libraries/at/yawk/lz4/lz4-java) in xxhash32_test.go.

import "encoding/binary"

const (
	xxh32Prime1 = 2654435761
	xxh32Prime2 = 2246822519
	xxh32Prime3 = 3266489917
	xxh32Prime4 = 668265263
	xxh32Prime5 = 374761393
)

func xxh32Rotl(x uint32, r uint) uint32 { return x<<r | x>>(32-r) }

// xxHash32 computes the 32-bit xxHash of data under seed, per the
// public specification: 16-byte stripes through four accumulators, a
// 4-byte-lane remainder, a byte-lane tail, and the final avalanche.
func xxHash32(data []byte, seed uint32) uint32 {
	h := seed + xxh32Prime5
	i := 0
	if n := len(data); n >= 16 {
		acc := [4]uint32{
			seed + xxh32Prime1 + xxh32Prime2,
			seed + xxh32Prime2,
			seed,
			seed - xxh32Prime1,
		}
		for ; i <= len(data)-16; i += 16 {
			for lane := 0; lane < 4; lane++ {
				v := binary.LittleEndian.Uint32(data[i+lane*4:])
				acc[lane] += v * xxh32Prime2
				acc[lane] = xxh32Rotl(acc[lane], 13)
				acc[lane] *= xxh32Prime1
			}
		}
		h = xxh32Rotl(acc[0], 1) + xxh32Rotl(acc[1], 7) + xxh32Rotl(acc[2], 12) + xxh32Rotl(acc[3], 18)
	}
	h += uint32(len(data))
	for ; i <= len(data)-4; i += 4 {
		h += binary.LittleEndian.Uint32(data[i:]) * xxh32Prime3
		h = xxh32Rotl(h, 17) * xxh32Prime4
	}
	for ; i < len(data); i++ {
		h += uint32(data[i]) * xxh32Prime5
		h = xxh32Rotl(h, 11) * xxh32Prime1
	}
	h ^= h >> 15
	h *= xxh32Prime2
	h ^= h >> 13
	h *= xxh32Prime3
	h ^= h >> 16
	return h
}
