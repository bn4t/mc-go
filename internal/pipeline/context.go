package pipeline

import (
	"net"
)

type ConnID uint64
type ProtoVer int

type SessionState uint8

const (
	StateHandshake SessionState = iota
	StateStatus
	StateLogin
	StatePlay
)

type Stage uint8

const (
	StageInboundRaw     Stage = iota // S0
	StageInboundDecoded              // S1
	StagePreApply                    // S2
	StageOutbound                    // S3
)

type Action uint8

const (
	Pass       Action = iota
	Drop              // silently drop
	Deny              // drop + send denial message
	Replace           // swap packet/body
	Quarantine        // mirror to recorder; may rate-limit
	Defer             // push to async worker, continue
)

type PktCtx struct {
	Conn         ConnID
	TimeNS       int64
	Stage        Stage
	State        SessionState
	Proto        ProtoVer
	ApproxPos    Vec3
	ApproxYaw    float32
	PingMillis   int
	IP           net.IP
	IsCompressed bool
	IsEncrypted  bool
}

type Vec3 struct{ X, Y, Z float64 }

// RawFrame is the post-framer, post-decompression body.
type RawFrame struct {
	Len  int
	Body []byte
}
