package server

import (
	"mc-go/internal/protocol"
)

// playerPoseDims is Avatar.POSES's box for the synced pose: half the
// width and the full height at the feet. The scalable dims all share
// the 0.6 half-width 0.3; SLEEPING/DYING shrink to the 0.2×0.2 fixed
// box, and every unmapped pose (LONG_JUMPING, SITTING, ...) takes the
// STANDING entry in vanilla's getOrDefault. EntityDimensions stores
// float dimensions; widen those values only after its float half-width.
func playerPoseDims(pose int32) (halfW, height float64) {
	return playerPoseDimsAtScale(pose, 1)
}

// LivingEntity.getDimensions applies the effective scale through
// EntityDimensions.scale. Float width/height multiply before the float
// half-width calculation; sleeping and dying dimensions are fixed.
func playerPoseDimsAtScale(pose int32, scale float32) (halfW, height float64) {
	width, h := float32(.6), float32(1.8)
	switch pose {
	case 2, 7:
		width, h, scale = .2, .2, 1
	case 5:
		h = 1.5
	case 1, 3, 4:
		h = .6
	}
	width *= scale
	h *= scale
	return float64(width / 2), float64(h)
}

// playerDesiredPose is Player.getDesiredPose: the pose the input state
// asks for before the fit check negotiates it down. SPIN_ATTACK is
// unreachable (riptide isn't modelled).
func playerDesiredPose(sess *Session) int32 {
	switch {
	case sess.sleeping.Load():
		return 2 // SLEEPING
	case sess.swimming:
		return 3 // SWIMMING
	case sess.fallFlying:
		return 1 // FALL_FLYING
	case sess.sneaking && !sess.flying:
		return 5 // CROUCHING
	default:
		return 0 // STANDING
	}
}

// playerCanFit is canPlayerFitWithinBlocksAndEntitiesWhen: the pose's
// bounding box at the feet must clear every block collision shape and
// every entity vanilla's noCollision counts — the hard-collidable set
// (boats, shulkers, happy ghasts), excluding the player itself and
// anything sharing its vehicle (canCollideWith folds pairs out).
func (s *Server) playerCanFit(sess *Session, pose int32) bool {
	x, y, z, _, _, _ := sess.Pos()
	d := sess.dimension()
	half, h := s.playerRecordPoseDims(&sess.playerRecord, pose)
	// Player.canPlayerFitWithinBlocksAndEntitiesWhen deflates the pose
	// box by 1e-7. Test the full shape, including its lower and lateral
	// bounds: a top slab above the head is not a solid cell-height column.
	const epsilon = 1e-7
	box := aabb{x - half + epsilon, y + epsilon, z - half + epsilon, x + half - epsilon, y + h - epsilon, z + half - epsilon}
	if playerPoseBlockCollision(sess, box) {
		return false
	}
	eid := sess.entityID.Load()
	riding := sess.riding.Load()
	for _, o := range s.entities.All() {
		if o.ID == eid || o.Dim != d.name {
			continue
		}
		// getEntityCollisions counts only entities whose
		// canBeCollidedWith is true: boats, shulkers and happy
		// ghasts — every other entity (mobs, players, drops)
		// answers false and the pose box slides through them.
		name, ok := EntityTypeName(o.Type)
		if !ok || !(isBoatEntityName(name) || name == "shulker" || name == "happy_ghast") {
			continue
		}
		if o.Living && o.Health <= 0 {
			continue
		}
		if o.ID == riding || o.Riding == eid {
			continue
		}
		bd := entityBBDims(&o)
		oh := bd[0] / 2
		if box.intersects(aabb{o.X - oh, o.Y, o.Z - oh, o.X + oh, o.Y + bd[1], o.Z + oh}) {
			return false
		}
	}
	return true
}

// updatePlayerPose is Player.updatePlayerPose: an every-player-tick
// pass that only runs while the smallest pose box still fits, resolves
// getDesiredPose through the fit ladder (spectator and passengers keep
// their desired pose outright, otherwise CROUCHING beats SWIMMING),
// and writes DATA_POSE on change — the change rides the entity-data
// diff to the owning client and every tracker.
func (s *Server) updatePlayerPose(sess *Session) {
	if !s.playerCanFit(sess, 3) {
		return
	}
	desired := playerDesiredPose(sess)
	var actual int32
	if sess.mode() == GameModeSpectator || sess.riding.Load() != 0 || s.playerCanFit(sess, desired) {
		actual = desired
	} else if s.playerCanFit(sess, 5) {
		actual = 5
	} else {
		actual = 3
	}
	eid := sess.entityID.Load()
	e, ok := s.entities.GetPtr(eid)
	if !ok || e.Pose == actual {
		return
	}
	e.Pose = actual
	s.refreshPlayerCollisionScale(&sess.playerRecord)
	// setSynchedData marks the entry dirty; the Set Entity Data packet
	// rides the next tracker pass (sendChanges), which runs after the
	// commands drained this tick — so a teleport's reply always lands
	// on the wire before the pose emit. Emitting inline here would race
	// that reply: commands run on the read goroutine while this pass
	// is the game thread.
	sess.poseEmit.Store(actual + 1)
}

// poseFlush emits a deferred DATA_POSE change to the owner and every
// tracker — vanilla's chunkMap tick sends the dirty entity-data diff
// a pass after the tick that marked it.
func (s *Server) poseFlush(sess *Session) {
	actual := sess.poseEmit.Swap(0) - 1
	if actual < 0 {
		return
	}
	eid := sess.entityID.Load()
	var b protocol.Buffer
	b.VarInt(eid)
	b.Byte(6)  // DATA_POSE
	b.Byte(20) // POSE serializer
	b.VarInt(actual)
	b.Byte(0xFF)
	body := b.Bytes()
	cache := javaEntityBodyCache{}
	s.sessions.Range(func(_, v any) bool {
		os := v.(*Session)
		if os.state.Load() != StatePlay || !os.spawned.Load() {
			return true
		}
		if os != sess && !os.tracksEntity(eid) {
			return true
		}
		_ = cache.writeMetadata(os, mustEntityType("player"), body)
		return true
	})
}
