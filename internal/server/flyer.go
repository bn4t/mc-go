package server

// Shared goal plumbing for the airborne hostile trio — Blaze, Ghast and
// Phantom (blaze.go, ghast.go, phantom.go). Each type keeps its goal
// state in one flyerState hung off the Entity, so the shared tick and
// metadata hooks stay a single line each.
//
// Measured on the 26.2 jar (javap of Mob.serverAiStep, GoalSelector,
// TargetGoal, NearestAttackableTargetGoal, TargetingConditions):
//
//   - The goal and target selectors re-evaluate canUse /
//     canContinueToUse only on even ticks (tickCount % 2 == 0); the odd
//     tick only ticks running goals whose requiresUpdateEveryTick is
//     true. A goal without it ticks every other tick, and its
//     adjustedTickDelay/reducedTickDelay halve the authored delays so
//     the wall-clock cadence matches the authored tick figure.
//   - NearestAttackableTargetGoal(Player) picks the nearest player by
//     distance from (x, eyeY, z) among those TargetingConditions.test
//     accepts: survival/adventure, alive, inside the follow range
//     (distance from the mob's feet), and — with mustSee — in line of
//     sight. randomInterval 10 rolls nextInt(reducedTickDelay(10)=5)
//     per evaluation before the scan runs.
//   - TargetGoal.canContinueToUse drops the target once it leaves the
//     follow range, turns creative/spectator, dies, or (mustSee) stays
//     unseen for more than reducedTickDelay(60) evaluations.

import (
	"math"
	"math/rand"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// flyerMobs is the dispatch set: types whose whole AI runs through
// flyerStep instead of the generic melee/wander chain.
var flyerMobs = map[string]bool{
	"blaze": true,
}

// flyerState is the per-mob goal state for the flyer family. Only the
// fields of the owning type are used.
type flyerState struct {
	inited bool
	// target is the player the target selector holds (zero = none);
	// unseen counts canContinueToUse evaluations without sight.
	target [16]byte
	unseen int32

	// Blaze: allowedHeightOffset / nextHeightOffsetChangeTick
	// (customServerAiStep) and BlazeAttackGoal's attackStep /
	// attackTime / lastSeen; charged is DATA_FLAGS_ID bit 0.
	heightOffset   float64
	nextHeightTick int32
	attackOn       bool
	attackStep     int32
	attackTime     int32
	lastSeen       int32
	charged        bool
	chargedSent    bool

	// Ghast: GhastShootFireballGoal.chargeTime, DATA_IS_CHARGING, and
	// GhastMoveControl's floatDuration + wanted position.
	shootOn      bool
	chargeTime   int32
	charging     bool
	chargingSent bool
	floatDur     int32
	moveTo       bool
	wantX        float64
	wantY        float64
	wantZ        float64

	// Phantom: ID_SIZE, anchorPoint, moveTargetPoint, attackPhase and
	// the four goals' counters plus PhantomMoveControl.speed.
	size, sizeSent int32
	sizeTouched    bool
	anchor         world.Pos
	hasAnchor      bool
	moveTX         float64
	moveTY         float64
	moveTZ         float64
	swoop          bool
	targetGoalOn   bool
	nextScan       int32
	strategyOn     bool
	nextSweep      int32
	sweepOn        bool
	catSearchTick  int32
	circleOn       bool
	angle          float32
	circleDist     float32
	circleHeight   float32
	clockwise      float32
	moveSpeed      float32
}

// flyerOf returns the entity's flyer state, seeding the constructor
// defaults on first use: Blaze's allowedHeightOffset 0.5,
// PhantomMoveControl's speed 0.1 and PhantomAttackPlayerTargetGoal's
// nextScanTick reducedTickDelay(20).
func flyerOf(e *Entity) *flyerState {
	if e.flyer == nil {
		e.flyer = &flyerState{}
	}
	st := e.flyer
	if !st.inited {
		st.inited = true
		st.heightOffset = 0.5
		st.moveSpeed = 0.1
		st.nextScan = 10
	}
	return st
}

// flyerStep runs one AI tick for a flyer-family mob — called from
// mobTickStep inside the isEffectiveAi gate. True means the step owned
// the movement this tick (the generic wander is skipped).
func (s *Server) flyerStep(e *Entity, name string) bool {
	switch name {
	case "blaze":
		return s.blazeStep(e)
	case "ghast":
		return s.ghastStep(e)
	case "phantom":
		return s.phantomStep(e)
	}
	return false
}

// flyerEvenTick is GoalSelector's evaluation cadence — Mob.serverAiStep
// runs the full selector tick only when tickCount is even.
func flyerEvenTick(e *Entity) bool {
	return e.TickCount%2 == 0
}

// triangle is RandomSource.triangle(center, spread):
// center + spread*(nextDouble - nextDouble).
func triangle(center, spread float64) float64 {
	return center + spread*(rand.Float64()-rand.Float64())
}

// flyerPlayerOK is TargetingConditions' player half — alive, in play,
// in the mob's dimension, and in survival or adventure.
func (s *Server) flyerPlayerOK(e *Entity, sess *Session) bool {
	if sess == nil || sess.dead || !sess.spawned.Load() || sess.state.Load() != StatePlay {
		return false
	}
	if m := sess.mode(); m != GameModeSurvival && m != GameModeAdventure {
		return false
	}
	return sess.dimName() == e.Dim
}

// flyerTargetSess resolves the held target, or nil.
func (s *Server) flyerTargetSess(st *flyerState) *Session {
	if st.target == ([16]byte{}) {
		return nil
	}
	return s.sessionByUUID(st.target)
}

// flyerSelectTarget runs a NearestAttackableTargetGoal(Player, 10,
// mustSee) + TargetGoal pass for the mob: keep a valid target, drop an
// invalid one, and on an empty slot roll the random interval and pick
// the nearest acceptable player. eyeH is the mob's eye height; pred
// is the goal's extra selector (nil = none). HurtByTargetGoal's
// retaliation rides along when hurtBy is set: a player that hurt the
// mob inside the last-hurt memory is taken first.
func (s *Server) flyerSelectTarget(e *Entity, st *flyerState, follow, eyeH float64, mustSee, hurtBy bool, pred func(px, py, pz float64) bool) *Session {
	cur := s.flyerTargetSess(st)
	if !flyerEvenTick(e) {
		if cur != nil && !s.flyerPlayerOK(e, cur) {
			st.target = [16]byte{}
			return nil
		}
		return cur
	}
	if cur != nil {
		ok := s.flyerPlayerOK(e, cur)
		if ok {
			px, py, pz, _, _, _ := cur.Pos()
			dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
			if dx*dx+dy*dy+dz*dz > follow*follow {
				ok = false
			}
		}
		if ok && mustSee {
			px, py, pz, _, _, _ := cur.Pos()
			if s.flyerLOS(e.Dim, e.X, e.Y+eyeH, e.Z, px, py+1.62, pz) {
				st.unseen = 0
			} else {
				st.unseen++
				if st.unseen > 30 {
					ok = false
				}
			}
		}
		if ok {
			return cur
		}
		st.target = [16]byte{}
		st.unseen = 0
	}
	// HurtByTargetGoal (priority 1) — the player behind the last hurt.
	if hurtBy && e.LastHurtByPlayerTicks > 0 {
		if att := s.sessionByUUID(e.LastHurtByPlayerUUID); s.flyerPlayerOK(e, att) {
			st.target = att.uuid
			st.unseen = 0
			return att
		}
	}
	// randomInterval: nextInt(reducedTickDelay(10)) != 0 skips the scan.
	if rand.Intn(5) != 0 {
		return nil
	}
	var best *Session
	bestD := math.MaxFloat64
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if !s.flyerPlayerOK(e, sess) {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
		if dx*dx+dy*dy+dz*dz > follow*follow {
			return true
		}
		if pred != nil && !pred(px, py, pz) {
			return true
		}
		if mustSee && !s.flyerLOS(e.Dim, e.X, e.Y+eyeH, e.Z, px, py+1.62, pz) {
			return true
		}
		ey := py - (e.Y + eyeH)
		if d := dx*dx + ey*ey + dz*dz; d < bestD {
			bestD = d
			best = sess
		}
		return true
	})
	if best != nil {
		st.target = best.uuid
		st.unseen = 0
	}
	return best
}

// flyerLOS approximates Sensing.hasLineOfSight's COLLIDER clip: the
// eye-to-eye segment is marched in 0.05-block steps and a sample that
// falls inside a cell's collision shape (collisionBoxesOf — a bottom
// slab only fills its lower half) blocks it. Fluids and shapeless
// blocks never do.
func (s *Server) flyerLOS(dim string, x0, y0, z0, x1, y1, z1 float64) bool {
	w := s.dimWorld(dim)
	if w == nil {
		return true
	}
	dx, dy, dz := x1-x0, y1-y0, z1-z0
	d := math.Sqrt(dx*dx + dy*dy + dz*dz)
	n := int(d/0.05) + 1
	for i := 1; i < n; i++ {
		t := float64(i) / float64(n)
		x, y, z := x0+dx*t, y0+dy*t, z0+dz*t
		p := world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}
		b := w.GetBlock(p)
		if b == world.Air || world.NonSolid(b) || moveNoCollision(b) {
			continue
		}
		boxes, has := collisionBoxesOf(b)
		if !has {
			return false
		}
		lx, ly, lz := x-float64(p.X), y-float64(p.Y), z-float64(p.Z)
		for _, bx := range boxes {
			if lx >= bx[0] && lx <= bx[3] && ly >= bx[1] && ly <= bx[4] && lz >= bx[2] && lz <= bx[5] {
				return false
			}
		}
	}
	return true
}

// flyerTravel is LivingEntity.travelFlying with a zero input vector —
// the Ghast/Phantom travel: move by the delta, then scale it by 0.8 in
// water, 0.5 in lava, 0.91 in air. No gravity term exists.
func (s *Server) flyerTravel(e *Entity, w *world.World, half, height float64) {
	if w == nil {
		return
	}
	e.NeedsSync = true
	wDepth, inWater, lDepth, inLava := entityFluidTick(w, s.dims[e.Dim], e, half, height)
	inWater = inWater && wDepth > 0
	inLava = inLava && lDepth > 0
	s.entityMove(e, w, half, height, e.MotionX, e.MotionY, e.MotionZ, 0, 1.0, true)
	f := 0.91
	if inWater {
		f = 0.8
	} else if inLava {
		f = 0.5
	}
	e.MotionX *= f
	e.MotionY *= f
	e.MotionZ *= f
	e.FallDist = 0
}

// approachDeg is Mth.approachDegrees: step cur toward target by at most
// max degrees along the shortest arc.
func approachDeg(cur, target, max float32) float32 {
	d := wrapDeg(target - cur)
	if d > max {
		d = max
	} else if d < -max {
		d = -max
	}
	return cur + d
}

// wrapDeg is Mth.wrapDegrees for floats.
func wrapDeg(v float32) float32 {
	f := float32(math.Mod(float64(v), 360))
	if f >= 180 {
		f -= 360
	}
	if f < -180 {
		f += 360
	}
	return f
}

// flyerMetaPack writes the flyer type's own synched entry for the
// pairing pack — only when it sits off its define default (0/false).
func flyerMetaPack(b *protocol.Buffer, e *Entity, name string) {
	st := e.flyer
	if st == nil {
		return
	}
	switch name {
	case "blaze":
		if st.charged {
			b.Byte(16)
			b.Byte(0) // byte serializer
			b.Byte(1)
		}
	case "ghast":
		if st.charging {
			b.Byte(16)
			b.Byte(8) // boolean
			b.Bool(true)
		}
	case "phantom":
		if st.size != 0 {
			b.Byte(16)
			b.Byte(1) // int (varint)
			b.VarInt(st.size)
		}
	}
}

// flyerMetaDirty writes the entry when it changed since the last emit
// — SynchedEntityData's packDirty for index 16. The sent baseline is
// the define default, so a non-default value replays once after the
// pairing pack the way every other dirty entry does.
func flyerMetaDirty(b *protocol.Buffer, e *Entity, name string) {
	st := e.flyer
	if st == nil {
		return
	}
	switch name {
	case "blaze":
		if st.charged != st.chargedSent {
			st.chargedSent = st.charged
			b.Byte(16)
			b.Byte(0)
			if st.charged {
				b.Byte(1)
			} else {
				b.Byte(0)
			}
		}
	case "ghast":
		if st.charging != st.chargingSent {
			st.chargingSent = st.charging
			b.Byte(16)
			b.Byte(8)
			b.Bool(st.charging)
		}
	case "phantom":
		if st.size != st.sizeSent {
			st.sizeSent = st.size
			b.Byte(16)
			b.Byte(1)
			b.VarInt(st.size)
		}
	}
}

// fireballHitPlayers is the player half of Fireball.onHitEntity for
// both fireball types — SmallFireball ignites the victim for five
// seconds (igniteForSeconds(5), restored when hurtServer refuses) and
// hurts 5.0, LargeFireball hurts 6.0. The source is
// DamageSources.fireball(ball, owner): minecraft:fireball credited to
// the owner, unattributed_fireball (death.attack.onFire) without one.
// The owner is skipped until the ball has left it.
func (s *Server) fireballHitPlayers(e *Entity, ox, oy, oz float64, dmg float32, ignite bool) bool {
	hit := false
	var owner *Entity
	if e.arrowShooter != 0 {
		owner, _ = s.entities.GetPtr(e.arrowShooter)
	}
	ownerSess := s.sessionByEntityID(e.arrowShooter)
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dead || !sess.spawned.Load() {
			return true
		}
		if m := sess.mode(); m != GameModeSurvival && m != GameModeAdventure {
			return true
		}
		if sess.dimName() != e.Dim {
			return true
		}
		if e.arrowShooter != 0 && sess.entityID.Load() == e.arrowShooter && !e.LeftOwner {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if !segmentHitsBox(ox, oy, oz, e.X, e.Y, e.Z, px, py, pz, 0.6, 1.8, projectilePickMargin(e.TicksLived)) {
			return true
		}
		hit = true
		typeName, cause := "fireball", e.arrowShooter
		var msg component
		switch {
		case cause == 0:
			typeName, cause = "unattributed_fireball", e.ID
			msg = translatable("death.attack.onFire", literal(sess.getName()))
		case ownerSess != nil:
			msg = translatable("death.attack.fireball", literal(sess.getName()), literal(ownerSess.getName()))
		default:
			kn := ""
			if owner != nil {
				kn, _ = EntityTypeName(owner.Type)
			}
			msg = translatable("death.attack.fireball", literal(sess.getName()), literal(mobDisplayName(kn)))
		}
		prevFire := sess.fireTicks
		if ignite && sess.fireTicks < 100 {
			sess.fireTicks = 100
		}
		dealt := s.damageWith(sess, dmg, msg, damageSource{typeName: typeName, cause: cause, direct: e.ID})
		if ignite && dealt <= 0 {
			sess.fireTicks = prevFire
		}
		if ignite && (prevFire > 0) != (sess.fireTicks > 0) {
			// The shared-flags byte picks up the on-fire bit.
			s.broadcastSharedFlags(sess)
		}
		return false
	})
	return hit
}
