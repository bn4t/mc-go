package server

// Player-fired arrows: the bow draws on Use Item, releases on the
// Player Action RELEASE_USE_ITEM status, and the arrow entity flies
// with vanilla's projectile physics — velocity decays 0.99 airborne,
// gravity pulls 0.05 per tick — until it strikes a living entity.
// Arrow hits run the normal damage paths so player_hurt_entity and the
// projectile killing-blow advancements fire off real state.

import (
	"math"
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

// arrowTickStep advances flying arrows one tick: integrate, apply drag
// and gravity, then collide against living mobs and players. A struck
// target takes the arrow's power-scaled damage; the arrow is spent.
func (s *Server) arrowTickStep() {
	s.entities.mu.RLock()
	arrows := make([]*Entity, 0, 4)
	for _, e := range s.entities.byID {
		if e.Dying {
			continue
		}
		if name, ok := EntityTypeName(e.Type); ok && name == "arrow" {
			arrows = append(arrows, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range arrows {
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
		if s.arrowCollide(e, ox, oy, oz) {
			s.entities.Remove(e.ID)
			s.broadcastEntityRemove([]int32{e.ID})
		}
	}
}

// arrowCollide resolves the arrow's hit against every living entity and
// player in its dimension, applying damage on the first target whose
// bounding box the arrow tip enters. The shooter is exempt for the
// first few ticks so a released arrow cannot hit its owner.
func (s *Server) arrowCollide(e *Entity, ox, oy, oz float64) bool {
	shooter := s.sessionByEntityID(e.arrowShooter)
	damage := float32(math.Ceil(math.Hypot(e.MotionX, e.MotionZ)*0 + math.Sqrt(e.MotionX*e.MotionX+e.MotionY*e.MotionY+e.MotionZ*e.MotionZ)*2.0))
	if damage < 1 {
		damage = 1
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
					typeName: "arrow",
					cause:    e.arrowShooter,
					direct:   e.ID,
				})
				if shooter != nil {
					s.firePlayerHurtEntity(shooter, "arrow", true, dealt)
					if died {
						shooter.addStat(statKilled, t.Type, 1)
						shooter.addCustomStat("mob_kills", 1)
						if tn, ok := EntityTypeName(t.Type); ok {
							vx, vz := t.X, t.Z
							ax, _, az, _, _, _ := shooter.Pos()
							s.firePlayerKilledEntityDamage(shooter, tn, vx, vz, ax, az, true, "arrow")
							s.fireKilledByArrow(shooter, tn, e.arrowWeapon)
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
				dealt := s.damageWith(sess, damage, translatable("death.attack.arrow",
					literal(sess.getName()), literal(shooter.getName())), damageSource{
					typeName: "arrow",
					cause:    e.arrowShooter,
					direct:   e.ID,
				})
				if dealt > 0 {
					s.firePlayerHurtEntity(shooter, "arrow", true, dealt)
				}
			} else {
				s.damageWith(sess, damage, translatable("death.attack.arrow",
					literal(sess.getName())), damageSource{
					typeName: "arrow",
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

// hasItem reports whether the inventory holds the named item.
func (sess *Session) hasItem(item string) bool {
	sess.invMu.Lock()
	defer sess.invMu.Unlock()
	for i := range sess.inv {
		it := sess.inv[i]
		if it.Empty() {
			continue
		}
		if n, ok := itemNameOf(it.ID); ok && n == item {
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
		if n, ok := itemNameOf(it.ID); ok && n == item {
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
