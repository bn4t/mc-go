package protocol

import (
	"bytes"
	"compress/zlib"
	"errors"
	"io"
)

// MaxInflatedLen caps a single decompressed packet body (8 MiB).
const MaxInflatedLen = 8 << 20

var ErrInflateMismatch = errors.New("inflated length mismatch")

// Deflate zlib-compresses body.
func Deflate(body []byte) ([]byte, error) {
	var buf bytes.Buffer
	w := zlib.NewWriter(&buf)
	if _, err := w.Write(body); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Inflate zlib-decompresses body, expecting dataLen bytes out.
func Inflate(body []byte, dataLen int32) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	out, err := io.ReadAll(io.LimitReader(r, int64(dataLen)+1))
	if err != nil {
		return nil, err
	}
	if int32(len(out)) != dataLen {
		return nil, ErrInflateMismatch
	}
	return out, nil
}
