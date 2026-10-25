package protocol

import (
	"errors"
	"io"
)

// Minecraft Java Edition uses signed 32-bit VarInts, max 5 bytes.
// See https://minecraft.wiki/w/Java_Edition_protocol/Data_types

var ErrVarIntTooBig = errors.New("varint too big")

func WriteVarInt(w io.Writer, v int32) error {
	u := uint32(v)
	var buf [5]byte
	i := 0
	for {
		b := byte(u & 0x7F)
		u >>= 7
		if u != 0 {
			b |= 0x80
		}
		buf[i] = b
		i++
		if u == 0 {
			break
		}
	}
	_, err := w.Write(buf[:i])
	return err
}

func ReadVarInt(r io.Reader) (int32, error) {
	var result uint32
	var shift uint
	var b [1]byte
	for i := 0; i < 5; i++ {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		result |= uint32(b[0]&0x7F) << shift
		shift += 7
		if b[0]&0x80 == 0 {
			return int32(result), nil
		}
	}
	return 0, ErrVarIntTooBig
}

// AppendVarInt appends to dst without allocating a writer.
func AppendVarInt(dst []byte, v int32) []byte {
	u := uint32(v)
	for {
		b := byte(u & 0x7F)
		u >>= 7
		if u != 0 {
			b |= 0x80
		}
		dst = append(dst, b)
		if u == 0 {
			return dst
		}
	}
}

// ReadVarIntFromBytes reads a VarInt from the front of buf.
func ReadVarIntFromBytes(buf []byte) (int32, int, error) {
	var result uint32
	var shift uint
	for i := 0; i < len(buf) && i < 5; i++ {
		b := buf[i]
		result |= uint32(b&0x7F) << shift
		shift += 7
		if b&0x80 == 0 {
			return int32(result), i + 1, nil
		}
	}
	if len(buf) >= 5 {
		return 0, 0, ErrVarIntTooBig
	}
	return 0, 0, io.ErrUnexpectedEOF
}

func VarIntLen(v int32) int {
	u := uint32(v)
	n := 1
	for u >>= 7; u != 0; u >>= 7 {
		n++
	}
	return n
}

// Varlongs are the 64-bit variant, max 10 bytes.

func WriteVarLong(w io.Writer, v int64) error {
	u := uint64(v)
	var buf [10]byte
	i := 0
	for {
		b := byte(u & 0x7F)
		u >>= 7
		if u != 0 {
			b |= 0x80
		}
		buf[i] = b
		i++
		if u == 0 {
			break
		}
	}
	_, err := w.Write(buf[:i])
	return err
}

func ReadVarLong(r io.Reader) (int64, error) {
	var result uint64
	var shift uint
	var b [1]byte
	for i := 0; i < 10; i++ {
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, err
		}
		result |= uint64(b[0]&0x7F) << shift
		shift += 7
		if b[0]&0x80 == 0 {
			return int64(result), nil
		}
	}
	return 0, ErrVarIntTooBig
}
