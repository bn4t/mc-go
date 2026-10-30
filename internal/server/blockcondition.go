package server

import "mc-go/internal/world"

// Runtime position failures stop both if and unless. A chain ending on the
// condition reports the position error; a fork followed by run is silent.
// Parse the block predicate first so invalid IDs/properties still win.
func (s *Server) executeBlockReadReady(ctx *executeContext, pos world.Pos) bool {
	d := ctx.dim
	if d == nil {
		return true
	}
	key := ""
	if available, known := s.scheduledPositionAvailable(d, pos.X>>4, pos.Z>>4, false); known && !available {
		key = "argument.pos.unloaded"
	} else if pos.Y < d.minY || pos.Y >= d.minY+d.height || pos.X < -30000000 || pos.X >= 30000000 || pos.Z < -30000000 || pos.Z >= 30000000 {
		key = "argument.pos.outofworld"
	}
	// Explicit in-process command embeddings with no ready player or forced
	// admission retain their existing unrestricted chunk reads.
	if key == "" {
		return true
	}
	r := componentReply(commandError(key))
	ctx.condErr = &r
	return false
}
