package conformance

import (
	"encoding/binary"
	"fmt"
	"math/bits"
)

// ChunkShape supplies independently known dimension and registry bounds. A
// decoder never infers compatibility by accepting arbitrary numeric IDs.
type ChunkShape struct{ Sections, BlockStates, Biomes int }
type ChunkBlockEntity struct {
	X, Z byte
	Y    int16
	Type int32
}
type Chunk struct {
	X, Z          int32
	Blocks        []int32
	BlockEntities []ChunkBlockEntity
}

// DecodeChunk checks the full 775/776 LevelChunkWithLight grammar, including
// packed palette bounds, anonymous block-entity NBT and light array framing.
// Blocks are section-major, then y,z,x. This deliberately implements decoding
// independently from the server's encoder and imports no internal packages.
func DecodeChunk(protocol int32, body []byte, shape ChunkShape) (Chunk, error) {
	var out Chunk
	if protocol != 775 && protocol != 776 {
		return out, fmt.Errorf("unsupported chunk profile%d", protocol)
	}
	if shape.Sections < 1 || shape.Sections > 256 || shape.BlockStates < 1 || shape.Biomes < 1 {
		return out, fmt.Errorf("invalid chunk registry/dimension bounds")
	}
	f := bodyFields(body)
	out.X = int32(binary.BigEndian.Uint32(padded(f.fixed(4), 4)))
	out.Z = int32(binary.BigEndian.Uint32(padded(f.fixed(4), 4)))
	for i, n := int32(0), f.count(); i < n && f.err == nil; i++ {
		typ := f.nonnegative()
		if typ > 5 {
			f.fail("unknown heightmap type")
		}
		count := f.count()
		f.fixed(int(count) * 8)
	}
	data := f.fixed(int(f.count()))
	sections := bodyFields(data)
	for range shape.Sections {
		for range 2 {
			v := binary.BigEndian.Uint16(padded(sections.fixed(2), 2))
			if v > 4096 {
				sections.fail("invalid section block count")
			}
		}
		out.Blocks = append(out.Blocks, decodePalette(sections, 4096, shape.BlockStates, 4, 8)...)
		_ = decodePalette(sections, 64, shape.Biomes, 1, 3)
	}
	if err := sections.complete(); err != nil {
		return out, fmt.Errorf("chunk sections: %w", err)
	}
	for i, n := int32(0), f.count(); i < n && f.err == nil; i++ {
		xz := f.byte()
		y := int16(binary.BigEndian.Uint16(padded(f.fixed(2), 2)))
		typ := f.nonnegative()
		nodes := 0
		if tag := f.byte(); tag != 0 {
			skipNBT(f, tag, 0, &nodes)
		}
		out.BlockEntities = append(out.BlockEntities, ChunkBlockEntity{X: xz >> 4, Z: xz & 15, Y: y, Type: typ})
	}
	var masks [4]int
	for i := range masks {
		n := f.count()
		if n > int32((shape.Sections+2+63)/64) {
			f.fail("light mask exceeds dimension")
		}
		for j := int32(0); j < n && f.err == nil; j++ {
			masks[i] += bits.OnesCount64(binary.BigEndian.Uint64(padded(f.fixed(8), 8)))
		}
	}
	for kind := range 2 {
		n := f.count()
		if int(n) != masks[kind] {
			f.fail("light array/mask count mismatch")
		}
		for i := int32(0); i < n && f.err == nil; i++ {
			size := f.count()
			if size != 2048 {
				f.fail("invalid light array length")
			}
			f.fixed(int(size))
		}
	}
	return out, f.complete()
}
func padded(b []byte, n int) []byte {
	if len(b) == n {
		return b
	}
	return make([]byte, n)
}
func decodePalette(f *fields, count, bound, minIndirect, maxIndirect int) []int32 {
	bpe := int(f.byte())
	var palette []int32
	if bpe == 0 {
		v := f.nonnegative()
		if int(v) >= bound {
			f.fail("palette value exceeds registry")
		}
		out := make([]int32, count)
		for i := range out {
			out[i] = v
		}
		return out
	}
	direct := bpe > maxIndirect
	if !direct {
		if bpe < minIndirect {
			f.fail("invalid indirect palette width")
		}
		n := f.count()
		if n < 1 || n > 1<<bpe {
			f.fail("invalid palette length")
		}
		for i := int32(0); i < n && f.err == nil; i++ {
			v := f.nonnegative()
			if int(v) >= bound {
				f.fail("palette value exceeds registry")
			}
			palette = append(palette, v)
		}
	} else if bpe != bits.Len(uint(bound-1)) {
		f.fail("direct palette width differs from registry")
	}
	if f.err != nil || bpe > 32 {
		return nil
	}
	per := 64 / bpe
	out := make([]int32, count)
	mask := uint64(1)<<bpe - 1
	for i := 0; i < count && f.err == nil; {
		word := binary.BigEndian.Uint64(padded(f.fixed(8), 8))
		for j := 0; j < per && i < count; j++ {
			v := int32(word & mask)
			word >>= bpe
			if direct {
				if int(v) >= bound {
					f.fail("packed value exceeds registry")
				}
			} else if int(v) >= len(palette) {
				f.fail("packed palette index out of range")
			} else {
				v = palette[v]
			}
			out[i] = v
			i++
		}
	}
	return out
}
