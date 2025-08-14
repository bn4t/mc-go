package protocol

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
)

type Packet struct {
	ID   byte
	Data *bytes.Buffer
}

func NewPacket(id byte) *Packet {
	return &Packet{
		ID:   id,
		Data: bytes.NewBuffer(nil),
	}
}

func (p *Packet) WriteByte(v byte) error {
	return p.Data.WriteByte(v)
}

func (p *Packet) WriteBytes(v []byte) {
	p.Data.Write(v)
}

func (p *Packet) WriteBoolean(v bool) {
	if v {
		_ = p.WriteByte(1)
	} else {
		_ = p.WriteByte(0)
	}
}

func (p *Packet) WriteShort(v int16) {
	binary.Write(p.Data, binary.BigEndian, v)
}

func (p *Packet) WriteUShort(v uint16) {
	binary.Write(p.Data, binary.BigEndian, v)
}

func (p *Packet) WriteInt(v int32) {
	binary.Write(p.Data, binary.BigEndian, v)
}

func (p *Packet) WriteLong(v int64) {
	binary.Write(p.Data, binary.BigEndian, v)
}

func (p *Packet) WriteFloat(v float32) {
	binary.Write(p.Data, binary.BigEndian, v)
}

func (p *Packet) WriteDouble(v float64) {
	binary.Write(p.Data, binary.BigEndian, v)
}

func (p *Packet) WriteString(v string) {
	p.WriteVarInt(int32(len(v)))
	p.Data.WriteString(v)
}

func (p *Packet) WriteVarInt(v int32) {
	for {
		if (v & ^int32(0x7F)) == 0 {
			_ = p.WriteByte(byte(v))
			return
		}
		_ = p.WriteByte(byte((v & 0x7F) | 0x80))
		v >>= 7
	}
}

func (p *Packet) WriteVarLong(v int64) {
	for {
		if (v & ^int64(0x7F)) == 0 {
			_ = p.WriteByte(byte(v))
			return
		}
		_ = p.WriteByte(byte((v & 0x7F) | 0x80))
		v >>= 7
	}
}

func (p *Packet) ReadByte() (byte, error) {
	return p.Data.ReadByte()
}

func (p *Packet) ReadBytes(n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(p.Data, buf)
	return buf, err
}

func (p *Packet) ReadBoolean() (bool, error) {
	b, err := p.ReadByte()
	return b != 0, err
}

func (p *Packet) ReadShort() (int16, error) {
	var v int16
	err := binary.Read(p.Data, binary.BigEndian, &v)
	return v, err
}

func (p *Packet) ReadUShort() (uint16, error) {
	var v uint16
	err := binary.Read(p.Data, binary.BigEndian, &v)
	return v, err
}

func (p *Packet) ReadInt() (int32, error) {
	var v int32
	err := binary.Read(p.Data, binary.BigEndian, &v)
	return v, err
}

func (p *Packet) ReadLong() (int64, error) {
	var v int64
	err := binary.Read(p.Data, binary.BigEndian, &v)
	return v, err
}

func (p *Packet) ReadFloat() (float32, error) {
	var v float32
	err := binary.Read(p.Data, binary.BigEndian, &v)
	return v, err
}

func (p *Packet) ReadDouble() (float64, error) {
	var v float64
	err := binary.Read(p.Data, binary.BigEndian, &v)
	return v, err
}

func (p *Packet) ReadString() (string, error) {
	length, err := p.ReadVarInt()
	if err != nil {
		return "", err
	}
	
	if length < 0 || length > 32767 {
		return "", fmt.Errorf("invalid string length: %d", length)
	}
	
	buf := make([]byte, length)
	_, err = io.ReadFull(p.Data, buf)
	return string(buf), err
}

func (p *Packet) ReadVarInt() (int32, error) {
	var v int32
	var shift uint
	
	for {
		b, err := p.ReadByte()
		if err != nil {
			return 0, err
		}
		
		v |= (int32(b&0x7F) << shift)
		
		if (b & 0x80) == 0 {
			return v, nil
		}
		
		shift += 7
		if shift >= 32 {
			return 0, fmt.Errorf("VarInt too big")
		}
	}
}

func (p *Packet) ReadVarLong() (int64, error) {
	var v int64
	var shift uint
	
	for {
		b, err := p.ReadByte()
		if err != nil {
			return 0, err
		}
		
		v |= (int64(b&0x7F) << shift)
		
		if (b & 0x80) == 0 {
			return v, nil
		}
		
		shift += 7
		if shift >= 64 {
			return 0, fmt.Errorf("VarLong too big")
		}
	}
}

type PacketReader struct {
	reader io.Reader
	compressed bool
}

func NewPacketReader(r io.Reader) *PacketReader {
	return &PacketReader{
		reader: r,
	}
}

func (pr *PacketReader) SetCompression(enabled bool) {
	pr.compressed = enabled
}

func (pr *PacketReader) ReadPacket() (*Packet, error) {
	lengthBuf := make([]byte, 5)
	n := 0
	
	for {
		b, err := pr.readByte()
		if err != nil {
			return nil, err
		}
		
		lengthBuf[n] = b
		n++
		
		if (b & 0x80) == 0 {
			break
		}
		
		if n >= 5 {
			return nil, fmt.Errorf("packet length VarInt too big")
		}
	}
	
	length := int32(0)
	shift := uint(0)
	for i := 0; i < n; i++ {
		length |= (int32(lengthBuf[i]&0x7F) << shift)
		shift += 7
	}
	
	data := make([]byte, length)
	_, err := io.ReadFull(pr.reader, data)
	if err != nil {
		return nil, err
	}
	
	buf := bytes.NewBuffer(data)
	
	if pr.compressed {
		dataLength := int32(0)
		shift := uint(0)
		
		for {
			b, err := buf.ReadByte()
			if err != nil {
				return nil, err
			}
			
			dataLength |= (int32(b&0x7F) << shift)
			
			if (b & 0x80) == 0 {
				break
			}
			
			shift += 7
		}
		
		if dataLength > 0 {
			zlibReader, err := zlib.NewReader(buf)
			if err != nil {
				return nil, err
			}
			defer zlibReader.Close()
			
			uncompressed := bytes.NewBuffer(nil)
			_, err = io.Copy(uncompressed, zlibReader)
			if err != nil {
				return nil, err
			}
			
			buf = uncompressed
		}
	}
	
	packet := &Packet{Data: buf}
	
	id, err := packet.ReadVarInt()
	if err != nil {
		return nil, err
	}
	packet.ID = byte(id)
	
	return packet, nil
}

func (pr *PacketReader) readByte() (byte, error) {
	buf := make([]byte, 1)
	_, err := io.ReadFull(pr.reader, buf)
	return buf[0], err
}

type PacketWriter struct {
	writer io.Writer
	compressed bool
	threshold int
}

func NewPacketWriter(w io.Writer) *PacketWriter {
	return &PacketWriter{
		writer: w,
		threshold: 256,
	}
}

func (pw *PacketWriter) SetCompression(enabled bool, threshold int) {
	pw.compressed = enabled
	pw.threshold = threshold
}

func (pw *PacketWriter) WritePacket(packet *Packet) error {
	var buf bytes.Buffer
	
	writeVarInt(&buf, int32(packet.ID))
	buf.Write(packet.Data.Bytes())
	
	data := buf.Bytes()
	
	if pw.compressed {
		return pw.writeCompressedPacket(data)
	}
	
	return pw.writeUncompressedPacket(data)
}

func (pw *PacketWriter) writeUncompressedPacket(data []byte) error {
	var buf bytes.Buffer
	
	writeVarInt(&buf, int32(len(data)))
	buf.Write(data)
	
	_, err := pw.writer.Write(buf.Bytes())
	return err
}

func (pw *PacketWriter) writeCompressedPacket(data []byte) error {
	var buf bytes.Buffer
	
	if len(data) < pw.threshold {
		writeVarInt(&buf, int32(len(data)+1))
		buf.WriteByte(0)
		buf.Write(data)
	} else {
		var compressed bytes.Buffer
		zlibWriter := zlib.NewWriter(&compressed)
		zlibWriter.Write(data)
		zlibWriter.Close()
		
		compressedData := compressed.Bytes()
		
		var packet bytes.Buffer
		writeVarInt(&packet, int32(len(data)))
		packet.Write(compressedData)
		
		writeVarInt(&buf, int32(packet.Len()))
		buf.Write(packet.Bytes())
	}
	
	_, err := pw.writer.Write(buf.Bytes())
	return err
}

func writeVarInt(w io.Writer, v int32) {
	for {
		if (v & ^int32(0x7F)) == 0 {
			w.Write([]byte{byte(v)})
			return
		}
		w.Write([]byte{byte((v & 0x7F) | 0x80)})
		v >>= 7
	}
}