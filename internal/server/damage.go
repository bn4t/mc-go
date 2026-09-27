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
		// Damage events reach trackers plus the victim itself —
		// vanilla's sendToTrackingPlayersAndSelf.
		if !sess.tracksEntity(eid) && sess.entityID.Load() != eid {
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
	hasPos   bool // explicit source position (the /damage `at` form)
	x, y, z  float64
}

var (
	srcGeneric = damageSource{typeName: "generic"}
	srcFall    = damageSource{typeName: "fall"}
	srcStarve  = damageSource{typeName: "starve"}
	srcKill    = damageSource{typeName: "generic_kill"}
	srcVoid    = damageSource{typeName: "out_of_world"}
	srcLava    = damageSource{typeName: "lava"}
	srcDrown   = damageSource{typeName: "drown"}
	srcInFire  = damageSource{typeName: "in_fire"}
	srcInWall  = damageSource{typeName: "in_wall"}
	srcFreeze  = damageSource{typeName: "freeze"}
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
	// and a stronger one lands only for its excess. That excess branch
	// (hurt's b8=false arm) runs actuallyHurt alone: no damage event,
	// no hurt sound, no knockback, no fresh invulnerability window —
	// but die() and the stats/post-attack tail still fire.
	graced := sess.invulnTicks > 10
	orig := amount
	if graced {
		if orig <= sess.lastHurt {
			return 0
		}
		amount = orig - sess.lastHurt
	}
	sess.lastHurt = orig
	if !graced {
		sess.invulnTicks = 20
	}
	sess.lastHurtBy = src.cause
	// Vanilla marks lastHurtByPlayer only when the damage source's
	// entity is a player — mob and unattributed hurts leave the
	// credit window untouched.
	if cs := s.sessionByEntityID(src.cause); cs != nil {
		sess.lastHurtPlayerBy = cs.uuid
		sess.lastHurtPlayerTicks = 100
	}
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
	// DAMAGES_HELMET types (falling_anvil/block/stalactite): hurtHelmet
	// wears the head piece on the post-grace amount, then a flat 25%
	// absorbs before the armor formula — vanilla runs both inside
	// hurtServer ahead of getDamageAfterArmorAbsorb.
	if damagesHelmet(src.typeName) {
		sess.invMu.Lock()
		hasHelmet := !sess.inv[slotArmorStart].Empty()
		sess.invMu.Unlock()
		if hasHelmet {
			s.hurtHelmet(sess, src, amount)
			amount *= 0.75
		}
	}
	// A hit that armor can absorb wears the equipped pieces first —
	// on the amount still standing after the i-frames delta — and is
	// then reduced by the armor formula. The defense value is the
	// pre-wear total: pieces that break still count for this hit.
	if !bypassesArmor(src.typeName) {
		defense := sess.armorDefense()
		s.armorWear(sess, src, amount)
		amount = reduceDamage(amount, defense, sess.armorToughness(), s.weaponBreach(src.direct))
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
	// LivingEntity.actuallyHurt posts ENTITY_DAMAGE at the hurt
	// position — shield-blocked hits and fully graced repeats never
	// reach this point.
	{
		x, y, z, _, _, _ := sess.Pos()
		s.emitGameEvent(sess.dimension(), "entity_damage", x, y, z, sess, "")
	}
	// Vanilla counts damage in tenths of a heart.
	sess.addCustomStat("damage_taken", int32(amount*10))
	eid := sess.entityID.Load()
	if !graced {
		s.broadcastDamageEvent(eid, damageInfo{
			typeName: src.typeName,
			cause:    src.cause,
			direct:   src.direct,
			hasPos:   src.hasPos,
			x:        src.x, y: src.y, z: src.z,
		})
	}
	// damage_dealt-style accounting counts health lost, not the raw
	// hit: an overkill blow still reports only what remained.
	healthBefore := sess.health
	s.damage(sess, amount, message, src)
	if !graced {
		s.playerHurtVoice(sess, src)
		// LivingEntity.hurt's secondary hurt sound: thorns damage
		// additionally plays enchant.thorns.hit — on the PLAYERS
		// source for a player victim — at flat volume and pitch,
		// and unlike the hurt voice it is NOT excluded for the victim.
		if src.typeName == "thorns" {
			x, y, z, _, _, _ := sess.Pos()
			s.playSoundSrc(sess.dimName(), x, y, z, sndThornsHit, srcPlayers, 1, 1, nil)
		}
	}
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
	kbSrc := src.cause
	if kbSrc == 0 {
		// getSourcePosition falls back to the direct entity — an
		// ownerless projectile still shoves away from where it hit.
		kbSrc = src.direct
	}
	if kbSrc != 0 && kbSrc != eid && !noKnockback(src.typeName) &&
		src.typeName != "player_attack" && !graced {
		s.playerHurtKnockback(sess, kbSrc)
	}
	// EnchantmentHelper.doPostAttackEffects, enchanted=victim side:
	// every thorns piece the victim wears rolls its 15%·level proc
	// against the hurt's source entity (nil sources don't retaliate).
	if src.cause != 0 {
		s.thornsVictimWear(sess, src.cause)
	}
	return amount
}

// Player sound registry ids for the hurt() voice path — the damage
// type selects the hurt variant the way Player.getHurtSound does.
const (
	sndPlayerDeath     = 1311
	sndPlayerHurt      = 1312
	sndPlayerDrown     = 1313
	sndPlayerFreeze    = 1314
	sndPlayerOnFire    = 1315
	sndPlayerBerryBush = 1316
	sndThornsHit       = 1622 // enchant.thorns.hit
	srcHostile         = 5
	srcPlayers         = 7
)

// damageTypeEffects is each damage_type's datapack `effects` field —
// the Player.getHurtSound dispatch reads it through
// DamageEffects.sound(). Types absent from the map use the default
// `hurt` effect; `thorns` deliberately maps to PLAYER_HURT too
// (DamageEffects.THORNS.sound() is PLAYER_HURT in 26.2).
var damageTypeEffects = map[string]int32{
	// burning
	"campfire": sndPlayerOnFire, "fireball": sndPlayerOnFire,
	"hot_floor": sndPlayerOnFire, "in_fire": sndPlayerOnFire,
	"lava": sndPlayerOnFire, "on_fire": sndPlayerOnFire,
	"sulfur_cube_hot": sndPlayerOnFire, "unattributed_fireball": sndPlayerOnFire,
	// drowning
	"drown": sndPlayerDrown,
	// freezing
	"freeze": sndPlayerFreeze,
	// poking
	"sweet_berry_bush": sndPlayerBerryBush,
}

// playerHurtVoice is LivingEntity.playHurtSound on the player victim
// path — a positional sound at the victim's feet on the PLAYERS
// source, broadcast to every listener EXCEPT the victim (its client
// plays the hurt locally off the damage event). A lethal hit carries
// entity.player.death in place of the hurt sound. getHurtSound picks
// by the damage type's effects field, not the type name.
func (s *Server) playerHurtVoice(sess *Session, src damageSource) {
	x, y, z, _, _, _ := sess.Pos()
	snd := int32(sndPlayerHurt)
	if sess.dead {
		snd = sndPlayerDeath
	} else if v, ok := damageTypeEffects[src.typeName]; ok {
		snd = v
	}
	s.playSoundSrc(sess.dimName(), x, y, z, snd, srcPlayers, 1, voicePitch(), sess)
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
		s.damageMob(ae, dmg, "thorns", s.sessionByEntityID(victimEid), &damageInfo{
			typeName: "thorns",
			cause:    victimEid,
			direct:   victimEid,
		})
	}
}

// weaponBreach resolves the breach level on the damage source's
// weapon item — vanilla's DamageSource.getWeaponItem is the DIRECT
// entity's mainhand (a player-held stack or a mob's Equips[0]).
func (s *Server) weaponBreach(direct int32) float32 {
	if direct == 0 {
		return 0
	}
	if sess := s.sessionByEntityID(direct); sess != nil {
		return float32(sess.heldStack().enchLevel("breach"))
	}
	if e, ok := s.entities.Get(direct); ok && len(e.Equips) > 0 {
		return float32(e.Equips[0].enchLevel("breach"))
	}
	return 0
}
