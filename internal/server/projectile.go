package server

// Player-fired arrows: the bow draws on Use Item, releases on the
// Player Action RELEASE_USE_ITEM status, and the arrow entity flies
// with vanilla's projectile physics — velocity decays 0.99 airborne,
// gravity pulls 0.05 per tick — until it strikes a living entity.
// Arrow hits run the normal damage paths so player_hurt_entity and the
// projectile killing-blow advancements fire off real state.

import (
	"math"
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
		Data: shooter.entityID.Load() + 1,
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
		if name, ok := EntityTypeName(e.Type); ok && (name == "arrow" || name == "trident") {
			arrows = append(arrows, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range arrows {
		trident, _ := EntityTypeName(e.Type)
		isTrident := trident == "trident"
		if e.arrowStuck || e.tridentStuck {
			if isTrident {
				s.tridentPickupStep(e)
			}
			continue
		}
		ox, oy, oz := e.X, e.Y, e.Z
		e.X += e.MotionX
		e.Y += e.MotionY
		e.Z += e.MotionZ
		e.MotionY -= arrowGravity
		e.MotionX *= arrowAirDrag
		e.MotionY *= arrowAirDrag
		e.MotionZ *= arrowAirDrag
		s.broadcastEntityTeleport(nil, e.ID, e.X, e.Y, e.Z, e.Yaw, e.Pitch, false)
		// Vanilla clips each target's inflated AABB against the whole
		// tick's movement segment — a point check at the new tip tunnels
		// straight through at full draw speed (3 blocks/tick).
		var collided bool
		if isTrident {
			collided = s.projectileCollide(e, ox, oy, oz, tridentHitDamage, "trident")
		} else {
			collided = s.arrowCollide(e, ox, oy, oz)
		}
		if collided {
			s.entities.Remove(e.ID)
			s.broadcastEntityRemove([]int32{e.ID})
			continue
		}
		// Blocks are tested second, matching vanilla's hit order —
		// a living hit consumes the arrow before the wall check runs.
		if bx, by, bz, axis, hx, hy, hz, hit := s.arrowBlockHit(e, ox, oy, oz); hit {
			e.X, e.Y, e.Z = hx, hy, hz
			e.MotionX, e.MotionY, e.MotionZ = 0, 0, 0
			if isTrident {
				e.tridentStuck = true
				s.playSoundSrc(e.Dim, hx, hy, hz, 1626, 6, 1.0, 1.0) // item.trident.hit_ground
			} else {
				e.arrowStuck = true
			}
			d := s.dimensionByName(e.Dim)
			if name, ok := blockNameAt(d, world.Pos{X: bx, Y: by, Z: bz}); ok && name == "target" {
				signal := targetSignal(axis, hx, hy, hz)
				if shooter := s.sessionByEntityID(e.arrowShooter); shooter != nil {
					s.fireTargetHit(shooter, e, signal)
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
		bb := mobBounds(t.Type)
		if segmentHitsBox(ox, oy, oz, e.X, e.Y, e.Z, t.X, t.Y, t.Z, bb[0], bb[1]) {
			hit = true
			dealt, _, died := s.damageMob(t, damage)
			if dealt > 0 {
				s.broadcastDamageEvent(t.ID, damageInfo{
					typeName: typeName,
					cause:    e.arrowShooter,
					direct:   e.ID,
				})
				if shooter != nil {
					s.firePlayerHurtEntity(shooter, typeName, true, damage, "", false)
					if died {
						shooter.addStat(statKilled, t.Type, 1)
						shooter.addCustomStat("mob_kills", 1)
						if tn, ok := EntityTypeName(t.Type); ok {
							vx, vz := t.X, t.Z
							ax, _, az, _, _, _ := shooter.Pos()
							s.firePlayerKilledEntityDamage(shooter, tn, vx, vz, ax, az, true, typeName)
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
				}
			} else {
				s.damageWith(sess, damage, translatable("death.attack."+typeName,
					literal(sess.getName())), damageSource{
					typeName: typeName,
					direct:   e.ID,
				})
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
	s.playSoundSrc(sess.dimName(), x, y, z, 1631, 1, 1.0, 1.0) // item.trident.throw, PLAYERS source
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
		Data: shooter.entityID.Load() + 1,
	})
	e.arrowShooter = shooter.entityID.Load()
	s.broadcastEntitySpawn(nil, *e)
	return e
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
