package server

import (
	"math"
	"slices"

	"mc-go/internal/world"
)

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
		// Behavior's TIME_OUT_DURATION: the whole shoot-tongue run —
		// move chase plus both animations — is capped at 100 ticks;
		// a timed-out chase stops and the sensor is free to repick
		// the same visible target on a later pick.
		e.FrogTongueAge++
		if e.FrogTongueAge >= 100 {
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
		// isUnreachableAttackTarget: entities on the FIFO are
		// excluded while the 100-tick memory is live.
		if e.SyncCount < e.FrogUnreachableExp &&
			slices.Contains(e.FrogUnreachable, o.ID) {
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
	// checkExtraStartConditions' canPathfindToTarget: a picked
	// target whose path can't end inside the eating distance is
	// dropped AND written to UNREACHABLE_TONGUE_TARGETS — the
	// sensor then skips it while the memory is live.
	if !s.frogCanTonguePath(e, t) {
		s.frogTongueUnreachable(e, t.ID)
		return false
	}
	// start(): lookAt the target, setTongueTarget, WALK_TARGET at
	// the target pos.
	e.FrogTongueEnt = t
	e.FrogTongueTarget = t.ID
	e.FrogTongueState = frogTongueMove
	e.FrogTongueTimer = 0
	e.FrogTongueAge = 0
	e.MetaDirty = true
	return true
}

// frogTongueUnreachable is ShootTongue's addUnreachableTargetTo-
// Memory: the id appends when new, the head drops at five entries,
// and the expiry rewrites to 100 ticks on every call — even a
// re-listed id refreshes the memory's TTL.
func (s *Server) frogTongueUnreachable(e *Entity, id int32) {
	e.FrogUnreachableExp = e.SyncCount + 100
	if slices.Contains(e.FrogUnreachable, id) {
		return
	}
	if len(e.FrogUnreachable) == 5 {
		e.FrogUnreachable = e.FrogUnreachable[1:]
	}
	e.FrogUnreachable = append(e.FrogUnreachable, id)
}

// frogCanTonguePath emulates canPathfindToTarget —
// createPath(target).getDistToTarget() < 1.75. With no nav graph the
// check simulates the same greedy ground walk the chase drive would
// run: step toward the target's x/z, take the one-block hop or short
// drop the walk drive takes, and fail on a wall or a ledge it can't
// take. Reaching the 1.75 eating distance means a path could end
// close enough; a target straight overhead (no horizontal approach)
// or behind solid ground has no such path.
func (s *Server) frogCanTonguePath(e *Entity, t *Entity) bool {
	w := s.dimWorld(e.Dim)
	if w == nil {
		return true
	}
	blocked := func(cx, cy, cz int) bool {
		b := w.GetBlock(world.Pos{X: cx, Y: cy, Z: cz})
		return b != world.Air && !moveNoCollision(b)
	}
	x, y, z := e.X, e.Y, e.Z
	for i := 0; i < 40; i++ {
		dx, dy, dz := t.X-x, t.Y-y, t.Z-z
		if dx*dx+dy*dy+dz*dz < 1.75*1.75 {
			return true
		}
		hd := math.Hypot(dx, dz)
		if hd < 0.05 {
			// No horizontal approach left: overhead the path
			// ends on the ground under the target — already
			// over 1.75 out. Below, the walk just slides off
			// the ledge and lands on it.
			return dy < 0
		}
		nx, nz := x+dx/hd*0.4, z+dz/hd*0.4
		cx, cz := int(math.Floor(nx)), int(math.Floor(nz))
		cy := int(math.Floor(y))
		if blocked(cx, cy, cz) {
			// A one-block ledge is what the walk drive's
			// auto-step climbs; two or more ends the path here.
			if blocked(cx, cy+1, cz) {
				return false
			}
			x, y, z = nx, float64(cy+1), nz
			continue
		}
		// Free cell — descend to the ground under it (the walk
		// falls off ledges); no floor within a block of the
		// descent cap leaves the path hanging over air.
		gy := cy
		for gy > cy-6 && !blocked(cx, gy-1, cz) {
			gy--
		}
		if cy-gy >= 6 {
			return false
		}
		x, y, z = nx, float64(gy), nz
	}
	return false
}

// frogTongueStop is ShootTongue.stop: erase the attack target and
// DATA_TONGUE_TARGET_ID, pose back to STANDING.
func (s *Server) frogTongueStop(e *Entity) {
	e.FrogTongueEnt = nil
	e.FrogTongueTarget = 0
	e.FrogTongueState = frogTongueMove
	e.FrogTongueTimer = 0
	e.FrogTongueAge = 0
	if e.Pose != 0 {
		e.Pose = 0
	}
	e.MetaDirty = true
}

// frogCroakStep ticks Croak's 60-tick pose activity: the pose flips
// CROAKING(8) at pick time and STANDING back when the counter lapses.
// The RunOne repicks as soon as a behavior ends, so the stroll repick
// is armed for the next tick rather than the usual ~6s idle gap.
func (s *Server) frogCroakStep(e *Entity) {
	e.FrogCroakTimer++
	if e.FrogCroakTimer >= 60 {
		e.FrogCroakTimer = 0
		e.Pose = 0
		e.MetaDirty = true
		e.WanderTicks = strollIntervalFor(e.ID) - 1
	}
}

// frogCroakRoll is the IDLE RunOne's weighted draw: Croak weight 3
// against RandomStroll 1 + SetWalkTargetFromLookTarget 1 + the
// triggerIf(onGround) no-op 2 — the no-op finishes the same tick and
// repicks, so the duration-bearing draw is 3/5 croak.
func frogCroakRoll(eid int32, ticks int64) bool {
	h := uint64(uint32(eid))*40503 ^ uint64(ticks)*1099511628211
	return h%5 < 3
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
