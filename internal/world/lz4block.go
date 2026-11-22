package world

// LZ4 record decompression for region files. Vanilla 26.2's
// RegionFileVersion id 4 wraps records in jpountz's LZ4BlockInputStream
// (measured from the yawk lz4-java 1.10.1 jar vanilla ships under
// vanilla/libraries): a stream of blocks, each a 21-byte header - the
// 8-byte magic "LZ4Block", a token byte holding the compression method
// in its high nibble (0x10 raw, 0x20 LZ4) and log2(block size)-10 in
// its low nibble, then little-endian u32s of compressed length,
// original length, and the xxHash32 of the decompressed bytes under
// seed 0x9747b28c - followed by the compressed length's worth of block
// data. The stream ends with an empty block (all three lengths zero,
// checksum zero). A record that violates any of this is rejected by the
// real reader with "Stream is corrupted", which the chunk loader
// treats as a chunk that failed to read and regenerates; this reader
// matches that outcome.
//
// The LZ4 block format itself is public: a sequence of tokens whose
// high nibble encodes literal length, low nibble encodes match length
// minus four (255 continuation bytes extend either), literals, then a
// 16-bit little-endian match offset; the last sequence is literals
// only. The reader verifies the exact consumed and produced lengths
// the real reader requires of LZ4FastDecompressor.

import (
	"encoding/binary"
	"fmt"
)

// lz4BlockFrameHeader is the jpountz per-block header: 8 magic bytes,
// one token, three little-endian u32s.
const lz4BlockFrameHeader = 21

var lz4BlockMagic = [8]byte{'L', 'Z', '4', 'B', 'l', 'o', 'c', 'k'}

// lz4BlockDecompress decodes one LZ4 block (the public block format,
// no frame) into dst, returning the compressed bytes consumed. It is
// strict: a literal or match that runs past its source, an offset of
// zero or past what is written, or a match extending past dst is
// malformed, as is anything other than the final literals-only
// sequence trailing bytes.
func lz4BlockDecompress(src, dst []byte) (int, error) {
	var w int // written to dst
	i := 0
	for i < len(src) {
		token := src[i]
		i++
		litLen := int(token >> 4)
		if litLen == 15 {
			for {
				if i >= len(src) {
					return 0, fmt.Errorf("lz4: literal length overruns block")
				}
				b := int(src[i])
				i++
				litLen += b
				if b != 255 {
					break
				}
			}
		}
		if litLen > 0 {
			if i+litLen > len(src) || w+litLen > len(dst) {
				return 0, fmt.Errorf("lz4: literals overrun block or destination")
			}
			w += copy(dst[w:], src[i:i+litLen])
			i += litLen
		}
		if i == len(src) {
			// The last sequence carries literals only.
			break
		}
		if i+2 > len(src) {
			return 0, fmt.Errorf("lz4: truncated match offset")
		}
		offset := int(binary.LittleEndian.Uint16(src[i:]))
		i += 2
		if offset < 1 || offset > w {
			return 0, fmt.Errorf("lz4: match offset out of range")
		}
		matchLen := int(token & 0xF)
		if matchLen == 15 {
			for {
				if i >= len(src) {
					return 0, fmt.Errorf("lz4: match length overruns block")
				}
				b := int(src[i])
				i++
				matchLen += b
				if b != 255 {
					break
				}
			}
		}
		matchLen += 4
		if w+matchLen > len(dst) {
			return 0, fmt.Errorf("lz4: match overruns destination")
		}
		// LZ4 matches may overlap their own output, so copy bytewise
		// from the already-written region.
		for j := 0; j < matchLen; j++ {
			dst[w] = dst[w-offset]
			w++
		}
	}
	return i, nil
}

// decodeLZ4BlockStream decompresses a whole jpountz LZ4Block stream,
// the format of a version-4 region record payload.
func decodeLZ4BlockStream(payload []byte) ([]byte, error) {
	var out []byte
	i := 0
	for {
		if i == len(payload) {
			// The real reader (LZ4BlockInputStream's one-argument
			// constructor, the one RegionFileVersion version 4 wraps
			// records in) stops on the closing empty block and throws
			// "Stream ended prematurely" on a header it cannot read
			// in full: a payload that simply ends was truncated and
			// the chunk regenerates.
			return nil, fmt.Errorf("lz4 stream: missing end block")
		}
		if i+lz4BlockFrameHeader > len(payload) {
			return nil, fmt.Errorf("lz4 stream: truncated block header")
		}
		for j, b := range lz4BlockMagic {
			if payload[i+j] != b {
				return nil, fmt.Errorf("lz4 stream: bad block magic")
			}
		}
		token := payload[i+8]
		method := token & 0xF0
		level := int(token&0x0F) + 10
		compressedLen := int(binary.LittleEndian.Uint32(payload[i+9:]))
		originalLen := int(binary.LittleEndian.Uint32(payload[i+13:]))
		// The writer stores (int)(checksum & 0x0FFFFFFF): jpountz's
		// StreamingXXHash32.asChecksum() getValue() masks the 32-bit
		// hash to its low 28 bits (measured: i2l; ldc2_w 268435455;
		// land), and the reader compares through the same adapter, so
		// the high nibble of the stored field is always zero.
		check := binary.LittleEndian.Uint32(payload[i+17:])
		i += lz4BlockFrameHeader
		switch method {
		case 0x10, 0x20:
		default:
			return nil, fmt.Errorf("lz4 stream: unknown compression method")
		}
		// The real reader bounds a block's original length by the
		// power of two its token declares (which also rejects the
		// Java-int negatives) and rejects degenerate length pairs.
		if originalLen > 1<<uint(level) {
			return nil, fmt.Errorf("lz4 stream: invalid block lengths")
		}
		if originalLen == 0 && compressedLen == 0 {
			if check != 0 {
				return nil, fmt.Errorf("lz4 stream: corrupted end block")
			}
			// The stream's final empty block; the record ends here.
			return out, nil
		}
		if (originalLen == 0) != (compressedLen == 0) {
			return nil, fmt.Errorf("lz4 stream: half-empty block")
		}
		if method == 0x10 && originalLen != compressedLen {
			return nil, fmt.Errorf("lz4 stream: raw block length mismatch")
		}
		if i+compressedLen > len(payload) {
			return nil, fmt.Errorf("lz4 stream: truncated block data")
		}
		block := payload[i : i+compressedLen]
		var decoded []byte
		if method == 0x10 {
			decoded = block
		} else {
			decoded = make([]byte, originalLen)
			consumed, err := lz4BlockDecompress(block, decoded)
			if err != nil {
				return nil, err
			}
			if consumed != compressedLen {
				return nil, fmt.Errorf("lz4 stream: trailing bytes in block")
			}
		}
		if xxHash32(decoded, 0x9747b28c)&0x0FFFFFFF != check {
			return nil, fmt.Errorf("lz4 stream: checksum mismatch")
		}
		out = append(out, decoded...)
		i += compressedLen
	}
}
