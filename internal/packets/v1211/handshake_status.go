package v1211

import (
	"bufio"
	"bytes"
	"encoding/binary"

	"github.com/mc-go/server/internal/pipeline"
	"github.com/mc-go/server/internal/proto"
)

// Packet IDs in their respective states for 1.21.1
const (
	// Handshake (C->S)
	PIDCHandshake int32 = 0x00
	// Status state (C->S)
	PIDCStatusRequest int32 = 0x00
	PIDCPing          int32 = 0x01
	// Status state (S->C)
	PIDSStatusResponse int32 = 0x00
	PIDSPong           int32 = 0x01
)

type CHandshake struct {
	ProtocolVersion int32
	ServerAddress   string
	ServerPort      uint16
	NextState       int32 // 1=status, 2=login
}

func (*CHandshake) ID() int32 { return PIDCHandshake }

func (p *CHandshake) ReadFrom(br *bufio.Reader) error {
	var err error
	if p.ProtocolVersion, _, err = proto.ReadVarInt(br); err != nil {
		return err
	}
	if p.ServerAddress, err = proto.ReadString(br); err != nil {
		return err
	}
	// unsigned short
	var portB [2]byte
	if _, err = br.Read(portB[:]); err != nil {
		return err
	}
	p.ServerPort = binary.BigEndian.Uint16(portB[:])
	if p.NextState, _, err = proto.ReadVarInt(br); err != nil {
		return err
	}
	return nil
}

type CStatusRequest struct{}

func (*CStatusRequest) ID() int32                         { return PIDCStatusRequest }
func (p *CStatusRequest) ReadFrom(br *bufio.Reader) error { return nil }

type CPing struct{ Payload int64 }

func (*CPing) ID() int32 { return PIDCPing }
func (p *CPing) ReadFrom(br *bufio.Reader) error {
	// Ping uses signed 64-bit payload (not VarLong) in status
	var b [8]byte
	if _, err := br.Read(b[:]); err != nil {
		return err
	}
	p.Payload = int64(binary.BigEndian.Uint64(b[:]))
	return nil
}

type SStatusResponse struct{ JSON string }

func (*SStatusResponse) ID() int32 { return PIDSStatusResponse }
func (p *SStatusResponse) EncodeBody() []byte {
	// body = VarInt(len(json)) + json bytes
	buf := &bytes.Buffer{}
	bw := bufio.NewWriter(buf)
	_ = proto.WriteString(bw, p.JSON)
	_ = bw.Flush()
	return buf.Bytes()
}

type SPong struct{ Payload int64 }

func (*SPong) ID() int32 { return PIDSPong }
func (p *SPong) EncodeBody() []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(p.Payload))
	return b[:]
}

// Ensure conformance to pipeline.Packet for typed handling.
var _ pipeline.Packet = (*CHandshake)(nil)
var _ pipeline.Packet = (*CStatusRequest)(nil)
var _ pipeline.Packet = (*CPing)(nil)
var _ pipeline.Packet = (*SStatusResponse)(nil)
var _ pipeline.Packet = (*SPong)(nil)
