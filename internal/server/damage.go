package server

import (
	"math/rand"

	"mc-go/internal/protocol"
)

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
		// Non-creative invulnerability also comes from equipment —
		// vanilla's isInvulnerableTo folds in the enchantment
		// damage_immunity component (frost walker vs burn_from_stepping).
		return sess.enchantImmunity(typeName)
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
		amount = reduceDamage(amount, defense, sess.armorToughness())
	}
	// getDamageAfterEnchantAbsorb: the EPF from protection-family
	// enchantments shaves min(20, epf)/25 off what armor left standing.
	// Only sonic_boom bypasses enchantments.
	if src.typeName != "sonic_boom" {
		if epf := sess.enchantProtection(src.typeName); epf > 0 {
			red := epf
			if red > 20 {
				red = 20
			}
			amount *= 1 - red/25
		}
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
	// dealDefaultKnockback: an entity-sourced hurt that isn't in
	// no_knockback shoves the victim 0.4 away from the source's
	// position, scaled by the victim's knockback_resistance (a
	// netherite set eats 40%). /damage ... by takes this path too.
	// player_attack skips it — Player.attack's hurt call carries the
	// flag that tells hurtServer the attack applies knockback itself
	// (playerAttackKnockback owns that push).
	if src.cause != 0 && src.cause != eid && !noKnockback(src.typeName) &&
		src.typeName != "player_attack" {
		s.playerHurtKnockback(sess, src.cause)
	}
	// EnchantmentHelper.doPostAttackEffects, enchanted=victim side:
	// every thorns piece the victim wears rolls its 15%·level proc
	// against the hurt's source entity (nil sources don't retaliate).
	if src.cause != 0 {
		s.thornsVictimWear(sess, src.cause)
	}
	return amount
}

// playerHurtKnockback applies LivingEntity.dealDefaultKnockback to a
// player victim: 0.4 of push directly away from the damage source's
// position. The hurt-marked delta sync reaches every tracker —
// vanilla also ships the packet to the victim itself. The victim's
// standing delta is treated as zero, matching playerAttackKnockback
// (vanilla folds whatever the client last reported, which for a
// stationary target is zero anyway).
func (s *Server) playerHurtKnockback(sess *Session, srcCause int32) {
	var sx, sz float64
	if cs := s.sessionByEntityID(srcCause); cs != nil {
		sx, _, sz, _, _, _ = cs.Pos()
	} else {
		e, ok := s.entities.Get(srcCause)
		if !ok {
			return
		}
		sx, sz = e.X, e.Z
	}
	px, _, pz, _, _, grounded := sess.Pos()
	vx, vy, vz := knockDelta(0, 0, 0, 0.4, sess.armorKBResist(), sx-px, sz-pz, grounded)
	s.broadcastEntityMotion(sess.dimName(), sess.entityID.Load(), vx, vy, vz)
}

// thornsVictimWear is runIterationOnEquipment over the victim's
// equipment (hands first, then feet..head like EquipmentSlot): each
// stack with thorns independently rolls 0.15·level; a proc hurts the
// attacker and costs that piece a flat 2 durability (the datapack's
// change_item_damage 2, which itself passes through unbreaking).
func (s *Server) thornsVictimWear(sess *Session, srcCause int32) {
	slots := [6]int16{
		invSlotForHotbar(sess.held.Load()), slotOffhand,
		slotArmorStart + 3, slotArmorStart + 2, slotArmorStart + 1, slotArmorStart,
	}
	for _, sl := range slots {
		it := sess.stackAt(sl)
		l := it.enchLevel("thorns")
		if l <= 0 || rand.Float32() >= 0.15*float32(l) {
			continue
		}
		s.thornsAttack(sess.entityID.Load(), srcCause, literal(sess.getName()))
		s.wearSlot(sess, sl, 2)
	}
}

// thornsMobWear is the same pass for a mob victim: proc per equipped
// thorns stack, attacker hurt, the piece wears 2 and can break.
func (s *Server) thornsMobWear(e *Entity, attacker *Session) {
	for slot, it := range e.Equips {
		l := it.enchLevel("thorns")
		if l <= 0 || rand.Float32() >= 0.15*float32(l) {
			continue
		}
		kn, _ := EntityTypeName(e.Type)
		s.thornsAttack(e.ID, attacker.entityID.Load(), literal(mobDisplayName(kn)))
		if name, ok := itemNameOf(it.ID); ok {
			if maxDmg, dmgOK := damageableItems[name]; dmgOK {
				it.Damage += unbreakingReduce(it, name, 2)
				if it.Damage >= maxDmg {
					delete(e.Equips, slot)
				} else {
					e.Equips[slot] = it
				}
			}
		}
	}
}

// thornsAttack deals one thorns proc to the hurt's source entity:
// uniform[1,5) damage with the victim as the causing entity, exactly
// the damage_entity effect in the thorns datapack entry. Chains are
// vanilla-legal — a thorns-armored attacker can proc the victim's
// thorns right back.
func (s *Server) thornsAttack(victimEid, srcCause int32, victimName component) {
	if srcCause == 0 || srcCause == victimEid {
		return
	}
	dmg := 1 + rand.Float32()*4
	src := damageSource{typeName: "thorns", cause: victimEid, direct: victimEid}
	if as := s.sessionByEntityID(srcCause); as != nil {
		s.damageWith(as, dmg, translatable("death.attack.thorns",
			literal(as.getName()), victimName), src)
		return
	}
	s.entities.mu.RLock()
	ae := s.entities.byID[srcCause]
	s.entities.mu.RUnlock()
	if ae != nil && ae.Living {
		s.damageMob(ae, dmg, "thorns", s.sessionByEntityID(victimEid))
	}
}
