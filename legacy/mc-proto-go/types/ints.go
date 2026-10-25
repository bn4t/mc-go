package types

import (
    "encoding/binary"
    "io"
    "math"
)

func WriteI16(w io.Writer, v int16) (int, error) {
    var buf [2]byte
    binary.BigEndian.PutUint16(buf[:], uint16(v))
    return w.Write(buf[:])
}

func ReadI16(r io.Reader) (int16, int, error) {
    var buf [2]byte
    if _, err := io.ReadFull(r, buf[:]); err != nil { return 0, 0, err }
    return int16(binary.BigEndian.Uint16(buf[:])), 2, nil
}

func WriteI32(w io.Writer, v int32) (int, error) {
    var buf [4]byte
    binary.BigEndian.PutUint32(buf[:], uint32(v))
    return w.Write(buf[:])
}

func ReadI32(r io.Reader) (int32, int, error) {
    var buf [4]byte
    if _, err := io.ReadFull(r, buf[:]); err != nil { return 0, 0, err }
    return int32(binary.BigEndian.Uint32(buf[:])), 4, nil
}

func WriteI64(w io.Writer, v int64) (int, error) {
    var buf [8]byte
    binary.BigEndian.PutUint64(buf[:], uint64(v))
    return w.Write(buf[:])
}

func ReadI64(r io.Reader) (int64, int, error) {
    var buf [8]byte
    if _, err := io.ReadFull(r, buf[:]); err != nil { return 0, 0, err }
    return int64(binary.BigEndian.Uint64(buf[:])), 8, nil
}

func WriteF32(w io.Writer, v float32) (int, error) { return WriteI32(w, int32(math.Float32bits(v))) }
func ReadF32(r io.Reader) (float32, int, error) { u, n, err := ReadI32(r); return math.Float32frombits(uint32(u)), n, err }
func WriteF64(w io.Writer, v float64) (int, error) { return WriteI64(w, int64(math.Float64bits(v))) }
func ReadF64(r io.Reader) (float64, int, error) { u, n, err := ReadI64(r); return math.Float64frombits(uint64(u)), n, err }

