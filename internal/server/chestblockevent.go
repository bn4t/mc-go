package server

import "mc-go/internal/world"

// ServerLevel queues an insertion-ordered set of BlockEventData. Chest
// events identify the block type rather than its facing/pairing state.
// A frozen world retains these until the next block-event phase.
func (s *Server) queueChestBlockEvent(d *dimension, pos world.Pos, block world.Block, count int) {
	owner := bstate(block)
	for _, event := range s.blockEvents {
		if event.dim == d.name && event.pos == pos && event.b0 == 1 && event.b1 == int32(count) && bstate(event.blk) == owner {
			return
		}
	}
	s.blockEvents = append(s.blockEvents, rsBlockEvent{dim: d.name, pos: pos, blk: block, b0: 1, b1: int32(count)})
}
