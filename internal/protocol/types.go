package protocol

import (
	"encoding/binary"
	"errors"
	"io"
)

var ErrStringTooLong = errors.New("string too long")

const MaxStringLen = 32767

func WriteString(w io.Writer, s string) error {
	if len(s) > MaxStringLen {
		return ErrStringTooLong
	}
	if err := WriteVarInt(w, int32(len(s))); err != nil {
		return err
	}
	_, err := w.Write([]byte(s))
	return err
}

func ReadString(r io.Reader) (string, error) {
	n, err := ReadVarInt(r)
	if err != nil {
		return "", err
	}
	if n < 0 || n > MaxStringLen {
		return "", ErrStringTooLong
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func WriteUShort(w io.Writer, v uint16) error {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	_, err := w.Write(b[:])
	return err
}

func ReadUShort(r io.Reader) (uint16, error) {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b[:]), nil
}

func WriteLong(w io.Writer, v int64) error {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	_, err := w.Write(b[:])
	return err
}

func ReadLong(r io.Reader) (int64, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(b[:])), nil
}

func WriteUUID(w io.Writer, id [16]byte) error {
	_, err := w.Write(id[:])
	return err
}

func ReadUUID(r io.Reader) ([16]byte, error) {
	var id [16]byte
	_, err := io.ReadFull(r, id[:])
	return id, err
}
