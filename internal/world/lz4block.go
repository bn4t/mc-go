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

// Write side. The compressor below emits the public block format any
// conforming LZ4 decoder accepts - including vanilla's JNI
// LZ4FastDecompressor - but its choice of matches is its own: the
// native library's compressed bytes for the same input may differ, and
// record parity is the format, not byte-identical compression.

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

// lz4StreamBlockSize is the block size of the one-argument
// LZ4BlockOutputStream constructor the vanilla lz4 region writer uses
// (measured: 64 KiB). lz4StreamLevel is the compressionLevel that
// constructor derives for it - max(0, 32 - nlz(blockSize-1) - 10),
// which lands in the token's low nibble; the reader adds 10 back to
// bound a block's original length by 1<<(6+10).
const (
	lz4StreamBlockSize = 64 << 10
	lz4StreamLevel     = 6
)

// lz4BlockCompress encodes src as one LZ4 block in the public block
// format and appends it to dst. Matches come from a one-candidate
// hash table like the reference compressor's fast mode; matches never
// start or extend into the final 12 bytes, so the block always ends
// with a literals-only sequence as the format requires.
func lz4BlockCompress(src, dst []byte) []byte {
	if len(src) == 0 {
		return dst
	}
	const (
		matchFindLimit = 12
		hashLog        = 16
	)
	var table [1 << hashLog]int32 // one-candidate: position + 1; 0 = empty
	out := dst
	literalStart := 0
	i := 0
	for i+matchFindLimit < len(src) {
		seq := binary.LittleEndian.Uint32(src[i:])
		h := (seq * 2654435761) >> (32 - hashLog)
		cand := int(table[h] - 1)
		table[h] = int32(i) + 1
		if cand < 0 || i-cand > 65535 || binary.LittleEndian.Uint32(src[cand:]) != seq {
			i++
			continue
		}
		// Extend forward only up to where the reference compressor
		// stops, so the tail stays literals-only.
		limit := len(src) - matchFindLimit
		matchLen := 4
		for i+matchLen < limit && src[cand+matchLen] == src[i+matchLen] {
			matchLen++
		}
		litLen := i - literalStart
		m := matchLen - 4
		var token byte
		if litLen < 15 {
			token = byte(litLen) << 4
		} else {
			token = 15 << 4
		}
		if m < 15 {
			token |= byte(m)
		} else {
			token |= 15
		}
		out = append(out, token)
		if litLen >= 15 {
			out = appendLZ4Length(out, litLen-15)
		}
		out = append(out, src[literalStart:i]...)
		out = binary.LittleEndian.AppendUint16(out, uint16(i-cand))
		if m >= 15 {
			out = appendLZ4Length(out, m-15)
		}
		for p := i + 1; p+4 <= len(src) && p < i+matchLen; p++ {
			ph := (binary.LittleEndian.Uint32(src[p:]) * 2654435761) >> (32 - hashLog)
			table[ph] = int32(p) + 1
		}
		i += matchLen
		literalStart = i
	}
	litLen := len(src) - literalStart
	if litLen < 15 {
		out = append(out, byte(litLen)<<4)
	} else {
		out = append(out, 15<<4)
		out = appendLZ4Length(out, litLen-15)
	}
	return append(out, src[literalStart:]...)
}

// appendLZ4Length appends a 255-continuation length extension.
func appendLZ4Length(out []byte, length int) []byte {
	for length >= 255 {
		out = append(out, 255)
		length -= 255
	}
	return append(out, byte(length))
}

// encodeLZ4BlockStream renders payload as the stream the vanilla lz4
// region writer produces (measured: RegionFileVersion id 4 wraps
// records in LZ4BlockOutputStream(OutputStream) - 64-KiB blocks whose
// header carries the magic, a token with raw (0x10) or LZ4 (0x20) in
// its method nibble and 6 in its level nibble, little-endian
// compressed and original lengths, and the block's xxHash32 checksum
// under seed 0x9747b28c masked to 28 bits, which is what the Checksum
// adapter's getValue reports). A block whose LZ4 encoding is not
// strictly shorter is stored raw. The empty block that finish() adds
// closes the stream; an empty payload is exactly that block.
func encodeLZ4BlockStream(payload []byte) []byte {
	var out []byte
	for start := 0; start < len(payload); start += lz4StreamBlockSize {
		end := start + lz4StreamBlockSize
		if end > len(payload) {
			end = len(payload)
		}
		block := payload[start:end]
		check := xxHash32(block, 0x9747b28c) & 0x0FFFFFFF
		method, data, size := byte(0x10), block, len(block)
		if compressed := lz4BlockCompress(block, nil); len(compressed) < len(block) {
			method, data, size = 0x20, compressed, len(compressed)
		}
		out = append(out, lz4BlockMagic[:]...)
		out = append(out, method|lz4StreamLevel)
		out = binary.LittleEndian.AppendUint32(out, uint32(size))
		out = binary.LittleEndian.AppendUint32(out, uint32(len(block)))
		out = binary.LittleEndian.AppendUint32(out, check)
		out = append(out, data...)
	}
	out = append(out, lz4BlockMagic[:]...)
	out = append(out, 0x10|lz4StreamLevel)
	out = binary.LittleEndian.AppendUint32(out, 0)
	out = binary.LittleEndian.AppendUint32(out, 0)
	return binary.LittleEndian.AppendUint32(out, 0)
}
