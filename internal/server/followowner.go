package server

import (
	"math"
	"math/rand"
	"strings"

	"mc-go/internal/world"
)

// followOwnerParams is FollowOwnerGoal's registration set —
// MOVE|LOOK at speedModifier 1.0 on all three tamables, but the
// (startDistance, stopDistance) pair differs per species: wolf runs
// 10/2, cat 10/5, and the parrot takes 5/1 at a higher goal priority
// (2 vs the wolf/cat's 6).
var followOwnerParams = map[string][2]float64{
	"wolf":   {10, 2},
	"cat":    {10, 5},
	"parrot": {5, 1},
}

// followOwnerTypes mirrors followOwnerParams for set membership.
var followOwnerTypes = map[string]bool{
	"wolf":   true,
	"cat":    true,
	"parrot": true,
}

// followOwnerFlyers is TamableAnimal.canFlyToOwner — true only for
// the parrot: a ground pet's teleport check rejects a leaf floor,
// a flyer's doesn't.
var followOwnerFlyers = map[string]bool{
	"parrot": true,
}

// followOwnerStep is FollowOwnerGoal's canUse + tick:
//
//	canUse: owner resolves (same dimension) && !unableToMoveToOwner
//	  && dist² >= 10². canContinueToUse keeps the goal while the
//	  mob is past 2² — the 4..100 window is a hysteresis band: a
//	  pet that already arrived stands (goal ended at the stop
//	  radius; re-arm needs the full start radius again).
//	tick: at dist² >= 12² (shouldTryTeleportToOwner) the 10-tick
//	  recalc cadence runs tryToTeleportToOwner; inside it the pet
//	  MOVE_TOs its owner at speedModifier×movement_speed and
//	  lookControl.setLookAt keeps the head on the owner.
//
//	unableToMoveToOwner: orderedToSit || isPassenger || leashed ||
//	  owner is a spectator.
func (s *Server) followOwnerStep(e *Entity, ambName string) bool {
	if e.ownerUUID == ([16]byte{}) {
		return false
	}
	p := s.sessionByUUID(e.ownerUUID)
	if p == nil || !p.spawned.Load() || p.dimName() != e.Dim {
		return false
	}
	if e.Sitting || e.Riding != 0 || e.LeashHolder != 0 ||
		p.mode() == GameModeSpectator {
		return false
	}
	px, py, pz, _, _, _ := p.Pos()
	dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
	dist2 := dx*dx + dy*dy + dz*dz
	start, stop := followOwnerParams[ambName][0], followOwnerParams[ambName][1]
	if dist2 <= stop*stop {
		e.FollowNav = false
		e.FollowRecalc = 0
		return false
	}
	if dist2 >= start*start {
		e.FollowNav = true
	}
	if dist2 >= 144.0 {
		if e.FollowRecalc <= 0 {
			e.FollowRecalc = 10
			s.followOwnerTeleport(e, p, px, py, pz)
		} else {
			e.FollowRecalc--
		}
		return true
	}
	if !e.FollowNav {
		return false
	}
	en, _ := EntityTypeName(e.Type)
	speed, ok := entityAttrBases[en]["movement_speed"]
	if !ok || speed <= 0 {
		speed = mobWalkSpeed
	}
	mobTurnToward(e, dx, dz, 90)
	e.LookYaw = e.Yaw
	if w := s.dimWorld(e.Dim); w != nil {
		s.mobTravelStep(e, w, speed)
	}
	return true
}

// followOwnerTeleport is TamableAnimal.tryToTeleportToOwner →
// teleportToAroundBlockPos: up to 10 draws of xd,zd ∈ [-3,3] with at
// least one axis |d|≥2 plus yd ∈ [-1,1] around the owner's block pos,
// landing on the first cell that passes canTeleportTo.
func (s *Server) followOwnerTeleport(e *Entity, p *Session, px, py, pz float64) {
	w := s.dimWorld(e.Dim)
	if w == nil {
		return
	}
	bx, by, bz := int(math.Floor(px)), int(math.Floor(py)), int(math.Floor(pz))
	for i := 0; i < 10; i++ {
		xd := rand.Int31n(7) - 3
		zd := rand.Int31n(7) - 3
		if abs32(xd) < 2 && abs32(zd) < 2 {
			continue
		}
		yd := rand.Int31n(3) - 1
		tx, ty, tz := bx+int(xd), by+int(yd), bz+int(zd)
		if !s.followOwnerCanLand(e, w, tx, ty, tz) {
			continue
		}
		// maybeTeleportTo's snapTo + navigation.stop — the position
		// rides the tracker as entity_position_sync next sendChanges.
		s.tpMountTeleport(e, float64(tx)+0.5, float64(ty), float64(tz)+0.5, e.Yaw, e.Pitch, 0)
		return
	}
}

// followOwnerCanLand approximates WalkNodeEvaluator.getPathTypeStatic
// == WALKABLE && the leaf-floor rejection && the noCollision box
// sweep: the cells the pet's box would span are collision-free and
// the block directly below the target cell is land (ground pets
// additionally refuse a leaf floor).
func (s *Server) followOwnerCanLand(e *Entity, w *world.World, x, y, z int) bool {
	below := w.GetBlock(world.Pos{X: x, Y: y - 1, Z: z})
	if below == world.Air || world.NonSolid(below) || moveNoCollision(below) {
		return false
	}
	en, _ := EntityTypeName(e.Type)
	if !followOwnerFlyers[en] {
		if bn, _ := world.StateOwner(below); strings.HasSuffix(strings.TrimPrefix(bn, "minecraft:"), "_leaves") {
			return false
		}
	}
	dims := entityBBDims(e)
	return !entityInsideSolid(w, float64(x)+0.5, float64(y), float64(z)+0.5, dims[0]/2, dims[1])
}

func abs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}
