package server

import (
	"math"
)

// playerPoseEyeHeight uses Avatar.POSES / LivingEntity.SLEEPING_DIMENSIONS
// float facts measured on the official26.2 jar with Temurin25 javap.
func playerPoseEyeHeight(pose int32) float64 {
	switch pose {
	case 1, 3, 4:
		return float64(float32(0.4))
	case 5:
		return float64(float32(1.27))
	case 2:
		return float64(float32(0.2))
	default:
		return float64(float32(1.62))
	}
}
func (s *Server) startBundleUse(sess *Session, hand int32, st ItemStack) {
	if hand < 0 || hand > 1 || sess.usingItemID != 0 {
		return
	}
	_, slot := sess.stackForHand(hand)
	sess.bundleUseRemaining = 200
	sess.bundleUseHand = uint8(hand + 1)
	sess.bundleUseSlot = slot
	sess.bundleUseItem = st
	sess.usingItemID = st.ID
	sess.eyeUseOffhand = hand == 1
	sess.eyeUseDirty = true
	x, y, z, _, _, _ := sess.Pos()
	s.emitGameEvent(sess.dimension(), "item_interact_start", x, y, z, sess, "")
}
func (s *Server) stopBundleUse(sess *Session) {
	if sess.bundleUseHand == 0 {
		return
	}
	sess.bundleUseHand = 0
	sess.bundleUseRemaining = 0
	sess.bundleUseItem = ItemStack{}
	sess.usingItemID = 0
	sess.eyeUseDirty = true
	x, y, z, _, _, _ := sess.Pos()
	s.emitGameEvent(sess.dimension(), "item_interact_finish", x, y, z, sess, "")
}

// BundleItem.onUseTick ejects one complete selected stack at remaining200,
// then after ten ticks on even remaining ticks. The active use lasts200 ticks.
func (s *Server) bundleUseTick(sess *Session) {
	if sess.bundleUseHand == 0 {
		return
	}
	st, slot := sess.stackForHand(int32(sess.bundleUseHand - 1))
	if sess.dead || slot != sess.bundleUseSlot || st != sess.bundleUseItem {
		s.stopBundleUse(sess)
		return
	}
	remaining := sess.bundleUseRemaining
	if remaining == 200 || remaining < 190 && remaining%2 == 0 {
		dropped := bundleRemove(&st)
		if !dropped.Empty() {
			sess.setStack(slot, st)
			sess.bundleUseItem = st
			_ = s.sendContainerSlot(sess, slot, st)
			s.bundleDropStack(sess, dropped)
			sess.addItemStat(statUsed, st.ID, 1)
			x, y, z, _, _, _ := sess.Pos()
			pitch := float32(0.8) + float32(s.rng.next()>>40)*(1.0/16777216.0)*0.4
			if id, ok := SoundEventID("item.bundle.remove_one"); ok {
				s.playSoundSrc(sess.dimName(), x, y, z, id, 7, 0.8, pitch, sess)
			}
			pitch = float32(0.8) + float32(s.rng.next()>>40)*(1.0/16777216.0)*0.4
			if id, ok := SoundEventID("item.bundle.drop_contents"); ok {
				s.playSoundSrc(sess.dimName(), math.Floor(x)+0.5, math.Floor(y)+0.5, math.Floor(z)+0.5, id, 7, 0.8, pitch, nil)
			}
		}
	}
	sess.bundleUseRemaining--
	if sess.bundleUseRemaining <= 0 {
		s.stopBundleUse(sess)
	}
}

// bundleDropStack uses the measured LivingEntity.createItemStackToDrop
// position, thrower/delay and directional velocity, rather than a block drop.
func (s *Server) bundleDropStack(sess *Session, it ItemStack) {
	x, y, z, yaw, pitch, _ := sess.Pos()
	pose := playerDesiredPose(sess)
	if player, ok := s.entities.Get(sess.entityID.Load()); ok {
		pose = player.Pose
	}
	rad := float32(0.017453292)
	sy, cy := mthSin(float64(yaw*rad)), mthCos(float64(yaw*rad))
	sp, cp := mthSin(float64(pitch*rad)), mthCos(float64(pitch*rad))
	randomFloat := func() float32 { return float32(s.rng.next()>>40) * (1.0 / 16777216.0) }
	jitterAngle := float64(randomFloat() * float32(6.2831855))
	jitter := float64(float32(0.02) * randomFloat())
	vx := float64(-sy*cp*float32(0.3)) + math.Cos(jitterAngle)*jitter
	vz := float64(cy*cp*float32(0.3)) + math.Sin(jitterAngle)*jitter
	vy := float64(-sp*float32(0.3) + float32(0.1) + (randomFloat()-randomFloat())*float32(0.1))
	typ, _ := EntityTypeID("item")
	e := s.entities.Spawn(&Entity{Type: typ, Dim: sess.dimName(), X: x, Y: y + playerPoseEyeHeight(pose) - float64(float32(0.3)), Z: z, MotionX: vx, MotionY: vy, MotionZ: vz, Item: it, PickupDelay: 40, Thrower: sess.entityID.Load()})
	s.broadcastEntitySpawn(nil, *e)
	s.broadcastEntityMetadata(e.ID, encodeItemMetadata(it))
	sess.addCustomStat("drop", 1)
	sess.addItemStat(statDropped, it.ID, it.Count)
}
