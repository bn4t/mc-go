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
	"go.uber.org/zap"
)

// Session handles a single connection's lifecycle.
type Session struct {
	conn   net.Conn
	br     *bufio.Reader
	bw     *bufio.Writer
	chain  *pipeline.Chain
	state  pipeline.SessionState
	proto  pipeline.ProtoVer
	log    *zap.Logger
	lastKA int64
	closed atomic.Bool
}

func New(c net.Conn, br *bufio.Reader, bw *bufio.Writer, chain *pipeline.Chain, log *zap.Logger) *Session {
	return &Session{conn: c, br: br, bw: bw, chain: chain, state: pipeline.StateHandshake, proto: 0, log: log}
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
		s.state = pipeline.StateLogin
		return s.runLoginPlay(ctx)
	default:
		return fmt.Errorf("unsupported next state: %d", hs.NextState)
	}
}

// runStatus handles the Status query+ping cycle.
func (s *Session) runStatus(ctx context.Context) error {
	for {
		rf, ok, err := s.readRaw(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		pid, body := s.peekPacketID(rf.Body)

		switch pid {
		case v1211.PIDCStatusRequest:
			var req v1211.CStatusRequest
			if err := req.ReadFrom(body); err != nil {
				return err
			}
			json := `{"version":{"name":"1.21.1","protocol":767},"players":{"max":20,"online":0},"description":{"text":"mc-go"}}`
			resp := v1211.SStatusResponse{JSON: json}
			if err := s.writePacket(resp.ID(), resp.EncodeBody()); err != nil {
				return err
			}
		case v1211.PIDCPing:
			var ping v1211.CPing
			if err := ping.ReadFrom(body); err != nil {
				return err
			}
			pong := v1211.SPong{Payload: ping.Payload}
			if err := s.writePacket(pong.ID(), pong.EncodeBody()); err != nil {
				return err
			}
			return nil
		default:
			return fmt.Errorf("unexpected pid in status: 0x%x", pid)
		}
	}
}

// runLoginPlay implements a minimal offline Login→Play flow and KeepAlive loop.
func (s *Session) runLoginPlay(ctx context.Context) error {
	// Expect Login Start (serverbound 0x00) — minimal read
	rf, ok, err := s.readRaw(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	pid, bodyR := s.peekPacketID(rf.Body)
	if pid != v1211.PIDCLoginStart {
		return fmt.Errorf("expected login start, got pid=0x%x", pid)
	}
	var ls v1211.CLoginStart
	_ = ls.ReadFrom(bodyR)

	// Transition directly to Play (Configuration state omitted in this cut)
	s.state = pipeline.StatePlay
	s.log.Info("login accepted", zap.String("user", ls.Name))

	// Start clientbound keepalive loop
	go s.keepAliveLoop()

	// Read loop: only handle serverbound keepalive replies for now
	for {
		rf, ok, err = s.readRaw(ctx)
		if err != nil {
			return err
		}
		if !ok || s.closed.Load() {
			return nil
		}
		pid, bodyR := s.peekPacketID(rf.Body)
		if pid == v1211.PIDCPlayKeepAlive {
			id, err := v1211.DecodeServerboundKeepAlive(readRemaining(bodyR))
			if err == nil {
				atomic.StoreInt64(&s.lastKA, id)
			}
			continue
		}
		// Ignore others for now
	}
}

func (s *Session) keepAliveLoop() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for !s.closed.Load() {
		select {
		case <-t.C:
			ka := v1211.SPlayKeepAlive{Value: time.Now().UnixNano()}
			if err := s.writePacket(ka.ID(), ka.EncodeBody()); err != nil {
				s.log.Debug("keepalive send failed", zap.Error(err))
				s.closed.Store(true)
				return
			}
		default:
			time.Sleep(10 * time.Millisecond)
		}
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
	if _, err := s.bw.Write(frame); err != nil {
		return err
	}
	return s.bw.Flush()
}

func (s *Session) ctx(stage pipeline.Stage) pipeline.PktCtx {
	return pipeline.PktCtx{Stage: stage}
}

func isEOF(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF)
}

func readRemaining(r *bufio.Reader) []byte {
	b := make([]byte, r.Buffered())
	_, _ = r.Read(b)
	return b
}
