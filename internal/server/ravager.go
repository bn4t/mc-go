package server

import (
	"math"
	"math/rand"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/world"
)

// Ravager — measured from the 26.2 jar (monster/Ravager, javap -c):
//
//   - registerGoals: FloatGoal(0), MeleeAttackGoal(1.0, true)(4),
//     WaterAvoidingRandomStroll(0.4)(5), LookAtPlayer(6/10); targets
//     HurtByTarget(Raider)(2), NearestAttackableTarget(Player, mustSee)
//     (3), (AbstractVillager, adults)(4), (IronGolem)(4). Attributes:
//     max_health 100, movement_speed 0.3, knockback_resistance 0.75,
//     attack_damage 12, attack_knockback 1.5, follow_range 32,
//     step_height 1.
//   - isImmobile: attackTick > 0 || stunnedTick > 0 || roarTick > 0 —
//     LivingEntity.aiStep then zeroes the inputs and skips
//     serverAiStep, so the goal selector (MeleeAttackGoal's attack
//     cooldown included) freezes for the whole window.
//   - aiStep (after super.aiStep, NoAI included): movement_speed base
//     lerps 0.1 toward 0.35 with a target / 0.3 without (0 while
//     immobile); a horizontally colliding ravager under mob_griefing
//     destroyBlock(drop)s every LeavesBlock cell of its box inflated
//     0.2 and jumpFromGround()s when none broke while on ground;
//     roarTick counts down and roar()s at 10; attackTick counts down;
//     stunnedTick counts down (stunEffect particles are client-only)
//     and on reaching 0 plays RAVAGER_ROAR and arms roarTick = 20.
//   - doHurtTarget: attackTick = 10, entity event 4, RAVAGER_ATTACK,
//     then Mob.doHurtTarget. getAttackBoundingBox deflates the reach
//     box by (0.05, 0, 0.05).
//   - blockedByItem (the target's shield ate the hit): roarTick == 0 →
//     nextDouble < 0.5: stunnedTick = 40, RAVAGER_STUNNED, entity event
//     39, target.push(this); else strongKnockback(target); the target
//     is hurtMarked either way.
//   - roar(): every live non-ravager LivingEntity in the box inflated
//     4 (armor stands excluded without mob_griefing) — non-illagers
//     take mob_attack 6.0, non-players get strongKnockback; then
//     entity event 69. strongKnockback pushes (dx/d·4, 0.2, dz/d·4)
//     with d = max(dx²+dz², 0.001).

func init() {
	registerHostileDims("ravager", 1.95, 2.2)
	registerHostile([]string{"ravager"}, &hostileFamily{
		goal:    func(s *Server, e *Entity, _ string) bool { return s.ravagerStep(e) },
		tickPre: func(s *Server, e *Entity) bool { s.ravagerTickPre(e); return false },
		nbt: func(_ *Server, e *Entity, tag *snbtTag) {
			// Ravager.readAdditionalSaveData.
			h := e.hostile()
			if v, ok := tag.vals["AttackTick"]; ok {
				h.attackTick = int32(snbtNum(v))
			}
			if v, ok := tag.vals["StunTick"]; ok {
				h.stunTick = int32(snbtNum(v))
			}
			if v, ok := tag.vals["RoarTick"]; ok {
				h.roarTick = int32(snbtNum(v))
			}
		},
	})
}

var ravagerVillagerTargets = map[string]bool{"villager": true, "wandering_trader": true}
var ravagerGolemTargets = map[string]bool{"iron_golem": true}

// ravagerImmobile is Ravager.isImmobile.
func ravagerImmobile(h *hostileAI) bool {
	return h.attackTick > 0 || h.stunTick > 0 || h.roarTick > 0
}

// ravagerTickPre is the counter half of Ravager.aiStep.
func (s *Server) ravagerTickPre(e *Entity) {
	h := e.hostile()
	if h.roarTick > 0 {
		h.roarTick--
		if h.roarTick == 10 {
			s.ravagerRoar(e)
		}
	}
	if h.attackTick > 0 {
		h.attackTick--
	}
	if h.stunTick > 0 {
		h.stunTick--
		if h.stunTick == 0 {
			s.hostileSound(e, "entity.ravager.roar", 1, 1)
			h.roarTick = 20
		}
	}
}

// ravagerTarget is the target selector's acquisition arm.
func (s *Server) ravagerTarget(e *Entity, full bool) hostileTarget {
	t := s.hostileCurrentTarget(e, "ravager")
	if t.ok() || !full {
		return t
	}
	fr := s.mobAttr(e, "ravager", "follow_range", 32)
	if p := s.nearestHostilePlayer(e, fr); p != nil {
		t = hostileTarget{sess: p}
	} else if v := s.nearestHostileEntity(e, fr, ravagerVillagerTargets, func(o *Entity) bool { return !o.Baby }); v != nil {
		t = hostileTarget{ent: v}
	} else if g := s.nearestHostileEntity(e, fr, ravagerGolemTargets, nil); g != nil {
		t = hostileTarget{ent: g}
	}
	if t.ok() {
		e.setHostileTarget(t)
		// MeleeAttackGoal.start: ticksUntilNextAttack = 0.
		e.attackCooldown = 0
	}
	return t
}

// ravagerStep is the goal selector + MeleeAttackGoal + the
// leaf-breaking tail of aiStep.
func (s *Server) ravagerStep(e *Entity) bool {
	h := e.hostile()
	w := s.dimWorld(e.Dim)
	if w == nil {
		return false
	}
	if ravagerImmobile(h) {
		e.HColl = false
		s.mobTravelStep(e, w, 0)
		s.ravagerLeafBreak(e, w)
		return true
	}
	full := e.TickCount%2 == 0
	t := s.ravagerTarget(e, full)
	if !t.ok() {
		e.HColl = false
		s.mobMoveStep(e, w)
		s.ravagerLeafBreak(e, w)
		s.mobLookStep(e)
		if e.Aggressive {
			e.Aggressive = false
			e.MetaDirty = true
		}
		return true
	}
	if !e.Aggressive {
		// MeleeAttackGoal.start → setAggressive(true).
		e.Aggressive = true
		e.MetaDirty = true
	}
	tx, ty, tz := t.pos()
	td := t.dims()
	mobTurnToward(e, tx-e.X, tz-e.Z, 30)
	e.HeadYaw = e.Yaw
	speed := s.mobAttr(e, "ravager", "movement_speed", 0.3)
	// Ravager.aiStep's lerp toward ATTACK_MOVEMENT_SPEED with a target.
	speed = speed + (0.35-speed)*0.1
	dims := entityBBDims(e)
	// isWithinMeleeAttackRange on the deflated attack box.
	reach := 0.8284271 - 0.05
	inRange := math.Abs(tx-e.X) < dims[0]/2+reach+td[0]/2 &&
		math.Abs(tz-e.Z) < dims[0]/2+reach+td[0]/2 &&
		ty < e.Y+dims[1] && ty+td[1] > e.Y
	e.HColl = false
	if !inRange {
		s.mobTravelStep(e, w, speed)
	} else {
		s.mobTravelStep(e, w, 0)
	}
	s.ravagerLeafBreak(e, w)
	if e.attackCooldown > 0 {
		e.attackCooldown--
	}
	if e.attackCooldown <= 0 && inRange {
		e.attackCooldown = 20
		s.broadcastEntityAnimation(nil, e.ID, 0)
		s.ravagerHurtTarget(e, t)
	}
	return true
}

// ravagerHurtTarget is Ravager.doHurtTarget plus the shield arm of
// the target's hurtServer.
func (s *Server) ravagerHurtTarget(e *Entity, t hostileTarget) {
	h := e.hostile()
	h.attackTick = 10
	s.broadcastEntityEventTo(nil, e.Dim, e.ID, 4)
	s.hostileSound(e, "entity.ravager.attack", 1, 1)
	if t.sess != nil && t.sess.blocking.Load() && s.ravagerShieldFaces(t.sess, e) {
		// The shield swallows the hit — item.shield.block — and the
		// attacker's blockedByItem runs.
		s.broadcastAttackSound(t.sess, 1455)
		s.ravagerBlockedByItem(e, t)
		return
	}
	s.hostileDamageTarget(e, "ravager", t, float32(s.mobAttr(e, "ravager", "attack_damage", 12)))
}

// ravagerShieldFaces is the shield's front-arc test: the attacker
// sits inside the 90° horizontal half-angle of the player's view.
func (s *Server) ravagerShieldFaces(sess *Session, e *Entity) bool {
	px, _, pz, yaw, _, _ := sess.Pos()
	yr := float64(yaw) * math.Pi / 180
	vx, vz := -math.Sin(yr), math.Cos(yr)
	dx, dz := e.X-px, e.Z-pz
	l := math.Hypot(dx, dz)
	if l < 1e-7 {
		return true
	}
	return (dx*vx+dz*vz)/l > 0
}

// ravagerBlockedByItem is Ravager.blockedByItem.
func (s *Server) ravagerBlockedByItem(e *Entity, t hostileTarget) {
	h := e.hostile()
	if h.roarTick != 0 {
		return
	}
	if rand.Float64() < 0.5 {
		h.stunTick = 40
		s.hostileSound(e, "entity.ravager.stunned", 1, 1)
		s.broadcastEntityEventTo(nil, e.Dim, e.ID, 39)
		// target.push(this): the separation shove along the centres.
		tx, _, tz := t.pos()
		dx, dz := tx-e.X, tz-e.Z
		if m := math.Max(math.Abs(dx), math.Abs(dz)); m >= 0.01 {
			m = math.Sqrt(m)
			dx, dz = dx/m, dz/m
			inv := 1 / m
			if inv > 1 {
				inv = 1
			}
			dx, dz = dx*inv*0.05, dz*inv*0.05
			s.hostilePush(e, t, dx, 0, dz)
		}
		return
	}
	s.ravagerStrongKnockback(e, t)
}

// ravagerStrongKnockback is Ravager.strongKnockback.
func (s *Server) ravagerStrongKnockback(e *Entity, t hostileTarget) {
	tx, _, tz := t.pos()
	dx, dz := tx-e.X, tz-e.Z
	d := math.Max(dx*dx+dz*dz, 0.001)
	s.hostilePush(e, t, dx/d*4, 0.2, dz/d*4)
}

// hostilePush is Entity.push on the target with hurtMarked latched.
func (s *Server) hostilePush(_ *Entity, t hostileTarget, x, y, z float64) {
	if t.sess != nil {
		s.broadcastEntityMotion(t.sess.dimName(), t.sess.entityID.Load(), x, y, z)
		return
	}
	if t.ent != nil {
		t.ent.MotionX += x
		t.ent.MotionY += y
		t.ent.MotionZ += z
		t.ent.HurtMarked = true
		t.ent.NeedsSync = true
	}
}

// ravagerRoar is Ravager.roar.
func (s *Server) ravagerRoar(e *Entity) {
	if e.Dying || e.Health <= 0 {
		return
	}
	griefing := s.gameRuleBool("minecraft:mob_griefing")
	d := entityBBDims(e)
	box := [2]float64{d[0] + 8, d[1] + 8}
	by := e.Y - 4
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dead || sess.dimName() != e.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if boxesIntersect(e.X, by, e.Z, box, px, py, pz, [2]float64{0.6, 1.8}) {
			s.hostileDamageTarget(e, "ravager", hostileTarget{sess: sess}, 6)
		}
		return true
	})
	var hits []*Entity
	s.entities.mu.RLock()
	for _, o := range s.entities.byID {
		if o.ID == e.ID || o.Dim != e.Dim || !o.Living || o.Dying || o.Health <= 0 {
			continue
		}
		on, _ := EntityTypeName(o.Type)
		if on == "ravager" || on == "player" || (!griefing && on == "armor_stand") {
			continue
		}
		if boxesIntersect(e.X, by, e.Z, box, o.X, o.Y, o.Z, entityBBDims(o)) {
			hits = append(hits, o)
		}
	}
	s.entities.mu.RUnlock()
	for _, o := range hits {
		on, _ := EntityTypeName(o.Type)
		switch on {
		case "evoker", "illusioner", "pillager", "vindicator":
		default:
			s.hostileDamageTarget(e, "ravager", hostileTarget{ent: o}, 6)
		}
		s.ravagerStrongKnockback(e, hostileTarget{ent: o})
	}
	s.broadcastEntityEventTo(nil, e.Dim, e.ID, 69)
}

// ravagerLeafBreak is aiStep's horizontalCollision arm.
func (s *Server) ravagerLeafBreak(e *Entity, w *world.World) {
	if !e.HColl || !s.gameRuleBool("minecraft:mob_griefing") {
		return
	}
	d := s.dimensionByName(e.Dim)
	if d == nil {
		return
	}
	dims := entityBBDims(e)
	hw := dims[0]/2 + 0.2
	minX, maxX := int(math.Floor(e.X-hw)), int(math.Floor(e.X+hw))
	minY, maxY := int(math.Floor(e.Y-0.2)), int(math.Floor(e.Y+dims[1]+0.2))
	minZ, maxZ := int(math.Floor(e.Z-hw)), int(math.Floor(e.Z+hw))
	broke := false
	for x := minX; x <= maxX; x++ {
		for y := minY; y <= maxY; y++ {
			for z := minZ; z <= maxZ; z++ {
				pos := world.Pos{X: x, Y: y, Z: z}
				b := w.GetBlock(pos)
				name, ok := world.StateOwner(b)
				if !ok || !isLeafBlock(strings.TrimPrefix(name, "minecraft:")) {
					continue
				}
				// ServerLevel.destroyBlock(pos, true, this): the 2001
				// break effect, the no-tool loot roll, then air.
				s.broadcastLevelEvent(d.name, 2001, pos, int32(b))
				table := "blocks/" + strings.TrimPrefix(name, "minecraft:")
				if _, has := data.LootTables()[table]; has {
					for _, st := range rollFluidLoot(table, b, pos, d) {
						s.spawnItemDrop(d.name, pos, st)
					}
				}
				s.setNaturalBlock(d, pos, world.Air)
				broke = true
			}
		}
	}
	if !broke && e.Grounded {
		// jumpFromGround — getJumpPower 0.42.
		e.MotionY = 0.42
		e.NeedsSync = true
	}
}
