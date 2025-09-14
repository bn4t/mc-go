package runtime

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	cfgpkg "github.com/mc-go/server/internal/config"
	"github.com/mc-go/server/internal/netx"
	"github.com/mc-go/server/internal/world"
	"go.uber.org/zap"
)

type App struct {
	Cfg      *cfgpkg.Config
	Logger   *zap.Logger
	Lis      net.Listener
	Acceptor *netx.Acceptor
}

// Boot initializes logging, loads config, starts the TCP acceptor, and
// blocks until a shutdown signal is received or context cancellation.
func Boot(ctx context.Context, configPath string) error {
	var _ *cfgpkg.Config
	cfg, err := LoadConfig(configPath)
	if err != nil {
		return err
	}
	log, err := NewLogger(cfg.Debug)
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}
	defer log.Sync() //nolint:errcheck

	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
	}
	log.Info("listening", zap.String("addr", lis.Addr().String()), zap.String("protocol", cfg.Protocol))

	// Start world loop
	wrld := world.New(log)
	wctx, wcancel := context.WithCancel(ctx)
	go wrld.Start(wctx)

	acceptor := netx.NewAcceptor(log, cfg, wrld)
	go acceptor.Serve(ctx, lis)

	// Wait for signal or context cancel
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	select {
	case <-ctx.Done():
		log.Info("context canceled, shutting down")
	case s := <-sigc:
		log.Info("signal received", zap.String("signal", s.String()))
	}
	_ = lis.Close()
	wcancel()
	acceptor.Shutdown()
	return nil
}
