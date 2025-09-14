package proto

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

var ErrVarIntTooBig = errors.New("varint too big")

// ReadVarInt reads a Minecraft VarInt from r.
func ReadVarInt(r *bufio.Reader) (int32, int, error) {
	var num int32
	var numRead int
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, numRead, err
		}
		val := int32(b & 0x7F)
		num |= val << (7 * numRead)
		numRead++
		if numRead > 5 { // VarInt max 5 bytes
			return 0, numRead, ErrVarIntTooBig
		}
		if (b & 0x80) == 0 {
			break
		}
	}
	return num, numRead, nil
}

// WriteVarInt writes v as a Minecraft VarInt to w.
func WriteVarInt(w *bufio.Writer, v int32) (int, error) {
	var n int
	for {
		temp := byte(v & 0x7F)
		v >>= 7
		if v != 0 {
			temp |= 0x80
		}
		if err := w.WriteByte(temp); err != nil {
			return n, err
		}
		n++
		if v == 0 {
			break
		}
	}
	return n, nil
}

// ReadVarLong reads a Minecraft VarLong (up to 10 bytes)
func ReadVarLong(r *bufio.Reader) (int64, int, error) {
	var num int64
	var numRead int
	for {
		b, err := r.ReadByte()
		if err != nil {
			return 0, numRead, err
		}
		val := int64(b & 0x7F)
		num |= val << (7 * numRead)
		numRead++
		if numRead > 10 {
			return 0, numRead, ErrVarIntTooBig
		}
		if (b & 0x80) == 0 {
			break
		}
	}
	return num, numRead, nil
}

func ReadString(r *bufio.Reader) (string, error) {
	l, _, err := ReadVarInt(r)
	if err != nil {
		return "", err
	}
	if l < 0 {
		return "", fmt.Errorf("negative string len: %d", l)
	}
	buf := make([]byte, int(l))
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func WriteString(w *bufio.Writer, s string) error {
	if _, err := WriteVarInt(w, int32(len(s))); err != nil {
		return err
	}
	if _, err := w.WriteString(s); err != nil {
		return err
	}
	return nil
}
