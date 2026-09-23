package server

import (
	"math"
	"strings"
)

// kineticWeapon mirrors the spear's minecraft:kinetic_weapon data
// component (vanilla 26.2). A held right-use jab: after delayTicks,
// damageEntities sweeps the look ray every tick. Each collected entity
// is remembered for contactCooldownTicks whether or not a condition
// passes; dismount/knockback/damage Conditions each gate on
// (hotTicks <= maxTicks) && a speed term:
//   - damage:   holder look-speed >= dmgMinSpd (9..14 b/s — riding only)
//   - knockback: holder look-speed >= 5.1
//   - dismount:  relative closing speed >= 4.6
//
// stabAttack applies whichever flags passed; the spear_mobs trigger
// counts every entity in the remembered set.
type kineticWeapon struct {
	delay      int
	dmgMaxT    int
	dmgMinSpd  float64
	kbMaxT     int
	kbMinSpd   float64
	dismMaxT   int
	dismMinRel float64
	dmgMult    float64
	attackDmg  float32 // base_attack_damage attribute (total = 1 + this)
	useSnd     int32
	hitSnd     int32
}

// Per-tier component defaults, decoded from Item.Properties.spear(...)
// calls in vanilla Items.<clinit>: (f2, f3=dmgMult, f4=delay s,
// f5=dmgWindow s + f6=dmgMinSpd, f7=kbWindow s + f8=kbMinSpd,
// f9=dismountWindow s + f10=dismountMinRel).
var kineticWeapons = map[string]kineticWeapon{
	"wooden_spear":    {15, 100, 14.0, 200, 5.1, 300, 4.6, 0.7, 0, 1529, 1530},
	"stone_spear":     {14, 90, 13.0, 180, 5.1, 275, 4.6, 0.82, 1, 1526, 1527},
	"copper_spear":    {13, 80, 12.0, 165, 5.1, 250, 4.6, 0.82, 1, 1526, 1527},
	"iron_spear":      {12, 50, 11.0, 135, 5.1, 225, 4.6, 0.95, 2, 1526, 1527},
	"golden_spear":    {14, 70, 13.0, 170, 5.1, 275, 4.6, 0.7, 0, 1526, 1527},
	"diamond_spear":   {10, 60, 10.0, 130, 5.1, 200, 4.6, 1.075, 3, 1526, 1527},
	"netherite_spear": {8, 50, 9.0, 110, 5.1, 175, 4.6, 1.2, 4, 1526, 1527},
}

const spearContactCooldown = 10 // KineticWeapon contact_cooldown_ticks

// spearStart begins a spear jab: the item stays in use until release
// and damageEntities runs every tick once past the delay.
func (s *Server) spearStart(sess *Session, itemID int32, name string) {
	kw := kineticWeapons[name]
	sess.usingItemID = itemID
	sess.spearStartTick = s.TickCount()
	sess.stabbed = map[int32]int64{}
	x, y, z, _, _, _ := sess.Pos()
	// playSound(except=holder): the holder's client plays the use sound
	// locally, so the packet goes to everyone else.
	s.playSoundSrc(sess.dimName(), x, y, z, kw.useSnd, 7, 1.0, 1.0, sess)
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
	kw, ok := kineticWeapons[name]
	if !ok {
		return
	}
	tick := s.TickCount()
	hot := int(tick-sess.spearStartTick) - kw.delay
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
	base := 1 + kw.attackDmg
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
		if last, ok := sess.stabbed[e.ID]; ok && tick-last < spearContactCooldown {
			continue
		}
		bb := mobBounds(e.Type)
		if !segmentHitsBoxMargin(x, eye, z, tx, ty, tz, e.X, e.Y, e.Z, bb[0], bb[1], 0.125) {
			continue
		}
		sess.stabbed[e.ID] = tick // remember before conditions — vanilla does
		// getMotion(target) is its deltaMovement; players' own last
		// client movement is the packet delta (idle targets ~0).
		targetSpeed := (lx*e.MotionX + ly*e.MotionY + lz*e.MotionZ) * 20
		relSpeed := math.Max(0, holderSpeed-targetSpeed)
		canDismount := hot <= kw.dismMaxT && relSpeed >= kw.dismMinRel
		canKb := hot <= kw.kbMaxT && holderSpeed >= kw.kbMinSpd
		canDmg := hot <= kw.dmgMaxT && holderSpeed >= kw.dmgMinSpd
		if !canDismount && !canKb && !canDmg {
			continue
		}
		hitAny = true
		dmg := base + float32(math.Floor(relSpeed*kw.dmgMult))
		if canDmg {
			dealt, _, died := s.damageMob(e, dmg, "player_attack", sess, &damageInfo{
				typeName: "spear",
				cause:    sess.entityID.Load(),
				direct:   sess.entityID.Load(),
			})
			if dealt > 0 {
				s.firePlayerHurtEntity(sess, "player", false, dmg, name, false)
				sess.addCustomStat("damage_dealt", int32(math.Round(float64(dealt*10))))
				if died {
					sess.addStat(statKilled, e.Type, 1)
					sess.addCustomStat("mob_kills", 1)
					s.onPlayerKilledMob(sess, *e)
				}
			}
		}
		if canKb {
			res := float64(0)
			if tn, ok := EntityTypeName(e.Type); ok {
				res = mobKnockbackResistance[tn]
			}
			push := 0.4 * (1 - res)
			e.MotionX += lx * push
			e.MotionY += 0.4 * push
			e.MotionZ += lz * push
			s.entities.SetMotion(e.ID, e.MotionX, e.MotionY, e.MotionZ)
			s.broadcastEntityMotion(e.Dim, e.ID, e.MotionX, e.MotionY, e.MotionZ)
		}
		// canDismount would stopRiding(); mc-go has no riding yet.
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
