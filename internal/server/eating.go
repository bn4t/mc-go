package server

import (
	"strconv"
	"strings"

	"math"
	"math/rand"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// cooldownReady reports whether the item's use_cooldown group is free —
// vanilla's ItemStack.use gates on player.getCooldowns().isOnCooldown
// before the item even dispatches, so a hot cooldown fails silently.
func (sess *Session) cooldownReady(group string, now int64) bool {
	sess.cdMu.Lock()
	defer sess.cdMu.Unlock()
	return sess.cooldowns[group] <= now
}

// addCooldown is ServerPlayerItemCooldowns.addCooldown: the group is
// use_cooldown's cooldownGroup (or the item id), the wire packet is
// ClientboundCooldownPacket {Identifier, VarInt ticks}.
func (s *Server) addCooldown(sess *Session, group string, ticks int32) {
	sess.cdMu.Lock()
	if sess.cooldowns == nil {
		sess.cooldowns = map[string]int64{}
	}
	sess.cooldowns[group] = s.clock.count + int64(ticks)
	sess.cdMu.Unlock()
	var b protocol.Buffer
	b.String("minecraft:" + group)
	b.VarInt(ticks)
	_ = sess.write(sess.ids().PlayCooldown, b.Bytes())
}

// handleUseItem processes Use Item: food begins eating; everything else
// is acknowledged by coverage only for now. Like a use on a block it is
// dropped until the client reports its terrain loaded.
func (s *Server) handleUseItemPacket(sess *Session, p protocol.Packet) {
	if !sess.interactionsOpen(s.TickCount()) {
		return
	}
	r := protocol.NewPacketReader(p.Data)
	hand, err := r.VarInt()
	if err != nil {
		return
	}
	seq, err := r.VarInt()
	if err != nil { // sequence
		return
	}
	yaw, err := r.Float()
	if err != nil { // yaw
		return
	}
	pitch, err := r.Float()
	if err != nil { // pitch
		return
	}
	if sess.dead {
		return
	}
	// ackBlockChangesUpTo runs unconditionally at the top of vanilla's
	// handler, before the item dispatch.
	s.sendBlockAck(sess, seq)
	// A shield raises instead of eating/drawing; the raised flag lives
	// until the release_use_item action. Vanilla reads the hand the
	// packet asked for — an offhand use ignores the main hand stack.
	st := sess.heldStack()
	if hand == 1 {
		st = sess.stackAt(slotOffhand)
	}
	if un, ok := itemNameOf(st.ID); ok && !st.Empty() && un == "shield" {
		sess.blocking.Store(true)
		return
	}
	if un, ok := itemNameOf(st.ID); ok && !st.Empty() && strings.HasSuffix(un, "_on_a_stick") {
		s.useFoodOnAStick(sess, hand)
		return
	}
	// WritableBookItem/WrittenBookItem.use → Player.openItemGui: the
	// packet goes out only when the stack carries written_book_content
	// (vanilla's ServerPlayer.openItemGui gates on the component), so
	// a bare book is a silent no-op, and either book type qualifies.
	if un, ok := itemNameOf(st.ID); ok && !st.Empty() && (un == "written_book" || un == "writable_book") && st.BookWritten != "" {
		var b protocol.Buffer
		b.VarInt(hand)
		sess.write(IDPlayOpenBook, b.Bytes())
		return
	}
	// EggItem/SnowballItem/ExperienceBottleItem/EnderPearlItem.use and
	// WindChargeItem.use: instant throws, not hold-uses. The pearl and
	// charge carry use_cooldown — an in-flight group is a silent FAIL.
	if un, ok := itemNameOf(st.ID); ok && !st.Empty() {
		switch un {
		case "egg", "blue_egg", "brown_egg", "snowball", "experience_bottle", "ender_pearl", "wind_charge":
			if f := foodTable[un]; f.cooldown > 0 && !sess.cooldownReady(un, s.TickCount()) {
				return
			}
			s.throwHandItem(sess, hand, st, un, yaw, pitch)
			if f := foodTable[un]; f.cooldown > 0 {
				s.addCooldown(sess, un, int32(f.cooldown))
			}
			return
		case "firework_rocket":
			// FireworkRocketItem.use acts only while the sender is
			// fall-flying — a grounded use is vanilla's PASS no-op.
			if sess.fallFlying {
				s.useFireworkBoost(sess, st, hand)
			}
			return
		}
	}
	// EmptyMapItem.use: consume one empty map, mint a filled_map
	// keyed to a fresh scale-0 data entry, and play the take_result
	// sound at the player.
	if un, ok := itemNameOf(st.ID); ok && !st.Empty() && un == "map" {
		_, handSlot := sess.stackForHand(hand)
		s.useEmptyMap(sess, st, handSlot)
		return
	}
	held := sess.heldStack()
	if held.Empty() {
		return
	}
	name, ok := itemNameOf(held.ID)
	if !ok {
		return
	}
	if name == "fishing_rod" {
		// FishingRodItem.use is not a hold-use: each use either casts
		// a new bobber or retrieves the live one.
		s.useFishingRod(sess)
		return
	}
	if name == "bow" {
		// Vanilla bows draw on use even in creative (which skips the
		// arrow check until release); the shot lands on release.
		sess.bowStartTick = s.TickCount()
		return
	}
	if strings.HasSuffix(name, "_spear") {
		s.spearStart(sess, held.ID, name)
		return
	}
	if name == "trident" {
		// TridentItem.use starts the same hold; releaseUsing needs
		// >= 10 ticks before the trident actually leaves the hand.
		sess.tridentStartTick = s.TickCount()
		return
	}
	if name == "crossbow" {
		s.useCrossbow(sess)
		return
	}
	if name == "spyglass" {
		// Spyglasses scope while held — vanilla keeps the item "in
		// use" until release or deselect and fires using_item every
		// tick in between.
		sess.usingItemID = held.ID
		return
	}
	f, ok := foodTable[name]
	if !ok {
		return
	}
	// use_cooldown gates before the edible check in ItemStack.use.
	if f.cooldown > 0 && !sess.cooldownReady(name, s.TickCount()) {
		return
	}
	// Consumable.canConsume: the hunger check only runs when the stack
	// carries a FOOD component — consumable-only items (milk, potions,
	// ominous bottle) are always consumable regardless of hunger.
	if !f.foodless && !f.always && sess.food >= defaultFood {
		return
	}
	sess.eatingTicks = f.eatTicks
	sess.eatingItem = held.ID
}

// eatTickStep advances eating; finishing restores hunger and consumes
// the item.
func (s *Server) eatTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.eatingTicks <= 0 {
			return true
		}
		held := sess.heldStack()
		if held.Empty() || held.ID != sess.eatingItem {
			sess.eatingTicks = 0
			return true
		}
		sess.eatingTicks--
		if sess.eatingTicks > 0 {
			return true
		}
		name, ok := itemNameOf(held.ID)
		if !ok {
			return true
		}
		f, ok := foodTable[name]
		if !ok {
			return true
		}
		s.finishConsume(sess, held, name, f)
		return true
	})
}

// finishConsume is ItemStack.finishUsingItem: Consumable.onConsume runs
// stat+trigger, the stack's ConsumableListeners (food hunger, potion
// contents, stew effects, ominous amplifier), then the consumable's
// own effect list; consume(1) shrinks the stack; afterwards
// applyAfterUseComponentSideEffects handles use_remainder and
// use_cooldown.
func (s *Server) finishConsume(sess *Session, held ItemStack, name string, f foodConsume) {
	slot := invSlotForHotbar(sess.held.Load())
	sess.addItemStat(statUsed, held.ID, 1)
	s.fireConsumeItem(sess, name)

	// FoodProperties.onConsume: hunger/saturation plus the PLAYER_BURP
	// broadcast (PLAYERS 0.5, pitch uniform 0.9-1.0).
	sess.food += int32(f.nutrition)
	if sess.food > defaultFood {
		sess.food = defaultFood
	}
	sess.saturation += float32(f.nutrition) * float32(f.satMod) * 2
	if sess.saturation > float32(sess.food) {
		sess.saturation = float32(sess.food)
	}
	sess.healthDirty.Store(true)
	x, y, z, _, _, _ := sess.Pos()
	s.playSoundSrc(sess.dimName(), x, y, z, 1310, 7, 0.5, 0.9+rand.Float32()*0.1, nil) // entity.player.burp

	// PotionContents.onConsume → applyToLivingEntity: the potion's
	// MobEffectInstance list with the default 1.0 duration scale.
	if held.Potion != "" {
		for _, e := range potionEffects[held.Potion] {
			if id, ok := MobEffectID(e.effect); ok {
				s.applyMobEffect(sess, id, int32(e.amplifier), int32(e.duration))
			}
		}
	}
	// SuspiciousStewEffects.onConsume: each entry applies with its own
	// stored duration (the "name:dur" pairs the component codec wrote).
	if held.StewEffects != "" {
		for _, part := range strings.Split(held.StewEffects, ",") {
			kv := strings.SplitN(part, ":", 2)
			if len(kv) != 2 {
				continue
			}
			dur, _ := strconv.Atoi(kv[1])
			if id, ok := MobEffectID(kv[0]); ok {
				s.applyMobEffect(sess, id, 0, int32(dur))
			}
		}
	}
	// OminousBottleAmplifier.onConsume: BAD_OMEN 6000s at the stack's
	// amplifier, ambient=false, visible=false, icon=true — the same
	// 0x04 flags byte the hero-of-the-village grant uses.
	if held.BottleAmpSet && name == "ominous_bottle" {
		if id, ok := MobEffectID("bad_omen"); ok {
			s.applyMobEffectWire(sess, id, held.BottleAmp, 120000, false, 0x04)
		}
	}

	// The consumable's own onConsume effects, in list order.
	if f.clear {
		s.removeAllMobEffects(sess)
	}
	for _, n := range f.remove {
		if id, ok := MobEffectID(n); ok {
			s.removeMobEffect(sess, id)
		}
	}
	for _, e := range f.effects {
		// ApplyStatusEffectsConsumeEffect: ONE shared rand roll decides
		// whether the whole effect list applies.
		if e.prob < 1 && rand.Float64() >= e.prob {
			continue
		}
		if id, ok := MobEffectID(e.effect); ok {
			s.applyMobEffect(sess, id, int32(e.amplifier), int32(e.duration))
		}
	}
	if f.teleport {
		s.chorusTeleport(sess)
	}

	// stack.consume(1, entity): shrinks unless the eater has infinite
	// materials (creative/spectator).
	creative := sess.mode() == GameModeCreative || sess.mode() == GameModeSpectator
	preCount := held.Count
	if !creative {
		sess.consumeHeld(1)
	}

	// applyAfterUseComponentSideEffects: use_remainder converts, then
	// use_cooldown. Infinite-material players get neither.
	if f.converts != "" && !creative {
		if rid, ok := world.ItemID(f.converts); ok {
			rem := ItemStack{ID: rid, Count: 1}
			// convertIntoRemainder: an emptied slot swaps in the
			// remainder; a still-full one adds the remainder to the
			// inventory, spilling to a drop when full.
			sess.invMu.Lock()
			cur := sess.inv[slot]
			sess.invMu.Unlock()
			if cur.Empty() || cur.Count <= 0 {
				sess.invMu.Lock()
				sess.inv[slot] = rem
				sess.invMu.Unlock()
				_ = s.sendContainerSlot(sess, slot, rem)
			} else {
				left, _ := sess.addStack(rem)
				_ = s.sendContainerContent(sess)
				if !left.Empty() {
					s.dropItemLocked(sess, left)
				}
			}
		}
		_ = preCount
	}
	if f.cooldown > 0 {
		s.addCooldown(sess, name, int32(f.cooldown))
	}
}

// chorusTeleport is TeleportRandomlyConsumeEffect.apply +
// LivingEntity.randomTeleport: up to 16 tries at
// (x+(rand-0.5)*16, clamp(y+(rand-0.5)*16, minY, minY+height-1),
// z+(rand-0.5)*16), walking down to the first solid support. A
// landing succeeds when the player's box is free of solids and
// liquids; on success the particles event (46), the chorus sound at
// the NEW position, and a fall-distance reset.
func (s *Server) chorusTeleport(sess *Session) {
	d := sess.dimension()
	if d == nil {
		return
	}
	x, y, z, _, _, _ := sess.Pos()
	for i := 0; i < 16; i++ {
		dx := x + (rand.Float64()-0.5)*16
		dy := math.Max(float64(d.minY), math.Min(float64(d.minY+d.height-1), y+(rand.Float64()-0.5)*16))
		dz := z + (rand.Float64()-0.5)*16
		bx, bz := int(math.Floor(dx)), int(math.Floor(dz))
		by := int(math.Floor(dy))
		for by > d.minY {
			below := d.w.GetBlock(world.Pos{X: bx, Y: by - 1, Z: bz})
			if !world.NonSolid(below) {
				break
			}
			by--
		}
		if by <= d.minY {
			continue
		}
		if !s.playerAABBClear(d, dx, float64(by), dz) {
			continue
		}
		s.teleportWithin(sess, dx, float64(by), dz)
		s.broadcastEntityEvent(sess.entityID.Load(), 46) // teleport particles
		// ChorusFruitItem's sound: PLAYERS 1.0/1.0 at the NEW pos.
		s.playSoundSrc(sess.dimName(), dx, float64(by), dz, 373, 7, 1.0, 1.0, nil)
		sess.fallDistance = 0
		return
	}
}

// playerAABBClear is the randomTeleport landing check: the 0.6×1.8
// player box must overlap no solid cell and contain no liquid.
func (s *Server) playerAABBClear(d *dimension, x, y, z float64) bool {
	const half = 0.3
	for bx := int(math.Floor(x - half)); float64(bx) < x+half; bx++ {
		for by := int(math.Floor(y)); float64(by) < y+1.8; by++ {
			for bz := int(math.Floor(z - half)); float64(bz) < z+half; bz++ {
				b := d.w.GetBlock(world.Pos{X: bx, Y: by, Z: bz})
				if _, _, isFluid := fluidOf(b); isFluid {
					return false
				}
				if !world.NonSolid(b) && !moveNoCollision(b) {
					return false
				}
			}
		}
	}
	return true
}

// removeAllMobEffects is LivingEntity.removeAllEffects: every active
// effect goes with its Remove Mob Effect broadcast.
func (s *Server) removeAllMobEffects(sess *Session) {
	sess.effMu.Lock()
	ids := make([]int32, 0, len(sess.effects))
	for id := range sess.effects {
		ids = append(ids, id)
	}
	sess.effMu.Unlock()
	for _, id := range ids {
		s.removeMobEffect(sess, id)
	}
}
