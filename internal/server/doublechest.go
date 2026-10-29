package server

import "mc-go/internal/world"

// A combined chest is a view over two independently saved block entities.
// RIGHT contributes the first27 slots and LEFT the second27, regardless
// of which block was clicked (26.2 ChestBlock/DoubleBlockCombiner wire).
func (s *Server) chestView(d *dimension, pos world.Pos) (world.Pos, world.Pos, bool, bool) {
	if s.chestBlocked(d, pos) {
		return pos, world.Pos{}, false, true
	}
	block := d.w.GetBlock(pos)
	name, _ := world.StateOwner(block)
	typ, _ := world.StateProperty(block, "type")
	facing, _ := world.StateProperty(block, "facing")
	if typ != "left" && typ != "right" {
		return pos, world.Pos{}, false, false
	}
	dir, ok := rsDirOfName[chestConnDir(typ, facing)]
	if !ok {
		return pos, world.Pos{}, false, false
	}
	step := rsOff[dir]
	other := world.Pos{X: pos.X + step.X, Y: pos.Y, Z: pos.Z + step.Z}
	otherBlock := d.w.GetBlock(other)
	otherName, _ := world.StateOwner(otherBlock)
	otherType, _ := world.StateProperty(otherBlock, "type")
	otherFacing, _ := world.StateProperty(otherBlock, "facing")
	if name != otherName || otherFacing != facing || otherType != dirOpposite2(typ) {
		return pos, world.Pos{}, false, false
	}
	if s.chestBlocked(d, other) {
		return pos, other, false, true
	}
	if typ == "right" {
		return pos, other, true, false
	}
	return other, pos, true, false
}

func (s *Server) chestBlocked(d *dimension, pos world.Pos) bool {
	if rsConductor(d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y + 1, Z: pos.Z})) {
		return true
	}
	catType, _ := EntityTypeID("cat")
	for _, entity := range s.entities.All() {
		if entity.Dim != d.name || entity.Type != catType || !entity.SittingPose {
			continue
		}
		bounds := mobBounds(catType)
		if entity.Age < 0 {
			bounds[0] *= 0.5
			bounds[1] *= 0.5
		}
		half := bounds[0] / 2
		if entity.X+half > float64(pos.X) && entity.X-half < float64(pos.X+1) && entity.Y+bounds[1] > float64(pos.Y+1) && entity.Y < float64(pos.Y+2) && entity.Z+half > float64(pos.Z) && entity.Z-half < float64(pos.Z+1) {
			return true
		}
	}
	return false
}

func (w *openWindow) chestSlot(slot int) (*chestData, world.Pos, int) {
	if w.chestOther != nil {
		if slot >= chestSlots {
			return w.chestOther, w.chestOtherPos, slot - chestSlots
		}
		return w.chest, w.chestFirstPos, slot
	}
	return w.chest, w.pos, slot
}

func (w *openWindow) blockMenuSlot(pos world.Pos, slot int) (int, bool) {
	if w.chestOther != nil {
		if pos == w.chestFirstPos {
			return slot, true
		}
		if pos == w.chestOtherPos {
			return chestSlots + slot, true
		}
		return 0, false
	}
	return slot, w.pos == pos
}
