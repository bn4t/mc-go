package server

import (
	"math"
	"math/rand"

	"mc-go/internal/world"
)

// EnderMan's stare pipeline: EndermanLookForPlayerGoal finds a
// staring (or already-angry) player inside follow_range, holds a
// 5-tick pending window with STARED_AT raised, then latches the
// target — setTarget raises CREEPY — and EndermanFreezeWhenLookedAt
// pins the mob while the stare persists: a stared-at enderman
// freezes and teleports off at dist²<16, an un-stared one chases
// (the shared melee step) or teleportTowards past dist²>256.

// enderStares is EnderMan.isBeingStaredBy: the head slot must not
// carry a gaze-disguise item (ItemTags.GAZE_DISGUISE_EQUIPMENT —
// carved_pumpkin in 26.2), the view vector must fall inside the
// 0.025 cone scaled by distance, and COLLIDER line of sight must
// reach the enderman's eye height (dims·0.85).
func (s *Server) enderStares(e *Entity, sess *Session) bool {
	if sess.dead || !sess.spawned.Load() {
		return false
	}
	if name, ok := itemNameOf(sess.inv[slotArmorStart].ID); ok && name == "carved_pumpkin" {
		return false
	}
	px, py, pz, yaw, pitch, _ := sess.Pos()
	lvx, lvy, lvz := lookVector(yaw, pitch)
	dims := entityBBDims(e)
	dx := e.X - px
	dy := e.Y + dims[1]*0.85 - (py + 1.62)
	dz := e.Z - pz
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if dist < 1e-6 {
		return true
	}
	if dot := (lvx*dx + lvy*dy + lvz*dz) / dist; dot <= 1.0-0.025/dist {
		return false
	}
	if d := s.dims[e.Dim]; d != nil {
		_, _, _, _, _, _, _, hit := s.clipSolidSegment(d,
			e.X, e.Y+dims[1]*0.85, e.Z, px, py+1.62, pz)
		return !hit
	}
	return false
}

// enderAngerInduces is the LookForPlayerGoal's isAngerInducing
// selector: a live stare or the persistent-anger record (damage
// provocation) both count.
func (s *Server) enderAngerInduces(e *Entity, sess *Session) bool {
	if s.enderStares(e, sess) {
		return true
	}
	return e.AngryAt == sess.uuid &&
		(e.AngryUntil == 0 || s.gameTime() < e.AngryUntil)
}

// enderHeadTrack is the enderman lookAt() path: only the HEAD yaw
// steps toward the target (10°/tick), leaving body yaw to
// navigation — vanilla's goal chain never turns the body here.
func enderHeadTrack(e *Entity, dx, dz, maxDeg float64) {
	target := float32(math.Atan2(-dx, dz) * 180 / math.Pi)
	d := target - e.HeadYaw
	for d > 180 {
		d -= 360
	}
	for d < -180 {
		d += 360
	}
	if d > float32(maxDeg) {
		d = float32(maxDeg)
	} else if d < float32(-maxDeg) {
		d = float32(-maxDeg)
	}
	e.HeadYaw += d
}

// enderStareStep runs one tick of the stare goals. The pick/pending
// work is a TARGET-selector goal — it head-tracks but never gates
// movement (returning false so the stroll keeps walking); only the
// latched FreezeWhenLookedAt (JUMP|MOVE) suppresses it. A latched,
// un-stared enderman reports false so the melee step chases.
func (s *Server) enderStareStep(e *Entity) bool {
	// Latched target: drops out when the combat conditions break —
	// setTarget(null) clears CREEPY and STARED_AT together.
	if e.EnderStareTarget != [16]byte{} {
		t := s.sessionByUUID(e.EnderStareTarget)
		if t == nil || t.dead || !t.spawned.Load() ||
			t.mode() == GameModeCreative || t.mode() == GameModeSpectator ||
			t.dimName() != e.Dim {
			e.EnderStareTarget = [16]byte{}
			e.EnderTeleportTicks = 0
			if e.EnderCreepy || e.EnderStared {
				e.EnderCreepy, e.EnderStared = false, false
				e.MetaDirty = true
			}
			return false
		}
		px, py, pz, _, _, _ := t.Pos()
		dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
		d2 := dx*dx + dy*dy + dz*dz
		enderHeadTrack(e, dx, dz, 10)
		if s.enderStares(e, t) {
			// FreezeWhenLookedAt: stop moving; inside dist² 16 the
			// tick()'s random teleport fires too.
			if d2 < 16 {
				s.enderTeleportRandom(e)
			}
			return true
		}
		if d2 > 256 {
			if e.EnderTeleportTicks++; e.EnderTeleportTicks >= 30 {
				if s.enderTeleportTowards(e, t) {
					e.EnderTeleportTicks = 0
				}
			}
		}
		return false
	}

	// Pending window: canContinueToUse retests isBeingStaredBy each
	// tick — looking away before the latch aborts the aggro and
	// clears STARED_AT; head tracks the pending target while the
	// countdown runs (movement is not gated).
	if e.EnderPending != [16]byte{} {
		t := s.sessionByUUID(e.EnderPending)
		if t == nil || !s.enderStares(e, t) {
			e.EnderPending = [16]byte{}
			if e.EnderStared {
				e.EnderStared = false
				e.MetaDirty = true
			}
		} else {
			px, _, pz, _, _, _ := t.Pos()
			enderHeadTrack(e, px-e.X, pz-e.Z, 10)
			if e.EnderAggroTicks--; e.EnderAggroTicks <= 0 {
				e.EnderStareTarget = e.EnderPending
				e.EnderPending = [16]byte{}
				e.EnderTeleportTicks = 0
				if !e.EnderCreepy {
					e.EnderCreepy = true
					e.MetaDirty = true
				}
			}
		}
		return false
	}

	// Goal pick, NearestAttackableTargetGoal's interval cadence:
	// nearest combat-eligible player inside follow_range whom the
	// anger selector admits.
	if e.EnderStareRecalc > 0 {
		e.EnderStareRecalc--
		return false
	}
	e.EnderStareRecalc = 10
	kn, _ := EntityTypeName(e.Type)
	best := s.mobAttr(e, kn, "follow_range", 64.0)
	best *= best
	var pick *Session
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dead ||
			sess.mode() == GameModeCreative || sess.mode() == GameModeSpectator ||
			sess.dimName() != e.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
		if d2 := dx*dx + dy*dy + dz*dz; d2 < best && s.enderAngerInduces(e, sess) {
			best = d2
			pick = sess
		}
		return true
	})
	if pick == nil {
		return false
	}
	e.EnderPending = pick.uuid
	e.EnderAggroTicks = 5
	if !e.EnderStared {
		e.EnderStared = true
		e.MetaDirty = true
	}
	return true
}

// enderTeleport is EnderMan.teleport(x,y,z): the target column's
// first blocksMotion cell walking down must exist and be dry, then
// randomTeleport lands the mob on the first cell above solid —
// rolling back when the landed box collides or is wet. Success fires
// the teleport game event, the particle burst (entity event 46), and
// ENDERMAN_TELEPORT at both the old and new positions.
func (s *Server) enderTeleport(e *Entity, tx, ty, tz float64) bool {
	w := s.dimWorld(e.Dim)
	if w == nil {
		return false
	}
	px, py, pz := int(math.Floor(tx)), int(math.Floor(ty)), int(math.Floor(tz))
	for py > -64 && !blocksMotionAt(w, world.Pos{X: px, Y: py, Z: pz}) {
		py--
	}
	cell := w.GetBlock(world.Pos{X: px, Y: py, Z: pz})
	if !blocksMotionAt(w, world.Pos{X: px, Y: py, Z: pz}) {
		return false
	}
	if cn, _ := world.StateOwner(cell); cn == "water" {
		return false
	}
	// randomTeleport: descend to the first cell above solid, place
	// the mob there, verify the box is collision- and liquid-free.
	ly := ty
	lx, lz := int(math.Floor(tx)), int(math.Floor(tz))
	lyi := int(math.Floor(ty))
	landed := false
	for lyi > -64 {
		if blocksMotionAt(w, world.Pos{X: lx, Y: lyi - 1, Z: lz}) {
			landed = true
			break
		}
		lyi--
		ly--
	}
	if !landed {
		return false
	}
	dims := entityBBDims(e)
	half, height := dims[0]/2, dims[1]
	if entityInsideSolid(w, tx, ly, tz, half, height) ||
		enderLiquidInBB(w, tx, ly, tz, half, height) {
		return false
	}
	ox, oy, oz := e.X, e.Y, e.Z
	s.tpMountTeleport(e, tx, ly, tz, e.Yaw, e.Pitch, 0)
	s.broadcastEntityEvent(e.ID, 46)
	s.playSoundSrc(e.Dim, ox, oy, oz, sndEndermanTeleport, srcHostile, 1, 1, nil)
	s.playSoundSrc(e.Dim, tx, ly, tz, sndEndermanTeleport, srcHostile, 1, 1, nil)
	return true
}

// enderTeleportRandom is EnderMan.teleport(): a uniform ±32 block
// hop with a ±32 y jitter.
func (s *Server) enderTeleportRandom(e *Entity) bool {
	tx := e.X + (rand.Float64()-0.5)*64.0
	ty := e.Y + float64(rand.Intn(64)-32)
	tz := e.Z + (rand.Float64()-0.5)*64.0
	return s.enderTeleport(e, tx, ty, tz)
}

// enderTeleportTowards is EnderMan.teleportTowards: an 8-block jitter
// biased 16 blocks along the direction toward the target's eye.
func (s *Server) enderTeleportTowards(e *Entity, sess *Session) bool {
	px, py, pz, _, _, _ := sess.Pos()
	dims := entityBBDims(e)
	dx := e.X - px
	dy := e.Y + dims[1]*0.5 - (py + 1.62)
	dz := e.Z - pz
	l := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if l < 1e-6 {
		return false
	}
	tx := e.X + (rand.Float64()-0.5)*8.0 - dx/l*16.0
	ty := e.Y + float64(rand.Intn(16)-8) - dy/l*16.0
	tz := e.Z + (rand.Float64()-0.5)*8.0 - dz/l*16.0
	return s.enderTeleport(e, tx, ty, tz)
}

// sndEndermanTeleport is entity.enderman.teleport — id 599 in the
// 26.2 sound registry.
const sndEndermanTeleport = 599

// enderLiquidInBB is containsAnyLiquid's check on the teleported
// bounding box — any cell holding water or lava fails the landing.
func enderLiquidInBB(w *world.World, x, y, z, half, height float64) bool {
	minX, maxX := int(math.Floor(x-half)), int(math.Floor(x+half))
	minY, maxY := int(math.Floor(y)), int(math.Floor(y+height))
	minZ, maxZ := int(math.Floor(z-half)), int(math.Floor(z+half))
	for cx := minX; cx <= maxX; cx++ {
		for cy := minY; cy <= maxY; cy++ {
			for cz := minZ; cz <= maxZ; cz++ {
				if bn, _ := world.StateOwner(w.GetBlock(world.Pos{X: cx, Y: cy, Z: cz})); bn == "water" || bn == "lava" {
					return true
				}
			}
		}
	}
	return false
}
