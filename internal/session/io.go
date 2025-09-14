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

	"github.com/mc-go/server/internal/pipeline"
	"github.com/mc-go/server/internal/proto"
	"go.uber.org/zap"
)

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
	for f := range s.out {
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

// writePacketNow writes directly to the connection for the Status flow, to avoid
// racing the async writer with connection close.
func (s *Session) writePacketNow(pid int32, payload []byte) error {
	frame := proto.WrapFrame(pid, payload)
	_ = s.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	if _, err := s.bw.Write(frame); err != nil {
		return err
	}
	return s.bw.Flush()
}

// readRaw reads one VarInt-length frame, applies S0 interceptors, and returns a RawFrame.
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

// peekPacketID returns the packet id and a reader over the packet body (after the id).
func (s *Session) peekPacketID(body []byte) (int32, *bufio.Reader) {
	br := bufio.NewReader(bytes.NewReader(body))
	pid, _, _ := proto.ReadVarInt(br)
	return pid, br
}

// readRemaining drains the buffered bytes from a bufio.Reader into a new slice.
func readRemaining(r *bufio.Reader) []byte {
	b := make([]byte, r.Buffered())
	_, _ = r.Read(b)
	return b
}

func isEOF(err error) bool {
	return errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF)
}
