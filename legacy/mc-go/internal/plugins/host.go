package plugins

import "github.com/mc-go/server/internal/pipeline"

// Host wires plugin-provided interceptors and domain event handlers.
// This is a minimal placeholder that returns a static interceptor chain.
type Host struct{}

func (Host) Interceptors() []pipeline.Interceptor { return nil }
