package anticheat

import (
	"github.com/mc-go/server/internal/pipeline"
)

// MovementIC is a placeholder interceptor for movement-related checks.
// Initially, it only enforces a conservative raw frame size and otherwise passes.
type MovementIC struct{ pipeline.NopInterceptor }

func (MovementIC) InboundRaw(ctx pipeline.PktCtx, f *pipeline.RawFrame) (pipeline.Action, *pipeline.RawFrame) {
	if f != nil && f.Len > 1<<20 { // 1MB cap to avoid abuse early
		return pipeline.Deny, nil
	}
	return pipeline.Pass, f
}
