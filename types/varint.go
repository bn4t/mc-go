package types

import (
    "errors"
    "io"
)

// VarInt/VarLong codec per Minecraft (LEB128-like, no ZigZag).
// Max bytes: VarInt=5, VarLong=10.

var (
    ErrVarIntTooLong  = errors.New("varint too long")
    ErrVarLongTooLong = errors.New("varlong too long")
)

func ReadVarInt(r io.ByteReader) (int32, int, error) {
    var numRead int
    var result int32
    for {
        if numRead >= 5 { return 0, numRead, ErrVarIntTooLong }
        b, err := r.ReadByte()
        if err != nil { return 0, numRead, err }
        value := int32(b & 0x7F)
        result |= value << (7 * numRead)
        numRead++
        if (b & 0x80) == 0 { break }
    }
    return result, numRead, nil
}

func WriteVarInt(w io.ByteWriter, v int32) (int, error) {
    var n int
    uv := uint32(v)
    for {
        temp := byte(uv & 0x7F)
        uv >>= 7
        if uv != 0 { temp |= 0x80 }
        if err := w.WriteByte(temp); err != nil { return n, err }
        n++
        if uv == 0 { return n, nil }
    }
}

func ReadVarLong(r io.ByteReader) (int64, int, error) {
    var numRead int
    var result int64
    for {
        if numRead >= 10 { return 0, numRead, ErrVarLongTooLong }
        b, err := r.ReadByte()
        if err != nil { return 0, numRead, err }
        value := int64(b & 0x7F)
        result |= value << (7 * numRead)
        numRead++
        if (b & 0x80) == 0 { break }
    }
    return result, numRead, nil
}

func WriteVarLong(w io.ByteWriter, v int64) (int, error) {
    var n int
    uv := uint64(v)
    for {
        temp := byte(uv & 0x7F)
        uv >>= 7
        if uv != 0 { temp |= 0x80 }
        if err := w.WriteByte(temp); err != nil { return n, err }
        n++
        if uv == 0 { return n, nil }
    }
}

