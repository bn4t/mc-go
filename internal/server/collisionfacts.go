package server

import "mc-go/internal/world"

// measuredCollisionShapeAt is the registry report's empty-context collision
// shape, with the block's position offset restored. The report uses an empty
// block getter; entity- or block-entity-dependent consumers need their own
// context policy. Returned boxes are read-only.
func measuredCollisionShapeAt(state world.Block, pos world.Pos) []cellBox {
	boxes := vanillaShapes[stateShapeRow(state).collision]
	if len(boxes) == 0 {
		return nil
	}
	dx, dy, dz := blockOffsetAt(state, pos)
	if dx == 0 && dy == 0 && dz == 0 {
		return boxes
	}
	shifted := make([]cellBox, len(boxes))
	for i, box := range boxes {
		box[0] += dx
		box[3] += dx
		box[1] += dy
		box[4] += dy
		box[2] += dz
		box[5] += dz
		shifted[i] = box
	}
	return shifted
}
