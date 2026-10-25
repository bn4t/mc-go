package main

import (
	"context"
	"flag"

	"github.com/mc-go/server/internal/runtime"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "server.yaml", "Path to server configuration file")
	flag.Parse()

	_ = runtime.Boot(context.Background(), configPath)
}
