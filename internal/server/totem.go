package server

// Totem of undying — LivingEntity.checkTotemDeathProtection parity.
// When a lethal non-bypassing hit lands, a player holding an item
// with the death_protection component (totem_of_undying is the only
// vanilla carrier) survives: the stack shrinks by one, the player
// returns to 1 health, the component's death_effects run (clear all,
// then regen II 45s / absorption II 5s / fire resistance 40s), and
// Entity Event 35 plays the pop animation for every viewer.

import (
	"math"
	"strings"

	"mc-go/internal/nbt"
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
	// born is the game clock at creation. Effects are applied mid-tick
	// (commands run off the tick loop), so the instance's first
	// observed tick is the step after its birth — vanilla's instance
	// ticks on the give tick itself with duration=dur, which is why the
	// fire checks below read rem+1 as the effective duration.
	born int64
	hide bool
	// flags pins the wire flags byte when nonzero or explicitly set;
	// otherwise derive the byte from hide (mobEffectFlags).
	flags    byte
	flagsSet bool
	hidden   *mobEffectInst
}

// Status-effect state on Session — small fixed map, its own mutex.
// The effect ids are the mob_effect registry protocol ids.
const (
	effectSpeed          = 0
	effectInstantHealth  = 5
	effectInstantDamage  = 6
	effectRegeneration   = 9
	effectFireResistance = 11
	effectAbsorption     = 21
	effectGlowing        = 23
	effectLevitation     = 24
	effectSlowFalling    = 27
	effectBadOmen        = 30
	effectHeroOfVillage  = 31
	effectRaidOmen       = 34
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

// mobEffectsNBT is the active_effects list of the entity save: each
// instance serializes id, duration (zero omitted; -1 = infinite),
// amplifier (omitted at 0 — a /data get of it replies
// nothing_found on vanilla), ambient/show_particles (default-omitted),
// show_icon, and a nested hidden_effect for a queued instance.
func mobEffectsNBT(effects map[int32]mobEffectInst, now int64) nbt.List {
	if len(effects) == 0 {
		return nbt.List{Elem: nbt.TagEnd}
	}
	items := make([]any, 0, len(effects))
	for id, e := range effects {
		items = append(items, mobEffectNBT(id, e, now))
	}
	return nbt.List{Elem: nbt.TagCompound, Items: items}
}

func mobEffectNBT(id int32, e mobEffectInst, now int64) map[string]any {
	name, _ := MobEffectName(id)
	m := map[string]any{"id": "minecraft:" + name}
	if e.amplifier != 0 {
		m["amplifier"] = int8(e.amplifier)
	}
	dur := int64(-1)
	if e.expiry >= 0 {
		dur = e.expiry - now
	}
	if dur != 0 {
		m["duration"] = int32(dur)
	}
	flags := e.flags
	if flags == 0 && !e.flagsSet {
		flags = mobEffectFlags(e.hide)
	}
	if flags&1 != 0 {
		m["ambient"] = int8(1)
	}
	if flags&2 == 0 {
		m["show_particles"] = int8(0)
	}
	m["show_icon"] = int8(0)
	if flags&4 != 0 {
		m["show_icon"] = int8(1)
	}
	if e.hidden != nil {
		hidden := mobEffectNBT(id, *e.hidden, now)
		delete(hidden, "id")
		m["hidden_effect"] = hidden
	}
	return m
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
		e.hidden.mergeHidden(inst, sess.effectNow())
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
// flag: hiding clears particles and icon from the fresh-effect mask 0x0E.
// A duration < 0 is vanilla's infinite and never expires.
func (s *Server) applyMobEffectFlags(sess *Session, id, amplifier int32, duration int32, hideParticles bool) {
	s.applyMobEffectWire(sess, id, amplifier, duration, hideParticles, mobEffectFlags(hideParticles))
}

// applyMobEffectWire is applyMobEffectFlags with the packet's flags
// byte pinned — vanilla constructs some instances with flag
// combinations the hide-derived mask can't express (raid-victory hero
// status is icon-only: ambient=false, visible=false, showIcon=true).
func (s *Server) applyMobEffectWire(sess *Session, id, amplifier int32, duration int32, hideParticles bool, flags byte) {
	sess.effMu.Lock()
	if sess.effects == nil {
		sess.effects = map[int32]mobEffectInst{}
	}
	expiry := int64(-1)
	if duration >= 0 {
		expiry = sess.effectNow() + int64(duration)
	}
	inst := mobEffectInst{amplifier: amplifier, expiry: expiry, born: sess.effectNow(), hide: hideParticles, flags: flags, flagsSet: true}
	if prev, ok := sess.effects[id]; ok {
		inst.flags &^= 8 // onEffectUpdated does not request a fresh blend.
		switch {
		case prev.amplifier < amplifier:
			// The stronger instance replaces the active one; when it is
			// shorter (other.isShorterDurationThan: finite new duration
			// <= the old remaining) vanilla pushes the old instance into
			// the new one's hiddenEffect so it resumes afterwards.
			if duration >= 0 && prev.expiry >= 0 && int64(duration) <= prev.expiry-sess.effectNow() {
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
	s.setPlayerEffectAttributes(sess, id, inst)
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
// 4<<amplifier, instant_damage hurts 6<<amplifier as magic. Java's
// ishl masks the shift to 5 bits (amplifier&31) — Go's << does not,
// so the mask is explicit: amp 200 lands on 4<<8=1024 like vanilla.
func (s *Server) applyInstantEffect(sess *Session, id, amplifier int32, src ...damageSource) {
	s.applyInstantEffectScaled(sess, id, amplifier, 1, src...)
}

func (s *Server) applyInstantEffectScaled(sess *Session, id, amplifier int32, scale float64, src ...damageSource) {
	srcs := damageSource{typeName: "magic"}
	if len(src) > 0 {
		srcs = src[0]
	}
	switch id {
	case effectInstantHealth:
		if !sess.dead {
			amount := max(int32(math.Floor(float64(int32(4)<<(amplifier&31))*scale+0.5)), 0)
			sess.healPlayer(float32(amount))
		}
	case effectInstantDamage:
		amount := max(int32(math.Floor(float64(int32(6)<<(amplifier&31))*scale+0.5)), 0)
		msg := translatable("death.attack.magic", literal(sess.getName()))
		if srcs.cause != 0 {
			if ce, ok := s.entities.Get(srcs.cause); ok {
				if cn, nok := EntityTypeName(ce.Type); nok {
					msg = translatable("death.attack.indirectMagic", literal(sess.getName()), translatable("entity.minecraft."+cn))
				}
			}
		}
		s.damageWith(sess, float32(amount), msg, srcs)
	}
}

// playerGiveEffect runs the player half of LivingEntity.addEffect for
// /effect give: the update() gate decides whether the store applies —
// declines and weaker-but-longer stash paths still fire the instant arm
// (vanilla calls onEffectStarted unconditionally at the end of
// addEffect). True reports the store took the instance.
func (s *Server) playerGiveEffect(t *Session, id, amplifier, ticks int32, hideParticles bool, src ...damageSource) bool {
	return s.playerGiveEffectFlags(t, id, amplifier, ticks, hideParticles, nil, src...)
}
func (s *Server) playerGiveEffectFlags(t *Session, id, amplifier, ticks int32, hideParticles bool, flags *byte, src ...damageSource) bool {
	now := t.effectNow()
	if e, live := sessEffect(t, id, now); live {
		newInst := mobEffectInst{amplifier: amplifier, expiry: effectExpiry(now, ticks), born: now}
		switch {
		case amplifier > e.amplifier:
			// fall through and apply
		case amplifier == e.amplifier && shorterDur(e, newInst, now):
			// duration update — apply
		case amplifier < e.amplifier && shorterDur(e, newInst, now):
			s.stashHiddenEffect(t, id, newInst)
			s.applyInstantEffect(t, id, amplifier, src...)
			if flags == nil {
				return s.potionPlayerFlags(t, id, amplifier, ticks, mobEffectFlags(hideParticles), false)
			}
			return false
		default:
			// Declined stores still run onEffectStarted — instant
			// effects apply (measured: a shorter re-give heals
			// while replying give.failed).
			s.applyInstantEffect(t, id, amplifier, src...)
			if flags == nil {
				return s.potionPlayerFlags(t, id, amplifier, ticks, mobEffectFlags(hideParticles), false)
			}
			return false
		}
	}
	if flags == nil {
		s.applyMobEffectFlags(t, id, amplifier, ticks, hideParticles)
	} else {
		s.applyMobEffectWire(t, id, amplifier, ticks, hideParticles, *flags)
	}
	return true
}

// mobEffectImmunity is LivingEntity.canBeAffected's per-type veto
// set — addEffect refuses these effect ids for the type outright.
// Measured on 26.2: WitherSkeleton.canBeAffected returns false for
// WITHER.
var mobEffectImmunity = map[string][]string{
	"wither_skeleton": {"wither"},
}

// mobGiveEffect is the world-entity arm of LivingEntity.addEffect: the
// canBeAffected veto, then the same update() gate on e.Effects,
// update_mob_effect out to trackers, and the instant arm routed
// through the mob heal/hurt helpers.
func (s *Server) mobGiveEffect(e *Entity, id, amplifier, ticks int32, hideParticles bool, src ...*damageInfo) bool {
	return s.mobGiveEffectFlags(e, id, amplifier, ticks, mobEffectFlags(hideParticles), src...)
}

func (s *Server) mobGiveEffectFlags(e *Entity, id, amplifier, ticks int32, flags byte, src ...*damageInfo) bool {
	if kn, _ := EntityTypeName(e.Type); kn != "" {
		for _, ename := range mobEffectImmunity[kn] {
			if eid, ok := MobEffectID(ename); ok && eid == id {
				return false
			}
		}
	}
	now := s.clock.count
	newInst := mobEffectInst{amplifier: amplifier, expiry: effectExpiry(now, ticks), born: now, hide: flags&2 == 0, flags: flags, flagsSet: true}
	if e.Effects == nil {
		e.Effects = map[int32]mobEffectInst{}
	}
	if prev, ok := e.Effects[id]; ok && (prev.expiry < 0 || prev.expiry > now) {
		switch {
		case amplifier > prev.amplifier:
			if ticks >= 0 && prev.expiry >= 0 && int64(ticks) <= prev.expiry-now {
				prevCopy := prev
				newInst.hidden = &prevCopy
			}
		case amplifier == prev.amplifier && shorterDur(prev, newInst, now):
			newInst.hidden = prev.hidden
		case amplifier < prev.amplifier && shorterDur(prev, newInst, now):
			prev.mergeHidden(newInst, now)
			changed := potionMergeFlags(&prev, flags, false, amplifier, ticks, now)
			e.Effects[id] = prev
			e.MetaDirty = e.MetaDirty || changed
			s.mobInstantEffect(e, id, amplifier, src...)
			return changed
		default:
			changed := potionMergeFlags(&prev, flags, false, amplifier, ticks, now)
			e.Effects[id] = prev
			e.MetaDirty = e.MetaDirty || changed
			s.mobInstantEffect(e, id, amplifier, src...)
			return changed
		}
	}
	e.Effects[id] = newInst
	s.mobInstantEffect(e, id, amplifier, src...)
	// LivingEntity.onEffectAdded sends update_mob_effect to passengers
	// only; trackers see the effect through entity data (particles) and
	// the attribute-modifier resend on the next sendChanges.
	e.MetaDirty = true
	e.AttrDirty = true
	return true
}

// mobInstantEffect is HealOrHarmMobEffect.applyInstantenousEffect for
// world entities: heal 4<<amplifier / magic-hurt 6<<amplifier, with the
// inverted_healing_and_harm tag flipping the arm for undead mobs
// (isHarm == isInverted picks heal). Java's ishl masks the shift to 5
// bits; Go's << needs it explicit — amp 200 lands on <<8 like vanilla.
func (s *Server) mobInstantEffect(e *Entity, id, amplifier int32, src ...*damageInfo) {
	s.mobInstantEffectScaled(e, id, amplifier, 1, src...)
}

func (s *Server) mobInstantEffectScaled(e *Entity, id, amplifier int32, scale float64, src ...*damageInfo) {
	if id != effectInstantHealth && id != effectInstantDamage {
		return
	}
	info := &damageInfo{typeName: "magic"}
	if len(src) > 0 && src[0] != nil {
		info = src[0]
	}
	d := &dataEntity{mob: e}
	inverted := d.entityInTag("inverted_healing_and_harm")
	if (id == effectInstantDamage) == inverted {
		amount := int32(math.Floor(float64(int32(4)<<(amplifier&31))*scale + 0.5))
		if amount > 0 {
			s.entities.Heal(e.ID, float32(amount))
		}
		return
	}
	if amount := int32(math.Floor(float64(int32(6)<<(amplifier&31))*scale + 0.5)); amount > 0 {
		s.damageMob(e, float32(amount), info.typeName, nil, info)
	}
}

// mobLiveEffectCount / mobHasLiveEffect mirror the session side's
// "expired-but-unflushed counts as gone" rule for world entities.
func mobLiveEffectCount(e *Entity, now int64) int {
	n := 0
	for _, inst := range e.Effects {
		if inst.expiry < 0 || inst.expiry > now {
			n++
		}
	}
	return n
}

func mobHasLiveEffect(e *Entity, id int32, now int64) bool {
	inst, ok := e.Effects[id]
	return ok && (inst.expiry < 0 || inst.expiry > now)
}

// mobClearAllEffects is LivingEntity.removeAllEffects for world
// entities; the removal surfaces on the next sendChanges metadata and
// attribute resend, not on remove_mob_effect (vanilla sends that packet
// to passengers only).
func (s *Server) mobClearAllEffects(e *Entity) {
	e.Effects = nil
	e.MetaDirty = true
	e.AttrDirty = true
}

// mobClearEffect removes one live instance the same way.
func (s *Server) mobClearEffect(e *Entity, id int32) {
	delete(e.Effects, id)
	e.MetaDirty = true
	e.AttrDirty = true
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
		s.finishRemovedMobEffect(sess, id)
	}
}

// removeMobEffect drops one effect and emits Remove Mob Effect.
func (s *Server) removeMobEffect(sess *Session, id int32) {
	sess.effMu.Lock()
	_, present := sess.effects[id]
	delete(sess.effects, id)
	sess.effMu.Unlock()
	if present {
		s.finishRemovedMobEffect(sess, id)
	}
}

// The caller has already removed a known active instance (expiry/clear/raid),
// or removeMobEffect captured that transition. Absent-effect requests do not
// remove unrelated command modifiers or emit effect-removal callbacks.
func (s *Server) finishRemovedMobEffect(sess *Session, id int32) {
	if id == effectAbsorption {
		sess.absorb = 0
	}
	if id == effectLevitation {
		sess.levitationStart = nil
	}
	s.removePlayerEffectAttributes(sess, id)
	var b protocol.Buffer
	b.VarInt(sess.entityID.Load())
	b.VarInt(id)
	body := b.Bytes()
	_ = sess.writeMobEffect776(body, true)
	s.broadcastMobEffect776(sess.dimName(), sess, body, true)
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
		flags &^= 0x06
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
	remaining := e.expiry - sess.effectNow()
	if e.expiry < 0 {
		remaining = -1
	}
	b.VarInt(int32(remaining))
	flags := e.flags
	if flags == 0 && !e.flagsSet {
		flags = mobEffectFlags(e.hide)
	}
	b.Byte(flags)
	body := b.Bytes()
	_ = sess.writeMobEffect776(body, false)
	s.broadcastMobEffect776(sess.dimName(), sess, body, false)
}

// mobEffectTickStep expires active effects on the game clock and emits
// Remove Mob Effect, matching vanilla's LivingEntity.tickEffects. When
// the expiring instance carries a hiddenEffect the slot downgrades to
// it instead of clearing (MobEffectInstance.downgradeToHiddenEffect),
// which surfaces as an Update Mob Effect with the lower amplifier.
func (s *Server) playerEffectTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if !sess.gameplayTickReady() {
			return true
		}
		// Per-tick applyEffectTick hooks run before the expiry sweep —
		// MobEffectInstance.tick applies the effect while it still has
		// duration, so a conversion at remaining>0 preempts removal.
		sess.effMu.Lock()
		badOmen, hasBadOmen := sess.effects[effectBadOmen]
		raidOmen, hasRaidOmen := sess.effects[effectRaidOmen]
		raidOmenDue := hasRaidOmen && raidOmen.expiry >= 0 && raidOmen.expiry-sess.effectNow() == 1
		sess.effMu.Unlock()
		if hasBadOmen {
			s.badOmenTick(sess, badOmen)
		}
		// Periodic applyEffectTick hooks on the session arm —
		// MobEffect.shouldApplyEffectTickThisTick rates: poison fires
		// every 25>>amplifier remaining ticks (magic 1.0 while
		// health>1), wither every 40>>amplifier, regeneration every
		// 50>>amplifier (heal 1.0 below max), hunger every tick
		// (0.005*(amplifier+1) exhaustion, player-only).
		sess.effMu.Lock()
		active := make([]mobEffectInst, 0, len(sess.effects))
		ids := make([]int32, 0, len(sess.effects))
		for id, inst := range sess.effects {
			if inst.expiry >= 0 && inst.expiry-sess.effectNow() > 0 {
				ids = append(ids, id)
				active = append(active, inst)
			}
		}
		sess.effMu.Unlock()
		for i, id := range ids {
			s.applyEffectTickSession(sess, id, active[i])
		}
		if raidOmenDue && sess.raidOmenPos != nil {
			// RaidOmenMobEffect fires exactly once, on the last live
			// tick of the 600-tick instance.
			pos := *sess.raidOmenPos
			sess.raidOmenPos = nil
			s.createOrExtendRaid(sess, sess.dimension(), pos)
		}
		sess.effMu.Lock()
		var expired, downgraded []int32
		for id, e := range sess.effects {
			if e.expiry >= 0 && sess.effectNow() >= e.expiry {
				if e.hidden != nil {
					resumed := *e.hidden
					resumed.flags = potionStoredFlags(resumed) &^ 8
					resumed.flagsSet = true
					sess.effects[id] = resumed
					downgraded = append(downgraded, id)
					continue
				}
				expired = append(expired, id)
				delete(sess.effects, id)
			}
		}
		sess.effMu.Unlock()
		for _, id := range expired {
			s.finishRemovedMobEffect(sess, id)
		}
		for _, id := range downgraded {
			e, live := sessEffect(sess, id, sess.effectNow())
			if live {
				s.setPlayerEffectAttributes(sess, id, e)
				s.sendMobEffectUpdate(sess, id, e)
				s.fireEffectsChanged(sess)
			}
		}
		return true
	})
}

func (s *Server) mobEffectTickStep() {
	if !s.clock.isFrozen() {
		s.playerEffectTickStep()
	}
	// The world-entity arm of LivingEntity.tickEffects: the same
	// expiry/downgrade pass on e.Effects, remove_mob_effect /
	// update_mob_effect out to trackers instead of the session write.
	// applyEffectTick hooks run first — the same periodic rates as
	// the session arm (poison 25, wither 40, regen 50 >>amplifier).
	cloudType, cloudOK := EntityTypeID("area_effect_cloud")
	for _, e := range s.entities.All() {
		if e.Dying || len(e.Effects) == 0 {
			continue
		}
		// AreaEffectCloud isn't a LivingEntity — its effects map is the
		// dose payload, not active effects, so it never expires or
		// apply-ticks here.
		if cloudOK && e.Type == cloudType {
			continue
		}
		ep, ok := s.entities.GetPtr(e.ID)
		if !ok {
			continue
		}
		for id, inst := range ep.Effects {
			if inst.expiry < 0 || inst.expiry-s.clock.count <= 0 {
				continue
			}
			s.applyEffectTickEntity(ep, id, inst)
		}
		changed := false
		for id, inst := range ep.Effects {
			if inst.expiry >= 0 && s.clock.count >= inst.expiry {
				if inst.hidden != nil {
					ep.Effects[id] = *inst.hidden
				} else {
					delete(ep.Effects, id)
				}
				changed = true
			}
		}
		if changed {
			ep.MetaDirty = true
			ep.AttrDirty = true
		}
	}
}

// applyEffectTickSession fires one periodic hook for a live finite
// effect instance on a player session — the session arm of
// MobEffectInstance.tick's applyEffectTick dispatch.
func (s *Server) applyEffectTickSession(sess *Session, id int32, inst mobEffectInst) {
	rem := inst.expiry - sess.effectNow()
	if rem <= 0 || sess.dead || sess.effectNow() <= inst.born {
		return
	}
	switch id {
	case 19: // wither: hurt 1.0 wither() every 40>>amplifier
		if effectPeriodicTickDue(40, inst.amplifier, rem+1) {
			s.damageWith(sess, 1.0, translatable("death.attack.wither", literal(sess.getName())),
				damageSource{typeName: "wither"})
		}
	case 18: // poison: hurt 1.0 magic() every 25>>amplifier, never below 1 hp
		if effectPeriodicTickDue(25, inst.amplifier, rem+1) && sess.health > 1 {
			s.damageWith(sess, 1.0, translatable("death.attack.magic", literal(sess.getName())),
				damageSource{typeName: "magic"})
		}
	case 9: // regeneration: heal 1.0 every 50>>amplifier below max
		if effectPeriodicTickDue(50, inst.amplifier, rem+1) && sess.health < sess.server.playerMaxHealth(&sess.playerRecord) {
			sess.healPlayer(1)
		}
	case 16: // hunger: causeFoodExhaustion every tick, players only
		if m := sess.mode(); m == GameModeSurvival || m == GameModeAdventure {
			sess.addExhaustion(0.005 * float32(inst.amplifier+1))
		}
	case 22: // saturation: FoodData.eat(amplifier+1, 1.0) every tick
		nutr := inst.amplifier + 1
		sess.food += nutr
		if sess.food > defaultFood {
			sess.food = defaultFood
		}
		sess.saturation += float32(nutr) * 2.0
		if sess.saturation > float32(sess.food) {
			sess.saturation = float32(sess.food)
		}
		sess.healthDirty.Store(true)
	}
}

// applyEffectTickEntity fires the same periodic hooks on a world
// entity — hunger has no mob arm (players only).
func (s *Server) applyEffectTickEntity(ep *Entity, id int32, inst mobEffectInst) {
	rem := inst.expiry - s.clock.count
	if rem <= 0 || s.clock.count <= inst.born {
		return
	}
	switch id {
	case 19:
		if effectPeriodicTickDue(40, inst.amplifier, rem+1) {
			s.damageMob(ep, 1.0, "wither", nil, &damageInfo{typeName: "wither"})
		}
	case 18:
		if effectPeriodicTickDue(25, inst.amplifier, rem+1) && ep.Health > 1 {
			s.damageMob(ep, 1.0, "magic", nil, &damageInfo{typeName: "magic"})
		}
	case 9:
		if effectPeriodicTickDue(50, inst.amplifier, rem+1) {
			s.entities.Heal(ep.ID, 1.0)
		}
	}
}

// badOmenTick is BadOmenMobEffect.applyEffectTick — runs every tick the
// effect is live: a non-spectator on non-peaceful difficulty inside a
// village converts Bad Omen into a 600-tick Raid Omen at the same
// amplifier and records the position the raid will anchor to. The one
// skip is a raid at that position already at max omen level. The
// return-false in vanilla removes Bad Omen the same tick.
func (s *Server) badOmenTick(sess *Session, inst mobEffectInst) {
	if sess.gameMode == 3 || int32(s.difficulty) == 0 {
		return
	}
	x, y, z, _, _, _ := sess.Pos()
	d := sess.dimension()
	pos := world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}
	if !d.raidIsVillage(pos) {
		return
	}
	if ex := d.raidAt(pos.X, pos.Y, pos.Z); ex != nil && ex.omen >= raidOmenMax {
		return
	}
	s.applyMobEffectWire(sess, effectRaidOmen, inst.amplifier, 600, false, 0)
	// Vanilla emits a second Update Mob Effect for the fresh Raid
	// Omen in the same tick — an onEffectUpdated frame identical to
	// the add except the blend bit is clear (0x06 vs 0x0E).
	s.sendMobEffectUpdateFlags(sess, effectRaidOmen, inst.amplifier, 600, 0x06)
	sess.raidOmenPos = &pos
	s.removeMobEffect(sess, effectBadOmen)
}

// sendMobEffectUpdateFlags emits one Update Mob Effect with the flags
// byte spelled out — ServerPlayer.onEffectUpdated always sends the
// no-blend form, which is what the extra raid-omen frame is.
func (s *Server) sendMobEffectUpdateFlags(sess *Session, id, amplifier, duration int32, flags byte) {
	var b protocol.Buffer
	b.VarInt(sess.entityID.Load())
	b.VarInt(id)
	b.VarInt(amplifier)
	b.VarInt(duration)
	b.Byte(flags)
	body := b.Bytes()
	_ = sess.writeMobEffect776(body, false)
	s.broadcastMobEffect776(sess.dimName(), sess, body, false)
}

// Each observer receives its own profile's effect identifier and packet ID.
func (s *Server) broadcastMobEffect776(dim string, except *Session, body []byte, remove bool) {
	cache := javaEntityBodyCache{}
	s.sessions.Range(func(_, value any) bool {
		other := value.(*Session)
		if other == except || other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		_ = cache.writeMobEffect(other, body, remove)
		return true
	})
}

// mobEffectsFromNBT restores scalar instances plus recursively encoded Details.
// A hidden Details record inherits its parent's effect ID; it has no id field.
func mobEffectsFromNBT(value any, now int64) map[int32]mobEffectInst {
	list, ok := value.(nbt.List)
	if !ok {
		return nil
	}
	out := map[int32]mobEffectInst{}
	for _, entry := range list.Items {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, ok := m["id"].(string)
		if !ok {
			continue
		}
		id, ok := MobEffectID(strings.TrimPrefix(name, "minecraft:"))
		if !ok {
			continue
		}
		if inst, ok := mobEffectDetailsFromNBT(m, now, 0); ok {
			out[id] = inst
		}
	}
	return out
}
func mobEffectDetailsFromNBT(m map[string]any, now int64, depth int) (mobEffectInst, bool) {
	if depth > 16 {
		return mobEffectInst{}, false
	}
	duration := nbtNumberOr(m["duration"], 0)
	expiry := now + int64(max(duration, 0))
	if duration == -1 {
		expiry = -1
	}
	flags := byte(8)
	if potionBool(m, "ambient", false) {
		flags |= 1
	}
	visible := potionBool(m, "show_particles", true)
	if visible {
		flags |= 2
	}
	if potionBool(m, "show_icon", visible) {
		flags |= 4
	}
	inst := mobEffectInst{amplifier: potionAmplifier(m["amplifier"]), expiry: expiry, born: now, hide: !visible, flags: flags, flagsSet: true}
	if raw, ok := m["hidden_effect"]; ok {
		child, ok := raw.(map[string]any)
		if !ok {
			return mobEffectInst{}, false
		}
		hidden, ok := mobEffectDetailsFromNBT(child, now, depth+1)
		if !ok {
			return mobEffectInst{}, false
		}
		inst.hidden = &hidden
	}
	return inst, true
}
