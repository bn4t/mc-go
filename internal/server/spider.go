package server

import (
	"math"
	"math/rand"
)

// Spider / cave spider — measured from the 26.2 jar
// (monster/spider/Spider, Spider$SpiderAttackGoal,
// Spider$SpiderTargetGoal, CaveSpider, ai/goal/LeapAtTargetGoal,
// LivingEntity.handleRelativeFrictionAndCalculateMovement /
// handleOnClimbable; javap -c):
//
//   - registerGoals: FloatGoal(1), AvoidEntity(Armadillo)(2),
//     LeapAtTargetGoal(0.4)(3, JUMP|MOVE), SpiderAttackGoal(4) — a
//     MeleeAttackGoal(1.0, true) that drops the target with a 1/100
//     roll per goal tick once getLightLevelDependentMagicValue >= 0.5
//     — WaterAvoidingRandomStroll(0.8)(5), LookAtPlayer/RandomLook(6);
//     targets HurtByTarget(1), SpiderTargetGoal(Player)(2) and
//     (IronGolem)(3), both refusing to acquire at brightness >= 0.5.
//   - LeapAtTargetGoal.canUse: no controlling passenger, a target at
//     distSqr in [4, 16], onGround, nextInt(reducedTickDelay(5)=3)==0;
//     start: delta = (normalize(dx,0,dz)·0.4 + delta·0.2).xz with y =
//     0.4; canContinueToUse = !onGround (the MOVE flag holds the melee
//     goal off for the whole arc).
//   - Spider.tick (server, every tick, NoAI included): setClimbing(
//     horizontalCollision) — DATA_FLAGS_ID (16) bit 0. onClimbable()
//     reads that bit, so the next travel tick clamps the delta to
//     ±0.15 horizontally and >= -0.15 vertically before the move, and
//     a move that collides horizontally rewrites the post-move y to
//     0.2 before gravity and drag.
//   - CaveSpider.doHurtTarget: POISON 7s NORMAL / 15s HARD (mobPostAttack).

func init() {
	registerHostile([]string{"spider", "cave_spider"}, &hostileFamily{
		goal:    func(s *Server, e *Entity, name string) bool { return s.spiderStep(e, name) },
		flagIdx: 16,
	})
}

// spiderStep runs the spider goal selector with the climb physics
// wrapped around whichever goal travels this tick.
func (s *Server) spiderStep(e *Entity, name string) bool {
	h := e.hostile()
	w := s.dimWorld(e.Dim)
	if w == nil {
		return false
	}
	climbing := h.flags&0x01 != 0
	full := e.TickCount%2 == 0
	if h.leaping && full && e.Grounded {
		h.leaping = false
	}
	if !h.leaping && full && e.Grounded && len(e.Passengers) == 0 {
		if t := s.nearestMobTarget(e, name); t != nil {
			px, py, pz, _, _, _ := t.Pos()
			dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
			if d2 := dx*dx + dy*dy + dz*dz; d2 >= 4 && d2 <= 16 && rand.Intn(3) == 0 {
				vx, vz := dx, dz
				if l2 := dx*dx + dz*dz; l2 > 1e-7 {
					l := math.Sqrt(l2)
					vx = dx/l*0.4 + e.MotionX*0.2
					vz = dz/l*0.4 + e.MotionZ*0.2
				}
				e.MotionX, e.MotionY, e.MotionZ = vx, 0.4, vz
				e.NeedsSync = true
				h.leaping = true
			}
		}
	}
	if climbing {
		// handleOnClimbable — the pre-move clamp.
		e.MotionX = math.Max(-0.15, math.Min(0.15, e.MotionX))
		e.MotionZ = math.Max(-0.15, math.Min(0.15, e.MotionZ))
		e.MotionY = math.Max(-0.15, e.MotionY)
		e.FallDist = 0
	}
	e.HColl = false
	if h.leaping {
		s.mobTravelStep(e, w, 0)
	} else if !s.mobMeleeStep(e) {
		s.mobMoveStep(e, w)
		s.mobLookStep(e)
	}
	if climbing && e.HColl {
		// The post-move climb: y = 0.2, then gravity and drag.
		e.MotionY = (0.2 - 0.08) * 0.98
		e.NeedsSync = true
	}
	e.setHostileFlag(0x01, e.HColl)
	return true
}
