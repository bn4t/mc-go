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
// hidden is vanilla's hiddenEffect: a lower-amplifier or replaced
// instance that resumes when the stronger one ends; its expiry is
// absolute too, so it keeps decaying while hidden (vanilla ticks
// the hidden instance down alongside the active one).
type mobEffectInst struct {
	amplifier int32
	expiry    int64
	hide      bool
	hidden    *mobEffectInst
}

// Status-effect state on Session — small fixed map, its own mutex.
// The effect ids are the mob_effect registry protocol ids.
const (
	effectInstantHealth  = 5
	effectInstantDamage  = 6
	effectRegeneration   = 9
	effectFireResistance = 11
	effectAbsorption     = 21
	effectLevitation     = 24
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

// effectExpiry converts a /effect duration to an absolute game tick;
// negative durations are vanilla's infinite.
func effectExpiry(now int64, ticks int32) int64 {
	if ticks < 0 {
		return -1
	}
	return now + int64(ticks)
}

// shorterDur is vanilla's MobEffectInstance.isShorterDurationThan:
// this must be finite and its remaining duration <= the other's (an
// infinite other counts as duration -1, so nothing is shorter).
func shorterDur(this, other mobEffectInst, now int64) bool {
	if this.expiry < 0 {
		return false
	}
	otherDur := int64(-1)
	if other.expiry >= 0 {
		otherDur = other.expiry - now
	}
	return this.expiry-now <= otherDur
}

// stashHiddenEffect stores a weaker-but-longer instance under the
// active one's hiddenEffect — MobEffectInstance.update's else arm.
func (s *Server) stashHiddenEffect(sess *Session, id int32, inst mobEffectInst) {
	sess.effMu.Lock()
	defer sess.effMu.Unlock()
	e, ok := sess.effects[id]
	if !ok {
		return
	}
	if e.hidden == nil {
		e.hidden = &inst
	} else {
		e.hidden.mergeHidden(inst, s.clock.count)
	}
	sess.effects[id] = e
}

// mergeHidden applies MobEffectInstance.update to a hidden chain
// node: stronger replaces (pushing this under it when shorter),
// same-amplifier-longer extends, weaker-longer recurses deeper.
func (e *mobEffectInst) mergeHidden(other mobEffectInst, now int64) {
	switch {
	case other.amplifier > e.amplifier:
		keep := e.hidden
		if shorterDur(other, *e, now) {
			prev := *e
			other.hidden = &prev
			*e = other
			return
		}
		*e = other
		e.hidden = keep
	case other.amplifier == e.amplifier && shorterDur(*e, other, now):
		e.expiry = other.expiry
	case other.amplifier != e.amplifier && shorterDur(*e, other, now):
		if e.hidden == nil {
			e.hidden = &other
		} else {
			e.hidden.mergeHidden(other, now)
		}
	}
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
	inst := mobEffectInst{amplifier: amplifier, expiry: expiry, hide: hideParticles}
	if prev, ok := sess.effects[id]; ok {
		switch {
		case prev.amplifier < amplifier:
			// The stronger instance replaces the active one; when it is
			// shorter (other.isShorterDurationThan: finite new duration
			// <= the old remaining) vanilla pushes the old instance into
			// the new one's hiddenEffect so it resumes afterwards.
			if duration >= 0 && prev.expiry >= 0 && int64(duration) <= prev.expiry-s.clock.count {
				inst.hidden = &prev
			}
		case prev.amplifier == amplifier:
			// Same-amplifier update: the duration is replaced outright
			// (the gate already ensured the new one is longer) and the
			// hidden chain carries over untouched.
			inst.hidden = prev.hidden
		}
	}
	sess.effects[id] = inst
	sess.effMu.Unlock()
	if id == effectLevitation {
		x, y, z, _, _, _ := sess.Pos()
		sess.levitationStart = []float64{x, y, z}
		sess.levitationStartTick = s.clock.count
	}
	// Vanilla's onEffectAdded raises the absorb pool to the effect's
	// ceiling — 4 half-hearts per level — but never lowers it.
	if id == effectAbsorption {
		pool := float32(4 * (amplifier + 1))
		if pool > sess.absorb {
			sess.absorb = pool
		}
	}
	// onEffectStarted fires at the end of addEffect even when the
	// store was declined — instant effects apply on every call.
	s.applyInstantEffect(sess, id, amplifier)
	s.sendMobEffectUpdate(sess, id, inst)
	s.fireEffectsChanged(sess)
}

// applyInstantEffect is MobEffectInstance.onEffectStarted's
// instantenous arm (HealOrHarmMobEffect): instant_health heals
// 4<<amplifier, instant_damage hurts 6<<amplifier as magic. The
// int32<<level shifts wrap exactly like Java's int math — large
// amplifiers land on nonpositive amounts that max(...,0) drops.
func (s *Server) applyInstantEffect(sess *Session, id, amplifier int32) {
	switch id {
	case effectInstantHealth:
		if !sess.dead {
			amount := max(int32(4)<<amplifier, 0)
			sess.health = min(sess.health+float32(amount), defaultHealth)
			sess.healthDirty.Store(true)
		}
	case effectInstantDamage:
		amount := max(int32(6)<<amplifier, 0)
		s.damageWith(sess, float32(amount), translatable("death.attack.magic", literal(sess.getName())),
			damageSource{typeName: "magic"})
	}
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
	if id == effectLevitation {
		sess.levitationStart = nil
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

func mobEffectFlags(hideParticles bool) byte {
	flags := byte(0x0E) // particles + icon + blend — vanilla's applied-effect flags
	if hideParticles {
		flags &^= 0x02
	}
	return flags
}

// sendMobEffectUpdate emits one Update Mob Effect for the live
// instance of id — used on give and on the hidden-effect downgrade.
func (s *Server) sendMobEffectUpdate(sess *Session, id int32, e mobEffectInst) {
	var b protocol.Buffer
	b.VarInt(sess.entityID.Load())
	b.VarInt(id)
	b.VarInt(e.amplifier)
	remaining := e.expiry - s.clock.count
	if e.expiry < 0 {
		remaining = -1
	}
	b.VarInt(int32(remaining))
	b.Byte(mobEffectFlags(e.hide))
	body := b.Bytes()
	_ = sess.write(sess.ids().PlayUpdateMobEffect, body)
	s.broadcastToDim(sess.dimName(), sess, sess.ids().PlayUpdateMobEffect, body)
}

// mobEffectTickStep expires active effects on the game clock and emits
// Remove Mob Effect, matching vanilla's LivingEntity.tickEffects. When
// the expiring instance carries a hiddenEffect the slot downgrades to
// it instead of clearing (MobEffectInstance.downgradeToHiddenEffect),
// which surfaces as an Update Mob Effect with the lower amplifier.
func (s *Server) mobEffectTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay {
			return true
		}
		sess.effMu.Lock()
		var expired, downgraded []int32
		for id, e := range sess.effects {
			if e.expiry >= 0 && s.clock.count >= e.expiry {
				if e.hidden != nil {
					sess.effects[id] = *e.hidden
					downgraded = append(downgraded, id)
					continue
				}
				expired = append(expired, id)
				delete(sess.effects, id)
			}
		}
		sess.effMu.Unlock()
		for _, id := range expired {
			s.removeMobEffect(sess, id)
		}
		for _, id := range downgraded {
			e, live := sessEffect(sess, id, s.clock.count)
			if live {
				s.sendMobEffectUpdate(sess, id, e)
				s.fireEffectsChanged(sess)
			}
		}
		return true
	})
}
