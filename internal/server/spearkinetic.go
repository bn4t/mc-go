package server

import "math"

// Default kinetic conditions use independently named holder/relative speed
// thresholds. The caller supplies time after the component's initial delay.
func kineticConditionPass(condition kineticConditionInfo, hotTicks int, holderSpeed, relativeSpeed float64) bool {
	return hotTicks <= condition.maxDurationTicks && holderSpeed >= condition.minSpeed && relativeSpeed >= condition.minRelativeSpeed
}

// kineticDamage preserves the promoted component multiplier during the double
// speed product and floor, then adds the holder's float attack-damage value.
func kineticDamage(base float32, relativeSpeed, multiplier float64) float32 {
	return base + float32(math.Floor(relativeSpeed*multiplier))
}

// Spear knockback uses the living horizontal step and preserves an airborne
// target's vertical velocity. General attack knockback remains independent.
func spearKnockbackMotion(motion [3]float64, resistance, lookX, lookZ float64, grounded bool) [3]float64 {
	x, y, z := knockDelta(motion[0], motion[1], motion[2], mobHurtKnockbackStrength, resistance, -lookX, -lookZ, grounded)
	if !grounded {
		y = motion[1]
	}
	return [3]float64{x, y, z}
}

// Kinetic damage uses the raw attack-damage base, excluding ordinary attack
// modifiers. This differs from the melee damage consumer.
func (sess *Session) kineticAttackBase() float32 {
	base := playerAttrBases["attack_damage"]
	if sess.server != nil && sess.server.entities != nil {
		if player, ok := sess.server.entities.Get(sess.entityID.Load()); ok {
			if value, present := player.AttrBase["attack_damage"]; present {
				base = value
			}
		}
	}
	return float32(base)
}
