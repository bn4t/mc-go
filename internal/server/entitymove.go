package server

import (
	"math"
	"strings"

	"mc-go/internal/world"
)

// blockSpeedFactorOf is Block.getSpeedFactor — the in-cell slow the
// Entity.move tail multiplies onto a moving entity's stored delta.
// In 26.2 only honey_block and soul_sand carry a Properties
// .speedFactor (both 0.4); everything else defaults to 1.
func blockSpeedFactorOf(blk world.Block) float64 {
	name, _ := world.StateOwner(blk)
	switch strings.TrimPrefix(name, "minecraft:") {
	case "honey_block", "soul_sand":
		return 0.4
	}
	return 1.0
}

// blockFrictionOf is Block.getFriction — the grounded slide the
// item/orb tick (and every LivingEntity.travel) multiplies onto
// horizontal motion. 26.2 overrides: ice/packed_ice/frosted_ice 0.98,
// blue_ice 0.989, slime_block 0.8; the default is 0.6.
func blockFrictionOf(blk world.Block) float64 {
	name, _ := world.StateOwner(blk)
	switch strings.TrimPrefix(name, "minecraft:") {
	case "ice", "packed_ice", "frosted_ice":
		return 0.98
	case "blue_ice":
		return 0.989
	case "slime_block":
		return 0.8
	}
	return 0.6
}

// blockBounceOf is Block.getBounceRestitution — the measured 26.2
// table: slime_block sets 1.0 and every coloured bed sets 0.75
// (getBounceRestitution dumped per block off the jar; the only
// registered _bed names are the 16 beds).
func blockBounceOf(blk world.Block) float64 {
	name, _ := world.StateOwner(blk)
	n := strings.TrimPrefix(name, "minecraft:")
	if n == "slime_block" {
		return 1.0
	}
	if strings.HasSuffix(n, "_bed") {
		return 0.75
	}
	return 0.0
}

// entitySpeedFactor is Entity.getBlockSpeedFactor: the factor of the
// block the entity is IN (its floor blockPosition cell). Water and
// bubble columns return their own factor directly; any other cell
// whose factor is 1 falls back to the supporting block —
// getBlockPosBelowThatAffectsMyMovement = getOnPos(0.500001), i.e. the
// cell holding the point half a block under the entity's feet, which
// is how an entity standing ON honey still gets slowed.
func entitySpeedFactor(w *world.World, x, y, z float64) float64 {
	feet := w.GetBlock(world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))})
	f := blockSpeedFactorOf(feet)
	name, _ := world.StateOwner(feet)
	if name == "minecraft:water" || name == "minecraft:bubble_column" {
		return f
	}
	if f != 1.0 {
		return f
	}
	// The getOnPos fallback resolves the supporting block a full cell
	// under the entity's blockPosition: measured slide distances match
	// floor(y)-1 exactly (vanilla chops approach-fall speed as soon as
	// the cell below the feet cell is honey, well before the 0.5 below
	// point enters the block).
	below := w.GetBlock(world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)) - 1, Z: int(math.Floor(z))})
	return blockSpeedFactorOf(below)
}

// entityFrictionBelow is the grounded friction half of the item/orb
// tick tail — getBlockPosBelowThatAffectsMyMovement's block friction.
func entityFrictionBelow(w *world.World, x, y, z float64) float64 {
	below := w.GetBlock(world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y - 0.500001)), Z: int(math.Floor(z))})
	return blockFrictionOf(below)
}

// entityInsideSolid is ItemEntity.tick's noPhysics gate:
// !level.noCollision(entity, bb.deflate(1e-7)) — the entity box,
// shrunk by epsilon, overlapping a block's collision shape. An
// entity resting exactly on a shape top does NOT overlap (so items
// on honey/soul_sand are not treated as stuck); the box has to be
// inside the shape for the push-out to run.
func entityInsideSolid(w *world.World, x, y, z, half, height float64) bool {
	minX, maxX := x-half+1e-7, x+half-1e-7
	minY, maxY := y+1e-7, y+height-1e-7
	minZ, maxZ := z-half+1e-7, z+half-1e-7
	for cx := int(math.Floor(minX)); cx <= int(math.Floor(maxX)); cx++ {
		for cy := int(math.Floor(minY)); cy <= int(math.Floor(maxY)); cy++ {
			for cz := int(math.Floor(minZ)); cz <= int(math.Floor(maxZ)); cz++ {
				b := w.GetBlock(world.Pos{X: cx, Y: cy, Z: cz})
				// noCollision checks collision SHAPES — water, lava
				// and the other shapeless cells don't count as solid
				// (an item resting in water is not inside a solid).
				if b == world.Air || world.NonSolid(b) || moveNoCollision(b) {
					continue
				}
				if maxY > float64(cy) && minY < float64(cy)+collisionTopOf(b) {
					return true
				}
			}
		}
	}
	return false
}

// entityMove is the physics half of Entity.move for mc-go's
// box-simmed entities (items, orbs, and anything else that needs a
// real collide): sweep the entity box against solid cells, advance by
// the clamped displacement, record the collision flags vanilla
// derives, run restitution, then multiply the stored horizontal delta
// by getBlockSpeedFactor. Returns the verticalCollisionBelow flag —
// the tick tails that read it (orb bounce) need it, and e.Grounded
// tracks verticalCollision like setOnGroundWithMovement (any vertical
// bound counts, ceiling hits included).
func (s *Server) entityMove(e *Entity, w *world.World, half, height, dx, dy, dz, gravity, airDrag float64, living bool) (vCollBelow bool) {
	if dx == 0 && dy == 0 && dz == 0 {
		return false
	}
	cx, cy, cz, _ := s.sweepEntityBox(w, e.X, e.Y, e.Z, half, height, dx, dy, dz)
	// A tiny remainder after a substantial collision does not relocate the
	// entity. Unobstructed small moves still advance; so do tiny components
	// accompanying meaningful movement on another axis.
	requestedSq := dx*dx + dy*dy + dz*dz
	resolvedSq := cx*cx + cy*cy + cz*cz
	if resolvedSq > 1e-7 || requestedSq-resolvedSq < 1e-7 {
		e.X += cx
		e.Y += cy
		e.Z += cz
	}
	xCol, zCol := cx != dx, cz != dz
	vColl := cy != dy
	vCollBelow = vColl && dy < 0
	e.Grounded = vColl
	hColl := xCol || zCol
	e.HColl = hColl
	// vanilla gates restitution on canSimulateMovement && ((|rawY|>0
	// && verticalCollision) || horizontalCollision).
	if (dy != 0 && vColl) || hColl {
		// Restitution uses effective gravity; NoGravity also removes its
		// contribution while resolving the remaining fraction of a move.
		if e.NoGravity {
			gravity = 0
		}
		s.restituteMove(e, w, xCol, zCol, vColl, vCollBelow, cy, dx, dy, dz, gravity, airDrag, living)
	}
	// SlimeBlock.stepOn — applyEffectsFromBlocks calls the support
	// block's stepOn (getOnPosLegacy = the cell holding the point 0.2
	// below the feet) every grounded tick: the current velocity after
	// restitution supplies |Δy| for the threshold and damping factor. It damps
	// horizontal delta by 0.4+|Δy|·0.2 — slime's walk drag.
	if e.Grounded && math.Abs(e.MotionY) < 0.1 {
		onBlk := w.GetBlock(world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y - 0.2)), Z: int(math.Floor(e.Z))})
		if onName, _ := world.StateOwner(onBlk); strings.TrimPrefix(onName, "minecraft:") == "slime_block" {
			sc := 0.4 + 0.2*math.Abs(e.MotionY)
			e.MotionX *= sc
			e.MotionZ *= sc
		}
	}
	f := entitySpeedFactor(w, e.X, e.Y, e.Z)
	e.MotionX *= f
	e.MotionZ *= f
	return vCollBelow
}

// collisionTopOf is the top of a block's collision shape in
// cell-local units — vanilla's VoxelShape column heights for the
// partial blocks mc-go models (honey 15/16, soul sand 14/16, every
// coloured bed's mattress column(16,3,9) at 9/16). Every other solid
// is a full cube.
func collisionTopOf(blk world.Block) float64 {
	name, _ := world.StateOwner(blk)
	switch strings.TrimPrefix(name, "minecraft:") {
	case "pointed_dripstone":
		// SpeleothemBlock's upward tip shape ends at 11/16. The
		// scalar movement check needs that top to accept a landing.
		direction, _ := world.StateProperty(blk, "vertical_direction")
		thickness, _ := world.StateProperty(blk, "thickness")
		if direction == "up" && thickness == "tip" {
			return 11.0 / 16
		}
	case "honey_block", "cactus":
		return 0.9375
	case "farmland", "dirt_path":
		return 0.9375
	case "soul_sand":
		return 0.875
	case "turtle_egg":
		// SHAPE_SINGLE is box(3,0,7, 12,12,14)/16 = 0.75 tall;
		// SHAPE_MULTIPLE (eggs 2-4) is column(14,7,12) = box
		// (1,0,2, 15,7,14)/16 = 0.4375 tall.
		if e, _ := world.StateProperty(blk, "eggs"); e == "1" {
			return 0.75
		}
		return 0.4375
	}
	if strings.HasSuffix(strings.TrimPrefix(name, "minecraft:"), "_bed") {
		return 0.5625
	}
	return 1.0
}

// restituteMove is Entity.restituteMovementAfterCollisions: a
// horizontal axis that bound reflects the stored delta by -bounce
// (getEntityBounciness, 0 for every entity mc-go models today), and
// any vertical collision rewrites the y component to
// (ratio*gravity - dy) * lerp(ratio,1,airDrag) * bounce — where ratio
// is the fraction of the fall the clamp actually consumed — which
// flattens the stored y to 0 whenever bounce is 0. The bounce itself
// is gated on verticalCollisionBelow, the incoming fall being faster
// than one tick of gravity, the block not carrying SUPPRESSES_BOUNCE
// (honey_block is the tag's only member), and the entity not sneaking
// (items never do); non-living entities take 0.8x the block's
// restitution. A real bounce posts the BOUNCE game event (sculk hears
// it at frequency 2).
func (s *Server) restituteMove(e *Entity, w *world.World, xCol, zCol, vColl, vCollBelow bool, cy, dx, dy, dz, gravity, airDrag float64, living bool) {
	bounce := 0.0
	if xCol {
		e.MotionX = -dx * bounce
	}
	if zCol {
		e.MotionZ = -dz * bounce
	}
	bounced := bounce > 0 && (xCol || zCol)
	if vColl {
		if vCollBelow {
			// Restitution reads the legacy support point, which remains below
			// the surface when the position-update gate retains a small gap.
			below := w.GetBlock(world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y - float64(float32(.2)))), Z: int(math.Floor(e.Z))})
			bname, _ := world.StateOwner(below)
			if -dy < gravity || strings.TrimPrefix(bname, "minecraft:") == "honey_block" {
				bounce = 0
			} else {
				if bb := blockBounceOf(below); bb > bounce {
					bounce = bb
					if !living {
						bounce = float64(float32(bounce) * float32(.8))
					}
				}
			}
		}
		if bounce > 0 {
			ratio := cy / dy
			e.MotionY = (ratio*gravity - dy) * (1 + ratio*(airDrag-1)) * bounce
			bounced = true
		} else {
			e.MotionY = 0
		}
	}
	if bounced {
		if d := s.dims[e.Dim]; d != nil {
			s.emitGameEvent(d, "bounce", e.X, e.Y, e.Z, nil, "")
		}
	}
}
