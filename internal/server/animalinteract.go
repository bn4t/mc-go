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
// non-baby mooshroom swaps for mushroom_stew (suspicious_stew once the
// mooshroom carries stewEffects from a flower eat — untracked), then
// the mooshroom's own playSound — positional at the mob, NEUTRAL.
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
	stew, ok := world.ItemID("mushroom_stew")
	if !ok {
		return true
	}
	s.createFilledResult(sess, hslot, ItemStack{ID: stew, Count: 1}, false)
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, 987, 6, 1, 1, nil) // entity.mooshroom.milk
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
