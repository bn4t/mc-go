package server

import "mc-go/internal/protocol"

// damageInfo describes one damage source for the Damage Event packet.
type damageInfo struct {
	typeName string // minecraft:damage_type registry entry
	cause    int32  // responsible entity id, or 0
	direct   int32  // directly dealing entity id, or 0
	hasPos   bool
	x, y, z  float64
}

// sendDamageEvent broadcasts one Damage Event for an entity to its
// dimension. The damage type resolves against the captured damage_type
// registry; an unknown type falls back to minecraft:generic.
func (s *Server) broadcastDamageEvent(eid int32, info damageInfo) {
	if eid == 0 {
		return
	}
	dim := dimOverworld
	if e, ok := s.entities.Get(eid); ok {
		dim = e.Dim
	}
	typeID := registryEntryID("minecraft:damage_type", "minecraft:"+info.typeName)
	if typeID < 0 {
		typeID = registryEntryID("minecraft:damage_type", "minecraft:generic")
		if typeID < 0 {
			typeID = 0
		}
	}
	var b protocol.Buffer
	b.VarInt(eid)
	b.VarInt(typeID)
	cause := int32(0)
	if info.cause != 0 {
		cause = info.cause + 1
	}
	b.VarInt(cause)
	direct := int32(0)
	if info.direct != 0 {
		direct = info.direct + 1
	}
	b.VarInt(direct)
	b.Bool(info.hasPos)
	if info.hasPos {
		b.Double(info.x)
		b.Double(info.y)
		b.Double(info.z)
	}
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		if sess.dimName() != dim {
			return true
		}
		_ = sess.write(sess.ids().PlayDamageEvent, body)
		return true
	})
}

// combatTimeoutTicks is vanilla's 15-second combat tag.
const combatTimeoutTicks = 15 * 20

// startCombat tags a player as in combat, sending Enter Combat the
// first time.
func (s *Server) startCombat(sess *Session) {
	if sess.combatTicks > 0 {
		sess.combatTicks = combatTimeoutTicks
		return
	}
	sess.combatTicks = combatTimeoutTicks
	var b protocol.Buffer // Enter Combat has an empty body
	_ = sess.write(sess.ids().PlayEnterCombat, b.Bytes())
}

// combatTickStep counts combat timers down and emits End Combat when a
// player leaves combat.
func (s *Server) combatTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.combatTicks <= 0 {
			return true
		}
		sess.combatTicks--
		if sess.combatTicks == 0 {
			var b protocol.Buffer
			b.VarInt(combatTimeoutTicks)
			_ = sess.write(sess.ids().PlayEndCombat, b.Bytes())
		}
		return true
	})
}

// damageSource tracks how a damage call should be attributed.
type damageSource struct {
	typeName string
	cause    int32
	direct   int32
}

var (
	srcGeneric = damageSource{typeName: "generic"}
	srcFall    = damageSource{typeName: "fall"}
	srcStarve  = damageSource{typeName: "starve"}
	srcKill    = damageSource{typeName: "generic_kill"}
	srcVoid    = damageSource{typeName: "out_of_world"}
)

// sessInvulnerableTo mirrors vanilla's hurtServer gate: creative and
// spectator abilities make the player invulnerable to everything except
// the bypasses_invulnerability damage types (void, /kill).
func sessInvulnerableTo(sess *Session, typeName string) bool {
	m := sess.mode()
	if m != GameModeCreative && m != GameModeSpectator {
		return false
	}
	return !damageTypeBypassesInvulnerability(typeName)
}

// voidTickStep damages players under the out-of-world band (64 blocks
// below each dimension's floor) on every player tick; vanilla hits
// there once a tick, and the post-hit invulnerability is what paces the
// visible damage to roughly every ten.
func (s *Server) voidTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dead {
			return true
		}
		_, y, _, _, _, _ := sess.Pos()
		if y >= float64(sess.dimension().minY-64) {
			return true
		}
		s.damageWith(sess, 4, translatable("death.attack.outOfWorld",
			playerChatComponent(sess)), srcVoid)
		return true
	})
}

// damageWith applies damage with an explicit source, emitting the
// Damage Event before the health sync, and returns the damage that
// actually landed after armor absorbs it (0 when the hit was
// rejected by the post-hit invulnerability window).
func (s *Server) damageWith(sess *Session, amount float32, message component, src damageSource) float32 {
	if sess.dead || sessInvulnerableTo(sess, src.typeName) {
		return 0
	}
	// Vanilla grants twenty ticks of invulnerability after a hit; while
	// more than half remains, a hit no stronger than the last is ignored
	// and a stronger one lands only for its excess.
	orig := amount
	if sess.invulnTicks > 10 {
		if orig <= sess.lastHurt {
			return 0
		}
		amount = orig - sess.lastHurt
	}
	sess.lastHurt = orig
	sess.invulnTicks = 20
	sess.lastHurtBy = src.cause
	// A raised shield negates projectile damage outright: vanilla swaps
	// the damage event for the blocked-by-shield type, plays the shield
	// sound, and fires entity_hurt_player with blocked=true. The hit
	// still costs the shield durability on vanilla; the item model has
	// no durability on players yet.
	if sess.blocking.Load() && src.typeName == "arrow" {
		s.broadcastAttackSound(sess, 1455) // minecraft:item.shield.block
		s.fireEntityHurtPlayer(sess, true, true)
		return 0
	}
	// A hit that armor can absorb wears the equipped pieces first —
	// on the amount still standing after the i-frames delta — and is
	// then reduced by the armor formula. The defense value is the
	// pre-wear total: pieces that break still count for this hit.
	if !bypassesArmor(src.typeName) {
		defense := sess.armorDefense()
		s.armorWear(sess, src, amount)
		amount = reduceDamage(amount, defense)
	}
	// Vanilla counts damage in tenths of a heart.
	sess.addCustomStat("damage_taken", int32(amount*10))
	eid := sess.entityID.Load()
	x, y, z, _, _, _ := sess.Pos()
	s.broadcastDamageEvent(eid, damageInfo{
		typeName: src.typeName,
		cause:    src.cause,
		direct:   src.direct,
		hasPos:   src.cause != 0,
		x:        x, y: y, z: z,
	})
	// damage_dealt-style accounting counts health lost, not the raw
	// hit: an overkill blow still reports only what remained.
	healthBefore := sess.health
	s.damage(sess, amount, message, src)
	if amount > healthBefore {
		amount = healthBefore
	}
	s.fireEntityHurtPlayer(sess, src.typeName == "arrow", false)
	return amount
}
