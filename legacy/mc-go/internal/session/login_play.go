package session

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/mc-go/server/internal/packets/v1211"
	"github.com/mc-go/server/internal/pipeline"
	"github.com/mc-go/server/internal/world"
	"go.uber.org/zap"
)

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
	if s.world != nil {
		s.world.Submit(world.Inbound{Conn: s.id, Cmd: pipeline.Command{Kind: "player/join"}, Ctx: s.ctx(pipeline.StagePreApply), Chain: s.chain})
		defer s.world.Submit(world.Inbound{Conn: s.id, Cmd: pipeline.Command{Kind: "player/leave"}, Ctx: s.ctx(pipeline.StagePreApply), Chain: s.chain})
	}

	defer func() { s.closed.Store(true); close(s.out) }()
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
	t := time.NewTicker(s.kaInt)
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
