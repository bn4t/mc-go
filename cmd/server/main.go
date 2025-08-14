package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/mc-go/server/pkg/config"
	"github.com/mc-go/server/pkg/server"
	"go.uber.org/zap"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "server.yaml", "Path to server configuration file")
	flag.Parse()
	
	logger, err := zap.NewProduction()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logger.Sync()
	
	logger.Info("Starting Minecraft Server", zap.String("version", "1.8.9"))
	
	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Fatal("Failed to load configuration", zap.Error(err))
	}
	
	if err := cfg.Validate(); err != nil {
		logger.Fatal("Invalid configuration", zap.Error(err))
	}
	
	srv, err := server.New(cfg)
	if err != nil {
		logger.Fatal("Failed to create server", zap.Error(err))
	}
	
	if err := srv.Start(); err != nil {
		logger.Fatal("Failed to start server", zap.Error(err))
	}
	
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	
	<-sigChan
	
	logger.Info("Received shutdown signal")
	
	if err := srv.Stop(); err != nil {
		logger.Error("Failed to stop server gracefully", zap.Error(err))
	}
	
	logger.Info("Server stopped")
}