package types

import (
    "errors"
    "io"
)

var ErrBytesTooLarge = errors.New("byte array exceeds maximum length")

// WriteBytesVar writes VarInt length followed by bytes.
func WriteBytesVar(w io.ByteWriter, bw io.Writer, b []byte, max int) (int, error) {
    if max <= 0 { max = 1 << 20 }
    if len(b) > max { return 0, ErrBytesTooLarge }
    n1, err := WriteVarInt(w, int32(len(b)))
    if err != nil { return n1, err }
    n2, err := bw.Write(b)
    return n1 + n2, err
}

// ReadBytesVar reads a VarInt length followed by that many bytes.
func ReadBytesVar(r io.ByteReader, br io.Reader, max int) ([]byte, int, error) {
    if max <= 0 { max = 1 << 20 }
    ln, n1, err := ReadVarInt(r)
    if err != nil { return nil, n1, err }
    if ln < 0 || int(ln) > max { return nil, n1, ErrBytesTooLarge }
    buf := make([]byte, int(ln))
    if _, err := io.ReadFull(br, buf); err != nil { return nil, n1, err }
    return buf, n1 + int(ln), nil
}

