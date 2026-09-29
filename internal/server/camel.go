package server

import (
	"math/rand/v2"

	"mc-go/internal/world"
)

// Camel's sitting state machine — Brain-driven pose toggles measured
// off the 26.2 decompile (Camel.java + CamelAi.RandomSitting):
//
//	isCamelSitting()      = DATA_LAST_POSE_CHANGE_TICK < 0        (the
//	                        negative marker IS the sitting state,
//	                        not DATA_POSE)
//	getPoseTime()         = gameTime - |lastPoseChangeTick|
//	isInPoseTransition()  = poseTime < (sitting ? 40 : 52)        (the
//	                        sit-down / stand-up animation windows)
//	refuseToMove()        = isCamelSitting || isInPoseTransition  (gates
//	                        stroll, look-follow, ridden input, canJump)
//
//	RandomSitting (Brain IDLE priority 4, one RunOne of {stroll w1,
//	lookwalk w1, RandomSitting w1, DoNothing(30,60) w1}) toggles the
//	pose when checkExtraStartConditions passes: !isInWater,
//	poseTime >= minimalPoseTicks (20 s = 400 t), !isLeashed,
//	onGround, !hasControllingPassenger, canCamelChangePose (the
//	target pose's bounding box must fit — adult standing
//	1.7×2.375, sitting 1.7×0.945; baby 0.95×1.4 / 0.95×0.425).
//	While the camel already sits, the gated-out walk entries drop
//	the pool to {RandomSitting, DoNothing} — the stand pick is
//	effectively 1/2 per repick.
//
//	Standing is also forced by: aiStep's water check (sitting &&
//	isInWater → standUpInstantly), actuallyHurt (every landed hurt
//	→ standUpInstantly), onElasticLeashPull (sitting && !transition
//	&& canCamelChangePose → standUp), and tickRidden (controlling
//	rider's forward input on a settled sitter → standUp).
//	standUpInstantly stamps max(0, gameTime-53) so the stand
//	animation reports already finished; sit/stand play the
//	CAMEL_SIT/CAMEL_STAND voice, standUpInstantly is silent. All
//	three post GameEvent.ENTITY_ACTION.
const (
	camelSitAnim       = 40 // sit-down transition, ticks
	camelStandAnim     = 52 // stand-up transition, ticks
	camelMinPoseTicks  = 400
	camelRepickStand   = 60 // standing repick cadence (RunOne slot)
	camelRepickSitting = 45 // sitting repick cadence (DoNothing-paced)
)

// camelPoseTime is Camel.getPoseTime — the age of the current pose.
func camelPoseTime(e *Entity, gameTime int64) int64 {
	t := e.LastPoseTick
	if t < 0 {
		t = -t
	}
	return gameTime - t
}

// camelInTransition is Camel.isInPoseTransition — the sit-down
// animation runs 40 ticks, the stand-up 52.
func camelInTransition(e *Entity, gameTime int64) bool {
	dur := int64(camelStandAnim)
	if e.LastPoseTick < 0 {
		dur = camelSitAnim
	}
	return camelPoseTime(e, gameTime) < dur
}

// camelRefuseToMove is Camel.refuseToMove — sitting or mid-transition
// blocks the stroll drive, look-follow, ridden input and canJump.
func camelRefuseToMove(e *Entity, gameTime int64) bool {
	return e.LastPoseTick < 0 || camelInTransition(e, gameTime)
}

// camelSitRoll is the RandomSitting repick draw, keyed like the other
// deterministic goal rolls: sides=4 while standing (one pick of four
// RunOne children), sides=2 while sitting (the pool collapses to
// sit/stand + doNothing).
func camelSitRoll(eid int32, ticks, sides int64) bool {
	h := uint64(uint32(eid))*40503 ^ uint64(ticks)*1099511628211 ^ uint64(0x9E3779B1)
	return h%uint64(sides) == 0
}

// camelVoicePitch is LivingEntity.getVoicePitch: adults roll
// (rand-rand)*0.2+1.0, babies (rand-rand)*0.2+1.5.
func camelVoicePitch(e *Entity) float32 {
	base := 1.0
	if e.Age < 0 {
		base = 1.5
	}
	return float32(base + (rand.Float64()-rand.Float64())*0.2)
}

// camelSit is Camel.sitDown: CAMEL_SIT + Pose.SITTING(10) +
// ENTITY_ACTION + LAST_POSE_CHANGE_TICK=-gameTime. camel_husk runs
// the same arm with its own sounds.
func (s *Server) camelSit(e *Entity) {
	if e.LastPoseTick < 0 {
		return
	}
	name, _ := EntityTypeName(e.Type)
	snd := sndCamelSit
	if name == "camel_husk" {
		snd = sndCamelHuskSit
	}
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, snd, srcNeutral, 1.0, camelVoicePitch(e), nil)
	e.Pose = 10 // Pose.SITTING
	e.LastPoseTick = -s.gameTime()
	e.MetaDirty = true
	if d := s.dims[e.Dim]; d != nil {
		s.emitGameEvent(d, "entity_action", e.X, e.Y, e.Z, nil, "")
	}
}

// camelStand is Camel.standUp (instant=false) / standUpInstantly
// (instant=true): the relaxed stand is sit-gated, plays CAMEL_STAND
// and stamps +gameTime; the instant arm — hurt, water, leash pull,
// panic start — is unconditional and silent, restamping
// max(0, gameTime-53) even on an already-standing camel (which is
// why a hurt sitting camel emits two positive ticks: the hurt arm
// then the panic arm).
func (s *Server) camelStand(e *Entity, instant bool) {
	if !instant && e.LastPoseTick >= 0 {
		return
	}
	if !instant {
		name, _ := EntityTypeName(e.Type)
		snd := sndCamelStand
		if name == "camel_husk" {
			snd = sndCamelHuskStand
		}
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, snd, srcNeutral, 1.0, camelVoicePitch(e), nil)
	}
	if e.Pose != 0 {
		e.Pose = 0 // Pose.STANDING
		e.MetaDirty = true
	}
	gt := s.gameTime()
	if instant {
		gt -= camelStandAnim + 1
		if gt < 0 {
			gt = 0
		}
	}
	if e.LastPoseTick != gt {
		e.LastPoseTick = gt
		e.MetaDirty = true
	}
	if d := s.dims[e.Dim]; d != nil {
		s.emitGameEvent(d, "entity_action", e.X, e.Y, e.Z, nil, "")
	}
}

// camelCanChangePose is Camel.canCamelChangePose's suffocation probe:
// the TARGET pose's bounding box must fit at the current position.
// Adults are 1.7 wide × 2.375/0.945 high (standing/sitting), babies
// 0.95 × 1.4/0.425.
func (s *Server) camelCanChangePose(e *Entity, w *world.World, toSitting bool) bool {
	half, h := 0.85, 2.375
	if e.Age < 0 {
		half, h = 0.475, 1.4
	}
	if toSitting {
		h = 0.945
		if e.Age < 0 {
			h = 0.425
		}
	}
	return s.entityBoxFree(w, e.X, e.Y, e.Z, half, h)
}

// camelControlled is Camel.hasControllingPassenger for the sit
// condition: a Player first-passenger drives the mount.
func (s *Server) camelControlled(e *Entity) bool {
	return len(e.Passengers) > 0 && s.sessionByEntityID(e.Passengers[0]) != nil
}

// camelPoseStep runs the Camel tick()/Brain arms inside the travel
// step and reports refuseToMove so the caller freezes the drive:
//
//   - tickRidden: a controlling rider pressing forward stands a
//     settled sitter (with the stand voice).
//   - RandomSitting repick slots: standing draws 1/4, sitting 1/2,
//     both gated on the measured checkExtraStartConditions.
//
// The water-stand and hurt arms live elsewhere: water is tick()-level
// (mobTickStep covers NoAI too) and hurt runs inside damageMob's
// actuallyHurt hook.
func (s *Server) camelPoseStep(e *Entity, w *world.World, inWater bool) bool {
	gt := s.gameTime()
	sitting := e.LastPoseTick < 0
	transition := camelInTransition(e, gt)
	// Camel.tickRidden: controller.zza > 0 on a settled sitter runs
	// standUp — the vanilla check needs isCamelSitting (the negative
	// tick) and a finished transition.
	if sitting && !transition && len(e.Passengers) > 0 {
		if rs := s.sessionByEntityID(e.Passengers[0]); rs != nil && rs.moveInputZ > 0 {
			s.camelStand(e, false)
			return true
		}
	}
	// RandomSitting only repicks while no walk target is latched —
	// vanilla's RunOne is gated on WALK_TARGET VALUE_ABSENT, so a
	// mid-stroll camel never draws the sit pick.
	if !e.Strolling {
		poseTime := camelPoseTime(e, gt)
		repick := int64(camelRepickStand)
		draw := int64(4)
		if sitting {
			repick = camelRepickSitting
			draw = 2
		}
		if e.WanderTicks%repick == 0 && poseTime >= camelMinPoseTicks && !transition {
			// checkExtraStartConditions: !isInWater && !isLeashed &&
			// onGround && !hasControllingPassenger && pose-age &&
			// canCamelChangePose.
			if e.Grounded && e.LeashHolder == 0 && !inWater &&
				(sitting || !s.camelControlled(e)) &&
				s.camelCanChangePose(e, w, !sitting) &&
				camelSitRoll(e.ID, e.WanderTicks, draw) {
				if sitting {
					s.camelStand(e, false)
				} else {
					s.camelSit(e)
				}
			}
		}
	}
	return camelRefuseToMove(e, gt)
}
