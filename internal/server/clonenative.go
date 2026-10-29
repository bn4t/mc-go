package server

import "mc-go/internal/world"

// Clone snapshots native block entity data while sampling its source, before
// any destination write or move removal can alter an overlapping cell.
func (s *Server) cloneNativeTag(d *dimension, pos world.Pos) map[string]any {
	tag, ok := s.blockEntitySaveMap(d, pos)
	if !ok {
		return nil
	}
	return cloneNBTValue(tag).(map[string]any)
}

func (s *Server) clonePlaceNativeCell(d *dimension, pos world.Pos, state world.Block, tag map[string]any, strict bool) {
	if tag != nil {
		// The temporary state retires an existing container without spilling its
		// inventory and ensures same-state copies replace its native data too.
		barrier, _ := world.BlockState("barrier")
		s.setSilentBlockWithPolicy(d, pos, barrier, true, false)
	} else if !strict {
		s.structureRetireBlockEntity(d, pos, d.w.GetBlock(pos), state)
	}
	if strict {
		// Clone strict uses the same818 policy as strict template writes:
		// known shape, suppressed removal drops and skipped callbacks.
		s.setSilentBlockWithPolicy(d, pos, state, true, true)
	} else {
		s.setNaturalBlockNotify(d, pos, state, state)
	}
	if tag != nil {
		tag["x"], tag["y"], tag["z"] = int32(pos.X), int32(pos.Y), int32(pos.Z)
		delete(tag, "keepPacked")
		s.restoreBlockEntity(d, tag)
		s.structureLoadName(d, pos, state, tag)
	}
}
