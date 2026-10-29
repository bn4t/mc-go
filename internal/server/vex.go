package server

import (
	"math"
	"math/rand"

	"mc-go/internal/world"
)

// Vex — measured from the 26.2 jar (monster/Vex and its inner goals,
// javap -c):
//
//   - Vex.tick sets noPhysics around super.tick(), then
//     setNoGravity(true) every tick (DATA_NO_GRAVITY leaves its false
//     default on the first tick), then the limited-life countdown:
//     hasLimitedLife && --limitedLifeTicks <= 0 re-arms the counter to
//     20 and hurts the vex 1.0 on damageSources().starve(). None of it
//     sits behind isEffectiveAi — a NoAI vex still starves.
//   - registerGoals: FloatGoal(0), VexChargeAttackGoal(4, MOVE),
//     VexRandomMoveGoal(8, MOVE), LookAtPlayer(9/10); targets
//     HurtByTarget(Raider)(1), VexCopyOwnerTargetGoal(2) — the owner's
//     getTarget when canAttack — and NearestAttackableTarget(Player)(3).
//   - VexChargeAttackGoal.canUse: live target, !moveControl.hasWanted,
//     nextInt(reducedTickDelay(7)=4)==0, distSqr > 4. start: wanted =
//     target eye at speed 1.0, setIsCharging(true) (DATA_FLAGS_ID bit
//     1), VEX_CHARGE. tick (requiresUpdateEveryTick): box overlap →
//     doHurtTarget + setIsCharging(false); else distSqr < 9 re-aims the
//     wanted point at the eye. canContinueToUse: hasWanted && charging
//     && live target; stop clears the flag.
//   - VexRandomMoveGoal.canUse: !hasWanted && nextInt(4)==0; tick
//     tries three cells boundOrigin (or blockPosition) + (rand15-7,
//     rand11-5, rand15-7) and takes the first empty one at speed 0.25.
//   - VexMoveControl.tick (MOVE_TO only): d = wanted - pos; when
//     |d| < getBoundingBox().getSize() the op drops to WAIT and the
//     delta halves; otherwise delta += d·(speed·0.05/|d|), and the yaw
//     faces the delta (no target) or the target.
//   - finalizeSpawn: mainhand iron_sword (drop chance 0) — attack
//     damage 4 base + the sword's +5 modifier.
//
// Travel is travelInAir with noPhysics: the move ignores collision,
// then delta·(0.91, 0.98, 0.91).

// vexEyeHeight is EntityType VEX's eyeHeight(0.51875).
const vexEyeHeight = 0.51875

// vexBoxSize is AABB.getSize() of the 0.4×0.8 box — the mean edge.
const vexBoxSize = (0.4 + 0.8 + 0.4) / 3

func init() {
	registerHostileDims("vex", 0.4, 0.8)
	registerHostile([]string{"vex"}, &hostileFamily{
		goal:    func(s *Server, e *Entity, _ string) bool { return s.vexStep(e) },
		tickPre: func(s *Server, e *Entity) bool { return s.vexTickPre(e) },
		nbt:     vexSummonNBT,
		flagIdx: 16,
	})
}

// vexSummonNBT is Vex.readAdditionalSaveData: life_ticks present →
// setLimitedLife; bound_pos → boundOrigin.
func vexSummonNBT(_ *Server, e *Entity, tag *snbtTag) {
	h := e.hostile()
	if v, ok := tag.vals["life_ticks"]; ok {
		h.limited = true
		h.lifeTicks = int32(snbtNum(v))
	}
	if v, ok := tag.vals["bound_pos"]; ok && len(v.items) == 3 {
		h.boundSet = true
		h.bound = world.Pos{X: int(snbtNum(v.items[0])), Y: int(snbtNum(v.items[1])), Z: int(snbtNum(v.items[2]))}
	}
}

// vexTickPre is Vex.tick's tail — outside isEffectiveAi.
func (s *Server) vexTickPre(e *Entity) bool {
	h := e.hostile()
	if !e.NoGravity {
		e.setNoGravity(true)
		e.MetaDirty = true
	}
	if h.limited {
		h.lifeTicks--
		if h.lifeTicks <= 0 {
			h.lifeTicks = 20
			_, _, died := s.damageMob(e, 1, "starve", nil, &damageInfo{typeName: "starve"})
			if died {
				return true
			}
		}
	}
	return false
}

// vexTarget runs the vex's target selector: HurtBy keeps a live
// attacker, CopyOwnerTarget mirrors the owner's target, then the
// nearest player.
func (s *Server) vexTarget(e *Entity, full bool) hostileTarget {
	h := e.hostile()
	if o, ok := s.entities.GetPtr(h.owner); ok && h.owner != 0 && !o.Dying && o.Dim == e.Dim {
		if on, _ := EntityTypeName(o.Type); hostileGoalTypes[on] {
			ot := s.hostileCurrentTarget(o, on)
			if ot.ok() {
				e.setHostileTarget(ot)
				return ot
			}
		}
	}
	t := s.hostileCurrentTarget(e, "vex")
	if !t.ok() && full {
		if p := s.nearestHostilePlayer(e, s.mobAttr(e, "vex", "follow_range", 16)); p != nil {
			t = hostileTarget{sess: p}
			e.setHostileTarget(t)
		}
	}
	return t
}

// vexStep is the vex goal selector + VexMoveControl + travel.
func (s *Server) vexStep(e *Entity) bool {
	h := e.hostile()
	full := e.TickCount%2 == 0
	t := s.vexTarget(e, full)

	// VexChargeAttackGoal canContinueToUse / stop.
	if h.charging && (!h.wantSet || !t.ok()) {
		h.charging = false
		e.setHostileFlag(0x01, false)
	}
	if full && !h.charging && t.ok() && !h.wantSet && rand.Intn(4) == 0 {
		tx, ty, tz := t.pos()
		if dx, dy, dz := tx-e.X, ty-e.Y, tz-e.Z; dx*dx+dy*dy+dz*dz > 4 {
			h.wantSet = true
			h.wantX, h.wantY, h.wantZ, h.wantSpeed = tx, t.eyeY(), tz, 1.0
			h.charging = true
			e.setHostileFlag(0x01, true)
			s.hostileSound(e, "entity.vex.charge", 1, 1)
		}
	}
	if h.charging && t.ok() {
		tx, ty, tz := t.pos()
		if boxesIntersect(e.X, e.Y, e.Z, entityBBDims(e), tx, ty, tz, t.dims()) {
			s.hostileDamageTarget(e, "vex", t, s.vexAttackDamage(e))
			h.charging = false
			e.setHostileFlag(0x01, false)
		} else if dx, dy, dz := tx-e.X, ty-e.Y, tz-e.Z; dx*dx+dy*dy+dz*dz < 9 {
			h.wantX, h.wantY, h.wantZ, h.wantSpeed = tx, t.eyeY(), tz, 1.0
		}
	}
	// VexRandomMoveGoal — MOVE is free while no charge runs.
	if full && !h.charging && !h.wantSet && rand.Intn(4) == 0 {
		origin := h.bound
		if !h.boundSet {
			origin = floorPos(e)
		}
		if w := s.dimWorld(e.Dim); w != nil {
			for i := 0; i < 3; i++ {
				p := world.Pos{
					X: origin.X + rand.Intn(15) - 7,
					Y: origin.Y + rand.Intn(11) - 5,
					Z: origin.Z + rand.Intn(15) - 7,
				}
				if w.GetBlock(p) == world.Air {
					h.wantSet = true
					h.wantX = float64(p.X) + 0.5
					h.wantY = float64(p.Y) + 0.5
					h.wantZ = float64(p.Z) + 0.5
					h.wantSpeed = 0.25
					break
				}
			}
		}
	}
	// VexMoveControl.tick.
	if h.wantSet {
		dx, dy, dz := h.wantX-e.X, h.wantY-e.Y, h.wantZ-e.Z
		l := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if l < vexBoxSize {
			h.wantSet = false
			e.MotionX *= 0.5
			e.MotionY *= 0.5
			e.MotionZ *= 0.5
		} else {
			k := h.wantSpeed * 0.05 / l
			e.MotionX += dx * k
			e.MotionY += dy * k
			e.MotionZ += dz * k
			if !t.ok() {
				e.Yaw = float32(-math.Atan2(e.MotionX, e.MotionZ) * 180 / math.Pi)
			} else {
				tx, _, tz := t.pos()
				e.Yaw = float32(-math.Atan2(tx-e.X, tz-e.Z) * 180 / math.Pi)
			}
			e.HeadYaw = e.Yaw
		}
	}
	// travelInAir with noPhysics: the box ignores collision.
	e.X += e.MotionX
	e.Y += e.MotionY
	e.Z += e.MotionZ
	e.MotionX *= 0.91
	e.MotionY *= 0.98
	e.MotionZ *= 0.91
	e.Grounded = false
	e.NeedsSync = true
	e.PosDirty = true
	return true
}

// vexAttackDamage is the attack_damage attribute with the mainhand
// weapon's modifier folded in: weaponDamage carries the item's
// player-facing total (1 + modifier), so the modifier is that minus 1.
func (s *Server) vexAttackDamage(e *Entity) float32 {
	dmg := float32(s.mobAttr(e, "vex", "attack_damage", 4))
	if st := e.Equips[equipMainHand]; !st.Empty() {
		if n, ok := world.ItemName(st.ID); ok {
			if wd, ok := weaponDamage[n]; ok {
				dmg += wd - 1
			}
		}
	}
	return dmg
}
