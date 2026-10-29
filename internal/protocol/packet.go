package protocol

import (
	"bytes"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"mc-go/internal/nbt"
)

// Packet is a single Minecraft packet payload (ID + data), without the length prefix.
type Packet struct {
	ID   int32
	Data []byte
}

const MaxPacketLen = 1 << 20 // 1 MiB safety cap, vanilla uses ~2MB+ for chunks later

var ErrPacketTooLarge = errors.New("packet too large")

// Writer buffers packet bodies and flushes framed packets.
// Compression is off until SetThreshold is called with a value >= 0,
// mirroring the vanilla Set Compression handshake.
type Writer struct {
	mu        sync.Mutex // protects complete frames and compression changes
	w         io.Writer
	threshold int32
	noComp    bool
}

func NewWriter(w io.Writer) *Writer { return &Writer{w: w, threshold: -1, noComp: true} }

// SetThreshold enables compression: bodies with uncompressed length
// >= n are zlib-compressed. Negative n disables compression.
func (pw *Writer) SetThreshold(n int32) {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	if n < 0 {
		pw.threshold, pw.noComp = -1, true
		return
	}
	pw.threshold, pw.noComp = n, false
}

// WritePacket writes one framed packet: length VarInt + id VarInt + data,
// or with compression: length + data-length + (id + data, raw or zlib).
func (pw *Writer) WritePacket(id int32, data []byte) error {
	pw.mu.Lock()
	defer pw.mu.Unlock()
	body := AppendVarInt(nil, id)
	body = append(body, data...)
	if pw.noComp {
		var hdr bytes.Buffer
		if err := WriteVarInt(&hdr, int32(len(body))); err != nil {
			return err
		}
		if err := pw.writePart(hdr.Bytes()); err != nil {
			return err
		}
		return pw.writePart(body)
	}
	if int32(len(body)) < pw.threshold {
		var frame bytes.Buffer
		_ = WriteVarInt(&frame, int32(VarIntLen(0)+len(body)))
		_ = WriteVarInt(&frame, 0) // data length 0 = uncompressed
		if err := pw.writePart(frame.Bytes()); err != nil {
			return err
		}
		return pw.writePart(body)
	}
	deflated, err := Deflate(body)
	if err != nil {
		return err
	}
	var frame bytes.Buffer
	_ = WriteVarInt(&frame, int32(VarIntLen(int32(len(body)))+len(deflated)))
	_ = WriteVarInt(&frame, int32(len(body)))
	if err := pw.writePart(frame.Bytes()); err != nil {
		return err
	}
	return pw.writePart(deflated)
}

// writePart enforces io.Writer's full-write contract. A truncated frame must
// fail the connection rather than allowing the next packet to corrupt it.
func (pw *Writer) writePart(p []byte) error {
	n, err := pw.w.Write(p)
	if err == nil && n != len(p) {
		return io.ErrShortWrite
	}
	return err
}

// Buffer helpers to build packet bodies without imports.
type Buffer struct {
	bytes.Buffer
}

func (b *Buffer) VarInt(v int32)   { _ = WriteVarInt(b, v) }
func (b *Buffer) VarLong(v int64)  { _ = WriteVarLong(b, v) }
func (b *Buffer) String(s string)  { _ = WriteString(b, s) }
func (b *Buffer) UShort(v uint16)  { _ = WriteUShort(b, v) }
func (b *Buffer) Long(v int64)     { _ = WriteLong(b, v) }
func (b *Buffer) UUID(v [16]byte)  { _ = WriteUUID(b, v) }
func (b *Buffer) Byte(v byte)      { _ = WriteByte(b, v) }
func (b *Buffer) Bool(v bool)      { _ = WriteBool(b, v) }
func (b *Buffer) Int(v int32)      { _ = WriteInt(b, v) }
func (b *Buffer) Short(v int16)    { _ = WriteShort(b, v) }
func (b *Buffer) Float(v float32)  { _ = WriteFloat(b, v) }
func (b *Buffer) Double(v float64) { _ = WriteDouble(b, v) }

// Position appends a packed BlockPos long.
func (b *Buffer) Position(x, y, z int32) { _ = WritePosition(b, x, y, z) }

// Raw appends raw bytes to the packet body.
func (b *Buffer) Raw(p []byte) { _, _ = b.Write(p) }

// Reader reads framed packets. Compression is off until SetThreshold
// is called with a value >= 0. A read deadline preserves a partial frame
// so the caller can clear the deadline and resume without losing bytes.
type Reader struct {
	r         io.Reader
	threshold int32
	noComp    bool
	length    [5]byte
	lengthN   int
	frame     []byte
	frameN    int
}

func NewReader(r io.Reader) *Reader { return &Reader{r: r, threshold: -1, noComp: true} }

// SetDeadline bounds the next reads when the reader wraps a net.Conn;
// a no-op otherwise. Tests use it to drain a burst until quiet.
func (pr *Reader) SetDeadline(t time.Time) {
	if c, ok := pr.r.(net.Conn); ok {
		_ = c.SetDeadline(t)
	}
}

// SetThreshold enables decompression of zlib-framed packets.
// Negative n disables it.
func (pr *Reader) SetThreshold(n int32) {
	if n < 0 {
		pr.threshold, pr.noComp = -1, true
		return
	}
	pr.threshold, pr.noComp = n, false
}

// readFrame retains only interrupted deadline reads. Terminal I/O errors and
// malformed lengths keep their previous behavior and discard partial state.
func (pr *Reader) readFrame() ([]byte, error) {
	readError := func(err error) ([]byte, error) {
		var ne net.Error
		if !errors.As(err, &ne) || !ne.Timeout() {
			pr.lengthN, pr.frameN, pr.frame = 0, 0, nil
		}
		return nil, err
	}
	if pr.frame == nil {
		for {
			n, err := io.ReadFull(pr.r, pr.length[pr.lengthN:pr.lengthN+1])
			pr.lengthN += n
			if err != nil {
				return readError(err)
			}
			if pr.length[pr.lengthN-1]&0x80 != 0 && pr.lengthN < len(pr.length) {
				continue
			}
			length, _, err := ReadVarIntFromBytes(pr.length[:pr.lengthN])
			pr.lengthN = 0
			if err != nil {
				return nil, err
			}
			if length < 0 || length > MaxPacketLen {
				return nil, ErrPacketTooLarge
			}
			pr.frame = make([]byte, length)
			break
		}
	}
	n, err := io.ReadFull(pr.r, pr.frame[pr.frameN:])
	pr.frameN += n
	if err != nil {
		return readError(err)
	}
	raw := pr.frame
	pr.frame, pr.frameN = nil, 0
	return raw, nil
}

func (pr *Reader) ReadPacket() (Packet, error) {
	raw, err := pr.readFrame()
	if err != nil {
		return Packet{}, err
	}
	if pr.noComp {
		id, n, err := ReadVarIntFromBytes(raw)
		if err != nil {
			return Packet{}, err
		}
		return Packet{ID: id, Data: raw[n:]}, nil
	}
	dataLen, n, err := ReadVarIntFromBytes(raw)
	if err != nil {
		return Packet{}, err
	}
	body := raw[n:]
	if dataLen == 0 {
		id, m, err := ReadVarIntFromBytes(body)
		if err != nil {
			return Packet{}, err
		}
		return Packet{ID: id, Data: body[m:]}, nil
	}
	if dataLen < 0 || dataLen > MaxInflatedLen {
		return Packet{}, ErrPacketTooLarge
	}
	inflated, err := Inflate(body, dataLen)
	if err != nil {
		return Packet{}, err
	}
	id, m, err := ReadVarIntFromBytes(inflated)
	if err != nil {
		return Packet{}, err
	}
	return Packet{ID: id, Data: inflated[m:]}, nil
}

// PacketReader reads fields sequentially from packet data.
type PacketReader struct {
	buf *bytes.Reader
}

func NewPacketReader(data []byte) *PacketReader {
	return &PacketReader{buf: bytes.NewReader(data)}
}

func (r *PacketReader) VarInt() (int32, error) {
	// adapt bytes.Reader to io.Reader for ReadVarInt
	return ReadVarInt(r.buf)
}

func (r *PacketReader) VarLong() (int64, error) { return ReadVarLong(r.buf) }

func (r *PacketReader) String() (string, error)  { return ReadString(r.buf) }
func (r *PacketReader) UShort() (uint16, error)  { return ReadUShort(r.buf) }
func (r *PacketReader) Long() (int64, error)     { return ReadLong(r.buf) }
func (r *PacketReader) UUID() ([16]byte, error)  { return ReadUUID(r.buf) }
func (r *PacketReader) Byte() (byte, error)      { return ReadByte(r.buf) }
func (r *PacketReader) Bool() (bool, error)      { return ReadBool(r.buf) }
func (r *PacketReader) Int() (int32, error)      { return ReadInt(r.buf) }
func (r *PacketReader) Short() (int16, error)    { return ReadShort(r.buf) }
func (r *PacketReader) Float() (float32, error)  { return ReadFloat(r.buf) }
func (r *PacketReader) Double() (float64, error) { return ReadDouble(r.buf) }

// Position reads a packed BlockPos long.
func (r *PacketReader) Position() (x, y, z int32, err error) {
	return ReadPosition(r.buf)
}

// lpVec3Unpack decodes one 15-bit coordinate: min(v & 32767, 32766)
// mapped into [-1, 1], which the caller scales by the vector's scale.
func lpVec3Unpack(v int64) float64 {
	q := v & 32767
	if q > 32766 {
		q = 32766
	}
	return float64(q)*2/32766.0 - 1
}

// ReadLpVec3 decodes a LpVec3 (26.2's compact vector on the interact
// packet): first byte 0 is Vec3.ZERO; otherwise two more bytes and a
// u32 carry three packed 15-bit fields, scaled by the low two bits of
// the first byte plus an optional continuation VarInt (bit 2).
func (r *PacketReader) ReadLpVec3() (x, y, z float64, err error) {
	b0, err := r.Byte()
	if err != nil {
		return 0, 0, 0, err
	}
	if b0 == 0 {
		return 0, 0, 0, nil
	}
	b1, err := r.Byte()
	if err != nil {
		return 0, 0, 0, err
	}
	raw, err := r.Read(4)
	if err != nil {
		return 0, 0, 0, err
	}
	u := int64(raw[0])<<24 | int64(raw[1])<<16 | int64(raw[2])<<8 | int64(raw[3])
	v := u<<16 | int64(b1)<<8 | int64(b0)
	scale := float64(b0 & 3)
	if b0&4 != 0 {
		cont, err := r.VarInt()
		if err != nil {
			return 0, 0, 0, err
		}
		scale = float64(int64(b0&3) | int64(cont)<<2)
	}
	return lpVec3Unpack(v>>3) * scale, lpVec3Unpack(v>>18) * scale, lpVec3Unpack(v>>33) * scale, nil
}

// ByteArray reads a VarInt-prefixed byte array (the protocol's
// "Prefixed Array of Byte"), as used for chat public keys and plugin
// message payloads.
func (r *PacketReader) ByteArray() ([]byte, error) {
	n, err := r.VarInt()
	if err != nil {
		return nil, err
	}
	if n < 0 {
		return nil, ErrStringTooLong
	}
	return r.Read(int(n))
}

// OptionalVarInt reads a Prefixed Optional VarInt: a boolean presence
// flag, then the value when present.
func (r *PacketReader) OptionalVarInt() (int32, bool, error) {
	has, err := r.Bool()
	if err != nil {
		return 0, false, err
	}
	if !has {
		return 0, false, nil
	}
	v, err := r.VarInt()
	if err != nil {
		return 0, false, err
	}
	return v, true, nil
}

// NamedTag reads one named NBT tag — the COMPOUND_TAG stream form
// (type byte, u16 name, payload) — as written by writeNbt.
func (r *PacketReader) NamedTag() (string, any, error) {
	return nbt.ReadNamed(r.buf)
}

func (r *PacketReader) Read(n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r.buf, buf)
	return buf, err
}

func (r *PacketReader) Remaining() int { return r.buf.Len() }
