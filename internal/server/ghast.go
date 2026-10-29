package server

// Ghast AI, measured off the 26.2 jar (javap of Ghast and its
// GhastMoveControl, RandomFloatAroundGoal, GhastLookGoal and
// GhastShootFireballGoal inner classes, plus LargeFireball/Fireball):
//
//   - registerGoals: RandomFloatAroundGoal(5, MOVE), GhastLookGoal(7,
//     LOOK), GhastShootFireballGoal(7); target NearestAttackableTarget
//     (Player, interval 10, mustSee) whose selector keeps only players
//     within 4 blocks vertically (|target.y − ghast.y| ≤ 4).
//     follow_range 100, flying_speed 0.06, explosionPower 1 (NBT
//     ExplosionPower, a byte).
//   - travel is travelFlying(input, 0.02): no gravity — move, then the
//     delta scales by 0.8 in water, 0.5 in lava, 0.91 in air.
//   - RandomFloatAroundGoal.canUse: no wanted position yet, or the
//     wanted spot is closer than 1 or farther than 60 blocks; it never
//     continues (one-shot start). start picks getSuitableFlyToPosition:
//     with distanceToBlocks 0 isGoodTarget accepts the first
//     ±16-per-axis random point (nextFloat·2−1)·16 outright (no home
//     restriction on a ghast); the MOTION_BLOCKING mirror only shapes
//     the 64-miss fallback, which that makes unreachable. Measured on
//     the wire: a ghast summoned on the flat floor climbs ~30 blocks in
//     ten seconds — the underground half of the picks fails canReach.
//   - GhastMoveControl.tick: while MOVE_TO, `floatDuration-- <= 0`
//     re-arms floatDuration += nextInt(5)+2 and, when canReach (the
//     box swept to the wanted spot meets no collision outside the
//     destination box), adds normalize(wanted − pos)·flying_speed·5/3
//     to the delta; an unreachable spot drops the operation to WAIT.
//   - GhastLookGoal: faceMovementDirection — no target: yRot =
//     −atan2(dx, dz) of the delta; a target inside 64 blocks: yRot
//     faces it. yBodyRot follows.
//   - GhastShootFireballGoal.tick (every tick, needs a target): within
//     64 blocks and in sight chargeTime++; at 10 level event 1015 (the
//     warning cry, skipped when silent); at 20 level event 1016 and a
//     LargeFireball(power = explosionPower) spawned 4 blocks along the
//     view vector at getY(0.5)+0.5, aimed from there at the target's
//     getY(0.5), then chargeTime = −40. Out of sight it decays toward
//     0. setCharging(chargeTime > 10) — DATA_IS_CHARGING (index 16,
//     boolean). stop() clears it.
//   - Ghast.hurtServer: a LargeFireball whose owner is a Player
//     (isReflectedFireball) deals 1000 instead of its 6.0.

import (
	"math"
	"math/rand"

	"mc-go/internal/world"
)

func init() {
	flyerMobs["ghast"] = true
}

const ghastEyeHeight = 2.6

func (s *Server) ghastStep(e *Entity) bool {
	st := flyerOf(e)
	w := s.dimWorld(e.Dim)
	if w == nil {
		return true
	}
	follow := s.mobAttr(e, "ghast", "follow_range", 100)
	t := s.flyerSelectTarget(e, st, follow, ghastEyeHeight, true, false, func(_, py, _ float64) bool {
		return math.Abs(py-e.Y) <= 4
	})
	// RandomFloatAroundGoal — evaluated with the selector.
	if flyerEvenTick(e) {
		dx, dy, dz := st.wantX-e.X, st.wantY-e.Y, st.wantZ-e.Z
		if d2 := dx*dx + dy*dy + dz*dz; !st.moveTo || d2 < 1 || d2 > 3600 {
			st.wantX, st.wantY, st.wantZ = s.ghastFlyToPosition(e)
			st.moveTo = true
		}
	}
	// GhastLookGoal.
	var tx, ty, tz float64
	if t != nil {
		tx, ty, tz, _, _, _ = t.Pos()
	}
	if t == nil {
		if e.MotionX != 0 || e.MotionZ != 0 {
			e.Yaw = float32(-math.Atan2(e.MotionX, e.MotionZ) * 180 / math.Pi)
		} else {
			e.Yaw = 0
		}
	} else if dx, dy, dz := tx-e.X, ty-e.Y, tz-e.Z; dx*dx+dy*dy+dz*dz < 4096 {
		e.Yaw = float32(-math.Atan2(dx, dz) * 180 / math.Pi)
	}
	e.HeadYaw = e.Yaw
	e.LookYaw = e.Yaw
	// GhastShootFireballGoal.
	if t != nil {
		if !st.shootOn {
			st.shootOn = true
			st.chargeTime = 0
		}
		s.ghastShootTick(e, st, tx, ty, tz)
	} else if st.shootOn {
		st.shootOn = false
		s.ghastSetCharging(e, st, false)
	}
	// GhastMoveControl.tick.
	if st.moveTo {
		old := st.floatDur
		st.floatDur--
		if old <= 0 {
			st.floatDur += int32(rand.Intn(5)) + 2
			vx, vy, vz := st.wantX-e.X, st.wantY-e.Y, st.wantZ-e.Z
			if s.ghastCanReach(e, w, vx, vy, vz) {
				l := math.Sqrt(vx*vx + vy*vy + vz*vz)
				if l >= 1e-4 {
					k := s.mobAttr(e, "ghast", "flying_speed", 0.06) * 5 / 3 / l
					e.MotionX += vx * k
					e.MotionY += vy * k
					e.MotionZ += vz * k
				}
			} else {
				st.moveTo = false
			}
		}
	}
	bb := entityBBDims(e)
	s.flyerTravel(e, w, bb[0]/2, bb[1])
	return true
}

// ghastSetCharging is Ghast.setCharging — DATA_IS_CHARGING.
func (s *Server) ghastSetCharging(e *Entity, st *flyerState, on bool) {
	if st.charging != on {
		st.charging = on
		e.MetaDirty = true
	}
}

// ghastShootTick is GhastShootFireballGoal.tick.
func (s *Server) ghastShootTick(e *Entity, st *flyerState, tx, ty, tz float64) {
	dx, dy, dz := tx-e.X, ty-e.Y, tz-e.Z
	if dx*dx+dy*dy+dz*dz < 4096 && s.flyerLOS(e.Dim, e.X, e.Y+ghastEyeHeight, e.Z, tx, ty+1.62, tz) {
		st.chargeTime++
		bp := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
		if st.chargeTime == 10 && !e.Silent {
			s.broadcastLevelEvent(e.Dim, 1015, bp, 0)
		}
		if st.chargeTime == 20 {
			// getViewVector(1.0): (sin(−yaw)·cos(pitch), −sin(pitch),
			// cos(−yaw)·cos(pitch)).
			p := float64(e.Pitch) * math.Pi / 180
			y := -float64(e.Yaw) * math.Pi / 180
			vx, vz := math.Sin(y)*math.Cos(p), math.Cos(y)*math.Cos(p)
			fx, fy, fz := e.X+vx*4, e.Y+2+0.5, e.Z+vz*4
			if !e.Silent {
				s.broadcastLevelEvent(e.Dim, 1016, bp, 0)
			}
			power := e.ExplosionPower
			if power == 0 {
				power = 1
			}
			s.spawnMobFireball(e, "fireball", fx, fy, fz, tx-fx, ty+0.9-fy, tz-fz, power)
			st.chargeTime = -40
		}
	} else if st.chargeTime > 0 {
		st.chargeTime--
	}
	s.ghastSetCharging(e, st, st.chargeTime > 10)
}

// ghastFlyToPosition is RandomFloatAroundGoal.getSuitableFlyToPosition
// with distanceToBlocks 0 and no home restriction: the first random
// point is accepted.
func (s *Server) ghastFlyToPosition(e *Entity) (x, y, z float64) {
	x = e.X + float64(rand.Float32()*2-1)*16
	y = e.Y + float64(rand.Float32()*2-1)*16
	z = e.Z + float64(rand.Float32()*2-1)*16
	return x, y, z
}

// ghastCanReach is GhastMoveControl.canReach (careful = false): the box
// swept along v must meet no collision shape, except cells that
// intersect the destination box. Sampled at half-block steps.
func (s *Server) ghastCanReach(e *Entity, w *world.World, vx, vy, vz float64) bool {
	bb := entityBBDims(e)
	half, h := bb[0]/2, bb[1]
	ex, ey, ez := e.X+vx, e.Y+vy, e.Z+vz
	l := math.Sqrt(vx*vx + vy*vy + vz*vz)
	n := int(math.Ceil(l / 0.5))
	inDest := func(cx, cy, cz int) bool {
		return float64(cx+1) > ex-half && float64(cx) < ex+half &&
			float64(cy+1) > ey && float64(cy) < ey+h &&
			float64(cz+1) > ez-half && float64(cz) < ez+half
	}
	for i := 1; i <= n; i++ {
		f := float64(i) / float64(n)
		x, y, z := e.X+vx*f, e.Y+vy*f, e.Z+vz*f
		for cx := int(math.Floor(x - half)); float64(cx) < x+half; cx++ {
			for cy := int(math.Floor(y)); float64(cy) < y+h; cy++ {
				for cz := int(math.Floor(z - half)); float64(cz) < z+half; cz++ {
					if inDest(cx, cy, cz) {
						continue
					}
					b := w.GetBlock(world.Pos{X: cx, Y: cy, Z: cz})
					if b == world.Air || world.NonSolid(b) || moveNoCollision(b) {
						continue
					}
					if float64(cy)+collisionTopOf(b) > y {
						return false
					}
				}
			}
		}
	}
	return true
}

// largeFireballDamage is the LargeFireball.onHitEntity hurt amount as
// the victim's hurtServer sees it: 6.0, except a ghast struck by a
// player-owned (reflected) ball takes 1000 (Ghast.isReflectedFireball).
func (s *Server) largeFireballDamage(e, victim *Entity) float32 {
	if vn, ok := EntityTypeName(victim.Type); ok && vn == "ghast" {
		if s.sessionByEntityID(e.arrowShooter) != nil || (e.HasProjOwner && s.sessionByUUID(e.ProjOwner) != nil) {
			return ghastReflectedDamage
		}
	}
	return 6.0
}

// ghastReflectedDamage is Ghast.hurtServer's isReflectedFireball arm:
// a LargeFireball owned by a Player hurts the ghast for 1000.
const ghastReflectedDamage = 1000

// ghastReflectedHit reports the reflected-fireball hit, which
// Ghast.isInvulnerableTo exempts from the fireImmune is_fire veto —
// the only source of a 1000 fireball hit on a ghast is
// largeFireballDamage's reflected arm.
func ghastReflectedHit(name, typeName string, amount float32) bool {
	return name == "ghast" && typeName == "fireball" && amount == ghastReflectedDamage
}

// flyerSummonNBT reads the flyer types' own summon keys: Ghast's
// ExplosionPower (getByteOr, default 1) and Phantom's size.
func (s *Server) flyerSummonNBT(e *Entity, name string, tag *snbtTag) {
	if tag == nil {
		return
	}
	switch name {
	case "ghast":
		if v, ok := tag.vals["ExplosionPower"]; ok {
			e.ExplosionPower = float32(int8(int64(snbtNum(v))))
		}
	case "phantom":
		s.phantomSummonNBT(e, tag)
	}
}
