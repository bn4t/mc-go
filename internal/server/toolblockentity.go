package server

import "mc-go/internal/world"

func compatibleBlockEntityTypes(old, next world.Block) bool {
	a, hasOld := blockEntityOf(old)
	b, hasNext := blockEntityOf(next)
	return hasOld && hasNext && a.name == b.name
}

// Tool state changes preserve a valid live block entity during callbacks.
// Command replacement retains its separate storage-retirement policy.
func (s *Server) setToolTransformBlock(d *dimension, pos world.Pos, next world.Block) {
	old := d.w.GetBlock(pos)
	preserve := compatibleBlockEntityTypes(old, next)
	s.setSilentBlockWithEntityPolicy(d, pos, next, false, true, preserve)
	s.wakeFluids(d, pos)
	s.neighborSurvival(d, pos, old)
}

func (s *Server) ensureCopperStatueEntity(d *dimension, pos world.Pos, state world.Block) {
	spec, ok := blockEntityOf(state)
	if !ok || spec.name != "minecraft:copper_golem_statue" {
		return
	}
	if _, _, ok := genericBlockEntityAt(d, pos); ok {
		return
	}
	s.restoreGenericBlockEntity(d, pos, spec.name, map[string]any{})
}
