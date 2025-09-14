package proto

import (
	"bufio"
	"bytes"
	"io"
)

// ReadFrame reads a length-prefixed frame (VarInt length) and returns the body bytes.
// Compression is not supported in this minimal build.
func ReadFrame(r *bufio.Reader) ([]byte, int, error) {
	l, _, err := ReadVarInt(r)
	if err != nil {
		return nil, 0, err
	}
	if l < 0 {
		return nil, 0, io.ErrUnexpectedEOF
	}
	buf := make([]byte, int(l))
	n, err := io.ReadFull(r, buf)
	return buf, n, err
}

// WrapFrame encodes a packet-id-prefixed payload with VarInt length.
func WrapFrame(pid int32, payload []byte) []byte {
	// prefix pid VarInt + payload, then length VarInt
	pb := &bytes.Buffer{}
	bw := bufio.NewWriter(pb)
	_, _ = WriteVarInt(bw, pid)
	_, _ = bw.Write(payload)
	_ = bw.Flush()

	fb := &bytes.Buffer{}
	fw := bufio.NewWriter(fb)
	_, _ = WriteVarInt(fw, int32(len(pb.Bytes())))
	_, _ = fw.Write(pb.Bytes())
	_ = fw.Flush()
	return fb.Bytes()
}
