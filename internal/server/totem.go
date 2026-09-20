package server

// Totem of undying — LivingEntity.checkTotemDeathProtection parity.
// When a lethal non-bypassing hit lands, a player holding an item
// with the death_protection component (totem_of_undying is the only
// vanilla carrier) survives: the stack shrinks by one, the player
// returns to 1 health, the component's death_effects run (clear all,
// then regen II 45s / absorption II 5s / fire resistance 40s), and
// Entity Event 35 plays the pop animation for every viewer.

import (
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// mobEffectInst is one active status effect: amplifier is the
// level-1 index (0 = level I), expiry the game tick it ends at.
type mobEffectInst struct {
	amplifier int32
	expiry    int64
}

// Status-effect state on Session — small fixed map, its own mutex.
// The effect ids are the mob_effect registry protocol ids.
const (
	effectRegeneration   = 9
	effectFireResistance = 11
	effectAbsorption     = 21
)

// damageTypeBypassesInvulnerability is the vanilla damage-type tag
// contents (data/minecraft/tags/damage_type/bypasses_invulnerability):
// /kill and the void ignore totems.
func damageTypeBypassesInvulnerability(name string) bool {
	return name == "out_of_world" || name == "generic_kill"
}

// tryTotem runs vanilla's checkTotemDeathProtection in the same order:
// main hand then off hand, first stack carrying death protection wins;
// the stack shrinks, the stat and trigger see the pre-shrink item,
// health resets to 1, death effects apply, and event 35 broadcasts.
// mc-go models death_protection as the totem item's default component —
// arbitrary items carrying it via /item components are not yet
// representable.
func (s *Server) tryTotem(sess *Session) bool {
	totemID, _ := world.ItemID("totem_of_undying")
	for _, slot := range []int16{invSlotForHotbar(sess.held.Load()), slotOffhand} {
		st := sess.stackAt(slot)
		if st.Empty() || st.ID != totemID {
			continue
		}
		st.Count--
		if st.Count <= 0 {
			st = ItemStack{}
		}
		sess.setStack(slot, st)
		_ = s.sendContainerSlot(sess, slot, st)
		sess.addItemStat(statUsed, totemID, 1)
		s.fireUsedTotem(sess)
		sess.health = 1
		sess.healthDirty.Store(true)
		s.clearMobEffects(sess)
		s.applyMobEffect(sess, effectRegeneration, 1, 900)
		s.applyMobEffect(sess, effectAbsorption, 1, 100)
		s.applyMobEffect(sess, effectFireResistance, 0, 800)
		s.broadcastEntityEvent(sess.entityID.Load(), 35)
		return true
	}
	return false
}

// applyMobEffect stores an active effect and emits Update Mob Effect
// to the player and everyone tracking them — vanilla's addEffect
// broadcasts to both.
func (s *Server) applyMobEffect(sess *Session, id, amplifier int32, duration int32) {
	s.applyMobEffectFlags(sess, id, amplifier, duration, false)
}

// applyMobEffectFlags is applyMobEffect with the /effect hideParticles
// flag: hiding clears bit 0x02 of vanilla's applied-effect mask 0x0E.
// A duration < 0 is vanilla's infinite and never expires.
func (s *Server) applyMobEffectFlags(sess *Session, id, amplifier int32, duration int32, hideParticles bool) {
	sess.effMu.Lock()
	if sess.effects == nil {
		sess.effects = map[int32]mobEffectInst{}
	}
	expiry := int64(-1)
	if duration >= 0 {
		expiry = s.clock.count + int64(duration)
	}
	sess.effects[id] = mobEffectInst{amplifier: amplifier, expiry: expiry}
	sess.effMu.Unlock()
	// Vanilla's onEffectAdded raises the absorb pool to the effect's
	// ceiling — 4 half-hearts per level — but never lowers it.
	if id == effectAbsorption {
		pool := float32(4 * (amplifier + 1))
		if pool > sess.absorb {
			sess.absorb = pool
		}
	}
	var b protocol.Buffer
	b.VarInt(sess.entityID.Load())
	b.VarInt(id) // bare mob_effect registry protocol id
	b.VarInt(amplifier)
	b.VarInt(duration)
	flags := byte(0x0E) // particles + icon + blend — vanilla's applied-effect flags
	if hideParticles {
		flags &^= 0x02
	}
	b.Byte(flags)
	body := b.Bytes()
	_ = sess.write(sess.ids().PlayUpdateMobEffect, body)
	s.broadcastToDim(sess.dimName(), sess, sess.ids().PlayUpdateMobEffect, body)
	s.fireEffectsChanged(sess)
}

// clearMobEffects is vanilla's ClearAllStatusEffectsConsumeEffect: each
// active effect leaves with a Remove Mob Effect packet.
func (s *Server) clearMobEffects(sess *Session) {
	sess.effMu.Lock()
	ids := make([]int32, 0, len(sess.effects))
	for id := range sess.effects {
		ids = append(ids, id)
	}
	sess.effects = nil
	sess.effMu.Unlock()
	for _, id := range ids {
		s.removeMobEffect(sess, id)
	}
}

// removeMobEffect drops one effect and emits Remove Mob Effect.
func (s *Server) removeMobEffect(sess *Session, id int32) {
	if id == effectAbsorption {
		sess.absorb = 0
	}
	sess.effMu.Lock()
	delete(sess.effects, id)
	sess.effMu.Unlock()
	var b protocol.Buffer
	b.VarInt(sess.entityID.Load())
	b.VarInt(id)
	body := b.Bytes()
	_ = sess.write(sess.ids().PlayRemoveMobEffect, body)
	s.broadcastToDim(sess.dimName(), sess, sess.ids().PlayRemoveMobEffect, body)
	s.fireEffectsChanged(sess)
}

// broadcastToDim writes body to every play-state session in dim except
// `except` — the entity's owner gets the direct write, trackers the
// fanout (vanilla's broadcastToPlayerAndTrackers shape).
func (s *Server) broadcastToDim(dim string, except *Session, id int32, body []byte) {
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other == except || other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		_ = other.write(id, body)
		return true
	})
}

// mobEffectTickStep expires active effects on the game clock and emits
// Remove Mob Effect, matching vanilla's LivingEntity.tickEffects.
func (s *Server) mobEffectTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		sess.effMu.Lock()
		var expired []int32
		for id, e := range sess.effects {
			if e.expiry >= 0 && s.clock.count >= e.expiry {
				expired = append(expired, id)
				delete(sess.effects, id)
			}
		}
		sess.effMu.Unlock()
		for _, id := range expired {
			s.removeMobEffect(sess, id)
		}
		return true
	})
}
