package session

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/mc-go/server/internal/packets/v1211"
	"github.com/mc-go/server/internal/pipeline"
	"github.com/mc-go/server/internal/proto"
	"go.uber.org/zap"
)

// Session handles a single connection's lifecycle. For now, only Status is supported.
type Session struct {
	conn  net.Conn
	br    *bufio.Reader
	bw    *bufio.Writer
	chain *pipeline.Chain
	state pipeline.SessionState
	proto pipeline.ProtoVer
	log   *zap.Logger
}

func New(c net.Conn, br *bufio.Reader, bw *bufio.Writer, chain *pipeline.Chain, log *zap.Logger) *Session {
	return &Session{conn: c, br: br, bw: bw, chain: chain, state: pipeline.StateHandshake, proto: 0, log: log}
}

func (s *Session) RunStatus(ctx context.Context) error {
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
	// For now, we accept any ProtocolVersion and pin to 1.21.1 semantics
	if hs.NextState != 1 { // 1 = status
		return fmt.Errorf("only status supported in this build")
	}
	s.state = pipeline.StateStatus

	// Loop for Status Request then Ping
	for {
		rf, ok, err = s.readRaw(ctx)
		if err != nil {
			return err
		}
		if !ok {
			return nil
		}
		pid, body := s.peekPacketID(rf.Body)

		switch pid {
		case v1211.PIDCStatusRequest:
			// decode (no fields)
			var req v1211.CStatusRequest
			if err := req.ReadFrom(body); err != nil {
				return err
			}
			// Build JSON per status response spec
			// Keep MOTD simple and player counts unknown (-1)
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
			return nil // status flow typically ends after ping/pong
		default:
			return fmt.Errorf("unexpected pid in status: 0x%x", pid)
		}
	}
}

func (s *Session) readRaw(ctx context.Context) (*pipeline.RawFrame, bool, error) {
	// Deadline safety for sluggish clients
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
	// Outbound S3 hook is applied at the packet level in a complete server.
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
