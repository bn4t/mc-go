package protocol

import (
	"encoding/binary"
	"errors"
	"io"
	"math"
)

// Scalar codecs beyond VarInt/String/UShort/Long/UUID.
// Clean-room from the public data-type docs:
// https://minecraft.wiki/w/Java_Edition_protocol/Data_types

var ErrBoolInvalid = errors.New("invalid boolean")

func WriteByte(w io.Writer, v byte) error {
	_, err := w.Write([]byte{v})
	return err
}

func ReadByte(r io.Reader) (byte, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return b[0], nil
}

func WriteBool(w io.Writer, v bool) error {
	if v {
		return WriteByte(w, 1)
	}
	return WriteByte(w, 0)
}

func ReadBool(r io.Reader) (bool, error) {
	b, err := ReadByte(r)
	if err != nil {
		return false, err
	}
	switch b {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, ErrBoolInvalid
	}
}

func WriteInt(w io.Writer, v int32) error {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(v))
	_, err := w.Write(b[:])
	return err
}

func ReadInt(r io.Reader) (int32, error) {
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return int32(binary.BigEndian.Uint32(b[:])), nil
}

func WriteFloat(w io.Writer, v float32) error {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], math.Float32bits(v))
	_, err := w.Write(b[:])
	return err
}

func ReadFloat(r io.Reader) (float32, error) {
	var b [4]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return math.Float32frombits(binary.BigEndian.Uint32(b[:])), nil
}

func WriteShort(w io.Writer, v int16) error {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], uint16(v))
	_, err := w.Write(b[:])
	return err
}

func ReadShort(r io.Reader) (int16, error) {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return int16(binary.BigEndian.Uint16(b[:])), nil
}

func WriteDouble(w io.Writer, v float64) error {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], math.Float64bits(v))
	_, err := w.Write(b[:])
	return err
}

func ReadDouble(r io.Reader) (float64, error) {
	var bb [8]byte
	if _, err := io.ReadFull(r, bb[:]); err != nil {
		return 0, err
	}
	return math.Float64frombits(binary.BigEndian.Uint64(bb[:])), nil
}

// EncodePosition packs x/y/z into the 64-bit BlockPos wire format,
// clean-room from the public data-type docs:
// ((x & 0x3FFFFFF) << 38) | ((z & 0x3FFFFFF) << 12) | (y & 0xFFF).
func EncodePosition(x, y, z int32) uint64 {
	return (uint64(uint32(x)&0x3FFFFFF) << 38) |
		(uint64(uint32(z)&0x3FFFFFF) << 12) |
		uint64(uint32(y)&0xFFF)
}

// DecodePosition unpacks a BlockPos long, sign-extending each axis.
func DecodePosition(v uint64) (x, y, z int32) {
	x = int32(v >> 38)
	if x&0x2000000 != 0 {
		x |= ^int32(0x3FFFFFF)
	} else {
		x &= 0x3FFFFFF
	}
	z = int32((v >> 12) & 0x3FFFFFF)
	if z&0x2000000 != 0 {
		z |= ^int32(0x3FFFFFF)
	} else {
		z &= 0x3FFFFFF
	}
	y = int32(v & 0xFFF)
	if y&0x800 != 0 {
		y |= ^int32(0xFFF)
	} else {
		y &= 0xFFF
	}
	return x, y, z
}

// WritePosition writes a packed BlockPos as a big-endian long.
func WritePosition(w io.Writer, x, y, z int32) error {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], EncodePosition(x, y, z))
	_, err := w.Write(b[:])
	return err
}

// ReadPosition reads a packed BlockPos long.
func ReadPosition(r io.Reader) (x, y, z int32, err error) {
	var b [8]byte
	if _, err = io.ReadFull(r, b[:]); err != nil {
		return 0, 0, 0, err
	}
	x, y, z = DecodePosition(binary.BigEndian.Uint64(b[:]))
	return x, y, z, nil
}
