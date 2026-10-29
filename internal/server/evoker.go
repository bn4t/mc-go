package server

import (
	"math"
	"math/rand"

	"mc-go/internal/world"
)

// Evoker — measured from the 26.2 jar (monster/illager/Evoker,
// SpellcasterIllager and their inner goals, projectile/EvokerFangs;
// javap -c):
//
//   - registerGoals: FloatGoal(0), EvokerCastingSpellGoal(1,
//     MOVE|LOOK), AvoidEntityGoal(Player, 8, 0.6, 1.0)(2),
//     AvoidEntityGoal(Creaking)(3), EvokerSummonSpellGoal(4),
//     EvokerAttackSpellGoal(5), EvokerWololoSpellGoal(6),
//     RandomStroll(0.6)(8), LookAtPlayer(9/10); targets
//     HurtByTarget(Raider)(1), NearestAttackableTarget(Player,
//     mustSee)(2), (AbstractVillager)(3), (IronGolem)(3). follow_range
//     12, movement_speed 0.5, max_health 24.
//   - SpellcasterUseSpellGoal: canUse = live target && !isCastingSpell
//     && tickCount >= nextAttackTickCount. start: attackWarmupDelay =
//     adjustedTickDelay(getCastWarmupTime) (20 → 10 goal ticks; the
//     goals tick on the every-other-tick full selector pass, the start
//     tick included), spellCastingTickCount = getCastingTime,
//     nextAttackTickCount = tickCount + getCastingInterval, the
//     prepare sound (vol 1, pitch 1), setIsCastingSpell(spell) →
//     DATA_SPELL_CASTING_ID (17) = spell id. tick: --warmup == 0 →
//     performSpellCasting + getCastingSoundEvent (EVOKER_CAST_SPELL).
//     canContinueToUse: live target && warmup > 0.
//   - SpellcasterCastingSpellGoal: canUse = spellCastingTickCount > 0;
//     stop → setIsCastingSpell(NONE). The count drops one per tick in
//     customServerAiStep. A stop and the next spell's start in one
//     selector pass leave only the new id on the wire.
//   - Summon (100 cast, 340 interval, PREPARE_SUMMON): canUse also
//     needs nextInt(8)+1 > vexes within 16; performs three
//     EntityTypes.VEX MOB_SUMMONED spawns at blockPosition +
//     (-2+rand5, 1, -2+rand5), snapTo the cell's bottom centre,
//     finalizeSpawn, setOwner, setBoundOrigin(cell),
//     setLimitedLife(20·(30+rand90)).
//   - Attack (40 cast, 100 interval, PREPARE_ATTACK): yaw = atan2(tz-
//     ez, tx-ex), minY = min(ty, ey), maxY = max(ty, ey)+1; distSqr <
//     9 → five fangs at r 1.5 (yaw + i·0.4π, warmup 0) and eight at r
//     2.5 (yaw + i·2π/8 + 1.2566371, warmup 3); else sixteen in a line
//     at 1.25·(i+1), warmup i. createSpellEntity walks down from
//     maxY to floor(minY)-1 for the first cell whose below is
//     isFaceSturdy(UP); a non-empty cell lifts the fang to its
//     collision shape's max Y.
//   - Wololo (warmup 40, 60 cast, 140 interval, PREPARE_WOLOLO):
//     canUse = no target, !casting, the interval, mob_griefing, and a
//     BLUE sheep inside the box inflate(16,4,16) within 16 — one picked
//     at random; perform sets it RED.
//   - EvokerFangs.tick: --warmupDelayTicks < 0 → at -8 dealDamageTo
//     every LivingEntity in the box inflate(0.2,0,0.2); the first
//     negative tick broadcasts entity event 4 (the client's bite +
//     local EVOKER_FANGS_ATTACK sound); --lifeTicks < 0 discards.
//     dealDamageTo: skip dead, invulnerable, or the owner; ownerless →
//     magic 6.0; owner allied → skip; else indirectMagic(fang, owner)
//     6.0.

// evokerSpellNames are the IllagerSpell enum ids the goals set.
const (
	spellNone   = 0
	spellSummon = 1
	spellFangs  = 2
	spellWololo = 3
)

func init() {
	registerHostileDims("evoker", 0.6, 1.95)
	registerHostileDims("evoker_fangs", 0.5, 0.8)
	registerHostile([]string{"evoker"}, &hostileFamily{
		goal: func(s *Server, e *Entity, _ string) bool { return s.evokerStep(e) },
		nbt: func(_ *Server, e *Entity, tag *snbtTag) {
			// SpellcasterIllager.readAdditionalSaveData: SpellTicks.
			if v, ok := tag.vals["SpellTicks"]; ok {
				e.hostile().spellTicks = int32(snbtNum(v))
			}
		},
		flagIdx: 17,
	})
}

var evokerVillagerTargets = map[string]bool{"villager": true, "wandering_trader": true}
var evokerGolemTargets = map[string]bool{"iron_golem": true}

// evokerAcquire runs the target selector's acquisition arm on a full
// selector tick: player, then villager, then iron golem.
func (s *Server) evokerAcquire(e *Entity) hostileTarget {
	fr := s.mobAttr(e, "evoker", "follow_range", 12)
	if p := s.nearestHostilePlayer(e, fr); p != nil {
		t := hostileTarget{sess: p}
		e.setHostileTarget(t)
		return t
	}
	if v := s.nearestHostileEntity(e, fr, evokerVillagerTargets, nil); v != nil {
		t := hostileTarget{ent: v}
		e.setHostileTarget(t)
		return t
	}
	if g := s.nearestHostileEntity(e, fr, evokerGolemTargets, nil); g != nil {
		t := hostileTarget{ent: g}
		e.setHostileTarget(t)
		return t
	}
	return hostileTarget{}
}

// evokerSetSpell is setIsCastingSpell.
func (e *Entity) evokerSetSpell(id byte) {
	h := e.hostile()
	if h.flags != id {
		h.flags = id
		e.MetaDirty = true
	}
}

// evokerStep is the evoker's goal selector. Returns true when a
// MOVE-flag goal (casting or avoid) drove the mob this tick.
func (s *Server) evokerStep(e *Entity) bool {
	h := e.hostile()
	full := e.TickCount%2 == 0
	t := s.hostileCurrentTarget(e, "evoker")
	if full {
		if !t.ok() {
			t = s.evokerAcquire(e)
		}
		// Stop pass.
		if h.castingGoal && h.spellTicks <= 0 {
			h.castingGoal = false
			e.evokerSetSpell(spellNone)
		}
		if h.useSpell != spellNone {
			cont := h.warmup > 0
			if h.useSpell == spellWololo {
				cont = cont && h.wololoTarget != 0
			} else {
				cont = cont && t.ok()
			}
			if !cont {
				if h.useSpell == spellWololo {
					h.wololoTarget = 0
				}
				h.useSpell = spellNone
			}
		}
		// Start pass in priority order.
		if !h.castingGoal && h.spellTicks > 0 {
			h.castingGoal = true
		}
		if h.useSpell == spellNone && h.spellTicks <= 0 {
			switch {
			case t.ok() && int64(e.TickCount) >= h.nextAttack[spellSummon] &&
				rand.Intn(8)+1 > s.evokerVexCount(e):
				s.evokerStartSpell(e, spellSummon, 100, 340, 20, "entity.evoker.prepare_summon")
			case t.ok() && int64(e.TickCount) >= h.nextAttack[spellFangs]:
				s.evokerStartSpell(e, spellFangs, 40, 100, 20, "entity.evoker.prepare_attack")
			case !t.ok() && int64(e.TickCount) >= h.nextAttack[spellWololo] &&
				s.gameRuleBool("minecraft:mob_griefing"):
				if sh := s.evokerWololoPick(e); sh != nil {
					h.wololoTarget = sh.ID
					s.evokerStartSpell(e, spellWololo, 60, 140, 40, "entity.evoker.prepare_wololo")
				}
			}
		}
		// Tick the running use goal.
		if h.useSpell != spellNone {
			h.warmup--
			if h.warmup == 0 {
				switch h.useSpell {
				case spellSummon:
					s.evokerSummonVexes(e)
				case spellFangs:
					s.evokerCastFangs(e, t)
				case spellWololo:
					s.evokerWololo(e)
				}
				s.hostileSound(e, "entity.evoker.cast_spell", 1, 1)
			}
		}
	}
	// customServerAiStep: spellCastingTickCount counts down.
	if h.spellTicks > 0 {
		h.spellTicks--
	}
	if h.castingGoal {
		// EvokerCastingSpellGoal.tick: look at the target (or the
		// wololo sheep); navigation stopped — the cast stands still.
		var lx, lz float64
		look := false
		if t.ok() {
			lx, _, lz = t.pos()
			look = true
		} else if sh, ok := s.entities.GetPtr(h.wololoTarget); ok && h.wololoTarget != 0 {
			lx, lz, look = sh.X, sh.Z, true
		}
		if look {
			mobTurnToward(e, lx-e.X, lz-e.Z, 30)
			e.HeadYaw = e.Yaw
		}
		s.mobTravelStep(e, s.dimWorld(e.Dim), 0)
		return true
	}
	// AvoidEntityGoal(Player, 8, 0.6, 1.0).
	return s.mobFleeStep(e, "evoker")
}

// evokerStartSpell is SpellcasterUseSpellGoal.start.
func (s *Server) evokerStartSpell(e *Entity, spell byte, castTime, interval, warmup int32, prepare string) {
	h := e.hostile()
	h.useSpell = spell
	h.warmup = (warmup + 1) / 2 // adjustedTickDelay → reducedTickDelay
	h.spellTicks = castTime
	h.nextAttack[spell] = int64(e.TickCount) + int64(interval)
	s.hostileSound(e, prepare, 1, 1)
	e.evokerSetSpell(spell)
}

// evokerVexCount counts the vexes inside 16 blocks (any owner) — the
// summon goal's vexCountTargeting.
func (s *Server) evokerVexCount(e *Entity) int {
	vexID := mustEntityType("vex")
	n := 0
	s.entities.mu.RLock()
	defer s.entities.mu.RUnlock()
	for _, o := range s.entities.byID {
		if o.Type != vexID || o.Dim != e.Dim || o.Dying {
			continue
		}
		if dx, dy, dz := o.X-e.X, o.Y-e.Y, o.Z-e.Z; dx*dx+dy*dy+dz*dz <= 256 {
			n++
		}
	}
	return n
}

// evokerSummonVexes is EvokerSummonSpellGoal.performSpellCasting.
func (s *Server) evokerSummonVexes(e *Entity) {
	d := s.dimensionByName(e.Dim)
	if d == nil {
		return
	}
	bp := floorPos(e)
	for i := 0; i < 3; i++ {
		p := world.Pos{X: bp.X - 2 + rand.Intn(5), Y: bp.Y + 1, Z: bp.Z - 2 + rand.Intn(5)}
		v, ok := s.summonMobRaw(d, "vex", float64(p.X)+0.5, float64(p.Y), float64(p.Z)+0.5, false, true)
		if !ok {
			continue
		}
		s.mobDefaultEquip(v)
		vh := v.hostile()
		vh.owner = e.ID
		vh.boundSet = true
		vh.bound = p
		vh.limited = true
		vh.lifeTicks = 20 * (30 + int32(rand.Intn(90)))
		if !v.Dormant {
			s.broadcastFreshMob(v)
		}
	}
}

// evokerCastFangs is EvokerAttackSpellGoal.performSpellCasting.
func (s *Server) evokerCastFangs(e *Entity, t hostileTarget) {
	if !t.ok() {
		return
	}
	tx, ty, tz := t.pos()
	minY := math.Min(ty, e.Y)
	maxY := math.Max(ty, e.Y) + 1
	yaw := float32(math.Atan2(tz-e.Z, tx-e.X))
	dx, dy, dz := tx-e.X, ty-e.Y, tz-e.Z
	if dx*dx+dy*dy+dz*dz < 9 {
		for i := 0; i < 5; i++ {
			a := yaw + float32(i)*float32(math.Pi)*0.4
			s.evokerFang(e, e.X+float64(mthCos(float64(a)))*1.5, e.Z+float64(mthSin(float64(a)))*1.5, minY, maxY, a, 0)
		}
		for i := 0; i < 8; i++ {
			a := yaw + float32(i)*float32(math.Pi)*2/8 + 1.2566371
			s.evokerFang(e, e.X+float64(mthCos(float64(a)))*2.5, e.Z+float64(mthSin(float64(a)))*2.5, minY, maxY, a, 3)
		}
		return
	}
	for i := 0; i < 16; i++ {
		r := 1.25 * float64(i+1)
		s.evokerFang(e, e.X+float64(mthCos(float64(yaw)))*r, e.Z+float64(mthSin(float64(yaw)))*r, minY, maxY, yaw, int32(i))
	}
}


// evokerFang is createSpellEntity: the downward support scan, then
// the fang spawn.
func (s *Server) evokerFang(e *Entity, x, z, minY, maxY float64, yaw float32, warmup int32) {
	w := s.dimWorld(e.Dim)
	if w == nil {
		return
	}
	pos := world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(maxY)), Z: int(math.Floor(z))}
	floorY := int(math.Floor(minY)) - 1
	found := false
	off := 0.0
	for {
		below := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
		if rsFaceSturdyUp(w.GetBlock(below)) {
			if w.GetBlock(pos) != world.Air {
				if top, ok := collisionTopAt(w, pos); ok {
					off = top
				}
			}
			found = true
			break
		}
		pos.Y--
		if pos.Y < floorY {
			break
		}
	}
	if !found {
		return
	}
	f := s.entities.Spawn(&Entity{
		Type:       mustEntityType("evoker_fangs"),
		Dim:        e.Dim,
		X:          x,
		Y:          float64(pos.Y) + off,
		Z:          z,
		Yaw:        yaw * 57.295776,
		FangWarmup: warmup,
		FangLife:   22,
	})
	f.hostile().fangOwner = e.ID
	s.broadcastEntitySpawn(nil, *f)
}

// evokerWololoPick is the wololo goal's sheep search.
func (s *Server) evokerWololoPick(e *Entity) *Entity {
	sheepID := mustEntityType("sheep")
	var list []*Entity
	half := entityBBDims(e)[0] / 2
	h := entityBBDims(e)[1]
	s.entities.mu.RLock()
	for _, o := range s.entities.byID {
		if o.Type != sheepID || o.Dim != e.Dim || o.Dying || o.Dormant || o.Color != 11 {
			continue
		}
		oh := entityBBDims(o)
		if o.X+oh[0]/2 < e.X-half-16 || o.X-oh[0]/2 > e.X+half+16 ||
			o.Z+oh[0]/2 < e.Z-half-16 || o.Z-oh[0]/2 > e.Z+half+16 ||
			o.Y+oh[1] < e.Y-4 || o.Y > e.Y+h+4 {
			continue
		}
		if dx, dy, dz := o.X-e.X, o.Y-e.Y, o.Z-e.Z; dx*dx+dy*dy+dz*dz > 256 {
			continue
		}
		// wololoTargeting is forNonCombat without ignoreLineOfSight.
		if !s.hostileLOS(e.Dim, e.X, hostileEye(e), e.Z, o.X, hostileEye(o), o.Z) {
			continue
		}
		list = append(list, o)
	}
	s.entities.mu.RUnlock()
	if len(list) == 0 {
		return nil
	}
	return list[rand.Intn(len(list))]
}

// evokerWololo is EvokerWololoSpellGoal.performSpellCasting.
func (s *Server) evokerWololo(e *Entity) {
	h := e.hostile()
	sh, ok := s.entities.GetPtr(h.wololoTarget)
	if !ok || sh.Dying || sh.Health <= 0 {
		return
	}
	if sh.Color != 14 {
		sh.Color = 14 // DyeColor.RED
		sh.MetaDirty = true
	}
}

// fangTick is EvokerFangs.tick's server branch.
func (s *Server) fangTick(e *Entity) {
	e.FangWarmup--
	if e.FangWarmup >= 0 {
		return
	}
	h := e.hostile()
	if e.FangWarmup == -8 {
		s.fangDealDamage(e)
	}
	if !h.fangSent {
		s.broadcastEntityEventTo(nil, e.Dim, e.ID, 4)
		h.fangSent = true
	}
	e.FangLife--
	if e.FangLife < 0 {
		s.ephemeralDiscard(e)
	}
}

// fangAllied is the owner's isAlliedTo(victim): the evoker allies
// itself, its own vexes, and the illager_friends set (no teams).
func (s *Server) fangAllied(owner *Entity, victim *Entity) bool {
	if victim == owner {
		return true
	}
	vn, _ := EntityTypeName(victim.Type)
	switch vn {
	case "evoker", "illusioner", "pillager", "vindicator":
		return true
	case "vex":
		if victim.hai != nil && victim.hai.owner != 0 {
			if ro, ok := s.entities.GetPtr(victim.hai.owner); ok {
				return ro == owner || s.fangAllied(owner, ro)
			}
		}
	}
	return false
}

// fangDealDamage is the warmup -8 bite: every living entity — players
// included — whose box meets the fang's box inflated (0.2, 0, 0.2).
func (s *Server) fangDealDamage(e *Entity) {
	h := e.hostile()
	var owner *Entity
	if h.fangOwner != 0 {
		if o, ok := s.entities.GetPtr(h.fangOwner); ok && !o.Dying {
			owner = o
		}
	}
	fd := mobBounds(e.Type)
	box := [2]float64{fd[0] + 0.4, fd[1]}
	typ := "magic"
	var cause int32
	if owner != nil {
		typ = "indirect_magic"
		cause = owner.ID
	}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dead || sess.dimName() != e.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if !boxesIntersect(e.X, e.Y, e.Z, box, px, py, pz, [2]float64{0.6, 1.8}) {
			return true
		}
		msg := translatable("death.attack.magic", literal(sess.getName()))
		if owner != nil {
			on, _ := EntityTypeName(owner.Type)
			msg = translatable("death.attack.indirectMagic", literal(sess.getName()), literal(mobDisplayName(on)))
		}
		s.damageWith(sess, 6, msg, damageSource{typeName: typ, cause: cause, direct: e.ID})
		return true
	})
	var victims []*Entity
	s.entities.mu.RLock()
	for _, o := range s.entities.byID {
		if o.ID == e.ID || o.Dim != e.Dim || !o.Living || o.Dying || o.Invulnerable {
			continue
		}
		if on, _ := EntityTypeName(o.Type); on == "player" {
			continue
		}
		if boxesIntersect(e.X, e.Y, e.Z, box, o.X, o.Y, o.Z, entityBBDims(o)) {
			victims = append(victims, o)
		}
	}
	s.entities.mu.RUnlock()
	for _, o := range victims {
		if owner != nil && s.fangAllied(owner, o) {
			continue
		}
		s.damageMob(o, 6, typ, nil, &damageInfo{typeName: typ, cause: cause, direct: e.ID})
	}
}
