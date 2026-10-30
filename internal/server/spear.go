package server

import (
	"math"
	"strings"
)

// Default kinetic component facts live in kineticinfo_gen.go. Their damage,
// knockback and dismount conditions have distinct speed/duration roles.

// spearStart begins a spear jab: the item stays in use until release
// and damageEntities runs every tick once past the delay.
func (s *Server) spearStart(sess *Session, itemID int32, name string) {
	kw := kineticWeaponMetadata[name]
	sess.usingItemID = itemID
	sess.spearStartTick = s.TickCount()
	sess.stabbed = map[int32]int64{}
	x, y, z, _, _, _ := sess.Pos()
	// playSound(except=holder): the holder's client plays the use sound
	// locally, so the packet goes to everyone else.
	s.playSoundSrc(sess.dimName(), x, y, z, kw.useSound, 7, 1.0, 1.0, sess)
}

// spearJabTick is the per-tick tail of a held spear use —
// KineticWeapon.damageEntities: collect entities along the look ray
// (spear reach 4.5, hitbox margin 0.125), skip anything in the 10-tick
// contact cooldown, REMEMBER each fresh hit, evaluate the three
// Conditions, stab, then on hitAny broadcast entity_event 2 and fire
// spear_mobs with the remembered-set size.
func (s *Server) spearJabTick(sess *Session) {
	name, ok := itemNameOf(sess.usingItemID)
	if !ok || !strings.HasSuffix(name, "_spear") {
		return
	}
	kw, ok := kineticWeaponMetadata[name]
	if !ok {
		return
	}
	tick := s.TickCount()
	hot := int(tick-sess.spearStartTick) - kw.delayTicks
	if hot < 0 {
		return
	}
	x, y, z, yaw, pitch, _ := sess.Pos()
	lx := -math.Sin(float64(yaw)*math.Pi/180) * math.Cos(float64(pitch)*math.Pi/180)
	ly := -math.Sin(float64(pitch) * math.Pi / 180)
	lz := math.Cos(float64(yaw)*math.Pi/180) * math.Cos(float64(pitch)*math.Pi/180)
	// Vanilla getMotion(holder) = lastKnownClientMovement * 20: the
	// per-tick position delta the client's move packets produced.
	holderSpeed := (lx*sess.lastDelta[0] + ly*sess.lastDelta[1] + lz*sess.lastDelta[2]) * 20
	base := sess.kineticAttackBase()
	eye := y + 1.62
	tx, ty, tz := x+lx*4.5, eye+ly*4.5, z+lz*4.5

	s.entities.mu.RLock()
	var cands []*Entity
	for _, e := range s.entities.byID {
		cands = append(cands, e)
	}
	s.entities.mu.RUnlock()
	hitAny := false
	for _, e := range cands {
		if !e.Living || e.Dying || e.Dim != sess.dimName() {
			continue
		}
		if last, ok := sess.stabbed[e.ID]; ok && tick-last < int64(kw.contactCooldownTicks) {
			continue
		}
		bb := entityBBDims(e)
		if !segmentHitsBoxMargin(x, eye, z, tx, ty, tz, e.X, e.Y, e.Z, bb[0], bb[1], 0.125) {
			continue
		}
		sess.stabbed[e.ID] = tick // remember before conditions — vanilla does
		// A mob's sampled actual movement can be zero while stored velocity
		// is nonzero (for example a stationary NoAI target).
		targetSpeed := (lx*e.knownSpeed[0] + ly*e.knownSpeed[1] + lz*e.knownSpeed[2]) * 20
		relSpeed := math.Max(0, holderSpeed-targetSpeed)
		canDismount := kineticConditionPass(kw.dismount, hot, holderSpeed, relSpeed)
		canKb := kineticConditionPass(kw.knockback, hot, holderSpeed, relSpeed)
		canDmg := kineticConditionPass(kw.damage, hot, holderSpeed, relSpeed)
		if !canDismount && !canKb && !canDmg {
			continue
		}
		hitAny = true
		source := &damageInfo{typeName: "spear", cause: sess.entityID.Load(), direct: sess.entityID.Load()}
		sourceInvulnerable := s.mobInvulnerableTo(e, "player_attack", source)
		dmg := kineticDamage(base, relSpeed, kw.damageMultiplier)
		if canDmg {
			headOminous := e.Equips[equipHelmet].Ominous
			dealt, _, died := s.damageMob(e, dmg, "player_attack", sess, source)
			if dealt > 0 {
				s.firePlayerHurtEntity(sess, "player", false, dmg, name, false)
				sess.addCustomStat("damage_dealt", int32(math.Round(float64(dealt*10))))
				if died {
					// killed:<type> lands inside die() on the
					// credited hurter, not at the hit site.
					s.onPlayerKilledMob(sess, *e, headOminous)
				}
			}
		}
		if canKb && !sourceInvulnerable {
			resistance := s.mobKnockbackResistance(e)
			if resistance < 1 {
				motion := spearKnockbackMotion([3]float64{e.MotionX, e.MotionY, e.MotionZ}, resistance, lx, lz, e.Grounded)
				e.MotionX, e.MotionY, e.MotionZ = motion[0], motion[1], motion[2]
				s.entities.SetMotion(e.ID, e.MotionX, e.MotionY, e.MotionZ)
				s.broadcastEntityMotion(e.Dim, e.ID, e.MotionX, e.MotionY, e.MotionZ)
			}
		}
		if canDismount && !sourceInvulnerable {
			s.dismountSpearBoatRider(e)
		}
	}
	if hitAny {
		// Vanilla makeLocalHitSound is Level.playLocalSound — an empty
		// stub on the dedicated server, so no packet goes out here.
		s.broadcastEntityEvent(sess.entityID.Load(), 2)
		s.fireSpearMobs(sess, len(sess.stabbed))
	}
}

// segmentHitsBoxMargin is segmentHitsBox with an explicit hitbox
// inflation (spear's AttackRange hitbox_margin is 0.125, projectiles
// use 0.3).
func segmentHitsBoxMargin(sx, sy, sz, ex, ey, ez, bx, by, bz, w, h, margin float64) bool {
	hw := w/2 + margin
	min := [3]float64{bx - hw, by - margin, bz - hw}
	max := [3]float64{bx + hw, by + h + margin, bz + hw}
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
