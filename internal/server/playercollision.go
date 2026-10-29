package server

import (
	"math"
	"strings"

	"mc-go/internal/world"
)

// cellShapeAt uses measured empty-context collision facts for static blocks.
// Moving pistons and shulker boxes retain the prior conservative shape until
// the world-dependent block-entity adapter supplies their live geometry.
func cellShapeAt(block world.Block, pos world.Pos) []cellBox {
	name, _ := world.StateOwner(block)
	if name == "moving_piston" || name == "shulker_box" || strings.HasSuffix(name, "_shulker_box") {
		return cellShape(block)
	}
	return measuredCollisionShapeAt(block, pos)
}

// playerBoxCollidesWithDims implements the measured new-collision rule:
// a target box may still overlap a block shape that the old box already
// overlapped, but entering a previously clear shape rejects the move.
// ServerGamePacketListenerImpl shrinks both boxes by a widened float1e-5.
func playerBoxCollidesWithDims(w *world.World, ox, oy, oz, nx, ny, nz, half, height float64) bool {
	return playerBoxCollidesWithShapes(w, ox, oy, oz, nx, ny, nz, half, height, playerContextShapeAt)
}

func playerBoxCollidesWithShapes(w *world.World, ox, oy, oz, nx, ny, nz, half, height float64, shapeAt func(world.Block, world.Pos, playerShapeContext) []cellBox) bool {
	const epsilon = float64(float32(1e-5))
	box := func(x, y, z float64) aabb {
		return aabb{x - half + epsilon, y + epsilon, z - half + epsilon, x + half - epsilon, y + height - epsilon, z + half - epsilon}
	}
	old, next := box(ox, oy, oz), box(nx, ny, nz)
	for x := int(math.Floor(next[0])) - 1; x <= int(math.Floor(next[3]))+1; x++ {
		for y := int(math.Floor(next[1])) - 1; y <= int(math.Floor(next[4]))+1; y++ {
			for z := int(math.Floor(next[2])) - 1; z <= int(math.Floor(next[5]))+1; z++ {
				position := world.Pos{X: x, Y: y, Z: z}
				oldHit, nextHit := false, false
				for _, shape := range shapeAt(w.GetBlock(position), position, playerShapeContext{feet: oy, placement: true}) {
					bounds := aabb{float64(x) + shape[0], float64(y) + shape[1], float64(z) + shape[2], float64(x) + shape[3], float64(y) + shape[4], float64(z) + shape[5]}
					oldHit = oldHit || bounds.intersects(old)
					nextHit = nextHit || bounds.intersects(next)
				}
				if nextHit && !oldHit {
					return true
				}
			}
		}
	}
	return false
}

func (s *Server) playerCollisionDims(player *playerRecord) (float64, float64) {
	pose := int32(0)
	if entity, ok := s.entities.Get(player.entityID.Load()); ok {
		pose = entity.Pose
	}
	scale := player.collisionScale
	if scale == 0 {
		scale = 1
	}
	return playerPoseDimsAtScale(pose, scale)
}

// Scale is a ranged attribute (1/16..16); vanilla sanitizes NaN to
// the minimum and narrows the effective double value to float.
func (s *Server) playerRecordPoseDims(player *playerRecord, pose int32) (float64, float64) {
	return playerPoseDimsAtScale(pose, s.playerEffectiveScale(player))
}

func (s *Server) playerEffectiveScale(player *playerRecord) float32 {
	scale := 1.0
	if entity, ok := s.entities.Get(player.entityID.Load()); ok {
		if base, ok := entity.AttrBase["scale"]; ok {
			scale = base
		}
		scale = attrValue(scale, entity.AttrMods["scale"])
	}
	if math.IsNaN(scale) {
		scale = .0625
	}
	scale = math.Max(.0625, math.Min(16, scale))
	return float32(scale)
}

// Living attribute updates refresh the current box at the end of the living
// tick. Hypothetical pose-fit queries still read effective attributes directly.
func (s *Server) playerCollisionScaleTick() {
	for _, player := range s.activePlayersOwned() {
		if player.java != nil && !player.java.gameplayTickReady() {
			continue
		}
		s.refreshPlayerCollisionScale(player)
	}
}

// Pose changes can refresh dimensions before the dirty-attribute tick pass.
func (s *Server) refreshPlayerCollisionScale(player *playerRecord) {
	player.collisionScale = s.playerEffectiveScale(player)
}
