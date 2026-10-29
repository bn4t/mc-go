package server

import "mc-go/internal/world"

// Scheduled block callbacks belong to the registered block type that queued
// them, not a later replacement at the same position (ServerLevel.tickBlock).
func (s *Server) bindScheduledBlockTypes() {
	s.ticks.blockType = s.scheduledBlockType
}
func (s *Server) scheduledBlockType(k schedKey) string {
	d := s.dims[k.dim]
	if d == nil {
		return ""
	}
	name, _ := world.StateOwner(d.w.GetBlock(k.pos))
	return name
}
