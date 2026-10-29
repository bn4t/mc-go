package server

import (
	"math"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// hostileAI carries the per-entity goal state of the hostile families
// modelled in evoker.go, vex.go, ravager.go and spider.go (plus the
// evoker_fangs projectile). Every field maps to a vanilla field or
// goal-local counter named in the comment of the family file that
// drives it; the struct hangs off Entity.hai so the shared Entity
// layout only grows by one pointer.
type hostileAI struct {
	// flags is the family's own synced byte: the evoker's
	// DATA_SPELL_CASTING_ID (17), the vex's and the spider's
	// DATA_FLAGS_ID (16). sentFlags is the client baseline the dirty
	// pack diffs against; sentNoGrav the same for the vex's
	// DATA_NO_GRAVITY (5) emit.
	flags      byte
	sentFlags  byte
	sentNoGrav bool

	// Shared Mob target: a session (by uuid) or an entity id.
	tgtUUID [16]byte
	tgtEnt  int32

	// SpellcasterIllager: spellCastingTickCount, the running
	// SpellcasterUseSpellGoal (spell id, 0 none) with its
	// attackWarmupDelay in goal ticks, the per-goal
	// nextAttackTickCount (indexed by spell id), whether
	// SpellcasterCastingSpellGoal is running, and the Evoker's
	// wololoTarget.
	spellTicks   int32
	useSpell     byte
	warmup       int32
	nextAttack   [4]int64
	castingGoal  bool
	wololoTarget int32

	// Vex: owner (entity id), boundOrigin, limitedLife, the
	// VexMoveControl wanted position + speedModifier + MOVE_TO
	// operation, and whether VexChargeAttackGoal is running.
	owner     int32
	boundSet  bool
	bound     world.Pos
	limited   bool
	lifeTicks int32
	wantSet   bool
	wantX     float64
	wantY     float64
	wantZ     float64
	wantSpeed float64
	charging  bool

	// EvokerFangs: owner + sentSpikeEvent.
	fangOwner int32
	fangSent  bool

	// Ravager: attackTick, stunnedTick, roarTick.
	attackTick int32
	stunTick   int32
	roarTick   int32

	// Spider: LeapAtTargetGoal running (canContinueToUse = !onGround).
	leaping bool
}

// hostile returns the entity's family state, allocating it on first use.
func (e *Entity) hostile() *hostileAI {
	if e.hai == nil {
		e.hai = &hostileAI{}
	}
	return e.hai
}

// hostileFamily is one family's registration: the goal selector run
// inside isEffectiveAi, the Entity.tick/aiStep work outside it, the
// summon-NBT reader, and the metadata slot of the family byte (0 for
// none). Family files register themselves in init().
type hostileFamily struct {
	goal    func(s *Server, e *Entity, name string) bool
	tickPre func(s *Server, e *Entity) bool
	nbt     func(s *Server, e *Entity, tag *snbtTag)
	flagIdx byte
}

var hostileFamilies = map[string]*hostileFamily{}

// hostileGoalTypes are the registered types whose goal selector runs
// through hostileGoalStep instead of the generic chase/wander chain;
// hostileTickTypes those with a pre-gate tick.
var hostileGoalTypes = map[string]bool{}
var hostileTickTypes = map[string]bool{}

func registerHostile(names []string, f *hostileFamily) {
	for _, n := range names {
		hostileFamilies[n] = f
		if f.goal != nil {
			hostileGoalTypes[n] = true
		}
		if f.tickPre != nil {
			hostileTickTypes[n] = true
		}
	}
}

// registerHostileDims fills mobBoundsTable for family types the
// shared table lacks (EntityTypes sized(...) off the 26.2 clinit).
func registerHostileDims(name string, w, h float64) {
	if id, ok := EntityTypeID(name); ok {
		if _, has := mobBoundsTable[id]; !has {
			mobBoundsTable[id] = [2]float64{w, h}
		}
	}
}

// hostileFlagIndex is the metadata slot each family's synced byte
// occupies (java_metadata_schema_gen: evoker DATA_SPELL_CASTING_ID 17,
// vex and spider DATA_FLAGS_ID 16).
func hostileFlagIndex(name string) (byte, bool) {
	if f := hostileFamilies[name]; f != nil && f.flagIdx != 0 {
		return f.flagIdx, true
	}
	return 0, false
}

// hostileMetaPack writes the family byte into a full pack when it
// sits off its 0 default and stamps the baseline.
func hostileMetaPack(b *protocol.Buffer, e *Entity, name string) {
	idx, ok := hostileFlagIndex(name)
	if !ok {
		return
	}
	var v byte
	if e.hai != nil {
		v = e.hai.flags
		e.hai.sentFlags = v
	}
	if v != 0 {
		b.Byte(idx)
		b.Byte(0) // type: byte
		b.Byte(v)
	}
}

// hostileMetaDirty writes the family byte into a dirty pack when it
// moved since the last emit.
func hostileMetaDirty(b *protocol.Buffer, e *Entity, name string) {
	idx, ok := hostileFlagIndex(name)
	if !ok || e.hai == nil {
		return
	}
	if e.hai.flags != e.hai.sentFlags {
		b.Byte(idx)
		b.Byte(0)
		b.Byte(e.hai.flags)
		e.hai.sentFlags = e.hai.flags
	}
}

// hostileNoGravPack / hostileNoGravDirty emit DATA_NO_GRAVITY (5) for
// the vex — Vex.tick calls setNoGravity(true) every tick, so the slot
// leaves its false default on the first tick after the summon.
func hostileNoGravPack(b *protocol.Buffer, e *Entity) {
	if e.hai == nil || e.Type != mustEntityType("vex") {
		return
	}
	e.hai.sentNoGrav = e.NoGravity
	if e.NoGravity {
		b.Byte(5)
		b.Byte(8)
		b.Bool(true)
	}
}

func hostileNoGravDirty(b *protocol.Buffer, e *Entity) {
	if e.hai == nil || e.Type != mustEntityType("vex") {
		return
	}
	if e.NoGravity != e.hai.sentNoGrav {
		b.Byte(5)
		b.Byte(8)
		b.Bool(e.NoGravity)
		e.hai.sentNoGrav = e.NoGravity
	}
}

// setHostileFlag sets or clears a bit of the family byte, marking the
// metadata dirty on a change.
func (e *Entity) setHostileFlag(bit byte, on bool) {
	h := e.hostile()
	v := h.flags
	if on {
		v |= bit
	} else {
		v &^= bit
	}
	if v != h.flags {
		h.flags = v
		e.MetaDirty = true
	}
}

// hostileTarget is a Mob target: either a player session or a living
// entity.
type hostileTarget struct {
	sess *Session
	ent  *Entity
}

func (t hostileTarget) ok() bool { return t.sess != nil || t.ent != nil }

func (t hostileTarget) pos() (float64, float64, float64) {
	if t.sess != nil {
		x, y, z, _, _, _ := t.sess.Pos()
		return x, y, z
	}
	if t.ent != nil {
		return t.ent.X, t.ent.Y, t.ent.Z
	}
	return 0, 0, 0
}

// dims is the target's (width, height) bounding box.
func (t hostileTarget) dims() [2]float64 {
	if t.sess != nil {
		return [2]float64{0.6, 1.8}
	}
	if t.ent != nil {
		return entityBBDims(t.ent)
	}
	return [2]float64{}
}

// eyeY is getEyePosition's y — 1.62 for a standing player, 0.85 of
// the box height for the mob families targeted here.
func (t hostileTarget) eyeY() float64 {
	_, y, _ := t.pos()
	if t.sess != nil {
		return y + 1.62
	}
	if t.ent != nil {
		return hostileEye(t.ent)
	}
	return y
}

func (t hostileTarget) id() int32 {
	if t.sess != nil {
		return t.sess.entityID.Load()
	}
	if t.ent != nil {
		return t.ent.ID
	}
	return 0
}

// sessAttackable is TargetingConditions.forCombat's player arm: a
// live, spawned survival/adventure player in the mob's dimension.
func sessAttackable(sess *Session, dim string) bool {
	if sess == nil || sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dead {
		return false
	}
	if sess.dimName() != dim {
		return false
	}
	m := sess.mode()
	return m == GameModeSurvival || m == GameModeAdventure
}

// hostileCurrentTarget resolves the stored target, clearing it when
// it went away, died, changed dimension, left forCombat (a player
// switched to creative/spectator), or moved past follow_range —
// TargetGoal.canContinueToUse.
func (s *Server) hostileCurrentTarget(e *Entity, name string) hostileTarget {
	h := e.hostile()
	fr := s.mobAttr(e, name, "follow_range", 16)
	if h.tgtUUID != ([16]byte{}) {
		sess := s.sessionByUUID(h.tgtUUID)
		if sessAttackable(sess, e.Dim) {
			px, py, pz, _, _, _ := sess.Pos()
			if dx, dy, dz := px-e.X, py-e.Y, pz-e.Z; dx*dx+dy*dy+dz*dz <= fr*fr {
				return hostileTarget{sess: sess}
			}
		}
		h.tgtUUID = [16]byte{}
	}
	if h.tgtEnt != 0 {
		if o, ok := s.entities.GetPtr(h.tgtEnt); ok && o.Living && !o.Dying && o.Dim == e.Dim && o.Health > 0 {
			if dx, dy, dz := o.X-e.X, o.Y-e.Y, o.Z-e.Z; dx*dx+dy*dy+dz*dz <= fr*fr {
				return hostileTarget{ent: o}
			}
		}
		h.tgtEnt = 0
	}
	return hostileTarget{}
}

// setHostileTarget is Mob.setTarget.
func (e *Entity) setHostileTarget(t hostileTarget) {
	h := e.hostile()
	h.tgtUUID = [16]byte{}
	h.tgtEnt = 0
	if t.sess != nil {
		h.tgtUUID = t.sess.uuid
	} else if t.ent != nil {
		h.tgtEnt = t.ent.ID
	}
}

// nearestHostilePlayer is NearestAttackableTargetGoal(Player): the
// closest forCombat player inside follow_range (the search box is
// inflate(range, 4, range) — the |dy| <= 4 gate).
func (s *Server) nearestHostilePlayer(e *Entity, rng float64) *Session {
	var best *Session
	bd := rng * rng
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if !sessAttackable(sess, e.Dim) {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if math.Abs(py-e.Y) > 4+entityBBDims(e)[1] {
			return true
		}
		dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
		// TargetingConditions keeps its line-of-sight check on.
		if d2 := dx*dx + dy*dy + dz*dz; d2 <= bd && s.hostileLOS(e.Dim, e.X, hostileEye(e), e.Z, px, py+1.62, pz) {
			bd = d2
			best = sess
		}
		return true
	})
	return best
}

// nearestHostileEntity is NearestAttackableTargetGoal over entity
// types: the closest live entity of one of names inside rng.
func (s *Server) nearestHostileEntity(e *Entity, rng float64, names map[string]bool, keep func(*Entity) bool) *Entity {
	var best *Entity
	bd := rng * rng
	s.entities.mu.RLock()
	defer s.entities.mu.RUnlock()
	for _, o := range s.entities.byID {
		if o == e || o.Dim != e.Dim || !o.Living || o.Dying || o.Dormant || o.Health <= 0 {
			continue
		}
		on, _ := EntityTypeName(o.Type)
		if !names[on] {
			continue
		}
		if keep != nil && !keep(o) {
			continue
		}
		if math.Abs(o.Y-e.Y) > 4+entityBBDims(e)[1] {
			continue
		}
		dx, dy, dz := o.X-e.X, o.Y-e.Y, o.Z-e.Z
		if d2 := dx*dx + dy*dy + dz*dz; d2 <= bd && s.hostileLOS(e.Dim, e.X, hostileEye(e), e.Z, o.X, hostileEye(o), o.Z) {
			bd = d2
			best = o
		}
	}
	return best
}

// hostileDamageTarget lands a mob_attack hit from e on the target —
// Mob.doHurtTarget's hurtServer call. Returns whether it landed.
func (s *Server) hostileDamageTarget(e *Entity, name string, t hostileTarget, dmg float32) bool {
	if t.sess != nil {
		dealt := s.damageWith(t.sess, dmg, translatable("death.attack.mob",
			literal(t.sess.getName()), literal(mobDisplayName(name))), damageSource{
			typeName: "mob_attack",
			cause:    e.ID,
			direct:   e.ID,
		})
		return dealt > 0
	}
	if t.ent != nil {
		dealt, _, _ := s.damageMob(t.ent, dmg, "mob_attack", nil, &damageInfo{
			typeName: "mob_attack", cause: e.ID, direct: e.ID,
		})
		return dealt > 0
	}
	return false
}

// boxesIntersect reports whether two entity boxes (feet position +
// width/height) overlap, AABB.intersects' strict comparison.
func boxesIntersect(ax, ay, az float64, ad [2]float64, bx, by, bz float64, bd [2]float64) bool {
	ah, bh := ad[0]/2, bd[0]/2
	return ax-ah < bx+bh && ax+ah > bx-bh &&
		ay < by+bd[1] && ay+ad[1] > by &&
		az-ah < bz+bh && az+ah > bz-bh
}

// hostileSound plays a mob sound on the HOSTILE source at the mob —
// Entity.playSound(sound, vol, pitch).
func (s *Server) hostileSound(e *Entity, snd string, vol, pitch float32) {
	if e.Silent {
		return
	}
	if id, ok := SoundEventID(snd); ok {
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, srcHostile, vol, pitch, nil)
	}
}

// hostileTickPre runs the families' Entity.tick / aiStep work that
// sits outside the isEffectiveAi gate. Returns true when the entity
// is gone (dead or removed) and the caller must skip it.
func (s *Server) hostileTickPre(e *Entity, name string) bool {
	if f := hostileFamilies[name]; f != nil && f.tickPre != nil {
		return f.tickPre(s, e)
	}
	return false
}

// hostileGoalStep is the goal-selector dispatch for the families —
// called inside the isEffectiveAi gate. Returns true when a goal
// drove the mob this tick (no generic wander).
func (s *Server) hostileGoalStep(e *Entity, name string) bool {
	if f := hostileFamilies[name]; f != nil && f.goal != nil {
		return f.goal(s, e, name)
	}
	return false
}

// hostileSummonNBT applies the families' readAdditionalSaveData keys
// on a tagged /summon.
func (s *Server) hostileSummonNBT(e *Entity, name string, tag *snbtTag) {
	if f := hostileFamilies[name]; tag != nil && f != nil && f.nbt != nil {
		f.nbt(s, e, tag)
	}
}

// hostileEyeHeights are EntityTypes' explicit eyeHeight(...) values
// for the types the families look from or at; the rest use the
// builder default 0.85·height.
var hostileEyeHeights = map[string]float64{
	"player": 1.62, "villager": 1.62, "wandering_trader": 1.62,
	"sheep": 1.235, "vex": vexEyeHeight, "spider": 0.65, "cave_spider": 0.45,
}

// hostileEye is an entity's getEyeY.
func hostileEye(e *Entity) float64 {
	n, _ := EntityTypeName(e.Type)
	if h, ok := hostileEyeHeights[n]; ok && !e.Baby {
		return e.Y + h
	}
	return e.Y + entityBBDims(e)[1]*0.85
}

// hostileLOS is Mob.hasLineOfSight's clip: an eye-to-eye segment
// (ClipContext COLLIDER, fluids ignored) blocked by any cell whose
// collision boxes contain a point of it — sampled every 1/32 block —
// and nothing past 128 blocks.
func (s *Server) hostileLOS(dim string, x0, y0, z0, x1, y1, z1 float64) bool {
	w := s.dimWorld(dim)
	if w == nil {
		return false
	}
	dx, dy, dz := x1-x0, y1-y0, z1-z0
	l := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if l > 128 {
		return false
	}
	n := int(l*32) + 1
	last := world.Pos{X: math.MinInt32}
	var boxes []cellBox
	solid := false
	for i := 0; i <= n; i++ {
		f := float64(i) / float64(n)
		px, py, pz := x0+dx*f, y0+dy*f, z0+dz*f
		pos := world.Pos{X: int(math.Floor(px)), Y: int(math.Floor(py)), Z: int(math.Floor(pz))}
		if pos != last {
			last = pos
			b := w.GetBlock(pos)
			solid = false
			boxes = nil
			if b != world.Air {
				if bx, has := collisionBoxesOf(b); has {
					boxes = bx
				} else if !world.NonSolid(b) && !moveNoCollision(b) {
					boxes = []cellBox{{0, 0, 0, 1, collisionTopOf(b), 1}}
				}
				solid = len(boxes) > 0
			}
		}
		if !solid {
			continue
		}
		lx, ly, lz := px-float64(pos.X), py-float64(pos.Y), pz-float64(pos.Z)
		for _, bx := range boxes {
			if lx >= bx[0] && lx <= bx[3] && ly >= bx[1] && ly <= bx[4] && lz >= bx[2] && lz <= bx[5] {
				return false
			}
		}
	}
	return true
}

// hostileSees is hasLineOfSight from e's eye to the target's eye.
func (s *Server) hostileSees(e *Entity, t hostileTarget) bool {
	tx, _, tz := t.pos()
	return s.hostileLOS(e.Dim, e.X, hostileEye(e), e.Z, tx, t.eyeY(), tz)
}

// collisionTopAt is a cell's collision-shape max Y in cell units, and
// whether the shape is non-empty.
func collisionTopAt(w *world.World, pos world.Pos) (float64, bool) {
	b := w.GetBlock(pos)
	if b == world.Air {
		return 0, false
	}
	if boxes, has := collisionBoxesOf(b); has {
		if len(boxes) == 0 {
			return 0, false
		}
		top := 0.0
		for _, bx := range boxes {
			if bx[4] > top {
				top = bx[4]
			}
		}
		return top, true
	}
	if world.NonSolid(b) || moveNoCollision(b) {
		return 0, false
	}
	return collisionTopOf(b), true
}
