package server

import (
	"math"
	"math/rand"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// eyeFlight is EyeOfEnder's signalled target and survival roll.
type eyeFlight struct {
	tx, ty, tz float64
	survive    bool
}

// throwEnderEye is EnderEyeItem.use: a NONE-fluid POV clip onto an end
// portal frame passes (useOn fills it); otherwise the nearest
// eye_of_ender_located structure within 100 chunks of the player's
// block — none consumes the use without a throw. The eye spawns at the
// player's x/z and mid height, signals the structure's corner, and
// item.ender_eye.launch (NEUTRAL, 1, lerp(nextFloat, 0.33, 0.5)) plays for
// everyone; survival consumes one and the use counts item_used.
func (s *Server) throwEnderEye(sess *Session, hand int32, yaw, pitch float32) {
	d := sess.dimension()
	if d == nil || sess.mode() == GameModeSpectator {
		return
	}
	if hit := s.playerPOVHit(sess, d, clipFluidNone, yaw, pitch); !hit.miss && bstate(d.w.GetBlock(hit.pos)) == "end_portal_frame" {
		return
	}
	// EnderEyeItem starts use before attempting a structure search, even
	// where no structure can be located (26.2, JDK 25 javap).
	if sess.usingItemID == 0 {
		st, _ := sess.stackForHand(hand)
		sess.usingItemID = st.ID
		sess.eyeUseHand = uint8(hand + 1)
		sess.eyeUseOffhand = hand == 1
		sess.eyeUseDirty = true
		x, y, z, _, _, _ := sess.Pos()
		s.emitGameEvent(d, "item_interact_start", x, y, z, sess, "")
	}
	if d.terrain == nil {
		return
	}
	g := d.terrain.g
	names, ok := g.StructureTag("eye_of_ender_located")
	if !ok {
		return
	}
	x, y, z, _, _, _ := sess.Pos()
	at, _, found := g.FindNearestMapStructure(names, worldgen.BlockPos{int32(math.Floor(x)), int32(math.Floor(y)), int32(math.Floor(z))}, 100)
	if !found {
		return
	}
	h := s.enderEyeLaunchHeight(sess)
	stack, slot := sess.stackForHand(hand)
	e := &Entity{Type: mustEntityType("eye_of_ender"), Dim: d.name, X: x, Y: y + h*0.5, Z: z,
		Item: ItemStack{ID: stack.ID, Count: 1}}
	e.eye = eyeSignal(e.X, e.Y, e.Z, float64(at[0]), float64(at[1]), float64(at[2]))
	spawned := s.entities.Spawn(e)
	s.broadcastEntitySpawn(nil, *spawned)
	s.broadcastEntityMetadata(spawned.ID, encodeItemMetadata(spawned.Item))
	if id, ok := soundEventIDs["entity.ender_eye.launch"]; ok {
		p := 0.33 + rand.Float32()*(0.5-0.33)
		s.playSoundSrc(d.name, x, y, z, id, srcNeutral, 1, p, nil)
	}
	if sess.mode() != GameModeCreative {
		sess.consumeSlot(slot, 1)
		_ = s.sendContainerContent(sess)
	}
	sess.addItemStat(statUsed, stack.ID, 1)
}

// eyeSignal is EyeOfEnder.signalTo: a target more than 12 blocks away
// horizontally is replaced by the point 12 blocks toward it and 8 up;
// surviveAfterDeath is nextInt(5) > 0.
func eyeSignal(x, y, z, tx, ty, tz float64) *eyeFlight {
	dx, dz := tx-x, tz-z
	if hd := math.Sqrt(dx*dx + dz*dz); hd > 12 {
		tx, ty, tz = x+dx/hd*12, y+8, z+dz/hd*12
	}
	return &eyeFlight{tx: tx, ty: ty, tz: tz, survive: rand.Intn(5) > 0}
}

// eyeTick is EyeOfEnder.tick on the server: the next position is the
// current one plus the motion; with a target the motion is re-steered
// from that next position (updateDeltaMovement: horizontal speed eased
// 0.25% toward the remaining distance, both it and the vertical part
// scaled 0.8 inside one block, vertical eased 1.5% toward ±1 by
// whether the target lies above); then the eye moves. Past 80 ticks
// entity.ender_eye.death plays, the eye goes, and it drops its item
// when it survives, else level event 2003 shatters it.
func (s *Server) eyeTick(e *Entity) {
	nx, ny, nz := e.X+e.MotionX, e.Y+e.MotionY, e.Z+e.MotionZ
	if f := e.eye; f != nil {
		dx, dz := f.tx-nx, f.tz-nz
		l := math.Sqrt(dx*dx + dz*dz)
		hd := math.Sqrt(e.MotionX*e.MotionX + e.MotionZ*e.MotionZ)
		speed := hd + 0.0025*(l-hd)
		vy := e.MotionY
		if l < 1 {
			speed *= 0.8
			vy *= 0.8
		}
		sign := -1.0
		if ny-vy < f.ty {
			sign = 1
		}
		if l > 0 {
			e.MotionX, e.MotionZ = dx*speed/l, dz*speed/l
		} else {
			e.MotionX, e.MotionZ = 0, 0
		}
		e.MotionY = vy + (sign-vy)*0.015
	}
	e.X, e.Y, e.Z = nx, ny, nz
	e.EphemeralAge++
	if e.EphemeralAge > 80 {
		if id, ok := soundEventIDs["entity.ender_eye.death"]; ok {
			s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, srcNeutral, 1, 1, nil)
		}
		s.ephemeralDiscard(e)
		if e.eye != nil && e.eye.survive {
			it := e.Item
			if it.Empty() {
				id, _ := world.ItemID("ender_eye")
				it = ItemStack{ID: id, Count: 1}
			}
			s.spawnItemDropAt(e.Dim, e.X, e.Y, e.Z, it)
		} else {
			s.broadcastLevelEvent(e.Dim, 2003, floorPos(e), 0)
		}
		return
	}
	s.mobSyncFlush(e)
}

// spawnItemDropAt is new ItemEntity(level, x, y, z, stack) added fresh:
// the item at the exact point on a (rand·0.2−0.1, 0.2, rand·0.2−0.1)
// delta, no pickup delay.
func (s *Server) spawnItemDropAt(dim string, x, y, z float64, stack ItemStack) *Entity {
	itemType, ok := EntityTypeID("item")
	if !ok || stack.Empty() {
		return nil
	}
	e := s.entities.Spawn(&Entity{Type: itemType, Dim: dim, X: x, Y: y, Z: z,
		MotionX: rand.Float64()*0.2 - 0.1, MotionY: 0.2, MotionZ: rand.Float64()*0.2 - 0.1, Item: stack})
	s.broadcastEntitySpawn(nil, *e)
	s.broadcastEntityMetadata(e.ID, encodeItemMetadata(e.Item))
	return e
}

// enderEyeUseTick preserves the zero-duration use until release or a
// different item occupies the active hand. Its remaining duration starts
// at zero and decreases, so it never reaches the completion boundary.
func (s *Server) enderEyeUseTick(sess *Session) {
	if sess.eyeUseHand != 0 {
		st, _ := sess.stackForHand(int32(sess.eyeUseHand - 1))
		if st.Empty() || st.ID != sess.usingItemID {
			s.stopEnderEyeUse(sess)
		}
	}
	if sess.eyeUseDirty {
		s.broadcastEntityMetadata(sess.entityID.Load(), []byte{8, 0, enderEyeUseFlags(sess), 0xff})
		sess.eyeUseDirty = false
	}
}

func (s *Server) stopEnderEyeUse(sess *Session) {
	if sess.eyeUseHand != 0 {
		x, y, z, _, _, _ := sess.Pos()
		s.emitGameEvent(sess.dimension(), "item_interact_finish", x, y, z, sess, "")
		sess.eyeUseHand = 0
		sess.eyeUseDirty = true
		sess.usingItemID = 0
	}
}

// stopUsingItem clears only the active bit; the selected-hand bit stays
// nondefault after offhand use until another start selects the main hand.
func enderEyeUseFlags(sess *Session) byte {
	var flags byte
	if sess.eyeUseOffhand {
		flags = 2
	}
	if sess.eyeUseHand != 0 || sess.bundleUseHand != 0 {
		flags |= 1
	}
	return flags
}

// enderEyeLaunchHeight is Entity.getBbHeight: the actual synced pose
// controls the launch point, even when it differs from current inputs.
func (s *Server) enderEyeLaunchHeight(sess *Session) float64 {
	pose := playerDesiredPose(sess)
	if player, ok := s.entities.Get(sess.entityID.Load()); ok {
		pose = player.Pose
	}
	_, h := playerPoseDims(pose)
	return float64(float32(h))
}
