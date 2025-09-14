package world

import (
	"context"
	"time"

	"github.com/mc-go/server/internal/pipeline"
	"go.uber.org/zap"
)

type Inbound struct {
	Conn  pipeline.ConnID
	Cmd   pipeline.Command
	Ctx   pipeline.PktCtx
	Chain *pipeline.Chain
}

type World struct {
	log    *zap.Logger
	inbox  chan Inbound
	online map[pipeline.ConnID]struct{}
}

func New(log *zap.Logger) *World {
	return &World{
		log:    log,
		inbox:  make(chan Inbound, 2048),
		online: make(map[pipeline.ConnID]struct{}),
	}
}

func (w *World) Start(ctx context.Context) {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	w.log.Info("world loop started", zap.Int("tps", 20))
	for {
		select {
		case <-ctx.Done():
			w.log.Info("world loop stopping")
			return
		case <-t.C:
			// Drain inbox
		drain:
			for {
				select {
				case in := <-w.inbox:
					w.handle(in)
				default:
					break drain
				}
			}
		}
	}
}

func (w *World) Submit(in Inbound) {
	select {
	case w.inbox <- in:
	default:
		w.log.Debug("world inbox overflow; dropping command", zap.Any("cmd", in.Cmd))
	}
}

func (w *World) handle(in Inbound) {
	// S2 PreApply hook point (command-only in this minimal slice)
	if in.Chain != nil {
		_, cmd, ok := in.Chain.RunPreApply(in.Ctx, nil, &in.Cmd)
		if !ok || cmd == nil {
			return
		}
		in.Cmd = *cmd
	}

	switch in.Cmd.Kind {
	case "player/join":
		w.online[in.Conn] = struct{}{}
	case "player/leave":
		delete(w.online, in.Conn)
	}
}

func (w *World) Online() int { return len(w.online) }
