package server

// Blaze AI, measured off the 26.2 jar (javap of Blaze and
// Blaze$BlazeAttackGoal):
//
//   - registerGoals: BlazeAttackGoal(4, MOVE|LOOK), MoveTowardsRestriction
//     (5), WaterAvoidingRandomStroll(7, 1.0), LookAtPlayer(8, 8.0) +
//     RandomLookAround(8); targets HurtByTarget(1) then
//     NearestAttackableTarget(Player, mustSee)(2). follow_range 48.
//   - aiStep's head: off the ground with a falling delta, y is scaled by
//     0.6 before the rest of the living tick (the slow fall — same arm
//     as Chicken's). isSensitiveToWater is true (LivingEntity.aiStep's
//     drown,1 tick in water or rain — mobWaterSensitive).
//   - customServerAiStep: nextHeightOffsetChangeTick counts down from
//     0, so the first tick already re-rolls allowedHeightOffset to
//     triangle(0.5, 6.891) and re-arms 100; while the target's eye sits
//     above eyeY + allowedHeightOffset the delta's y climbs by
//     (0.3 - y)·0.3 (needsSync latched).
//   - BlazeAttackGoal.tick (requiresUpdateEveryTick): attackTime--,
//     lastSeen tracks line of sight. Inside dist² < 4 with sight the
//     goal melees (doHurtTarget) when attackTime ≤ 0 — re-armed to 20 —
//     and steers onto the target. Inside follow_range² with sight the
//     burst machine runs when attackTime ≤ 0: attackStep 1 charges
//     (DATA_FLAGS_ID bit 0 on, attackTime 60), steps 2-4 fire one
//     SmallFireball each 6 ticks apart, step 5 rests 100 ticks with
//     the flag cleared and the step reset. Every shot (step > 1) plays
//     level event 1018 at the blaze's block position (skipped when
//     silent) and aims at (dx ± triangle(2.297·√√dist²·0.5), target
//     getY(0.5) − blaze getY(0.5), dz ± same) — y never jitters. The
//     ball spawns at the blaze's x/z and getY(0.5)+0.5 with the
//     normalized aim × accelerationPower 0.1 as its delta and the
//     blaze's rotation. Out of sight for fewer than 5 ticks the goal
//     still steers to the target. stop() clears the flag and lastSeen.
//   - SmallFireball.onHitEntity: igniteForSeconds(5) then hurt 5.0
//     (fireball source, owner-credited); onHitBlock lights the face
//     cell unless a Mob owner meets mob_griefing=false.

import (
	"math"

	"mc-go/internal/world"
)

// blazeEyeHeight is the blaze's eye height (0.85 × 1.8).
const blazeEyeHeight = 1.53

func (s *Server) blazeStep(e *Entity) bool {
	st := flyerOf(e)
	follow := s.mobAttr(e, "blaze", "follow_range", 48)
	t := s.flyerSelectTarget(e, st, follow, blazeEyeHeight, true, true, nil)
	if t == nil {
		if st.attackOn {
			// BlazeAttackGoal.stop.
			st.attackOn = false
			s.blazeSetCharged(e, st, false)
			st.lastSeen = 0
		}
		s.blazeHeightRoll(st)
		// No target: the stroll/look goals and the plain living
		// travel — the generic wander carries the slow fall
		// (mobSlowFalls).
		return false
	}
	w := s.dimWorld(e.Dim)
	if w == nil {
		return true
	}
	// Blaze.aiStep's slow fall precedes the whole living tick.
	if !e.Grounded && e.MotionY < 0 {
		e.MotionY *= 0.6
	}
	if !st.attackOn {
		// BlazeAttackGoal.start.
		st.attackOn = true
		st.attackStep = 0
	}
	moveTo, look := s.blazeAttackTick(e, st, t, follow)
	px, py, pz, _, _, _ := t.Pos()
	// customServerAiStep — after the goal ticks, before moveControl.
	s.blazeHeightRoll(st)
	if py+1.62 > e.Y+blazeEyeHeight+st.heightOffset {
		e.MotionY += (0.3 - e.MotionY) * 0.3
		e.NeedsSync = true
	}
	e.NeedsSync = true
	dx, dz := px-e.X, pz-e.Z
	if moveTo {
		// MoveControl MOVE_TO at speedModifier 1.0: rotlerp the yaw
		// 90°/tick, drive movement_speed along it, and jump when the
		// wanted spot sits above the step height straight overhead.
		mobTurnToward(e, dx, dz, 90)
		if py-e.Y > 0.6 && dx*dx+dz*dz < 1 && e.Grounded {
			e.MotionY = 0.42
		}
		s.mobTravelStep(e, w, s.mobAttr(e, "blaze", "movement_speed", 0.23))
		e.HeadYaw = approachDeg(e.HeadYaw, e.Yaw, 10)
	} else {
		s.mobTravelStep(e, w, 0)
	}
	if look {
		// LookControl.setLookAt(target, 10, 10).
		target := float32(math.Atan2(-dx, dz) * 180 / math.Pi)
		e.HeadYaw = approachDeg(e.HeadYaw, target, 10)
		horiz := math.Hypot(dx, dz)
		pitch := float32(-math.Atan2(py+1.62-(e.Y+blazeEyeHeight), horiz) * 180 / math.Pi)
		e.Pitch = approachDeg(e.Pitch, pitch, 10)
	}
	e.LookYaw = e.HeadYaw
	return true
}

// blazeHeightRoll is customServerAiStep's allowedHeightOffset re-roll.
func (s *Server) blazeHeightRoll(st *flyerState) {
	st.nextHeightTick--
	if st.nextHeightTick <= 0 {
		st.nextHeightTick = 100
		st.heightOffset = triangle(0.5, 6.891)
	}
}

// blazeSetCharged is Blaze.setCharged — DATA_FLAGS_ID bit 0.
func (s *Server) blazeSetCharged(e *Entity, st *flyerState, on bool) {
	if st.charged != on {
		st.charged = on
		e.MetaDirty = true
	}
}

// blazeAttackTick is BlazeAttackGoal.tick; it reports whether the goal
// set a MOVE_TO toward the target and whether it aimed the look.
func (s *Server) blazeAttackTick(e *Entity, st *flyerState, t *Session, follow float64) (moveTo, look bool) {
	st.attackTime--
	px, py, pz, _, _, _ := t.Pos()
	see := s.flyerLOS(e.Dim, e.X, e.Y+blazeEyeHeight, e.Z, px, py+1.62, pz)
	if see {
		st.lastSeen = 0
	} else {
		st.lastSeen++
	}
	dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
	d2 := dx*dx + dy*dy + dz*dz
	switch {
	case d2 < 4:
		if !see {
			return false, false
		}
		if st.attackTime <= 0 {
			st.attackTime = 20
			dmg := s.mobMeleeDamage(e, "blaze")
			dealt := s.damageWith(t, dmg, translatable("death.attack.mob",
				literal(t.getName()), literal(mobDisplayName("blaze"))), damageSource{
				typeName: "mob_attack", cause: e.ID, direct: e.ID,
			})
			if dealt > 0 {
				s.mobPostAttack(e, "blaze", t)
			}
		}
		return true, false
	case d2 < follow*follow && see:
		if st.attackTime <= 0 {
			st.attackStep++
			switch {
			case st.attackStep == 1:
				st.attackTime = 60
				s.blazeSetCharged(e, st, true)
			case st.attackStep <= 4:
				st.attackTime = 6
			default:
				st.attackTime = 100
				st.attackStep = 0
				s.blazeSetCharged(e, st, false)
			}
			if st.attackStep > 1 {
				spread := math.Sqrt(math.Sqrt(d2)) * 0.5
				if !e.Silent {
					s.broadcastLevelEvent(e.Dim, 1018, world.Pos{
						X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z)),
					}, 0)
				}
				ax := triangle(dx, 2.297*spread)
				ay := (py + 0.9) - (e.Y + 0.9)
				az := triangle(dz, 2.297*spread)
				s.spawnMobFireball(e, "small_fireball", e.X, e.Y+0.9+0.5, e.Z, ax, ay, az, 0)
			}
		}
		return false, true
	case st.lastSeen < 5:
		return true, false
	}
	return false, false
}

// spawnMobFireball is the mob-fired Fireball construction:
// AbstractHurtingProjectile(type, owner, movement) places the ball at
// the owner, takes the owner's rotation and sets the delta to
// normalize(movement)·accelerationPower (0.1); the caller's setPos
// moves it to the muzzle. addFreshEntity pairs it with the owner's id
// riding add_entity's data field.
func (s *Server) spawnMobFireball(owner *Entity, name string, x, y, z, ax, ay, az float64, power float32) *Entity {
	l := math.Sqrt(ax*ax + ay*ay + az*az)
	if l < 1e-4 {
		// Vec3.normalize of a near-zero vector is ZERO.
		ax, ay, az, l = 0, 0, 0, 1
	}
	const accel = 0.1
	mx, my, mz := ax/l*accel, ay/l*accel, az/l*accel
	fb := s.entities.Spawn(&Entity{
		Type:    mustEntityType(name),
		Dim:     owner.Dim,
		X:       x,
		Y:       y,
		Z:       z,
		Yaw:     owner.Yaw,
		Pitch:   owner.Pitch,
		MotionX: mx, MotionY: my, MotionZ: mz,
		VX: int16(mx * 8000), VY: int16(my * 8000), VZ: int16(mz * 8000),
		AccelPower:     accel,
		ExplosionPower: power,
		Data:           owner.ID,
	})
	fb.arrowShooter = owner.ID
	s.broadcastEntitySpawn(nil, *fb)
	return fb
}
