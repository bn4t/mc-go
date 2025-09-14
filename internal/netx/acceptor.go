package netx

import (
	"bufio"
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mc-go/server/internal/anticheat"
	cfgpkg "github.com/mc-go/server/internal/config"
	"github.com/mc-go/server/internal/pipeline"
	"github.com/mc-go/server/internal/session"
	"github.com/mc-go/server/internal/world"
	"go.uber.org/zap"
)

type Acceptor struct {
	log     *zap.Logger
	readTO  time.Duration
	writeTO time.Duration
	wg      sync.WaitGroup
	closed  atomic.Bool
	world   *world.World
	nextID  atomic.Uint64
	cfg     *cfgpkg.Config
}

func NewAcceptor(log *zap.Logger, cfg *cfgpkg.Config, w *world.World) *Acceptor {
	return &Acceptor{log: log, readTO: cfg.ReadTimeout, writeTO: cfg.WriteTimeout, world: w, cfg: cfg}
}

func (a *Acceptor) Serve(ctx context.Context, lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			if a.closed.Load() {
				return
			}
			a.log.Warn("accept error", zap.Error(err))
			continue
		}
		a.wg.Add(1)
		go func(c net.Conn) {
			defer a.wg.Done()
			_ = c.SetDeadline(time.Time{})
			_ = c.SetReadDeadline(time.Now().Add(a.readTO))
			_ = c.SetWriteDeadline(time.Now().Add(a.writeTO))
			a.handleConn(ctx, c)
		}(conn)
	}
}

func (a *Acceptor) Shutdown() {
	a.closed.Store(true)
	a.wg.Wait()
}

// Note: configuration and world are provided via constructor to avoid WithX patterns.

func (a *Acceptor) handleConn(ctx context.Context, c net.Conn) {
	br := bufio.NewReaderSize(c, 1<<15)
	bw := bufio.NewWriterSize(c, 1<<15)

	chain := pipeline.NewChain([]pipeline.Interceptor{anticheat.MovementIC{}})
	id := pipeline.ConnID(a.nextID.Add(1))
	sess := session.New(id, c, br, bw, chain, a.world, a.log)
	sess.SetConfig(a.cfg.KeepAliveInterval, a.cfg.MOTD, a.cfg.MaxPlayers)
	if err := sess.Run(ctx); err != nil {
		a.log.Debug("session end", zap.String("remote", c.RemoteAddr().String()), zap.Error(err))
	}
	_ = c.Close()
}
