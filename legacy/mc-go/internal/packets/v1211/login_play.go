package v1211

import (
	"bufio"
	"bytes"
	"encoding/binary"

	"github.com/mc-go/server/internal/pipeline"
	"github.com/mc-go/server/internal/proto"
)

// Protocol version: 767 (Minecraft 1.21.1)
// NOTE: Only minimal packets needed for Login→Play and KeepAlive are modeled here.

// Login state (serverbound)
const (
	PIDCLoginStart int32 = 0x00 // Login Start (serverbound)
)

// Configuration state (serverbound)
const (
	PIDCConfigAckFinish int32 = 0x03 // Acknowledge Finish Configuration (serverbound)
	PIDCConfigKeepAlive int32 = 0x04 // KeepAlive (serverbound)
)

// Play state (serverbound)
const (
	PIDCPlayKeepAlive int32 = 0x18 // Serverbound KeepAlive (play)
)

// Play state (clientbound)
const (
	PIDSPlayKeepAlive int32 = 0x21 // Clientbound KeepAlive (play)
)

// CLoginStart is a placeholder for the serverbound Login Start packet (0x00).
// Newer protocol versions include more fields; we only read the username up to
// the first string for this skeleton.
type CLoginStart struct {
	Name string
}

func (*CLoginStart) ID() int32 { return PIDCLoginStart }

func (p *CLoginStart) ReadFrom(br *bufio.Reader) error {
	// name string (additional optional fields are ignored in this skeleton)
	nm, err := proto.ReadString(br)
	if err != nil {
		return err
	}
	p.Name = nm
	return nil
}

// SPlayKeepAlive is the minimal clientbound keep-alive packet in Play.
type SPlayKeepAlive struct{ Value int64 }

func (*SPlayKeepAlive) ID() int32 { return PIDSPlayKeepAlive }
func (p *SPlayKeepAlive) EncodeBody() []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(p.Value))
	return b[:]
}

// DecodeServerboundKeepAlive decodes a serverbound KeepAlive packet body (play/config).
func DecodeServerboundKeepAlive(body []byte) (int64, error) {
	br := bufio.NewReader(bytes.NewReader(body))
	var b [8]byte
	if _, err := br.Read(b[:]); err != nil {
		return 0, err
	}
	return int64(binary.BigEndian.Uint64(b[:])), nil
}

var _ pipeline.Packet = (*CLoginStart)(nil)
var _ pipeline.Packet = (*SPlayKeepAlive)(nil)
