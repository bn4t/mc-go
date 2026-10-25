package server

import (
	"math"
)

// witherSkeletonPrey is WitherSkeleton's targetSelector@3 pick —
// NearestAttackableTargetGoal(AbstractPiglin): every living member
// of the piglin family in the same dimension.
var witherSkeletonPrey = map[string]bool{
	"piglin":       true,
	"piglin_brute": true,
}

// witherPiglinStep is the wither skeleton's piglin-hunt arm —
// NearestAttackableTargetGoal(AbstractPiglin, priority 3) feeding
// the shared MeleeAttackGoal: repick the nearest visible piglin
// inside follow_range every ~10 ticks (reducedTickDelay(10)), hold it
// while it stays alive and inside the follow box, chase at the
// skeleton family's 1.2 × movement_speed, and swing attack_damage on
// the 20-tick cooldown. A landed hit also lands WITHER 200 via
// WitherSkeleton.doHurtTarget — no difficulty gate.
func (s *Server) witherPiglinStep(e *Entity) bool {
	e.HuntScan--
	if e.HuntScan <= 0 {
		e.HuntScan = 10
		e.HuntMob = s.witherPiglinTarget(e)
	}
	m, ok := s.entities.GetPtr(e.HuntMob)
	if !ok || m.Dying || m.Dim != e.Dim || m.Dormant {
		e.HuntMob = 0
		return false
	}
	if mn, _ := EntityTypeName(m.Type); !witherSkeletonPrey[mn] {
		e.HuntMob = 0
		return false
	}
	// canContinueToUse: the mark drops once the target leaves
	// follow_range's distanceToSqr box (full 3D).
	dx, dy, dz := m.X-e.X, m.Y-e.Y, m.Z-e.Z
	fr := s.mobAttr(e, "wither_skeleton", "follow_range", 16.0)
	if dx*dx+dy*dy+dz*dz > fr*fr {
		e.HuntMob = 0
		return false
	}
	dist := math.Hypot(dx, dz)
	mobTurnToward(e, dx, dz, 30)
	e.HeadYaw = e.Yaw
	dims := entityBBDims(e)
	mdims := entityBBDims(m)
	reach := dims[0]/2 + mdims[0]/2 + 0.8284271
	if dist > reach {
		s.mobTravelStep(e, s.dimWorld(e.Dim),
			s.mobAttr(e, "wither_skeleton", "movement_speed", mobChaseSpeed)*
				mobMeleeSpeedMod["wither_skeleton"])
	}
	if e.attackCooldown > 0 {
		e.attackCooldown--
	}
	if e.attackCooldown <= 0 && dist <= reach && m.Y+mdims[1] > e.Y && m.Y < e.Y+dims[1] {
		e.attackCooldown = 20
		s.broadcastEntityAnimation(nil, e.ID, 0) // swing main hand
		dealt, _, _ := s.damageMob(m, s.mobMeleeDamage(e, "wither_skeleton"), "mob_attack", nil, &damageInfo{
			typeName: "mob_attack",
			cause:    e.ID,
			direct:   e.ID,
		})
		if dealt > 0 {
			if id, ok2 := MobEffectID("wither"); ok2 {
				s.mobGiveEffect(m, id, 0, 200, false)
			}
		}
	}
	// MeleeAttackGoal.start latches the aggressive flag for as long
	// as the goal runs.
	if !e.Aggressive {
		e.Aggressive = true
		e.MetaDirty = true
	}
	return true
}

// witherPiglinTarget is NearestAttackableTargetGoal's repick — the
// nearest AbstractPiglin member inside follow_range with a clear
// eye-to-eye line (mustSee's clipSolidSegment, same as the llama
// family's wolf hunt).
func (s *Server) witherPiglinTarget(e *Entity) int32 {
	d := s.dims[e.Dim]
	if d == nil {
		return 0
	}
	ed := entityBBDims(e)
	fr := s.mobAttr(e, "wither_skeleton", "follow_range", 16.0)
	best, bd := int32(0), fr*fr
	for _, o := range s.entities.All() {
		if o.ID == e.ID || !o.Living || o.Dim != e.Dim || o.Dying || o.Dormant {
			continue
		}
		on, _ := EntityTypeName(o.Type)
		if !witherSkeletonPrey[on] {
			continue
		}
		dx, dy, dz := o.X-e.X, o.Y-e.Y, o.Z-e.Z
		d2 := dx*dx + dy*dy + dz*dz
		if d2 >= bd {
			continue
		}
		od := entityBBDims(&o)
		if _, _, _, _, _, _, _, hit := s.clipSolidSegment(d,
			e.X, e.Y+ed[1]*0.85, e.Z,
			o.X, o.Y+od[1]*0.85, o.Z); hit {
			continue
		}
		best, bd = o.ID, d2
	}
	return best
}
