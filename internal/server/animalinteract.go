package server

import (
	"mc-go/internal/world"
)

// Animal item interacts (26.2 jar-measured):
//
//   AbstractCow.mobInteract / Goat.mobInteract — BUCKET on a non-baby
//     runs player.playSound(<milk sound>, 1, 1): a POSITIONAL packet
//     at the player, PLAYERS source, self-excluded — then
//     ItemUtils.createFilledResult swaps bucket -> milk_bucket.
//
//   MushroomCow.mobInteract — BOWL on a non-baby, checked before the
//     shears arm: createFilledResult(bowl -> stew, preventDuplicates=
//     false) then the mooshroom's own playSound(MOOSHROOM_MILK /
//     _SUSPICIOUSLY, 1, 1) — POSITIONAL at the mooshroom, NEUTRAL,
//     heard by everyone. The suspicious arm needs the brown-variant
//     stewEffects the flower eat sets — untracked for now.
//
//   DyeItem.interactLivingEntity — dye on a live un-sheared sheep
//   whose color differs runs level.playSound(player /*except*/,
//   sheep /*at*/, DYE_USE, PLAYERS, 1, 1) — entity-anchored on the
//   sheep, self-excluded for the dyer — then setColor + shrink(1).

// dyeColorName maps a DyeColor id (sheep wool byte low nibble) to the
// item/block name stem — "red_wool", "red_dye" share it.
var dyeColorName = [16]string{
	"white", "orange", "magenta", "light_blue",
	"yellow", "lime", "pink", "gray",
	"light_gray", "cyan", "purple", "blue",
	"brown", "green", "red", "black",
}

// isDyeItem reports whether the item name is one of the 16 <color>_dye
// items — DyeItem's interactLivingEntity arm and the collar-dye gates
// share the check.
func isDyeItem(name string) bool {
	for _, n := range dyeColorName {
		if name == n+"_dye" {
			return true
		}
	}
	return false
}

// milkInteract is AbstractCow.mobInteract's (and Goat.mobInteract's)
// bucket arm: bucket on a non-baby cow/mooshroom/goat.
func (s *Server) milkInteract(sess *Session, eid int32, hand int32) bool {
	e, ok := s.entities.Get(eid)
	if !ok || !e.Living || e.Dying || e.Baby {
		return false
	}
	var snd int32
	switch name, _ := EntityTypeName(e.Type); name {
	case "cow", "mooshroom":
		snd = 449 // entity.cow.milk
	case "goat":
		// Goat.getMilkingSound selects by isScreamingGoat — the
		// screaming field is untracked, so only the plain arm emits.
		snd = 740 // entity.goat.milk
	default:
		return false
	}
	held, hslot := sess.stackForHand(hand)
	if n, ok := itemNameOf(held.ID); !ok || n != "bucket" {
		return false
	}
	x, y, z, _, _, _ := sess.Pos()
	s.playSoundSrc(e.Dim, x, y, z, snd, 7, 1, 1, sess)
	milk, ok := world.ItemID("milk_bucket")
	if !ok {
		return true
	}
	// AbstractCow/Goat pass the 3-arg createFilledResult — creative
	// keeps the bucket, survival consumes and swaps.
	s.createFilledResult(sess, hslot, ItemStack{ID: milk, Count: 1}, true)
	return true
}

// stewInteract is MushroomCow.mobInteract's bowl arm: a bowl on a
// non-baby mooshroom swaps for mushroom_stew — suspicious_stew with
// the pending stew_effects copied onto the stack (and cleared) once
// a flower eat primed them — then the mooshroom's own playSound
// (MOOSHROOM_MILK or _SUSPICIOUSLY), positional NEUTRAL to everyone.
func (s *Server) stewInteract(sess *Session, eid int32, hand int32) bool {
	e, ok := s.entities.Get(eid)
	if !ok || !e.Living || e.Dying || e.Baby {
		return false
	}
	if name, _ := EntityTypeName(e.Type); name != "mooshroom" {
		return false
	}
	held, hslot := sess.stackForHand(hand)
	if n, ok := itemNameOf(held.ID); !ok || n != "bowl" {
		return false
	}
	snd := int32(987) // entity.mooshroom.milk
	result := ItemStack{ID: 0, Count: 1}
	if e.StewEffects != "" {
		snd = 988 // entity.mooshroom.suspicious_milk
		if st, ok2 := world.ItemID("suspicious_stew"); ok2 {
			result.ID = st
			result.StewEffects = e.StewEffects
			if p, ok3 := s.entities.GetPtr(eid); ok3 {
				p.StewEffects = ""
			}
		}
	}
	if result.ID == 0 {
		stew, ok := world.ItemID("mushroom_stew")
		if !ok {
			return true
		}
		result.ID = stew
	}
	s.createFilledResult(sess, hslot, result, false)
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, snd, 6, 1, 1, nil)
	return true
}

// flowerStewEffects is SuspiciousEffectHolder's per-flower
// suspicious_stew_effects component: each small flower maps to one
// (mob_effect, duration ticks) entry — the item's default component
// data, so no stack-level tag is needed.
var flowerStewEffects = map[string]string{
	"allium":             "fire_resistance:60",
	"azure_bluet":        "blindness:220",
	"blue_orchid":        "saturation:7",
	"closed_eyeblossom":  "nausea:140",
	"cornflower":         "jump_boost:100",
	"dandelion":          "saturation:7",
	"golden_dandelion":   "saturation:7",
	"lily_of_the_valley": "poison:220",
	"open_eyeblossom":    "blindness:220",
	"orange_tulip":       "weakness:140",
	"oxeye_daisy":        "regeneration:140",
	"pink_tulip":         "weakness:140",
	"poppy":              "night_vision:100",
	"red_tulip":          "weakness:140",
	"torchflower":        "night_vision:100",
	"white_tulip":        "weakness:140",
	"wither_rose":        "wither:140",
}

// mooshroomFeedInteract is MushroomCow.mobInteract's flower arm,
// reached only by a BROWN non-baby mooshroom holding-effect flowers:
// already-fed → 2 SMOKE puffs and the item stays; else consume 1,
// spawn 4 entity_effect puffs, record stewEffects and play
// MOOSHROOM_EAT (positional NEUTRAL, vol 2).
func (s *Server) mooshroomFeedInteract(sess *Session, eid int32, hand int32) bool {
	e, ok := s.entities.Get(eid)
	if !ok || !e.Living || e.Dying || e.Baby || e.MooshroomVariant != 1 {
		return false
	}
	if name, _ := EntityTypeName(e.Type); name != "mooshroom" {
		return false
	}
	held, hslot := sess.stackForHand(hand)
	iname, ok := itemNameOf(held.ID)
	if !ok {
		return false
	}
	eff, ok := flowerStewEffects[iname]
	if !ok {
		return false
	}
	if e.StewEffects != "" {
		for i := 0; i < 2; i++ {
			s.broadcastParticles(e.Dim, 69, // smoke
				e.X+float64(s.rng.intn(1024))/1024/2, e.Y+0.5,
				e.Z+float64(s.rng.intn(1024))/1024/2,
				0, float64(s.rng.intn(1024))/1024/5, 0, 0, 1)
		}
		return true
	}
	sess.consumeSlot(hslot, 1)
	for i := 0; i < 4; i++ {
		s.broadcastSpellParticles(e.Dim, -1, 1,
			e.X+float64(s.rng.intn(1024))/1024/2, e.Y+0.5,
			e.Z+float64(s.rng.intn(1024))/1024/2,
			0, float64(s.rng.intn(1024))/1024/5, 0, 0, 1)
	}
	if p, ok := s.entities.GetPtr(eid); ok {
		p.StewEffects = eff
	}
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, 986, 6, 2, 1, nil) // entity.mooshroom.eat
	return true
}

// dyeSheepInteract is DyeItem.interactLivingEntity's sheep arm: runs
// in the item phase, gated on a <color>_dye in hand on a live
// un-sheared sheep of a different color.
func (s *Server) dyeSheepInteract(sess *Session, eid int32, hand int32) bool {
	e, ok := s.entities.Get(eid)
	if !ok || !e.Living || e.Dying || e.Sheared {
		return false
	}
	if name, _ := EntityTypeName(e.Type); name != "sheep" {
		return false
	}
	held, hslot := sess.stackForHand(hand)
	iname, ok := itemNameOf(held.ID)
	if !ok {
		return false
	}
	var dye int8 = -1
	for i, n := range dyeColorName {
		if iname == n+"_dye" {
			dye = int8(i)
			break
		}
	}
	if dye < 0 || dye == e.Color {
		return false
	}
	// level.playSound(player /*except*/, sheep /*at*/, DYE_USE,
	// PLAYERS, 1, 1) — anchored on the sheep, silent for the dyer.
	s.playSoundEntity(e.Dim, eid, 571, 7, 1, 1, sess)
	if p, ok := s.entities.GetPtr(eid); ok {
		p.Color = dye
		p.MetaDirty = true
	}
	sess.consumeSlot(hslot, 1) // shrink(1) — unconditional
	return true
}

// createFilledResult is ItemUtils.createFilledResult: under
// preventDuplicates a creative player keeps the held stack and the
// result lands in the inventory only when absent; otherwise
// consume(1) — the result replaces the slot when the stack empties,
// else merges into the inventory (the drop-on-full tail unmodelled).
func (s *Server) createFilledResult(sess *Session, hslot int16, result ItemStack, preventDuplicates bool) {
	if preventDuplicates && sess.mode() == GameModeCreative {
		if !sess.inventoryContains(result.ID) {
			sess.addStack(result)
		}
		return
	}
	sess.consumeSlot(hslot, 1)
	if sess.stackAt(hslot).Empty() {
		sess.setStack(hslot, result)
		_ = s.sendContainerSlot(sess, hslot, result)
		return
	}
	sess.addStack(result)
}

// inventoryContains is Inventory.contains: any slot holding the id.
func (sess *Session) inventoryContains(id int32) bool {
	sess.invMu.RLock()
	defer sess.invMu.RUnlock()
	for i := int16(0); i < slotCount; i++ {
		if it := sess.inv[i]; !it.Empty() && it.ID == id {
			return true
		}
	}
	return false
}
