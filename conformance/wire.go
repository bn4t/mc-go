package conformance

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"unicode/utf8"
)

// The codec is intentionally independent of the implementation under test.
// Packet IDs and layouts are pinned to Java Edition 26.1/775 and 26.2/776.
const maxFrame = 2*1024*1024 - 1

type packet struct {
	id   int32
	data []byte
}
type wire struct {
	conn      net.Conn
	threshold int32
	events    *[]Event
	budget    *int
}

func appendInt(b []byte, n int32) []byte {
	u := uint32(n)
	for {
		v := byte(u & 127)
		u >>= 7
		if u != 0 {
			v |= 128
		}
		b = append(b, v)
		if u == 0 {
			return b
		}
	}
}
func readInt(r io.ByteReader) (int32, error) {
	var u uint32
	for i := 0; i < 5; i++ {
		b, e := r.ReadByte()
		if e != nil {
			return 0, e
		}
		if i == 4 && b&0xf0 != 0 {
			return 0, fmt.Errorf("VarInt overflow")
		}
		u |= uint32(b&127) << uint(7*i)
		if b&128 == 0 {
			return int32(u), nil
		}
	}
	return 0, fmt.Errorf("VarInt exceeds five bytes")
}

type byteReader struct{ io.Reader }

func (r byteReader) ReadByte() (byte, error) {
	var b [1]byte
	_, e := io.ReadFull(r.Reader, b[:])
	return b[0], e
}
func str(b []byte, s string) []byte { b = appendInt(b, int32(len(s))); return append(b, s...) }
func takeString(r *bytes.Reader) (string, error) {
	n, e := readInt(r)
	if e != nil {
		return "", e
	}
	if n < 0 || int64(n) > int64(r.Len()) {
		return "", fmt.Errorf("invalid string length %d", n)
	}
	b := make([]byte, n)
	_, e = io.ReadFull(r, b)
	if e != nil {
		return "", e
	}
	if !utf8.Valid(b) {
		return "", fmt.Errorf("invalid UTF-8")
	}
	return string(b), nil
}
func (w *wire) record(dir string, raw []byte, id int32, err error) {
	e := Event{Direction: dir, PacketID: id, WireBytes: len(raw)}
	n := len(raw)
	if n > *w.budget {
		n = *w.budget
		e.Truncated = true
	}
	if n > 0 {
		e.WireHex = hex.EncodeToString(raw[:n])
		*w.budget -= n
	}
	if err != nil {
		e.Error = err.Error()
	}
	*w.events = append(*w.events, e)
}
func (w *wire) writeRaw(raw []byte, id int32) error {
	b := raw
	for len(b) > 0 {
		n, e := w.conn.Write(b)
		b = b[n:]
		if e != nil {
			w.record("serverbound", raw[:len(raw)-len(b)], id, e)
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	w.record("serverbound", raw, id, nil)
	return nil
}
func (w *wire) send(id int32, payload []byte) error {
	body := appendInt(nil, id)
	body = append(body, payload...)
	if w.threshold >= 0 {
		if len(body) >= int(w.threshold) {
			var z bytes.Buffer
			zw := zlib.NewWriter(&z)
			if _, e := zw.Write(body); e != nil {
				return e
			}
			if e := zw.Close(); e != nil {
				return e
			}
			body = append(appendInt(nil, int32(len(body))), z.Bytes()...)
		} else {
			body = append([]byte{0}, body...)
		}
	}
	frame := appendInt(nil, int32(len(body)))
	frame = append(frame, body...)
	return w.writeRaw(frame, id)
}
func (w *wire) read() (p packet, err error) {
	var raw bytes.Buffer
	defer func() { w.record("clientbound", raw.Bytes(), p.id, err) }()
	n, e := readInt(byteReader{io.TeeReader(w.conn, &raw)})
	if e != nil {
		return p, e
	}
	if raw.Len() > 3 {
		return p, fmt.Errorf("frame length VarInt exceeds three bytes")
	}
	if n < 1 || n > maxFrame {
		return p, fmt.Errorf("frame length %d out of bounds", n)
	}
	b := make([]byte, n)
	if _, e = io.ReadFull(io.TeeReader(w.conn, &raw), b); e != nil {
		return p, e
	}
	if w.threshold >= 0 {
		r := bytes.NewReader(b)
		size, e := readInt(r)
		if e != nil {
			return p, e
		}
		if size < 0 || size > maxFrame {
			return p, fmt.Errorf("uncompressed length %d out of bounds", size)
		}
		if size == 0 {
			b = b[len(b)-r.Len():]
			if len(b) >= int(w.threshold) {
				return p, fmt.Errorf("uncompressed packet at or above threshold")
			}
		} else {
			if size < w.threshold {
				return p, fmt.Errorf("compressed packet below threshold")
			}
			zr, e := zlib.NewReader(r)
			if e != nil {
				return p, e
			}
			b, e = io.ReadAll(io.LimitReader(zr, int64(size)+1))
			ce := zr.Close()
			if e != nil {
				return p, e
			}
			if ce != nil {
				return p, ce
			}
			if len(b) != int(size) {
				return p, fmt.Errorf("compression size mismatch: got %d want %d", len(b), size)
			}
			if r.Len() != 0 {
				return p, fmt.Errorf("trailing compressed bytes")
			}
		}
	}
	r := bytes.NewReader(b)
	p.id, e = readInt(r)
	if e != nil {
		return p, e
	}
	if p.id < 0 {
		return p, fmt.Errorf("negative packet ID")
	}
	p.data = b[len(b)-r.Len():]
	return p, nil
}
func (w *wire) handshake(c Config, next int32) error {
	host, port, e := net.SplitHostPort(c.Endpoint)
	if e != nil {
		return e
	}
	var n uint16
	if _, e = fmt.Sscan(port, &n); e != nil {
		return e
	}
	if c.HandshakeHost != "" {
		host = c.HandshakeHost
	}
	b := appendInt(nil, c.Protocol)
	b = str(b, host)
	b = binary.BigEndian.AppendUint16(b, n)
	b = appendInt(b, next)
	return w.send(0, b)
}
