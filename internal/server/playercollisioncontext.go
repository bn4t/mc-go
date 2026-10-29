package server

import (
	"math"
	"strconv"

	"mc-go/internal/world"
)

// Player pose fitting uses EntityCollisionContext.of; packet new-collision
// validation uses withPosition, whose placement flag is true in26.2.
// Context fields are detached inputs to the measured per-block gates.
type playerShapeContext struct {
	feet, fallDistance                  float64
	descending, leatherBoots, placement bool
}

func playerContextShapeAt(block world.Block, pos world.Pos, context playerShapeContext) []cellBox {
	name, _ := world.StateOwner(block)
	const epsilon = float64(float32(1e-5))
	switch name {
	case "scaffolding":
		if context.placement {
			return nil
		}
		if context.feet > float64(pos.Y)+1-epsilon && !context.descending {
			return measuredCollisionShapeAt(block, pos)
		}
		bottom, _ := world.StateProperty(block, "bottom")
		distance, _ := world.StateProperty(block, "distance")
		support, _ := strconv.Atoi(distance)
		if support > 0 && bottom == "true" && context.feet > float64(pos.Y)-epsilon {
			return []cellBox{{0, 0, 0, 1, .125, 1}}
		}
		return nil
	case "powder_snow":
		if context.placement {
			return nil
		}
		if context.fallDistance > 2.5 {
			return []cellBox{{0, 0, 0, 1, float64(float32(.9)), 1}}
		}
		if context.leatherBoots && !context.descending && context.feet > float64(pos.Y)+1-epsilon {
			return []cellBox{{0, 0, 0, 1, 1, 1}}
		}
		return nil
	}
	return cellShapeAt(block, pos)
}

func playerPoseContext(sess *Session) playerShapeContext {
	_, feet, _, _, _, _ := sess.Pos()
	boots := sess.stackAt(slotArmorStart + 3)
	return playerShapeContext{feet: feet, fallDistance: sess.fallDistance, descending: sess.sneaking, leatherBoots: !boots.Empty() && boots.ID == world.ItemIDs["leather_boots"]}
}

func (s *Server) playerWorldContextShapeAt(d *dimension, block world.Block, pos world.Pos, context playerShapeContext) []cellBox {
	name, _ := world.StateOwner(block)
	if name == "scaffolding" || name == "powder_snow" {
		return playerContextShapeAt(block, pos, context)
	}
	return s.worldCollisionShapeAt(d, block, pos)
}

func (s *Server) playerBoxCollidesInDimension(d *dimension, ox, oy, oz, nx, ny, nz, half, height float64) bool {
	return playerBoxCollidesWithShapes(d.w, ox, oy, oz, nx, ny, nz, half, height, func(block world.Block, pos world.Pos, context playerShapeContext) []cellBox {
		return s.playerWorldContextShapeAt(d, block, pos, context)
	})
}

func playerPoseBlockCollision(sess *Session, box aabb) bool {
	context := playerPoseContext(sess)
	for x := int(math.Floor(box[0])) - 1; x <= int(math.Floor(box[3]))+1; x++ {
		for y := int(math.Floor(box[1])) - 1; y <= int(math.Floor(box[4]))+1; y++ {
			for z := int(math.Floor(box[2])) - 1; z <= int(math.Floor(box[5]))+1; z++ {
				pos := world.Pos{X: x, Y: y, Z: z}
				for _, shape := range sess.server.playerWorldContextShapeAt(sess.dimension(), sess.world().GetBlock(pos), pos, context) {
					if box.intersects(aabb{float64(x) + shape[0], float64(y) + shape[1], float64(z) + shape[2], float64(x) + shape[3], float64(y) + shape[4], float64(z) + shape[5]}) {
						return true
					}
				}
			}
		}
	}
	return false
}
