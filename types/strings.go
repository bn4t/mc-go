package types

import (
    "errors"
    "io"
    "unicode/utf8"
)

// String codec: VarInt length prefix of UTF-8 bytes.
// Enforce sane caps to prevent OOM; cap default at 262144 bytes (256 KiB).

var ErrStringTooLarge = errors.New("string exceeds maximum length")

// WriteString writes a VarInt length followed by UTF-8 bytes.
func WriteString(w io.ByteWriter, bw io.Writer, s string, max int) (int, error) {
    if max <= 0 { max = 262144 }
    if len(s) > max { return 0, ErrStringTooLarge }
    if !utf8.ValidString(s) { return 0, errors.New("invalid UTF-8") }
    n1, err := WriteVarInt(w, int32(len(s)))
    if err != nil { return n1, err }
    n2, err := bw.Write([]byte(s))
    return n1 + n2, err
}

// ReadString reads a VarInt length and then that many bytes.
func ReadString(r io.ByteReader, br io.Reader, max int) (string, int, error) {
    if max <= 0 { max = 262144 }
    ln, n1, err := ReadVarInt(r)
    if err != nil { return "", n1, err }
    if ln < 0 || int(ln) > max { return "", n1, ErrStringTooLarge }
    buf := make([]byte, int(ln))
    read := 0
    for read < int(ln) {
        m, err := br.Read(buf[read:])
        if err != nil { return "", n1 + read, err }
        read += m
    }
    if !utf8.Valid(buf) { return "", n1 + read, errors.New("invalid UTF-8") }
    return string(buf), n1 + read, nil
}

