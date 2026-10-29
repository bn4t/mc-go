package server

import "mc-go/internal/world"

// FluidState uses distinct registered owners for a source and its flowing
// states. ServerLevel.tickFluid compares that exact owner before dispatch.
func (s *Server) scheduledFluidType(k schedKey) string {
	d := s.dims[k.dim]
	if d == nil {
		return ""
	}
	return scheduledFluidStateType(d.w.GetBlock(k.pos))
}

func scheduledFluidStateType(state world.Block) string {
	kind, level, ok := fluidOf(state)
	if !ok {
		return "empty"
	}
	if level == 0 {
		return kind.name()
	}
	return "flowing_" + kind.name()
}
