package types

import (
    "encoding/binary"
    "io"
)

func WriteU16(w io.Writer, v uint16) (int, error) {
    var buf [2]byte
    binary.BigEndian.PutUint16(buf[:], v)
    return w.Write(buf[:])
}

func ReadU16(r io.Reader) (uint16, int, error) {
    var buf [2]byte
    if _, err := io.ReadFull(r, buf[:]); err != nil { return 0, 0, err }
    return binary.BigEndian.Uint16(buf[:]), 2, nil
}

func WriteU8(w io.Writer, v uint8) (int, error) { var b [1]byte; b[0] = byte(v); return w.Write(b[:]) }
func ReadU8(r io.Reader) (uint8, int, error) { var b [1]byte; if _, err := io.ReadFull(r, b[:]); err != nil { return 0, 0, err }; return uint8(b[0]), 1, nil }
func WriteI8(w io.Writer, v int8) (int, error) { return WriteU8(w, uint8(v)) }
func ReadI8(r io.Reader) (int8, int, error) { u, n, err := ReadU8(r); return int8(u), n, err }
func WriteBool(w io.Writer, v bool) (int, error) { if v { return WriteU8(w, 1) } ; return WriteU8(w, 0) }
func ReadBool(r io.Reader) (bool, int, error) { u, n, err := ReadU8(r); if err != nil { return false, n, err }; return u != 0, n, nil }

