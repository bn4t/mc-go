package session

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"

	"github.com/mc-go/server/internal/packets/v1211"
	"github.com/mc-go/server/internal/pipeline"
	"github.com/mc-go/server/internal/proto"
	"github.com/mc-go/server/internal/world"
	"go.uber.org/zap"
)

// Session handles a single connection's lifecycle.
type Session struct {
	id       pipeline.ConnID
	conn     net.Conn
	br       *bufio.Reader
	bw       *bufio.Writer
	out      chan []byte
	chain    *pipeline.Chain
	state    pipeline.SessionState
	proto    pipeline.ProtoVer
	log      *zap.Logger
	world    *world.World
	kaInt    time.Duration
	motd     string
	maxP     int
	lastKA   int64
	lastKAAt int64
	closed   atomic.Bool
}

func New(id pipeline.ConnID, c net.Conn, br *bufio.Reader, bw *bufio.Writer, chain *pipeline.Chain, w *world.World, log *zap.Logger) *Session {
	return &Session{id: id, conn: c, br: br, bw: bw, out: make(chan []byte, 256), chain: chain, state: pipeline.StateHandshake, proto: 0, log: log, world: w}
}

func (s *Session) SetConfig(keepAlive time.Duration, motd string, maxPlayers int) {
	s.kaInt = keepAlive
	s.motd = motd
	s.maxP = maxPlayers
}

// Run reads the handshake and dispatches to Status or Login→Play.
func (s *Session) Run(ctx context.Context) error {
	// Handshake
	rf, ok, err := s.readRaw(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	pid, bodyR := s.peekPacketID(rf.Body)
	if pid != v1211.PIDCHandshake {
		return fmt.Errorf("expected handshake, got pid=0x%x", pid)
	}
	var hs v1211.CHandshake
	if err := hs.ReadFrom(bodyR); err != nil {
		return fmt.Errorf("decode handshake: %w", err)
	}

	switch hs.NextState {
	case 1:
		s.state = pipeline.StateStatus
		return s.runStatus(ctx)
	case 2:
		// start writer -> this is a long running connection
		go s.writer()
		s.state = pipeline.StateLogin
		return s.runLoginPlay(ctx)
	default:
		return fmt.Errorf("unsupported next state: %d", hs.NextState)
	}
}

func (s *Session) readRaw(ctx context.Context) (*pipeline.RawFrame, bool, error) {
	_ = s.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	body, _, err := proto.ReadFrame(s.br)
	if err != nil {
		if errors.Is(err, net.ErrClosed) || isEOF(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	rf := &pipeline.RawFrame{Len: len(body), Body: body}
	ctx0 := s.ctx(pipeline.StageInboundRaw)
	rf, ok := s.chain.RunInboundRaw(ctx0, rf)
	if !ok {
		return nil, false, nil
	}
	return rf, true, nil
}

func (s *Session) peekPacketID(body []byte) (int32, *bufio.Reader) {
	br := bufio.NewReader(bytes.NewReader(body))
	pid, _, _ := proto.ReadVarInt(br)
	return pid, br
}

func (s *Session) writePacket(pid int32, payload []byte) error {
	frame := proto.WrapFrame(pid, payload)
	select {
	case s.out <- frame:
		return nil
	default:
		return fmt.Errorf("outbox full")
	}
}

func (s *Session) writer() {
	s.log.Info("writer started")
	for f := range s.out {
		s.log.Info("writer write", zap.Int("len", len(f)))
		_ = s.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
		if _, err := s.bw.Write(f); err != nil {
			s.log.Debug("write error", zap.Error(err))
			return
		}
		if err := s.bw.Flush(); err != nil {
			s.log.Debug("flush error", zap.Error(err))
			return
		}
	}
}

// writePacketNow writes directly to the connection for Status flow.
func (s *Session) writePacketNow(pid int32, payload []byte) error {
	frame := proto.WrapFrame(pid, payload)
	_ = s.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := s.bw.Write(frame); err != nil {
		return err
	}
	return s.bw.Flush()
}

func (s *Session) ctx(stage pipeline.Stage) pipeline.PktCtx {
	var ip net.IP
	if ra, ok := s.conn.RemoteAddr().(*net.TCPAddr); ok && ra != nil {
		ip = ra.IP
	}
	return pipeline.PktCtx{Conn: s.id, Stage: stage, State: s.state, Proto: s.proto, IP: ip, TimeNS: time.Now().UnixNano()}
}

func isEOF(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF)
}

func readRemaining(r *bufio.Reader) []byte {
	b := make([]byte, r.Buffered())
	_, _ = r.Read(b)
	return b
}
