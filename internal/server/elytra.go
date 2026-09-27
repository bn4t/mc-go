package server

import (
	"math"

	"mc-go/internal/world"
)

// canGlide is LivingEntity.canGlide: airborne, not a passenger, no
// levitation effect, and some equipment slot holds a glider — the
// item carries the glider component, its equippable slot is the one
// it sits in, and nextDamageWillBreak (damage >= max-1) is still
// false. The elytra is the only vanilla glider and it equips to the
// chest slot, so the scan reduces to that one check.
func (s *Server) canGlide(sess *Session) bool {
	_, _, _, _, _, onGround := sess.Pos()
	if onGround || sess.riding.Load() != 0 {
		return false
	}
	if _, ok := sess.effects[effectLevitation]; ok {
		return false
	}
	sess.invMu.RLock()
	chest := sess.inv[slotArmorStart+1]
	sess.invMu.RUnlock()
	name, ok := world.ItemName(chest.ID)
	if !ok || chest.Empty() || name != "elytra" {
		return false
	}
	return chest.Damage < damageableItems["elytra"]-1
}

// playerInWater is LivingEntity.isInWater for the start-glide check:
// the feet cell holding water.
func (s *Server) playerInWater(sess *Session) bool {
	x, y, z, _, _, _ := sess.Pos()
	feet, _ := world.StateOwner(sess.world().GetBlock(world.Pos{
		X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z)),
	}))
	return feet == "water"
}

// tryStartFallFlying runs the START_FALL_FLYING action:
// tryToStartFallFlying sets shared flag 7 when the player is not
// already gliding, canGlide holds, and they are not in water; any
// other state calls stopFallFlying, which clears the flag (a
// start-fly packet while gliding toggles the glide back off).
func (s *Server) tryStartFallFlying(sess *Session) {
	if !sess.fallFlying && s.canGlide(sess) && !s.playerInWater(sess) {
		sess.fallFlying = true
		s.broadcastSharedFlags(sess)
		return
	}
	if sess.fallFlying {
		sess.fallFlying = false
		s.broadcastSharedFlags(sess)
	}
}

// fallFlyingTick mirrors the two vanilla sites: LivingEntity.tick
// counts fallFlyTicks while the flag is on and zeroes it while off,
// and updateFallFlying — run each tick the flag is set — clears it
// when canGlide lapses, wears a random glide-capable slot whenever
// (fallFlyTicks+1) lands on a multiple of 20, and fires the
// ELYTRA_GLIDE game event every (fallFlyTicks+1)%10. Only the chest
// elytra ever qualifies as a slot.
func (s *Server) fallFlyingTick(sess *Session) {
	if !sess.fallFlying {
		sess.fallFlyTicks = 0
		return
	}
	if !s.canGlide(sess) {
		sess.fallFlying = false
		s.broadcastSharedFlags(sess)
		return
	}
	sess.fallFlyTicks++
	if (sess.fallFlyTicks+1)%20 == 0 {
		s.wearSlot(sess, slotArmorStart+1, 1)
	}
}
