package server

// Ordinary hurt uses float32 0.4 widened to double (26.2 Motion readback).
const mobHurtKnockbackStrength = float64(float32(0.4))

// mobKnockbackResistance reads the effective instance value, including the
// generated species default and command modifiers. Bounds also permit negative
// resistance, which increases the push.
func (s *Server) mobKnockbackResistance(e *Entity) float64 {
	name, _ := EntityTypeName(e.Type)
	return sanitizeAttributeValue("knockback_resistance", s.mobAttr(e, name, "knockback_resistance", 0))
}
