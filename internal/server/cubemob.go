package server

import (
	"math"
	"math/rand"

	"mc-go/internal/world"
)

// AbstractCubeMob's per-tick model (slime, magma_cube on the 26.2
// decompile): CubeMobMoveControl's jump/wait rhythm driven by
// KeepOnJumpingGoal's always-on wanted movement, heading picks from
// CubeMobRandomDirectionGoal (~40-99 ticks) or CubeMobAttackGoal's
// target-locked heading + aggressive pace, landing squish
// particles/sound, and the playerTouch contact damage.

// cubeJumpDelayFor is AbstractCubeMob.getJumpDelay:
// random.nextInt(20)+10, MagmaCube ×4 — seeded per cube per hop so
// the cadence varies the way vanilla's RNG does.
func cubeJumpDelayFor(eid int32, seq int64, name string) int32 {
	h := uint64(uint32(eid))*40503 ^ uint64(seq)*1099511628211
	d := int32(h%20) + 10
	if name == "magma_cube" {
		d *= 4
	}
	return d
}

// cubeDealsDamage is AbstractCubeMob.isDealsDamage — the slime needs
// getSize()>1 while MagmaCube deals damage at every size.
func cubeDealsDamage(e *Entity, name string) bool {
	if e.NoAI {
		return false
	}
	if name == "magma_cube" {
		return true
	}
	return cubeSizeOf(e) > 1
}

// cubeHopStep runs the cube goals and the MoveControl tick. Called
// instead of mobMoveStep for slime/magma_cube — their drive model is
// discrete hops, not the continuous wander.
func (s *Server) cubeHopStep(e *Entity, w *world.World, name string) {
	bounds := entityBBDims(e)
	e.NeedsSync = true
	wDepth, inWater, lDepth, inLava := entityFluidTick(w, s.dims[e.Dim], e, bounds[0]/2, bounds[1])
	thresh := floatJumpThreshold(e, bounds)
	inFluid := inWater || inLava
	// CubeMobFloatGoal (priority 1): in water or lava, 0.8 per tick
	// arms the jump and the wanted movement rises to 1.2.
	speedMod := 1.0
	if inFluid {
		if floatGoalRoll(e.ID, e.WanderTicks2) {
			e.Jumping = true
		}
		speedMod = 1.2
	}
	// The aiStep jump chain — jumpFromGround's vy for cubes is
	// getJumpPower (magma + size*0.1); jumpInLiquid(LAVA) on the
	// magma is 0.22 + size*0.05.
	if e.Jumping {
		h := wDepth
		if inLava {
			h = lDepth
		}
		b1 := inWater && h > 0
		switch {
		case b1 && (!e.Grounded || h > thresh):
			e.MotionY += 0.04
		case inLava && (!e.Grounded || lDepth > thresh):
			if name == "magma_cube" {
				e.MotionY = 0.22 + float64(cubeSizeOf(e))*0.05
			} else {
				e.MotionY += 0.04
			}
		case (e.Grounded || (b1 && h <= thresh)) && e.NoJumpDelay == 0:
			jump := 0.42
			if j, ok := entityAttrBases[name]["jump_strength"]; ok {
				jump = j
			}
			if name == "magma_cube" {
				jump += float64(cubeSizeOf(e)) * 0.1
			}
			e.MotionY = jump
			e.NoJumpDelay = 10
			e.NeedsSync = true
		}
	} else {
		e.NoJumpDelay = 0
	}
	e.Jumping = false
	if e.NoJumpDelay > 0 {
		e.NoJumpDelay--
	}
	// CubeMobAttackGoal: the targeting goals give a player inside 10
	// blocks with |dy| <= 4 (or an iron golem); the heading locks on
	// and the jump pace triples while the cube deals damage.
	e.CubeAggro = false
	if tx, ty, tz, ok := s.cubeTarget(e); ok {
		_ = ty
		e.CubeHeading = float32(math.Atan2(-(tx-e.X), tz-e.Z) * 180 / math.Pi)
		e.CubeAggro = cubeDealsDamage(e, name)
	} else if e.Grounded || inFluid {
		// CubeMobRandomDirectionGoal: while targetless and grounded
		// (or floating), repick the heading every 40-99 ticks.
		e.CubeDirTimer--
		if e.CubeDirTimer <= 0 {
			h := uint64(uint32(e.ID))*2654435761 ^ uint64(e.CubeDirSeq)*6364136223846793005
			e.CubeDirTimer = 40 + int32(h%60)
			e.CubeHeading = float32((h >> 20) % 360)
			e.CubeDirSeq++
		}
	}
	// CubeMobMoveControl.tick: rotlerp the body yaw toward the
	// heading 90°/tick; head and body follow.
	mobTurnDeg(e, e.CubeHeading, 90)
	e.HeadYaw = e.Yaw
	ms := 0.2 + 0.1*float64(cubeSizeOf(e))
	drive := speedMod * ms
	if e.Grounded {
		e.CubeJumpDelay--
		if e.CubeJumpDelay <= 0 {
			d := cubeJumpDelayFor(e.ID, e.CubeJumpSeq, name)
			e.CubeJumpSeq++
			if e.CubeAggro {
				d /= 3
			}
			e.CubeJumpDelay = d
			// JumpControl.jump(): the flag's jumpFromGround lands
			// the same tick — vy = getJumpPower (+size*0.1 magma).
			vy := 0.42
			if j, ok := entityAttrBases[name]["jump_strength"]; ok {
				vy = j
			}
			if name == "magma_cube" {
				vy += float64(cubeSizeOf(e)) * 0.1
			}
			e.MotionY = vy
			e.NeedsSync = true
			s.cubeSound(e, name, "jump")
			// This tick still drives on the ground: setSpeed(drive)
			// leaves zza=drive (input normalizes only above 1, so a
			// sub-1 drive stays sub-1) at ground-friction influence.
			s.mobTravelStep(e, w, drive)
		} else {
			// Between hops: xxa=zza=0 — the move control zeroes the
			// inputs outright rather than letting them decay.
			e.WalkZZA = 0
			e.WalkXXA = 0
			s.mobTravelStep(e, w, 0)
		}
	} else {
		// Airborne: MOVE_TO stays armed, so zza keeps being re-issued
		// at the drive each tick — the air influence is
		// getFlyingSpeed, a flat 0.02 for a mob.
		s.mobTravelStep(e, w, drive)
	}
	// wasOnGround edge after travel: a landing cube bursts
	// size*16 particles in a width ring and plays the squish.
	if e.Grounded && !e.CubeWasGround {
		s.cubeLand(e, name, bounds)
	}
	e.CubeWasGround = e.Grounded
	// playerTouch: an overlapping player takes the contact damage.
	s.cubeTouchDamage(e, name, bounds)
}

// mobTurnDeg is MoveControl's rotlerp(yRot, heading, maxDeg) — the
// heading is an absolute yaw in vanilla degrees.
func mobTurnDeg(e *Entity, heading float32, maxDeg float64) {
	d := heading - e.Yaw
	for d > 180 {
		d -= 360
	}
	for d < -180 {
		d += 360
	}
	if d > float32(maxDeg) {
		d = float32(maxDeg)
	} else if d < float32(-maxDeg) {
		d = float32(-maxDeg)
	}
	e.Yaw += d
}

// cubeTarget is the NearestAttackableTargetGoal pair: the nearest
// survival player inside 10 blocks with |dy| <= 4, else the nearest
// iron golem inside follow_range.
func (s *Server) cubeTarget(e *Entity) (x, y, z float64, ok bool) {
	best := 100.0
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dead ||
			sess.dimName() != e.Dim {
			return true
		}
		if m := sess.mode(); m != GameModeSurvival && m != GameModeAdventure {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		dx, dz := px-e.X, pz-e.Z
		if d2 := dx*dx + dz*dz; d2 < best && math.Abs(py-e.Y) <= 4.0 {
			best = d2
			x, y, z, ok = px, py, pz, true
		}
		return true
	})
	if ok {
		return x, y, z, true
	}
	// Iron golem — the secondary NearestAttackableTargetGoal.
	best = 256.0 // follow_range 16
	s.entities.mu.RLock()
	for _, o := range s.entities.byID {
		if o.Dim != e.Dim || o.Dying || o.Dormant {
			continue
		}
		on, _ := EntityTypeName(o.Type)
		if on != "iron_golem" {
			continue
		}
		dx, dz := o.X-e.X, o.Z-e.Z
		if d2 := dx*dx + dz*dz; d2 < best {
			best = d2
			x, y, z, ok = o.X, o.Y, o.Z, true
		}
	}
	s.entities.mu.RUnlock()
	return x, y, z, ok
}

// cubeSound plays the jump/squish family sounds: getSoundVolume is
// 0.4*size and the pitch rolls ((r-r)*0.2+1)*(tiny?1.4:0.8)/0.8 for
// the squish. deterministic hash stands in for the RNG.
func (s *Server) cubeSound(e *Entity, name, kind string) {
	tiny := cubeSizeOf(e) <= 1
	var snd string
	switch {
	case name == "magma_cube" && kind == "jump":
		snd = "entity.magma_cube.jump"
	case name == "magma_cube" && kind == "squish" && tiny:
		snd = "entity.magma_cube.squish_small"
	case name == "magma_cube":
		snd = "entity.magma_cube.squish"
	case kind == "jump" && tiny:
		snd = "entity.slime.jump_small"
	case kind == "jump":
		snd = "entity.slime.jump"
	case tiny:
		snd = "entity.slime.squish_small"
	default:
		snd = "entity.slime.squish"
	}
	id, ok := SoundEventID(snd)
	if !ok {
		return
	}
	vol := 0.4 * float32(cubeSizeOf(e))
	h := uint64(uint32(e.ID))*40503 ^ uint64(e.CubeJumpSeq+int64(len(kind)))*1099511628211
	r1 := float64(h%1000) / 1000
	r2 := float64((h>>20)%1000) / 1000
	pitch := float32((r1-r2)*0.2 + 1.0)
	if tiny {
		pitch *= 1.4
	} else {
		pitch *= 0.8
	}
	if kind == "squish" {
		pitch /= 0.8
	}
	// cube mobs route HOSTILE, not the ambient NEUTRAL.
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, srcHostile, vol, pitch, nil)
}

// cubeLand is AbstractCubeMob.tick's onGround edge: size*16
// individual addParticle calls in a width-radius ring plus the
// squish sound.
func (s *Server) cubeLand(e *Entity, name string, bounds [2]float64) {
	size := bounds[0] * 2.0
	radius := size / 2.0
	ptype := "item_slime"
	if name == "magma_cube" {
		ptype = "flame"
	}
	for i := 0; float64(i) < size*16; i++ {
		h := uint64(uint32(e.ID))*2654435761 ^ uint64(i+1)*6364136223846793005 ^
			uint64(e.CubeJumpSeq+1)*1099511628211
		dir := float64(h%6283) / 1000.0
		d := 0.5 + float64((h>>20)%1000)/2000.0
		xd := math.Sin(dir) * radius * d
		zd := math.Cos(dir) * radius * d
		s.sendLevelParticles(e.Dim, ptype, e.X+xd, e.Y, e.Z+zd, 0, 0, 0, 0, 1)
	}
	s.cubeSound(e, name, "squish")
}

// cubeTouchDamage is AbstractCubeMob.playerTouch + dealDamage: when
// the boxes overlap, isDealsDamage cubes hurt the player on their
// mob_attack damage source and play SLIME_ATTACK.
func (s *Server) cubeTouchDamage(e *Entity, name string, bounds [2]float64) {
	if !cubeDealsDamage(e, name) {
		return
	}
	dmg := float32(cubeSizeOf(e))
	if name == "magma_cube" {
		dmg += 2.0
	}
	half, height := bounds[0]/2, bounds[1]
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dead ||
			sess.dimName() != e.Dim {
			return true
		}
		if m := sess.mode(); m != GameModeSurvival && m != GameModeAdventure {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		// Vanilla calls playerTouch from the entity-contact push —
		// strict bounding-box overlap (attack_range 0).
		if px+0.3 < e.X-half || px-0.3 > e.X+half ||
			pz+0.3 < e.Z-half || pz-0.3 > e.Z+half ||
			py+1.8 < e.Y || py-0.5 > e.Y+height {
			return true
		}
		dealt := s.damageWith(sess, dmg, translatable("death.attack.mob",
			literal(sess.getName()), literal(mobDisplayName(name))), damageSource{
			typeName: "mob_attack",
			cause:    e.ID,
			direct:   e.ID,
		})
		if dealt > 0 {
			if id, ok := SoundEventID("entity.slime.attack"); ok {
				s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, srcHostile, 1.0, 1.0, nil)
			}
		}
		return true
	})
}

// cubeMobSplit is AbstractCubeMob.remove's SPLIT_ON_DEATH half: a
// size>1 cube dying spawns getSplitCount() = 2+rand(3) children of
// half its size on a 2x2 grid — (i%2-0.5, i/2-0.5)·width/2 where the
// width is getType().getDimensions().scale(size).width() = 0.52·size
// — each snapped to +0.5y and a random [0,360) yaw. The children are
// fresh entities (TRIGGERED spawn — no natural-spawn rolls) carrying
// convertCommon's field set: absorption, active effects, baby + age,
// left-handedness, NoAI, persistence, name visibility, fire,
// invulnerable, NoGravity, portal cooldown, silent, and the custom
// name. Equipment, position, motion, passengers and leash do not
// carry on the split path.
func (s *Server) cubeMobSplit(e *Entity) {
	size := cubeSizeOf(e)
	if size <= 1 {
		return
	}
	name, ok := EntityTypeName(e.Type)
	if !ok || (name != "slime" && name != "magma_cube" && name != "sulfur_cube") {
		return
	}
	half := size / 2
	// xd/zd = (i%2-0.5)·scaledWidth/2 — the table's 1.04 is the
	// size-2 box, so scaledWidth = mobBounds·size/2 and the grid
	// half-step is another /2: size 4 → ±0.52, size 2 → ±0.26.
	off := mobBounds(e.Type)[0] * float64(size) / 4
	count := 2 + rand.Intn(3)
	for i := 0; i < count; i++ {
		ne := &Entity{
			Type:      e.Type,
			Dim:       e.Dim,
			Living:    e.Living,
			X:         e.X + (float64(i%2)-0.5)*off,
			Y:         e.Y + 0.5,
			Z:         e.Z + (float64(i/2)-0.5)*off,
			Yaw:       rand.Float32() * 360,
			CubeSize:  half,
			Health:    float32(half * half),
			MaxHealth: float32(half * half),
			// convertCommon's carry list.
			Baby:                e.Baby,
			Age:                 e.Age,
			LeftHanded:          e.LeftHanded,
			NoAI:                e.NoAI,
			PersistenceRequired: e.PersistenceRequired,
			CustomNameVisible:   e.CustomNameVisible,
			FireTicks:           e.FireTicks,
			Invulnerable:        e.Invulnerable,
			NoGravity:           e.NoGravity,
			PortalCooldown:      e.PortalCooldown,
			Silent:              e.Silent,
			CustomName:          e.CustomName,
			CustomNameComp:      e.CustomNameComp,
			Dormant:             e.Dormant,
		}
		ne.AttrBase = map[string]float64{"max_health": float64(half * half)}
		if len(e.Effects) > 0 {
			ne.Effects = make(map[int32]mobEffectInst, len(e.Effects))
			for k, v := range e.Effects {
				ne.Effects[k] = v
			}
		}
		ne.AbsorptionAmount = e.AbsorptionAmount
		if max := float32(attrValue(mobAttrBase(ne, "max_absorption"), ne.AttrMods["max_absorption"])); ne.AbsorptionAmount > max {
			ne.AbsorptionAmount = max
		}
		s.entities.Spawn(ne)
		s.broadcastFreshMob(ne)
	}
}
