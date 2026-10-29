package server

import (
	"math"
	"mc-go/internal/world"
)

// damageItemEntity follows ItemEntity.hurtServer. Item health is an integer
// counter, with a float subtraction and truncation on every accepted hit.
func (s *Server) damageItemEntity(e *Entity, amount float32, kind string, cause int32) bool {
	if current, ok := s.entities.GetPtr(e.ID); !ok || current != e {
		return false
	}
	creative := false
	if p := s.sessionByEntityID(cause); p != nil {
		creative = p.mode() == 1
	}
	if e.Invulnerable && !damageTypeBypassesInvulnerability(kind) && !creative {
		return false
	}
	if isFireDamageType[kind] && itemFireImmune(e) {
		return false
	}
	if !s.gameRuleBool("minecraft:mob_griefing") {
		if c, ok := s.entities.GetPtr(cause); ok && c.Living {
			n, _ := EntityTypeName(c.Type)
			if n != "player" && n != "armor_stand" && n != "mannequin" {
				return false
			}
		}
	}
	n, _ := world.ItemName(e.Item.ID)
	if n == "nether_star" && itemExplosionDamage[kind] {
		return false
	}
	e.HurtMarked = true
	v := float32(e.ItemHP) - amount
	switch {
	case math.IsNaN(float64(v)):
		e.ItemHP = 0
	case v >= 2147483648:
		e.ItemHP = math.MaxInt32
	case v <= -2147483648:
		e.ItemHP = math.MinInt32
	default:
		e.ItemHP = int32(v)
	}
	if d := s.dimensionByName(e.Dim); d != nil {
		s.emitGameEvent(d, "entity_damage", e.X, e.Y, e.Z, nil, "")
	}
	if e.ItemHP <= 0 {
		s.spillDestroyedBundle(e)
		s.entities.Remove(e.ID)
		s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
	}
	return true
}

var itemExplosionDamage = map[string]bool{"fireworks": true, "explosion": true, "player_explosion": true, "bad_respawn_point": true}
