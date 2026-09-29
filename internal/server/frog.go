package server

import "math"

// cubeSizeOf is AbstractCubeMob.getSize: the DATA_SIZE value, with
// the defined 1 standing in on a zero field (unsized spawn).
func cubeSizeOf(e *Entity) int32 {
	if e.CubeSize <= 0 {
		return 1
	}
	return e.CubeSize
}

// Frog tongue goal states — ShootTongue.State.
const (
	frogTongueMove  = iota // MOVE_TO_TARGET
	frogTongueCatch        // CATCH_ANIMATION
	frogTongueEat          // EAT_ANIMATION
)

// frogTongueStep is FrogAi's TONGUE activity (ShootTongue): the
// FROG_ATTACKABLES sensor finds a FROG_FOOD entity — slime or
// magma_cube at getSize()==1 — inside follow_range; the frog walks
// at it with the WALK_TARGET speed modifier 2.0 while it carries
// DATA_TONGUE_TARGET_ID (metadata 19, eid+1). Inside EATING_DISTANCE
// 1.75 the tongue plays, the pose flips to USING_TONGUE(9), and the
// target is yanked toward the frog at 0.75 — then CATCH (6 ticks),
// eatEntity's doHurtTarget kill, EAT (10 ticks), and stop() clears
// the target id + pose.
func (s *Server) frogTongueStep(e *Entity) bool {
	if e.FrogTongueEnt != nil {
		t := e.FrogTongueEnt
		// canStillUse: target present + state!=DONE (+ no panic —
		// mobPanicStep's continue already intercepts).
		if t.Dying || t.Dormant {
			s.frogTongueStop(e)
			return false
		}
		switch e.FrogTongueState {
		case frogTongueMove:
			dx, dy, dz := t.X-e.X, t.Y-e.Y, t.Z-e.Z
			dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
			if dist < 1.75 {
				if id, ok := SoundEventID("entity.frog.tongue"); ok {
					s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, srcNeutral, 2.0, 1.0, nil)
				}
				e.Pose = 9 // Pose.USING_TONGUE
				e.MetaDirty = true
				// target.setDeltaMovement: the unit vector toward
				// the frog scaled 0.75 — the tongue yank.
				if dist > 1e-6 {
					mx, my, mz := -dx/dist*0.75, -dy/dist*0.75, -dz/dist*0.75
					t.MotionX, t.MotionY, t.MotionZ = mx, my, mz
					s.broadcastEntityMotion(e.Dim, t.ID, mx, my, mz)
				}
				e.FrogTongueTimer = 0
				e.FrogTongueState = frogTongueCatch
				return true
			}
			mobTurnToward(e, dx, dz, 35)
			e.HeadYaw = e.Yaw
			speed := 1.0
			if mv, ok := entityAttrBases["frog"]["movement_speed"]; ok {
				speed = mv
			}
			s.mobWalkToSpeed(e, t.X, t.Z, 2.0*speed)
			return true
		case frogTongueCatch:
			e.FrogTongueTimer++
			if e.FrogTongueTimer >= 6 {
				e.FrogTongueState = frogTongueEat
				s.frogTongueEat(e, t)
			}
			return true
		case frogTongueEat:
			e.FrogTongueTimer++
			if e.FrogTongueTimer >= 10 {
				s.frogTongueStop(e)
				return false
			}
			return true
		}
		return false
	}
	// checkExtraStartConditions: FrogAttackablesSensor picks the
	// NEAREST VISIBLE FROG_FOOD entity at getSize()==1 inside
	// TARGET_DETECTION_DISTANCE 10.0 — a fixed range, not
	// follow_range, and line-of-sight gated like every
	// NearestVisibleLivingEntitySensor pick.
	var t *Entity
	best := 10.0 * 10.0
	frogBB := entityBBDims(e)
	feY := e.Y + frogBB[1]*0.85
	s.entities.mu.RLock()
	for _, o := range s.entities.byID {
		if o == e || o.Dim != e.Dim || o.Dying || o.Dormant {
			continue
		}
		on, _ := EntityTypeName(o.Type)
		if on != "slime" && on != "magma_cube" {
			continue
		}
		if cubeSizeOf(o) != 1 {
			continue
		}
		dx, dy, dz := o.X-e.X, o.Y-e.Y, o.Z-e.Z
		if d2 := dx*dx + dy*dy + dz*dz; d2 < best {
			// hasLineOfSight: a solid cell between the eyes
			// keeps the target out of the visible set.
			if d := s.dimensionByName(e.Dim); d != nil {
				oBB := entityBBDims(o)
				if _, _, _, _, _, _, _, hit := s.clipSolidSegment(d,
					e.X, feY, e.Z,
					o.X, o.Y+oBB[1]*0.85, o.Z); hit {
					continue
				}
			}
			best = d2
			t = o
		}
	}
	s.entities.mu.RUnlock()
	if t == nil {
		return false
	}
	// start(): lookAt the target, setTongueTarget, WALK_TARGET at
	// the target pos.
	e.FrogTongueEnt = t
	e.FrogTongueTarget = t.ID
	e.FrogTongueState = frogTongueMove
	e.FrogTongueTimer = 0
	e.MetaDirty = true
	return true
}

// frogTongueStop is ShootTongue.stop: erase the attack target and
// DATA_TONGUE_TARGET_ID, pose back to STANDING.
func (s *Server) frogTongueStop(e *Entity) {
	e.FrogTongueEnt = nil
	e.FrogTongueTarget = 0
	e.FrogTongueState = frogTongueMove
	e.FrogTongueTimer = 0
	if e.Pose != 0 {
		e.Pose = 0
	}
	e.MetaDirty = true
}

// frogTongueEat is eatEntity: the eat sound, then doHurtTarget —
// vanilla's mobAttack damage source off the frog's attack_damage.
func (s *Server) frogTongueEat(e *Entity, t *Entity) {
	if id, ok := SoundEventID("entity.frog.eat"); ok {
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, srcNeutral, 2.0, 1.0, nil)
	}
	dmg := float32(10.0)
	if dv, ok := entityAttrBases["frog"]["attack_damage"]; ok {
		dmg = float32(dv)
	}
	s.damageMob(t, dmg, "mob_attack", nil,
		&damageInfo{typeName: "mob_attack", cause: e.ID, direct: e.ID})
	// eatEntity removes the target outright when the hurt kills it;
	// mobDie already marks Dying and schedules the remove.
}
