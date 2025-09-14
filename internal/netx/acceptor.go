package netx

import (
	"bufio"
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mc-go/server/internal/anticheat"
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
	cfgKA   time.Duration
	motd    string
	maxP    int
}

func NewAcceptor(log *zap.Logger, readTO, writeTO time.Duration, w *world.World, keepAlive time.Duration, motd string, maxPlayers int) *Acceptor {
	if keepAlive <= 0 {
		keepAlive = 15 * time.Second
	}
	return &Acceptor{log: log, readTO: readTO, writeTO: writeTO, world: w, cfgKA: keepAlive, motd: motd, maxP: maxPlayers}
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
	sess.SetConfig(a.cfgKA, a.motd, a.maxP)
	if err := sess.Run(ctx); err != nil {
		a.log.Debug("session end", zap.String("remote", c.RemoteAddr().String()), zap.Error(err))
	}
	_ = c.Close()
}
