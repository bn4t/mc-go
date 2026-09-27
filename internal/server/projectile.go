package server

// Player-fired arrows: the bow draws on Use Item, releases on the
// Player Action RELEASE_USE_ITEM status, and the arrow entity flies
// with vanilla's projectile physics — velocity decays 0.99 airborne,
// gravity pulls 0.05 per tick — until it strikes a living entity.
// Arrow hits run the normal damage paths so player_hurt_entity and the
// projectile killing-blow advancements fire off real state.

import (
	"math"
	"math/rand"
	"strings"

	"mc-go/internal/world"
)

// bowUseTicks is the bow's use duration floor: vanilla's
// BowItem.getPowerForTime discounts releases under 3 ticks to a dud.
const bowUseTicks = 3

// arrowGravity / arrowAirDrag are vanilla AbstractArrow's per-tick
// physics: Y slows 0.05 then every axis scales 0.99.
const (
	arrowGravity = 0.05
	arrowAirDrag = 0.99
)

// projectileTickTypes — the entity names arrowTickStep integrates:
// player-fired plus dispenser-spawned projectiles. Wind charges run
// their own step (windChargeTickStep) because they burst, not stick.
var projectileTickTypes = map[string]bool{
	"arrow": true, "spectral_arrow": true, "trident": true,
	"splash_potion": true, "lingering_potion": true,
	"egg": true, "snowball": true, "experience_bottle": true,
	"small_fireball": true, "firework_rocket": true, "ender_pearl": true,
	"wither_skull": true, "fireball": true, "dragon_fireball": true,
	"shulker_bullet": true,
}

// bowStartTick timestamps a draw begun by Use Item on a bow so the
// release can measure the charge; zero means not drawing.
func (sess *Session) drawingBow() bool { return sess.bowStartTick != 0 }

// releaseBow fires an arrow when the player stops using a drawn bow.
// Vanilla's power curve is t*(t+2)/20 capped at 1.0 over ~20 ticks of
// draw; under the floor the release is silent.
func (s *Server) releaseBow(sess *Session) {
	tick := s.TickCount()
	held := sess.bowStartTick != 0
	dt := tick - sess.bowStartTick
	sess.bowStartTick = 0
	if !held || dt < bowUseTicks {
		return
	}
	st := sess.heldStack()
	if name, ok := itemNameOf(st.ID); !ok || name != "bow" {
		return
	}
	power := float64(dt) * (float64(dt) + 2.0) / 20.0
	if power > 1.0 {
		power = 1.0
	}
	if power < 0.1 {
		return
	}
	// Survival consumes an arrow; without one the draw fizzles (vanilla
	// aborts the release on the client side the same way).
	if sess.mode() == GameModeSurvival && !sess.takeOneOf("arrow") {
		return
	}
	x, y, z, yaw, pitch, _ := sess.Pos()
	// Projectile.spawnProjectile posts PROJECTILE_SHOOT at the
	// shooter's feet — sneak-suppressible.
	s.emitGameEvent(sess.dimension(), "projectile_shoot", x, y, z, sess, "")
	y += 1.52 // vanilla spawns at eye height minus a bow-length offset
	s.spawnArrow(sess, x, y, z, yaw, pitch, power*3.0)
	s.wearHeld(sess, 1)
	sess.addItemStat(statUsed, st.ID, 1)
}

// crossbowChargeTicks is vanilla's CrossbowItem.DEFAULT_CHARGE_TIME:
// 1.25 seconds of holding use.
const crossbowChargeTicks = 25

// useCrossbow runs CrossbowItem.use: a charged crossbow fires
// immediately; an uncharged one starts its 25-tick charge (survival
// needs a projectile on hand, creative charges regardless). The charge
// completes lazily — the flag is checked against the start tick, the
// way vanilla's releaseUsing only arms the stack after the use
// duration elapses.
func (s *Server) useCrossbow(sess *Session) {
	if sess.crossbowCharged {
		s.fireCrossbow(sess)
		return
	}
	if sess.mode() == GameModeSurvival && !sess.hasItem("arrow") {
		return
	}
	sess.crossbowStartTick = s.TickCount()
}

// fireCrossbow shoots the stored projectile: arrow at the vanilla
// crossbow speed 3.15, a point of durability, the item stat, and
// shot_crossbow (adventure/ol_betsy) with the crossbow itself as the
// used item.
func (s *Server) fireCrossbow(sess *Session) {
	sess.crossbowCharged = false
	sess.crossbowStartTick = 0
	if sess.mode() == GameModeSurvival && !sess.takeOneOf("arrow") {
		return
	}
	st := sess.heldStack()
	x, y, z, yaw, pitch, _ := sess.Pos()
	y += 1.52
	if e := s.spawnArrow(sess, x, y, z, yaw, pitch, 3.15); e != nil {
		e.arrowWeapon = "crossbow"
	}
	s.wearHeld(sess, 1)
	sess.addItemStat(statUsed, st.ID, 1)
	s.fireShotCrossbow(sess, "crossbow")
}

// releaseCrossbow cancels an incomplete charge; a finished charge
// survives the release (vanilla arms the stack mid-hold).
func (s *Server) releaseCrossbow(sess *Session) {
	if sess.crossbowStartTick != 0 && s.TickCount()-sess.crossbowStartTick >= crossbowChargeTicks {
		sess.crossbowCharged = true
	}
	sess.crossbowStartTick = 0
}

// spawnArrow creates the arrow entity travelling at speed blocks/tick
// along the look vector. Entity.Data is the shooter id + 1 — vanilla's
// spawn-packet owner field.
func (s *Server) spawnArrow(shooter *Session, x, y, z float64, yaw, pitch float32, speed float64) *Entity {
	yawR := float64(yaw) * math.Pi / 180
	pitchR := float64(pitch) * math.Pi / 180
	dx := -math.Sin(yawR) * math.Cos(pitchR)
	dy := -math.Sin(pitchR)
	dz := math.Cos(yawR) * math.Cos(pitchR)
	e := s.entities.Spawn(&Entity{
		Type: mustEntityType("arrow"),
		Dim:  shooter.dimName(),
		X:    x, Y: y, Z: z,
		Yaw: yaw, Pitch: pitch,
		MotionX: dx * speed, MotionY: dy * speed, MotionZ: dz * speed,
		VX:   int16(dx * speed * 8000),
		VY:   int16(dy * speed * 8000),
		VZ:   int16(dz * speed * 8000),
		Data: shooter.entityID.Load(),
	})
	e.arrowShooter = shooter.entityID.Load()
	e.arrowWeapon = "bow"
	s.broadcastEntitySpawn(nil, *e)
	return e
}

// arrowTickStep advances flying arrows and thrown tridents one tick:
// integrate, apply drag and gravity, then collide against living mobs
// and players. A struck target takes the projectile's damage; the
// projectile is spent. Stuck tridents try their owner pickup here.
func (s *Server) arrowTickStep() {
	s.entities.mu.RLock()
	arrows := make([]*Entity, 0, 4)
	for _, e := range s.entities.byID {
		if e.Dying {
			continue
		}
		if name, ok := EntityTypeName(e.Type); ok && projectileTickTypes[name] {
			arrows = append(arrows, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range arrows {
		name, _ := EntityTypeName(e.Type)
		isTrident := name == "trident"
		isPotion := name == "splash_potion" || name == "lingering_potion"
		// Arrows, spectral arrows and tridents stick in the block they
		// hit; the thrown items and hurting projectiles just despawn.
		sticks := name == "arrow" || name == "spectral_arrow" || isTrident
		if e.arrowStuck || e.tridentStuck {
			if isTrident {
				s.tridentPickupStep(e)
			}
			continue
		}
		if name == "firework_rocket" {
			// FireworkRocketEntity.tick runs its own whole path —
			// launch sound, attached-entity riding, free-flight
			// accel, fuse-end explode — none of the generic
			// move/gravity/collide code applies.
			s.fireworkTick(e)
			continue
		}
		// ShulkerBullet has no gravity — the wander lerp owns the
		// whole velocity vector (and must run before the move).
		if name == "shulker_bullet" {
			s.shulkerBulletSteer(e)
		}
		ox, oy, oz := e.X, e.Y, e.Z
		e.X += e.MotionX
		e.Y += e.MotionY
		e.Z += e.MotionZ
		// ThrowableProjectile's default gravity is 0.03 vs the
		// arrow's 0.05; hurting projectiles and rockets fly flat
		// (no gravity). The 0.99 air drag is shared.
		grav := arrowGravity
		switch name {
		case "egg", "snowball", "splash_potion", "lingering_potion", "ender_pearl":
			grav = 0.03
		case "experience_bottle":
			// ThrownExperienceBottle.getDefaultGravity — the bottle
			// arcs down harder than the other thrown items.
			grav = 0.07
		case "small_fireball", "wind_charge", "wither_skull", "fireball", "dragon_fireball":
			grav = 0
		case "shulker_bullet":
			// ShulkerBullet.getDefaultGravity — the wander lerp rides
			// on top of a real gravity pull.
			grav = 0.04
		}
		e.MotionY -= grav
		e.MotionX *= arrowAirDrag
		e.MotionY *= arrowAirDrag
		e.MotionZ *= arrowAirDrag
		s.broadcastEntityTeleport(nil, e.ID, e.X, e.Y, e.Z, e.Yaw, e.Pitch, false)
		// Vanilla clips each target's inflated AABB against the whole
		// tick's movement segment — a point check at the new tip tunnels
		// straight through at full draw speed (3 blocks/tick).
		var collided bool
		switch {
		case isPotion:
			collided = s.potionCollide(e, ox, oy, oz)
		case isTrident:
			collided = s.projectileCollide(e, ox, oy, oz, tridentHitDamage, "trident")
		case sticks:
			collided = s.arrowCollide(e, ox, oy, oz)
		case name == "egg" || name == "snowball" ||
			name == "experience_bottle" || name == "ender_pearl":
			// ThrowableItemProjectile.onHitEntity — hurt(0): the hit
			// flashes the hurt animation and knockback but deals
			// nothing (snowballs do 3 to blazes). The ender pearl runs
			// the hurt twice — the grace eats the second.
			collided = s.thrownCollide(e, ox, oy, oz, name)
		case name == "small_fireball":
			// SmallFireball.onHitEntity — ignite 5s + hurt(5.0)
			// through the fireball damage type.
			collided = s.fireballCollide(e, ox, oy, oz)
		case name == "wither_skull":
			// WitherSkull.onHitEntity — living owner: hurt 8.0
			// wither_skull; ownerless: hurt 5.0 magic; a landed hit
			// then applies the wither effect by difficulty.
			collided = s.witherSkullCollide(e, ox, oy, oz)
		case name == "fireball":
			// LargeFireball.onHitEntity — hurt 6.0 through
			// fireball(ball, owner); ownerless wires cause 0.
			collided = s.largeFireballCollide(e, ox, oy, oz)
		case name == "dragon_fireball":
			// DragonFireball: no direct hurt — the entity hit only
			// triggers the AreaEffectCloud spawn (base onHitEntity
			// is empty).
			collided = s.dragonFireballCollide(e, ox, oy, oz)
		case name == "shulker_bullet":
			// ShulkerBullet.onHitEntity — mobProjectile 4.0 +
			// LEVITATION 200t when the hurt lands.
			collided = s.shulkerBulletCollide(e, ox, oy, oz)
		}
		// Hurting projectiles (fireball/wind_charge/rocket) keep
		// their own impact paths; the block hit below still applies.
		if collided && isTrident {
			// The channeling enchantment's post_attack effect is
			// data-driven in 26.2: thundering + can_see_sky at the
			// victim summons a bolt at the struck point.
			s.channelingStrike(e.arrowShooter, e, e.X, e.Y, e.Z)
		}
		if collided {
			// Projectile.onHit → gameEvent(PROJECTILE_LAND) at the
			// hit result's location.
			if d := s.dimensionByName(e.Dim); d != nil {
				s.emitGameEvent(d, "projectile_land", e.X, e.Y, e.Z, nil, "")
			}
			if isPotion {
				s.potionSplash(e, e.X, e.Y, e.Z)
				if name == "lingering_potion" {
					s.spawnLingeringCloud(e)
				}
			}
			if name == "egg" || name == "snowball" {
				s.thrownItemImpact(e, name, false)
			}
			if name == "experience_bottle" {
				// entity hit: direction = -deltaMovement
				s.xpBottleImpact(e, -e.MotionX, -e.MotionY, -e.MotionZ)
			}
			if name == "ender_pearl" {
				s.enderPearlImpact(e, ox, oy, oz)
			}
			if name == "wither_skull" {
				s.witherSkullExplode(e)
			}
			if name == "fireball" {
				s.largeFireballExplode(e)
			}
			if name == "dragon_fireball" {
				s.dragonFireballImpact(e)
			}
			s.entities.Remove(e.ID)
			s.broadcastEntityRemove([]int32{e.ID})
			continue
		}
		// Blocks are tested second, matching vanilla's hit order —
		// a living hit consumes the arrow before the wall check runs.
		if bx, by, bz, axis, hx, hy, hz, hit := s.arrowBlockHit(e, ox, oy, oz); hit {
			e.X, e.Y, e.Z = hx, hy, hz
			e.MotionX, e.MotionY, e.MotionZ = 0, 0, 0
			if d := s.dimensionByName(e.Dim); d != nil {
				s.emitGameEvent(d, "projectile_land", hx, hy, hz, nil, "")
			}
			if isPotion {
				s.potionSplash(e, hx, hy, hz)
				if name == "lingering_potion" {
					s.spawnLingeringCloud(e)
				}
				s.entities.Remove(e.ID)
				s.broadcastEntityRemove([]int32{e.ID})
				continue
			}
			if name == "small_fireball" && s.fireballIgnites(e) {
				// SmallFireball.onHitBlock: ignite unless the owner is
				// a Mob and mob_griefing is off — dispenser shots have
				// no owner, so they always ignite. The face cell must
				// be empty — getState writes the plain fire state.
				fp := world.Pos{X: bx, Y: by, Z: bz}
				switch axis {
				case 0:
					fp.X += fireballHitDir(hx, bx)
				case 1:
					fp.Y += fireballHitDir(hy, by)
				case 2:
					fp.Z += fireballHitDir(hz, bz)
				}
				if d := s.dimensionByName(e.Dim); d != nil && d.w.GetBlock(fp) == world.Air {
					if nb, ok := world.BlockStateOf("fire", nil); ok {
						s.setSilentBlock(d, fp, nb)
						s.rsNeighborFanOut(d, fp)
					}
				}
			}
			if !sticks {
				// Egg/snowball/xp bottle/fireball/rocket: the block hit
				// is terminal — the entity despawns.
				if name == "egg" || name == "snowball" {
					s.thrownItemImpact(e, name, axis == 1)
				}
				if name == "experience_bottle" {
					// awardWithDirection gets the hit face's unit normal.
					dx, dy, dz := 0.0, 0.0, 0.0
					switch axis {
					case 0:
						dx = float64(fireballHitDir(hx, bx))
					case 1:
						dy = float64(fireballHitDir(hy, by))
					case 2:
						dz = float64(fireballHitDir(hz, bz))
					}
					s.xpBottleImpact(e, dx, dy, dz)
				}
				if name == "ender_pearl" {
					s.enderPearlImpact(e, ox, oy, oz)
				}
				if name == "wither_skull" {
					s.witherSkullExplode(e)
				}
				if name == "fireball" {
					s.largeFireballExplode(e)
				}
				if name == "dragon_fireball" {
					s.dragonFireballImpact(e)
				}
				if name == "shulker_bullet" {
					s.shulkerBulletBlockHit(e)
				}
				s.entities.Remove(e.ID)
				s.broadcastEntityRemove([]int32{e.ID})
				continue
			}
			if isTrident {
				e.tridentStuck = true
				s.playSoundSrc(e.Dim, hx, hy, hz, 1626, 6, 1.0, 1.0, nil) // item.trident.hit_ground
			} else {
				e.arrowStuck = true
			}
			d := s.dimensionByName(e.Dim)
			if name, ok := blockNameAt(d, world.Pos{X: bx, Y: by, Z: bz}); ok {
				if name == "target" {
					signal := targetSignal(axis, hx, hy, hz)
					if shooter := s.sessionByEntityID(e.arrowShooter); shooter != nil {
						s.fireTargetHit(shooter, e, signal)
					}
				}
				// hit_block channels only onto a lightning rod, per
				// channeling.json's block predicate.
				if isTrident && name == "lightning_rod" {
					s.channelingStrike(e.arrowShooter, e, hx, hy, hz)
				}
			}
		}
	}
}

// tridentPickupStep gives a stuck trident back to the first player
// whose pickup box reaches it — vanilla's AbstractArrow.playerTouch.
// Creative-thrown tridents are Pickup.CREATIVE_ONLY and stay put.
func (s *Server) tridentPickupStep(e *Entity) {
	if e.tridentCreativeOnly {
		return
	}
	var target *Session
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dimName() != e.Dim {
			return true
		}
		if m := sess.mode(); m != GameModeSurvival && m != GameModeAdventure {
			return true
		}
		x, y, z, _, _, _ := sess.Pos()
		if abs(x-e.X) <= 1.425 && abs(z-e.Z) <= 1.425 && e.Y >= y-0.75 && e.Y <= y+2.3 {
			target = sess
			return false
		}
		return true
	})
	if target == nil {
		return
	}
	tid, _ := world.ItemIDs["trident"]
	if _, picked := target.addStack(ItemStack{ID: tid, Count: 1, Damage: e.tridentDamage}); !picked {
		return
	}
	s.broadcastTakeItem(e.Dim, e.ID, target.entityID.Load(), 1)
	s.entities.Remove(e.ID)
	s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
	target.addItemStat(statPickedUp, tid, 1)
}

// arrowBlockHit steps the tick's movement segment in small increments
// and reports the first solid block entered: its cell, the axis of the
// face the ray crossed (0=x,1=y,2=z), and the exact hit point on that
// face. Face detection reuses the slab test against the unit cube.
func (s *Server) arrowBlockHit(e *Entity, ox, oy, oz float64) (bx, by, bz, axis int, hx, hy, hz float64, ok bool) {
	d := s.dimensionByName(e.Dim)
	if d == nil {
		return
	}
	dx, dy, dz := e.X-ox, e.Y-oy, e.Z-oz
	length := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if length == 0 {
		return
	}
	nx, ny, nz := dx/length, dy/length, dz/length
	steps := int(length/0.05) + 1
	prev := world.Pos{X: int(math.Floor(ox)), Y: int(math.Floor(oy)), Z: int(math.Floor(oz))}
	for i := 1; i <= steps; i++ {
		t := length * float64(i) / float64(steps)
		px, py, pz := ox+nx*t, oy+ny*t, oz+nz*t
		p := world.Pos{X: int(math.Floor(px)), Y: int(math.Floor(py)), Z: int(math.Floor(pz))}
		if p == prev {
			continue
		}
		prev = p
		if _, has := blockNameAt(d, p); !has || world.NonSolid(d.w.GetBlock(p)) {
			continue
		}
		bx, by, bz = p.X, p.Y, p.Z
		// Re-clip the full segment against the unit cube for the exact
		// entry point; the face is whichever bound the point sits on.
		ent := rayAABB(ox, oy, oz, nx, ny, nz,
			float64(bx), float64(by), float64(bz),
			float64(bx+1), float64(by+1), float64(bz+1))
		if ent < 0 || ent > length {
			ent = t - 0.05
			if ent < 0 {
				ent = 0
			}
		}
		hx, hy, hz = ox+nx*ent, oy+ny*ent, oz+nz*ent
		switch {
		case math.Abs(hx-float64(bx)) < 1e-6 || math.Abs(hx-float64(bx+1)) < 1e-6:
			axis = 0
		case math.Abs(hy-float64(by)) < 1e-6 || math.Abs(hy-float64(by+1)) < 1e-6:
			axis = 1
		default:
			axis = 2
		}
		// Nudge the arrow a hair back out of the face so it rests on
		// the surface rather than inside it.
		const eps = 0.001
		hx -= nx * eps
		hy -= ny * eps
		hz -= nz * eps
		ok = true
		return
	}
	return
}

// targetSignal is vanilla TargetBlock.getRedstoneStrength: the
// fractional distance from the block center in the two axes of the hit
// face, strength = max(1, ceil(15·(0.5−d)/0.5)).
func targetSignal(axis int, hx, hy, hz float64) int {
	frac := func(v float64) float64 { return math.Abs((v - math.Floor(v)) - 0.5) }
	var d float64
	switch axis {
	case 0:
		d = math.Max(frac(hy), frac(hz))
	case 1:
		d = math.Max(frac(hx), frac(hz))
	default:
		d = math.Max(frac(hx), frac(hy))
	}
	s := math.Max(1, float64(math.Ceil(15*((0.5-d)/0.5))))
	if s < 0 {
		s = 0
	}
	return int(s)
}

// blockNameAt resolves the block-name owning the state at p.
func blockNameAt(d *dimension, p world.Pos) (string, bool) {
	if d == nil {
		return "", false
	}
	return world.StateOwner(d.w.GetBlock(p))
}

// tridentHitDamage is ThrownTrident's flat BASE_DAMAGE — 8.0 hearts
// regardless of flight speed (impaling adds per level, unmodeled).
const tridentHitDamage = 8

// arrowCollide resolves the arrow's hit against every living entity and
// player in its dimension, applying damage on the first target whose
// bounding box the arrow tip enters. The shooter is exempt for the
// first few ticks so a released arrow cannot hit its owner.
func (s *Server) arrowCollide(e *Entity, ox, oy, oz float64) bool {
	return s.projectileCollide(e, ox, oy, oz, 0, "arrow")
}

// projectileCollide is the shared arrow/trident entity-collision path.
// flat > 0 pins the hit damage (tridents deal a constant 8); flat = 0
// uses the arrow's speed-scaled formula. typeName drives the damage
// type name — the is_projectile tag and the adv predicate read it.
func (s *Server) projectileCollide(e *Entity, ox, oy, oz float64, flat float32, typeName string) bool {
	shooter := s.sessionByEntityID(e.arrowShooter)
	damage := flat
	if damage <= 0 {
		damage = float32(math.Ceil(math.Sqrt(e.MotionX*e.MotionX+e.MotionY*e.MotionY+e.MotionZ*e.MotionZ) * 2.0))
		if damage < 1 {
			damage = 1
		}
	}
	hit := false
	s.entities.mu.RLock()
	targets := make([]*Entity, 0, len(s.entities.byID))
	for _, t := range s.entities.byID {
		targets = append(targets, t)
	}
	s.entities.mu.RUnlock()
	for _, t := range targets {
		if hit || !t.Living || t.Dying || t.ID == e.ID || t.ID == e.arrowShooter {
			continue
		}
		if t.Dim != e.Dim {
			continue
		}
		bb := entityBBDims(t)
		if segmentHitsBox(ox, oy, oz, e.X, e.Y, e.Z, t.X, t.Y, t.Z, bb[0], bb[1]) {
			hit = true
			dealt, _, died := s.damageMob(t, damage, typeName, shooter, &damageInfo{
				typeName: typeName,
				cause:    e.arrowShooter,
				direct:   e.ID,
			})
			if dealt > 0 {
				for _, eff := range e.arrowEffects {
					mobAddEffect(t, eff.id, 0, s.gameTime()+int64(eff.dur), s.gameTime())
				}
				if shooter != nil {
					s.firePlayerHurtEntity(shooter, typeName, true, damage, "", false)
					if died {
						shooter.addStat(statKilled, t.Type, 1)
						if tn, ok := EntityTypeName(t.Type); ok {
							vx, vz := t.X, t.Z
							ax, _, az, _, _, _ := shooter.Pos()
							s.firePlayerKilledEntityDamage(shooter, tn, vx, vz, ax, az, true, typeName, t.Equips[equipHelmet].Ominous)
							s.fireKillNearSculk(shooter, t.X, t.Y, t.Z, t.Dim)
							if e.arrowWeapon != "" {
								s.fireKilledByArrow(shooter, tn, e.arrowWeapon)
							}
						}
					}
				}
			}
		}
	}
	s.sessions.Range(func(_, v any) bool {
		if hit {
			return false
		}
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dead || sess.mode() != GameModeSurvival {
			return true
		}
		if sess.entityID.Load() == e.arrowShooter {
			return true
		}
		if sess.dimName() != e.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if segmentHitsBox(ox, oy, oz, e.X, e.Y, e.Z, px, py, pz, 0.6, 1.8) {
			hit = true
			if shooter != nil {
				dealt := s.damageWith(sess, damage, translatable("death.attack."+typeName,
					literal(sess.getName()), literal(shooter.getName())), damageSource{
					typeName: typeName,
					cause:    e.arrowShooter,
					direct:   e.ID,
				})
				if dealt > 0 {
					s.firePlayerHurtEntity(shooter, typeName, true, damage, "", false)
					for _, eff := range e.arrowEffects {
						s.playerGiveEffect(sess, eff.id, 0, eff.dur, false)
					}
				}
			} else {
				dealt := s.damageWith(sess, damage, translatable("death.attack."+typeName,
					literal(sess.getName())), damageSource{
					typeName: typeName,
					direct:   e.ID,
				})
				if dealt > 0 {
					for _, eff := range e.arrowEffects {
						s.playerGiveEffect(sess, eff.id, 0, eff.dur, false)
					}
				}
			}
		}
		return true
	})
	return hit
}

// segmentHitsBox is vanilla's AABB.clip: the movement segment is
// slab-tested against the entity box inflated by 0.3 — the expansion
// ProjectileUtil applies to hit targets.
func segmentHitsBox(sx, sy, sz, ex, ey, ez, bx, by, bz, w, h float64) bool {
	hw := w/2 + 0.3
	min := [3]float64{bx - hw, by - 0.3, bz - hw}
	max := [3]float64{bx + hw, by + h + 0.3, bz + hw}
	dx, dy, dz := ex-sx, ey-sy, ez-sz
	t0, t1 := 0.0, 1.0
	axes := [][2]float64{{sx, dx}, {sy, dy}, {sz, dz}}
	for i, a := range axes {
		if a[1] == 0 {
			if a[0] < min[i] || a[0] > max[i] {
				return false
			}
			continue
		}
		ta := (min[i] - a[0]) / a[1]
		tb := (max[i] - a[0]) / a[1]
		if ta > tb {
			ta, tb = tb, ta
		}
		if ta > t0 {
			t0 = ta
		}
		if tb < t1 {
			t1 = tb
		}
		if t0 > t1 {
			return false
		}
	}
	return true
}

// tridentUseTicks is TridentItem.releaseUsing's hold floor: under 10
// ticks the release is a silent no-op.
const tridentUseTicks = 10

// releaseTrident throws a charged trident on release_use_item. The
// stack takes one point of durability (hurtWithoutBreaking) before a
// single trident is consumed — and survives in the thrown entity for
// pickup. A stack whose next point would break refuses to throw
// (vanilla's nextDamageWillBreak gate). Creative throws keep the
// stack but mark the entity Pickup.CREATIVE_ONLY.
func (s *Server) releaseTrident(sess *Session) {
	tick := s.TickCount()
	held := sess.tridentStartTick != 0
	dt := tick - sess.tridentStartTick
	sess.tridentStartTick = 0
	if !held || dt < tridentUseTicks {
		return
	}
	slot := invSlotForHotbar(sess.held.Load())
	sess.invMu.Lock()
	st := sess.inv[slot]
	if name, ok := itemNameOf(st.ID); !ok || name != "trident" {
		sess.invMu.Unlock()
		return
	}
	maxDmg := damageableItems["trident"]
	creative := sess.mode() != GameModeSurvival && sess.mode() != GameModeAdventure
	carry := st.Damage
	if creative {
		// hasInfiniteMaterials: the stack is not worn or consumed.
	} else {
		if st.Damage+1 >= maxDmg {
			sess.invMu.Unlock()
			return
		}
		carry++
		st.Damage++
		st.Count--
		if st.Count <= 0 {
			sess.inv[slot] = ItemStack{}
		} else {
			sess.inv[slot] = st
		}
		sess.invState.Add(1)
	}
	sess.invMu.Unlock()
	if !creative {
		_ = s.sendContainerSlot(sess, slot, sess.stackAt(slot))
		s.fireInventoryChanged(sess)
	}
	x, y, z, yaw, pitch, _ := sess.Pos()
	y += 1.52
	e := s.spawnTrident(sess, x, y, z, yaw, pitch, 2.5)
	e.tridentDamage = carry
	e.tridentCreativeOnly = creative
	e.tridentEnch = st.Ench
	s.playSoundEntity(sess.dimName(), sess.entityID.Load(), 1631, 7, 1.0, 1.0, sess) // item.trident.throw, PLAYERS source
	sess.addItemStat(statUsed, st.ID, 1)
}

// spawnTrident creates the thrown_trident entity at 2.5 blocks/tick
// along the look vector — vanilla's spawnProjectileFromRotation
// constants. Entity.Data is the shooter id + 1.
func (s *Server) spawnTrident(shooter *Session, x, y, z float64, yaw, pitch float32, speed float64) *Entity {
	yawR := float64(yaw) * math.Pi / 180
	pitchR := float64(pitch) * math.Pi / 180
	dx := -math.Sin(yawR) * math.Cos(pitchR)
	dy := -math.Sin(pitchR)
	dz := math.Cos(yawR) * math.Cos(pitchR)
	e := s.entities.Spawn(&Entity{
		Type: mustEntityType("trident"),
		Dim:  shooter.dimName(),
		X:    x, Y: y, Z: z,
		Yaw: yaw, Pitch: pitch,
		MotionX: dx * speed, MotionY: dy * speed, MotionZ: dz * speed,
		VX:   int16(dx * speed * 8000),
		VY:   int16(dy * speed * 8000),
		VZ:   int16(dz * speed * 8000),
		Data: shooter.entityID.Load(),
	})
	e.arrowShooter = shooter.entityID.Load()
	s.broadcastEntitySpawn(nil, *e)
	return e
}

// channelingStrike runs the channeling enchantment's strike once the
// trident's entity- or block-hit resolves: the 26.2 channeling.json
// effect is summon_entity lightning_bolt gated by weather_check
// thundering + can_see_sky at the hit position. The spawned bolt then
// does what every vanilla bolt does on its first tick — collects the
// living entities inside its ±3 box, thunderHits them (5 damage; the
// fire component is unmodeled), fires lightning_strike for the
// bystanders, and fires channeled_lightning on the trident's owner
// with the struck set as victims.
func (s *Server) channelingStrike(ownerID int32, e *Entity, x, y, z float64) {
	shooter := s.sessionByEntityID(ownerID)
	if shooter == nil || (ItemStack{Ench: e.tridentEnch}).enchLevel("channeling") <= 0 {
		return
	}
	if !s.isThundering() {
		return
	}
	d := s.dimensionByName(e.Dim)
	if !bobberSeesSky(d, world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}) {
		return
	}
	bolt := s.entities.Spawn(&Entity{
		Type: mustEntityType("lightning_bolt"),
		Dim:  e.Dim,
		X:    x, Y: y, Z: z,
	})
	s.broadcastEntitySpawn(nil, *bolt)
	var victims []string
	for _, ts := range s.entities.All() {
		if ts.ID == bolt.ID || ts.Dim != e.Dim || ts.Dying || !ts.Living {
			continue
		}
		if math.Abs(ts.X-x) > 3 || math.Abs(ts.Y-y) > 3 || math.Abs(ts.Z-z) > 3 {
			continue
		}
		t, ok := s.entities.GetPtr(ts.ID)
		if !ok {
			continue
		}
		s.damageMob(t, 5, "lightning_bolt", shooter, &damageInfo{
			typeName: "lightning_bolt",
			cause:    bolt.ID,
			direct:   bolt.ID,
		})
		if tn, ok := EntityTypeName(t.Type); ok {
			victims = append(victims, tn)
		}
	}
	s.fireLightningStrike(d, x, y, z, 1)
	s.fireChanneledLightning(shooter, victims)
	s.entities.Remove(bolt.ID)
	s.broadcastEntityRemoveIn(e.Dim, nil, []int32{bolt.ID})
}

// hasItem reports whether the inventory holds the named item.
// itemNameOf returns the prefixed minecraft: name; callers pass the
// bare name, so compare on the stripped form.
func (sess *Session) hasItem(item string) bool {
	sess.invMu.Lock()
	defer sess.invMu.Unlock()
	for i := range sess.inv {
		it := sess.inv[i]
		if it.Empty() {
			continue
		}
		if n, ok := itemNameOf(it.ID); ok && strings.TrimPrefix(n, "minecraft:") == item {
			return true
		}
	}
	return false
}

// takeOneOf removes one item by name from the inventory, preferring
// the offhand then scanning. Reports whether an item was found.
func (sess *Session) takeOneOf(item string) bool {
	sess.invMu.Lock()
	defer sess.invMu.Unlock()
	for i := range sess.inv {
		it := sess.inv[i]
		if it.Empty() {
			continue
		}
		if n, ok := itemNameOf(it.ID); ok && strings.TrimPrefix(n, "minecraft:") == item {
			sess.inv[i].Count--
			if sess.inv[i].Count <= 0 {
				sess.inv[i] = ItemStack{}
			}
			sess.invState.Add(1)
			return true
		}
	}
	return false
}

// potionCollide is ThrowableItemProjectile's entity hit: the first
// living entity along the tick's segment (the thrower excluded —
// Projectile.canHitEntity skips the owner until it leaves). A potion
// never damages on contact; the caller splashes.
func (s *Server) potionCollide(e *Entity, ox, oy, oz float64) bool {
	s.entities.mu.RLock()
	targets := make([]*Entity, 0, len(s.entities.byID))
	for _, t := range s.entities.byID {
		targets = append(targets, t)
	}
	s.entities.mu.RUnlock()
	for _, t := range targets {
		if !t.Living || t.Dying || t.ID == e.ID || t.ID == e.arrowShooter || t.Dim != e.Dim {
			continue
		}
		bb := entityBBDims(t)
		if segmentHitsBox(ox, oy, oz, e.X, e.Y, e.Z, t.X, t.Y, t.Z, bb[0], bb[1]) {
			return true
		}
	}
	hit := false
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dead ||
			sess.mode() == GameModeSpectator || sess.dimName() != e.Dim ||
			sess.entityID.Load() == e.arrowShooter {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if segmentHitsBox(ox, oy, oz, e.X, e.Y, e.Z, px, py, pz, 0.6, 1.8) {
			hit = true
			return false
		}
		return true
	})
	return hit
}

// splashEffect is one potion-content entry: effect name, base
// duration ticks (0 = instantaneous), amplifier.
type splashEffect struct {
	name string
	dur  int32
	amp  int32
}

// splashPotionContents is the Potions registry entries a thrown or
// drunk splash can carry (26.2 values, javap-measured).
var splashPotionContents = map[string][]splashEffect{
	"harming":         {{"instant_damage", 0, 0}},
	"healing":         {{"instant_health", 0, 0}},
	"slowness":        {{"slowness", 1800, 0}},
	"poison":          {{"poison", 900, 0}},
	"weakness":        {{"weakness", 1800, 0}},
	"regeneration":    {{"regeneration", 900, 0}},
	"swiftness":       {{"speed", 3600, 0}},
	"water_breathing": {{"water_breathing", 3600, 0}},
	"fire_resistance": {{"fire_resistance", 3600, 0}},
}

// splashPotionColors are PotionContents.getColor's packed RGB per
// potion id — the level-event 2002 data field clients read.
var splashPotionColors = map[string]int32{
	"harming":         0x430A09,
	"healing":         0xF82423,
	"slowness":        0x5A6C81,
	"poison":          0x4E9331,
	"weakness":        0x484D48,
	"regeneration":    0xCD5CAB,
	"swiftness":       0x7CAFC6,
	"water_breathing": 0x2E5299,
	"fire_resistance": 0xE49A3A,
}

// potionSplash is ThrownPotion.onHitAsPotion: every living entity
// within four blocks of the impact gets each effect — timed effects
// scaled by the 1 - dist/4 proximity factor, instant ones applied
// with the same scale on the amount. The 2002 level event plays the
// splash particles and glass crack.
func (s *Server) potionSplash(e *Entity, x, y, z float64) {
	effects := splashPotionContents[e.splashPotion]
	if len(effects) == 0 {
		return
	}
	s.broadcastLevelEvent(e.Dim, 2002, world.Pos{
		X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z)),
	}, splashPotionColors[e.splashPotion])

	s.entities.mu.RLock()
	mobs := make([]*Entity, 0, len(s.entities.byID))
	for _, t := range s.entities.byID {
		mobs = append(mobs, t)
	}
	s.entities.mu.RUnlock()
	for _, t := range mobs {
		if !t.Living || t.Dying || t.Dim != e.Dim {
			continue
		}
		bb := entityBBDims(t)
		// Vanilla measures AABB-to-AABB distance — half-width + half-
		// height approximates it close enough for the ±4 window.
		dx, dy, dz := x-t.X, y-(t.Y+bb[1]/2), z-t.Z
		dist := math.Sqrt(dx*dx+dy*dy+dz*dz) - math.Max(bb[0], bb[1])/2
		if dist < 0 {
			dist = 0
		}
		prox := 1 - dist/4
		if prox <= 0 {
			continue
		}
		s.splashApplyMob(e, t, effects, prox)
	}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dead ||
			sess.mode() == GameModeSpectator || sess.dimName() != e.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		dx, dy, dz := x-px, y-(py+0.9), z-pz
		dist := math.Sqrt(dx*dx+dy*dy+dz*dz) - 0.75
		if dist < 0 {
			dist = 0
		}
		prox := 1 - dist/4
		if prox <= 0 {
			return true
		}
		s.splashApplyPlayer(e, sess, effects, prox)
		return true
	})
}

// splashApplyMob lands the scaled contents on one mob.
func (s *Server) splashApplyMob(e, t *Entity, effects []splashEffect, prox float64) {
	for _, eff := range effects {
		id, ok := MobEffectID(eff.name)
		if !ok {
			continue
		}
		if eff.dur <= 0 {
			switch eff.name {
			case "instant_health":
				heal := float32(math.Round(4 * float64(int(1)<<uint(eff.amp)) * prox))
				name, _ := EntityTypeName(t.Type)
				max := float32(mobAttrSupplierBase(name, "max_health"))
				if heal > 0 && max > 0 {
					t.Health += heal
					if t.Health > max {
						t.Health = max
					}
					s.broadcastEntityMetadata(t.ID, encodeLivingHealth(t.Health))
				}
			case "instant_damage":
				dmg := float32(math.Round(6 * float64(int(1)<<uint(eff.amp)) * prox))
				if dmg > 0 {
					s.damageMob(t, dmg, "indirect_magic",
						s.sessionByEntityID(e.arrowShooter),
						&damageInfo{typeName: "indirect_magic",
							cause: e.arrowShooter, direct: e.ID})
				}
			}
			continue
		}
		dur := int32(math.Round(float64(eff.dur) * prox))
		if dur < 1 {
			continue
		}
		mobAddEffect(t, id, eff.amp, s.clock.count+int64(dur), s.clock.count)
		t.MetaDirty = true
	}
}

// splashApplyPlayer lands the scaled contents on one player.
func (s *Server) splashApplyPlayer(e *Entity, sess *Session, effects []splashEffect, prox float64) {
	for _, eff := range effects {
		id, ok := MobEffectID(eff.name)
		if !ok {
			continue
		}
		if eff.dur <= 0 {
			switch eff.name {
			case "instant_health":
				heal := float32(math.Round(4 * float64(int(1)<<uint(eff.amp)) * prox))
				if heal > 0 && sess.health < 20 {
					sess.health += heal
					if sess.health > 20 {
						sess.health = 20
					}
					s.sendHealth(sess)
				}
			case "instant_damage":
				dmg := float32(math.Round(6 * float64(int(1)<<uint(eff.amp)) * prox))
				if dmg > 0 {
					attacker := "Splash Potion"
					if sh, ok := s.entities.Get(e.arrowShooter); ok {
						if n, ok2 := EntityTypeName(sh.Type); ok2 {
							attacker = "entity.minecraft." + n
						}
					}
					s.damageWith(sess, dmg, translatable("death.attack.indirectMagic",
						literal(sess.getName()), translatable(attacker)), damageSource{
						typeName: "indirect_magic",
						cause:    e.arrowShooter,
						direct:   e.ID,
					})
				}
			}
			continue
		}
		dur := int32(math.Round(float64(eff.dur) * prox))
		if dur < 1 {
			continue
		}
		s.applyMobEffectFlags(sess, id, eff.amp, dur, false)
	}
}

// thrownCollide is ThrowableItemProjectile.onHitEntity for the plain
// thrown items: the hurt amount is 0 (a knockback/animation-only hit)
// except a snowball's 3 damage to blazes.
func (s *Server) thrownCollide(e *Entity, ox, oy, oz float64, name string) bool {
	shooter := s.sessionByEntityID(e.arrowShooter)
	hit := false
	s.entities.mu.RLock()
	targets := make([]*Entity, 0, len(s.entities.byID))
	for _, t := range s.entities.byID {
		targets = append(targets, t)
	}
	s.entities.mu.RUnlock()
	for _, t := range targets {
		if hit || !t.Living || t.Dying || t.ID == e.ID || t.ID == e.arrowShooter {
			continue
		}
		if t.Dim != e.Dim {
			continue
		}
		bb := entityBBDims(t)
		if !segmentHitsBox(ox, oy, oz, e.X, e.Y, e.Z, t.X, t.Y, t.Z, bb[0], bb[1]) {
			continue
		}
		hit = true
		// Egg and snowball onHitEntity run hurt(source, 0); the xp
		// bottle doesn't override onHitEntity at all — its entity hit
		// only runs the onHit splash (no hurt tail on the wire).
		if name != "experience_bottle" {
			dmg := float32(0)
			if name == "snowball" {
				if tn, ok := EntityTypeName(t.Type); ok && tn == "blaze" {
					dmg = 3
				}
			}
			// DamageSources.thrown -> damage_type minecraft:thrown (45).
			s.damageMob(t, dmg, "thrown", shooter, &damageInfo{
				typeName: "thrown",
				cause:    e.arrowShooter,
				direct:   e.ID,
			})
		}
		// Egg/Snowball.onHit broadcast entity_event 3 on the
		// projectile — measured: ships on entity hits only, never
		// on block hits. The xp bottle broadcasts nothing.
		if name == "egg" || name == "blue_egg" || name == "brown_egg" || name == "snowball" {
			s.broadcastEntityEventTo(nil, e.Dim, e.ID, 3)
		}
	}
	return hit
}

// fireballCollide is SmallFireball.onHitEntity: igniteForSeconds(5)
// on the victim, then hurt 5.0 through DamageSources.fireball —
// damage_type minecraft:fireball with cause falling back to the
// projectile itself when the shot has no owner (summon/dispenser).
// When hurt() returns false (invulnerability grace, immunity) the
// saved remainingFireTicks are restored — the victim doesn't keep
// the ignition.
func (s *Server) fireballCollide(e *Entity, ox, oy, oz float64) bool {
	shooter := s.sessionByEntityID(e.arrowShooter)
	hit := false
	s.entities.mu.RLock()
	targets := make([]*Entity, 0, len(s.entities.byID))
	for _, t := range s.entities.byID {
		targets = append(targets, t)
	}
	s.entities.mu.RUnlock()
	for _, t := range targets {
		if hit || !t.Living || t.Dying || t.ID == e.ID || t.ID == e.arrowShooter {
			continue
		}
		if t.Dim != e.Dim {
			continue
		}
		bb := entityBBDims(t)
		if !segmentHitsBox(ox, oy, oz, e.X, e.Y, e.Z, t.X, t.Y, t.Z, bb[0], bb[1]) {
			continue
		}
		hit = true
		prevFire := t.FireTicks
		// DamageSources.fireball picks unattributed_fireball when
		// the shot has no owner; the source's entity then reports
		// as the projectile itself on the wire.
		typeName := "fireball"
		cause := e.arrowShooter
		if cause == 0 {
			typeName = "unattributed_fireball"
			cause = e.ID
		}
		dealt, graced, _ := s.damageMob(t, 5.0, typeName, shooter, &damageInfo{
			typeName: typeName,
			cause:    cause,
			direct:   e.ID,
		})
		if dealt > 0 && !graced {
			s.entities.SetFireTicks(t.ID, 100)
		} else {
			s.entities.SetFireTicks(t.ID, prevFire)
		}
		s.broadcastMobSharedFlags(t.ID, t.FireTicks > 0, t.GlowUntil > s.gameTime())
	}
	return hit
}

// fireballHitDir picks the side of the struck block the hit point
// sits on (+1/-1 along the axis).
func fireballHitDir(hit float64, cell int) int {
	if hit-float64(cell) > 0.5 {
		return 1
	}
	return -1
}

// fireballIgnites — SmallFireball.onHitBlock's gate:
// !(owner instanceof Mob) || mob_griefing. A dispenser-fired shot has
// no owner so it always ignites; a mob-fired shot ignites only when
// mob_griefing holds. (Ownerless entities and player sessions both
// take the "not a Mob" branch.)
func (s *Server) fireballIgnites(e *Entity) bool {
	ownerIsMob := false
	if e.arrowShooter != 0 {
		if owner, ok := s.entities.GetPtr(e.arrowShooter); ok && owner.Living {
			ownerIsMob = true
		}
	}
	return !ownerIsMob || s.gameRuleBool("minecraft:mob_griefing")
}

// throwHandItem is EggItem.use / SnowballItem.use: a named throw sound
// at the player (PLAYERS for the egg, NEUTRAL for the snowball), the
// projectile spawned at eye-0.1 with shootFromRotation's look vector —
// normalize, +triangle(0.0172275*inaccuracy) per axis, *1.5 power — the
// entity yaw/pitch derived from the resulting motion, the thrown item
// riding DATA_ITEM_STACK, and one unit consumed unless instabuild.
// handleUseItemPacket already sent the block-change ack; SUCCESS then
// swings the arm for the other sessions.
func (s *Server) throwHandItem(sess *Session, hand int32, st ItemStack, name string, yaw, pitch float32) {
	px, py, pz, _, _, _ := sess.Pos()
	sndID, src := int32(572), int32(7) // entity.egg.throw, PLAYERS
	entName := "egg"
	pitchOff, power := float32(0), float64(1.5)
	switch name {
	case "snowball":
		sndID, src = 1567, 6 // entity.snowball.throw, NEUTRAL
		entName = "snowball"
	case "experience_bottle":
		sndID, src = 617, 6 // entity.experience_bottle.throw, NEUTRAL
		entName = "experience_bottle"
		// spawnProjectileFromRotation(xRotDelta -20, power 0.7)
		pitchOff, power = -20, 0.7
	case "ender_pearl":
		sndID, src = 604, 6 // entity.ender_pearl.throw, NEUTRAL
		entName = "ender_pearl"
	case "wind_charge":
		sndID, src = 1773, 6 // entity.wind_charge.throw, NEUTRAL
		entName = "wind_charge"
	}
	s.playSoundSrc(sess.dimName(), px, py, pz, sndID, src, 0.5, 0.4/(rand.Float32()*0.4+0.8), nil)
	et, ok := EntityTypeID(entName)
	if !ok {
		return
	}
	yawR := float64(yaw) * math.Pi / 180
	pitchR := float64(pitch+pitchOff) * math.Pi / 180
	dx := -math.Sin(yawR) * math.Cos(pitchR)
	dy := -math.Sin(pitchR)
	dz := math.Cos(yawR) * math.Cos(pitchR)
	l := math.Sqrt(dx*dx + dy*dy + dz*dz)
	// getMovementToShoot: unit look + independent triangle jitter,
	// then scaled by the power (no re-normalize).
	vx := (dx/l + randTriangle(0, 0.0172275)) * power
	vy := (dy/l + randTriangle(0, 0.0172275)) * power
	vz := (dz/l + randTriangle(0, 0.0172275)) * power
	horiz := math.Sqrt(vx*vx + vz*vz)
	e := s.entities.Spawn(&Entity{
		Type: et,
		Dim:  sess.dimName(),
		X:    px, Y: py + 1.62 - 0.1, Z: pz,
		Yaw:     float32(math.Atan2(vx, vz) * 180 / math.Pi),
		Pitch:   float32(math.Atan2(vy, horiz) * 180 / math.Pi),
		MotionX: vx, MotionY: vy, MotionZ: vz,
		VX: int16(vx * 8000), VY: int16(vy * 8000), VZ: int16(vz * 8000),
		Data: sess.entityID.Load(), // spawn packet owner field
	})
	e.arrowShooter = sess.entityID.Load()
	s.broadcastEntitySpawn(nil, *e)
	s.broadcastEntityMetadata(e.ID, encodeItemMetadata(ItemStack{ID: st.ID, Count: 1}))
	// ItemStack.consume(1, living) shrinks unless the thrower has
	// infinite materials (creative/spectator).
	if m := sess.mode(); m != GameModeCreative && m != GameModeSpectator {
		var slot int16
		if hand == 1 {
			slot = slotOffhand
		} else {
			slot = invSlotForHotbar(sess.held.Load())
		}
		sess.invMu.Lock()
		it := sess.inv[slot]
		if !it.Empty() {
			it.Count--
			if it.Count <= 0 {
				it = ItemStack{}
			}
			sess.inv[slot] = it
		}
		sess.invMu.Unlock()
		_ = s.sendContainerSlot(sess, slot, it)
	}
	anim := byte(animSwingMainArm)
	if hand == 1 {
		anim = animSwingOffhand
	}
	s.broadcastEntityAnimation(sess, sess.entityID.Load(), anim)
}

// thrownItemImpact is the shared end of ThrownEgg.onHit and
// Snowball.onHit: broadcastEntityEvent(entity, 3) — which vanilla
// routes through sendToTrackingPlayersAndSelf and which never
// actually ships on the wire (the same-tick discard drops it —
// measured: only remove_entities arrives) — then the egg's hatch
// roll: 1/8 → one chick, and inside that 1/32 → four. Chicks hatch
// as Age -24000 babies snapped to the egg's position and yaw.
//
// faceVeto models the chick's fudgePositionAfterSizeChange gate
// measured on vanilla: eggs dying on a block's top or bottom face
// never produce chicks (0/64 measured on flat ground vs ~1/8 off
// side faces and entity hits).
func (s *Server) thrownItemImpact(e *Entity, name string, faceVeto bool) {
	if name != "egg" || faceVeto || rand.Intn(8) != 0 {
		return
	}
	n := 1
	if rand.Intn(32) == 0 {
		n = 4
	}
	ct, ok := EntityTypeID("chicken")
	if !ok {
		return
	}
	for i := 0; i < n; i++ {
		chick := s.entities.Spawn(&Entity{
			Type: ct,
			Dim:  e.Dim,
			X:    e.X, Y: e.Y, Z: e.Z,
			Yaw:       e.Yaw,
			Living:    true,
			Baby:      true,
			Age:       -24000,
			Health:    mobHealth["chicken"],
			MaxHealth: mobHealth["chicken"],
		})
		s.broadcastEntitySpawn(nil, *chick)
		s.broadcastEntityMetadata(chick.ID, encodeLivingMetadata(chick, nil))
	}
}

// xpBottleImpact is ThrownExperienceBottle.onHit: a level_event 2002
// splash (particle color -13083194) at the bottle's block position,
// then 3-11 xp split into orb denominations awarded along `dir` —
// block hits pass the face's unit normal, entity hits the mirrored
// deltaMovement. Each orb's initial velocity is the vanilla random
// pop ((rand-0.5)*0.4 per horizontal axis, rand*0.4 up), flipped into
// dir's hemisphere when dir is non-zero.
func (s *Server) xpBottleImpact(e *Entity, dx, dy, dz float64) {
	bp := world.Pos{
		X: int(math.Floor(e.X)),
		Y: int(math.Floor(e.Y)),
		Z: int(math.Floor(e.Z)),
	}
	s.broadcastLevelEvent(e.Dim, 2002, bp, -13083194)
	xp := 3 + rand.Intn(5) + rand.Intn(5)
	for xp > 0 {
		v := xpOrbDenomination(xp)
		xp -= v
		vx := (rand.Float64()*0.2 - 0.1) * 2
		vy := rand.Float64() * 0.2 * 2
		vz := (rand.Float64()*0.2 - 0.1) * 2
		if dx*dx+dy*dy+dz*dz > 0 && vx*dx+vy*dy+vz*dz < 0 {
			vx, vy, vz = -vx, -vy, -vz
		}
		orb := s.entities.Spawn(&Entity{
			Type: mustEntityType("experience_orb"),
			Dim:  e.Dim,
			X:    e.X, Y: e.Y, Z: e.Z,
			Yaw:      rand.Float32() * 360,
			OrbValue: int32(v),
			OrbCount: 1,
			MotionX:  vx, MotionY: vy, MotionZ: vz,
			VX: int16(vx * 8000), VY: int16(vy * 8000), VZ: int16(vz * 8000),
		})
		s.broadcastEntitySpawn(nil, *orb)
		s.broadcastEntityMetadata(orb.ID, encodeOrbMetadata(int32(v)))
	}
}

// xpOrbDenomination is ExperienceOrb.getExperienceValue: the largest
// standard denomination not exceeding the remaining xp.
func xpOrbDenomination(xp int) int {
	switch {
	case xp >= 2477:
		return 2477
	case xp >= 1237:
		return 1237
	case xp >= 617:
		return 617
	case xp >= 307:
		return 307
	case xp >= 149:
		return 149
	case xp >= 73:
		return 73
	case xp >= 37:
		return 37
	case xp >= 17:
		return 17
	case xp >= 7:
		return 7
	case xp >= 3:
		return 3
	default:
		return 1
	}
}

// enderPearlImpact is ThrownEnderpearl.onHit minus the dispatch:
// vanilla loops 32 Level.addParticle calls, but that method is an
// empty no-op on the dedicated server — nothing lands on the wire.
// When the owner is a living connected player it then teleports to
// the pearl's pre-tick position, eats 5.0 hurt through the
// entity-less ender_pearl damage type (death.attack.fall) and hears
// entity.player.teleport. Without a teleportable owner the pearl
// just discards. The 5% endermite roll needs monster spawning and a
// non-peaceful difficulty, both gated like vanilla.
func (s *Server) enderPearlImpact(e *Entity, ox, oy, oz float64) {
	shooter := s.sessionByEntityID(e.arrowShooter)
	if shooter == nil || !shooter.spawned.Load() || shooter.dead {
		return
	}
	if rand.Float32() < 0.05 && s.difficulty != DifficultyPeaceful {
		if et, ok := EntityTypeID("endermite"); ok {
			px, py, pz, yaw, pitch, _ := shooter.Pos()
			m := s.entities.Spawn(&Entity{
				Type: et, Dim: e.Dim,
				X: px, Y: py, Z: pz, Yaw: yaw, Pitch: pitch,
				Living:    true,
				Health:    mobHealth["endermite"],
				MaxHealth: mobHealth["endermite"],
			})
			s.broadcastEntitySpawn(nil, *m)
			s.broadcastEntityMetadata(m.ID, encodeLivingMetadata(m, nil))
		}
	}
	s.teleportWithin(shooter, ox, oy, oz)
	s.damageWith(shooter, 5.0, translatable("death.attack.fall", literal(shooter.getName())), damageSource{typeName: "ender_pearl"})
	s.playSoundSrc(e.Dim, ox, oy, oz, 1322, 7, 1, 1, nil) // entity.player.teleport, PLAYERS
}

// witherSkullCollide is WitherSkull.onHitEntity: with a LivingEntity
// owner the victim takes hurt 8.0 through witherSkull(skull, owner) —
// and a killed target heals the shooter 5.0 — while the ownerless
// summon arm falls back to hurt 5.0 through the entity-less magic
// type. A landed hit then applies wither for 10s on NORMAL or 40s on
// HARD (amplifier 1, skipped entirely on easy/peaceful).
func (s *Server) witherSkullCollide(e *Entity, ox, oy, oz float64) bool {
	s.entities.mu.RLock()
	targets := make([]*Entity, 0, len(s.entities.byID))
	for _, t := range s.entities.byID {
		targets = append(targets, t)
	}
	s.entities.mu.RUnlock()
	var hit *Entity
	for _, t := range targets {
		if hit != nil || !t.Living || t.Dying || t.ID == e.ID || t.ID == e.arrowShooter {
			continue
		}
		if t.Dim != e.Dim {
			continue
		}
		bb := entityBBDims(t)
		if !segmentHitsBox(ox, oy, oz, e.X, e.Y, e.Z, t.X, t.Y, t.Z, bb[0], bb[1]) {
			continue
		}
		hit = t
	}
	if hit == nil {
		return false
	}
	var graced, died bool
	if shooter := s.sessionByEntityID(e.arrowShooter); shooter != nil {
		var dealt float32
		dealt, graced, died = s.damageMob(hit, 8.0, "wither_skull", shooter, &damageInfo{
			typeName: "wither_skull", cause: e.arrowShooter, direct: e.ID,
		})
		if dealt > 0 && died {
			// The kill heals the wither's shooter — the leech arm.
			shooter.health += 5.0
			if shooter.health > 20 {
				shooter.health = 20
			}
			shooter.healthDirty.Store(true)
		}
	} else {
		_, graced, _ = s.damageMob(hit, 5.0, "magic", nil, &damageInfo{
			typeName: "magic",
		})
	}
	if !graced && hit.Living {
		// MobEffectInstance(WITHER, seconds*20, 1) — NORMAL 10s,
		// HARD 40s, easy/peaceful none. Vanilla gates this only on
		// hurt returning true + Living, not on isAlive.
		secs := int32(0)
		switch s.difficulty {
		case DifficultyNormal:
			secs = 10
		case DifficultyHard:
			secs = 40
		}
		if secs > 0 {
			if wid, ok := MobEffectID("wither"); ok {
				s.mobGiveEffect(hit, wid, 1, secs*20, false)
			}
		}
	}
	return true
}

// witherSkullExplode is WitherSkull.onHit's tail: explode(
// this, pos, 1.0, no fire, MOB) at the skull's position — a real
// power-1 explosion that hurts entities and carves blocks under
// mob_griefing — then the caller discards.
func (s *Server) witherSkullExplode(e *Entity) {
	s.explodePower(e, e.X, e.Y, e.Z, 1.0, s.gameRuleBool("minecraft:mob_griefing"))
}

// largeFireballCollide is LargeFireball.onHitEntity: hurt 6.0 through
// fireball(ball, getOwner()) — the owner may be null on a summon, in
// which case the source's cause field wires 0 and direct is the ball.
func (s *Server) largeFireballCollide(e *Entity, ox, oy, oz float64) bool {
	s.entities.mu.RLock()
	targets := make([]*Entity, 0, len(s.entities.byID))
	for _, t := range s.entities.byID {
		targets = append(targets, t)
	}
	s.entities.mu.RUnlock()
	for _, t := range targets {
		if !t.Living || t.Dying || t.ID == e.ID || t.ID == e.arrowShooter {
			continue
		}
		if t.Dim != e.Dim {
			continue
		}
		bb := entityBBDims(t)
		if !segmentHitsBox(ox, oy, oz, e.X, e.Y, e.Z, t.X, t.Y, t.Z, bb[0], bb[1]) {
			continue
		}
		// DamageSources.fireball picks unattributed_fireball when
		// the shot has no owner; the source's entity then reports
		// as the projectile itself on the wire.
		typeName := "fireball"
		cause := e.arrowShooter
		if cause == 0 {
			typeName = "unattributed_fireball"
			cause = e.ID
		}
		s.damageMob(t, 6.0, typeName, s.sessionByEntityID(e.arrowShooter), &damageInfo{
			typeName: typeName, cause: cause, direct: e.ID,
		})
		return true
	}
	return false
}

// largeFireballExplode is LargeFireball.onHit's tail: explode(
// this, pos, explosionPower, setFire=MOB_GRIEFING, MOB) then discard.
func (s *Server) largeFireballExplode(e *Entity) {
	power := e.ExplosionPower
	if power == 0 {
		power = 1
	}
	grief := s.gameRuleBool("minecraft:mob_griefing")
	s.explodePowerFire(e, e.X, e.Y, e.Z, power, grief, grief)
}

// dragonFireballImpact is DragonFireball.onHit's cloud arm: a fresh
// AreaEffectCloud at the hit spot (radius 3.0 growing toward 7.0 over
// 600 ticks, waitTime 10, potionDurationScale 0.25, contents
// INSTANT_DAMAGE amp 1), relocated onto the nearest living entity the
// inflated hit box catches within 4 blocks, a levelEvent 2006
// (data 1 — the ball isn't silent), and then the caller discards.
func (s *Server) dragonFireballImpact(e *Entity) {
	d := s.dims[e.Dim]
	if d == nil {
		return
	}
	cx, cy, cz := e.X, e.Y, e.Z
	// getEntitiesOfClass(LivingEntity, box.inflate(4,2,4)) then the
	// distanceToSqr<16 pick — the cloud teleports onto that victim.
	s.entities.mu.RLock()
	cands := make([]*Entity, 0, len(s.entities.byID))
	for _, t := range s.entities.byID {
		cands = append(cands, t)
	}
	s.entities.mu.RUnlock()
	best := -1.0
	var near *Entity
	for _, t := range cands {
		if !t.Living || t.Dying || t.Dim != e.Dim || t.ID == e.ID {
			continue
		}
		bb := entityBBDims(t)
		// Inside the 4/2/4-inflated ball box only.
		hw, h := bb[0]+4.0, bb[1]+2.0
		if t.X < cx-hw || t.X > cx+hw || t.Z < cz-hw || t.Z > cz+hw ||
			t.Y+h < cy-0.5-2.0 || t.Y > cy+0.5+2.0 {
			continue
		}
		dx, dy, dz := t.X-cx, t.Y-cy, t.Z-cz
		ds := dx*dx + dy*dy + dz*dz
		if ds < 16.0 && (best < 0 || ds < best) {
			best, near = ds, t
		}
	}
	if near != nil {
		cx, cy, cz = near.X, near.Y, near.Z
	}
	cloudType, ok := EntityTypeID("area_effect_cloud")
	if !ok {
		return
	}
	effects := map[int32]mobEffectInst{}
	if id, ok := MobEffectID("instant_damage"); ok {
		effects[id] = mobEffectInst{amplifier: 1, expiry: -1}
	}
	cloud := s.entities.Spawn(&Entity{
		Type:               cloudType,
		Dim:                e.Dim,
		X:                  cx,
		Y:                  cy,
		Z:                  cz,
		CloudWait:          10,
		CloudDuration:      600,
		CloudRadius:        3.0,
		CloudRadiusPerTick: 4.0 / 600,
		CloudRadiusOnUse:   -0.5,
		CloudVictims:       map[int32]int32{},
		Effects:            effects,
		// onHit wires the fireball's owner as the cloud owner when it
		// is a LivingEntity — ArrowShooter is exactly that (0 for
		// /summon, which has no owner).
		CloudOwnerID: e.arrowShooter,
	})
	s.broadcastEntitySpawn(nil, *cloud)
	if meta := encodeCloudMeta(cloud); len(meta) > 0 {
		s.broadcastEntityMetadata(cloud.ID, meta)
	}
	s.broadcastLevelEvent(e.Dim, 2006, world.Pos{
		X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z)),
	}, 1)
}

// dragonFireballCollide runs the entity-hit scan only — onHitEntity
// is empty on the base class, and onHit's cloud spawn is shared with
// the block-hit arm in dragonFireballImpact.
func (s *Server) dragonFireballCollide(e *Entity, ox, oy, oz float64) bool {
	s.entities.mu.RLock()
	targets := make([]*Entity, 0, len(s.entities.byID))
	for _, t := range s.entities.byID {
		targets = append(targets, t)
	}
	s.entities.mu.RUnlock()
	for _, t := range targets {
		if !t.Living || t.Dying || t.ID == e.ID || t.ID == e.arrowShooter {
			continue
		}
		if t.Dim != e.Dim {
			continue
		}
		bb := entityBBDims(t)
		if segmentHitsBox(ox, oy, oz, e.X, e.Y, e.Z, t.X, t.Y, t.Z, bb[0], bb[1]) {
			return true
		}
	}
	return false
}

// shulkerBulletCollide is ShulkerBullet.onHitEntity: hurtOrSimulate
// through damageSources.mobProjectile(this, livingOwner) for 4.0 —
// cause = the shooter when it is a LivingEntity, 0 for /summon —
// then LEVITATION 200t on the victim when the hurt lands.
func (s *Server) shulkerBulletCollide(e *Entity, ox, oy, oz float64) bool {
	shooter := s.sessionByEntityID(e.arrowShooter)
	hit := false
	levID, levOK := MobEffectID("levitation")
	s.entities.mu.RLock()
	targets := make([]*Entity, 0, len(s.entities.byID))
	for _, t := range s.entities.byID {
		targets = append(targets, t)
	}
	s.entities.mu.RUnlock()
	for _, t := range targets {
		if hit || !t.Living || t.Dying || t.ID == e.ID || t.ID == e.arrowShooter {
			continue
		}
		if t.Dim != e.Dim {
			continue
		}
		bb := entityBBDims(t)
		if segmentHitsBox(ox, oy, oz, e.X, e.Y, e.Z, t.X, t.Y, t.Z, bb[0], bb[1]) {
			hit = true
			dealt, graced, _ := s.damageMob(t, 4.0, "mob_projectile", shooter, &damageInfo{
				typeName: "mob_projectile",
				cause:    e.arrowShooter,
				direct:   e.ID,
			})
			if dealt > 0 && !graced && levOK {
				s.mobGiveEffect(t, levID, 0, 200, false)
			}
		}
	}
	s.sessions.Range(func(_, v any) bool {
		if hit {
			return false
		}
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dead || sess.mode() == GameModeSpectator {
			return true
		}
		if sess.entityID.Load() == e.arrowShooter {
			return true
		}
		if sess.dimName() != e.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if segmentHitsBox(ox, oy, oz, e.X, e.Y, e.Z, px, py, pz, 0.6, 1.8) {
			hit = true
			msg := translatable("death.attack.mob_projectile", literal(sess.getName()))
			if shooter != nil {
				msg = translatable("death.attack.mob", literal(sess.getName()), literal(shooter.getName()))
			}
			dealt := s.damageWith(sess, 4.0, msg, damageSource{
				typeName: "mob_projectile",
				cause:    e.arrowShooter,
				direct:   e.ID,
			})
			if dealt > 0 && levOK {
				s.playerGiveEffect(sess, levID, 0, 200, false)
			}
		}
		return !hit
	})
	return hit
}

// shulkerBulletBlockHit is ShulkerBullet.onHitBlock + onHit's
// destroy: EXPLOSION particles x2, the SHULKER_BULLET_HIT sound, then
// discard (the shared remove below carries that).
func (s *Server) shulkerBulletBlockHit(e *Entity) {
	s.broadcastParticles(e.Dim, 30, e.X, e.Y, e.Z, 0.2, 0.2, 0.2, 0, 2)
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, soundEventIDs["entity.shulker_bullet.hit"], 6, 1.0, 1.0, nil)
}

// shulkerBulletSteer is the no-target arm of ShulkerBullet.tick: the
// bullet wanders — flightSteps counts down, then
// selectNextMoveDirection re-picks a Direction (never the current
// axis), the target-delta latches step*0.15, and the velocity lerps
// toward it every tick (delta += (target-delta)*0.2).
func (s *Server) shulkerBulletSteer(e *Entity) {
	if e.ShulkerAxis == 0 && e.ShulkerSteps == 0 && e.ShulkerTdx == 0 && e.ShulkerTdy == 0 && e.ShulkerTdz == 0 {
		e.ShulkerAxis = -1
	}
	e.ShulkerSteps--
	if e.ShulkerSteps <= 0 {
		// Vanilla excludes the current direction's axis and directions
		// blocked by a solid neighbor; on a flat world every direction
		// is free, so the only live filter is the axis exclusion.
		dir := shulkerPickDirection(e.ShulkerAxis)
		e.ShulkerAxis = shulkerDirAxis(dir)
		e.ShulkerTdx = float64(shulkerDirStepX(dir)) * 0.15
		e.ShulkerTdy = float64(shulkerDirStepY(dir)) * 0.15
		e.ShulkerTdz = float64(shulkerDirStepZ(dir)) * 0.15
		// Vanilla: flightSteps = 10 + nextInt(5)*10 ticks between
		// direction rolls.
		e.ShulkerSteps = 10 + int32(rand.Intn(5)*10)
	}
	e.MotionX += (e.ShulkerTdx - e.MotionX) * 0.2
	e.MotionY += (e.ShulkerTdy - e.MotionY) * 0.2
	e.MotionZ += (e.ShulkerTdz - e.MotionZ) * 0.2
	e.VX = int16(e.MotionX * 8000)
	e.VY = int16(e.MotionY * 8000)
	e.VZ = int16(e.MotionZ * 8000)
}

// shulker direction ids: 0=DOWN 1=UP 2=NORTH 3=SOUTH 4=WEST 5=EAST.
func shulkerPickDirection(excludeAxis int32) int32 {
	for {
		d := int32(rand.Intn(6))
		if shulkerDirAxis(d) != excludeAxis {
			return d
		}
	}
}

func shulkerDirAxis(d int32) int32 {
	switch d {
	case 0, 1:
		return 0
	case 2, 3:
		return 2
	default:
		return 1
	}
}

func shulkerDirStepX(d int32) int32 {
	switch d {
	case 4:
		return -1
	case 5:
		return 1
	}
	return 0
}

func shulkerDirStepY(d int32) int32 {
	switch d {
	case 0:
		return -1
	case 1:
		return 1
	}
	return 0
}

func shulkerDirStepZ(d int32) int32 {
	switch d {
	case 2:
		return -1
	case 3:
		return 1
	}
	return 0
}
