package types

import (
    "encoding/hex"
    "errors"
    "io"
    "strings"
)

var ErrUUIDLength = errors.New("uuid must be 16 bytes")

func UUIDBytesFromString(s string) ([16]byte, error) {
    s = strings.ToLower(strings.ReplaceAll(s, "-", ""))
    var out [16]byte
    if len(s) != 32 { return out, ErrUUIDLength }
    b, err := hex.DecodeString(s)
    if err != nil { return out, err }
    copy(out[:], b)
    return out, nil
}

func UUIDString(b [16]byte) string {
    hexs := hex.EncodeToString(b[:])
    return hexs[0:8] + "-" + hexs[8:12] + "-" + hexs[12:16] + "-" + hexs[16:20] + "-" + hexs[20:32]
}

func WriteUUID(w io.Writer, u [16]byte) (int, error) { return w.Write(u[:]) }

func ReadUUID(r io.Reader) ([16]byte, int, error) {
    var out [16]byte
    n, err := r.Read(out[:])
    if err != nil { return out, n, err }
    if n != 16 { return out, n, io.ErrUnexpectedEOF }
    return out, n, nil
}

