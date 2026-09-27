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
		sess.glideDelta = [3]float64{}
		return
	}
	if !s.canGlide(sess) {
		sess.fallFlying = false
		sess.glideDelta = [3]float64{}
		s.broadcastSharedFlags(sess)
		return
	}
	sess.fallFlyTicks++
	if (sess.fallFlyTicks+1)%20 == 0 {
		s.wearSlot(sess, slotArmorStart+1, 1)
	}
	// LivingEntity.updateFallFlying posts ELYTRA_GLIDE every 10 ticks.
	if (sess.fallFlyTicks+1)%10 == 0 {
		x, y, z, _, _, _ := sess.Pos()
		s.emitGameEvent(sess.dimension(), "elytra_glide", x, y, z, sess, "")
	}
	s.glideCollisionTick(sess)
}

// glideCollisionTick is the server half of LivingEntity.travelFallFlying
// (26.2 jar): vanilla keeps a real deltaMovement for a gliding player,
// recomputed each tick by updateFallFlyingMovement from the look
// angle — unclamped, so it keeps building against a wall. Entity.move
// sweeps it against the world, advances the entity by the collided
// delta (accepted packets snap that back, so the box drifts between
// claims), and restituteMovementAfterCollisions zeroes the delta's
// components on axes that hit (-bounciness, 0 for a player). Then
// handleFallFlyingCollisions pays (oldH-newH)·10-3 fly_into_wall
// damage when horizontalCollision and the restituted speed fell
// enough. The rendered position stays packet-driven here; only the
// damage rides the sim.
func (s *Server) glideCollisionTick(sess *Session) {
	_, _, _, yaw, pitch, _ := sess.Pos()
	sess.posMu.Lock()
	sx, sy, sz := sess.glideSim[0], sess.glideSim[1], sess.glideSim[2]
	sess.posMu.Unlock()
	d := sess.glideDelta
	oldH := math.Hypot(d[0], d[2])
	yr := float64(yaw) * (math.Pi / 180)
	pr := float64(pitch) * (math.Pi / 180)
	lx, ly, lz := -math.Sin(yr)*math.Cos(pr), -math.Sin(pr), math.Cos(yr)*math.Cos(pr)
	nd := updateFallFlyingDelta(d, lx, ly, lz, pr, s.effectiveGlideGravity(sess))
	cx, cy, cz, hColl := s.sweepGlideBox(sess, sx, sy, sz, nd[0], nd[1], nd[2])
	sess.posMu.Lock()
	sess.glideSim = [3]float64{sx + cx, sy + cy, sz + cz}
	sess.posMu.Unlock()
	xColl, zColl := cx != nd[0], cz != nd[2]
	if xColl {
		nd[0] = 0
	}
	if zColl {
		nd[2] = 0
	}
	if cy != nd[1] {
		nd[1] = 0
	}
	sess.glideDelta = nd
	if !hColl {
		return
	}
	newH := math.Hypot(nd[0], nd[2])
	if f := float32((oldH-newH)*10 - 3); f > 0 {
		snd := int32(soundEventIDs["entity.player.small_fall"])
		if int(f) > 4 {
			snd = int32(soundEventIDs["entity.player.big_fall"])
		}
		s.playSoundSrc(sess.dimName(), sx, sy, sz, snd, srcPlayers, 1, 1, nil)
		s.damageWith(sess, f, translatable("death.attack.flyIntoWall",
			playerChatComponent(sess)), damageSource{typeName: "fly_into_wall"})
	}
}

// effectiveGlideGravity is LivingEntity.getEffectiveGravity: the
// gravity attribute (0.08 for players) unless slow falling is up,
// which caps it at 0.01.
func (s *Server) effectiveGlideGravity(sess *Session) float64 {
	g := 0.08
	if _, ok := sess.effects[effectSlowFalling]; ok {
		g = math.Min(g, 0.01)
	}
	return g
}

// updateFallFlyingDelta mirrors LivingEntity.updateFallFlyingMovement:
// gravity is damped by the pitch, diving pulls horizontal speed out
// of the drop, and the horizontal component eases toward the look
// direction's scale — then everything drags by (0.99, 0.98, 0.99).
func updateFallFlyingDelta(d [3]float64, lx, ly, lz, pitchRad, gravity float64) [3]float64 {
	horizLook := math.Hypot(lx, lz)
	h := math.Hypot(d[0], d[2])
	cosSq := math.Cos(pitchRad) * math.Cos(pitchRad)
	d[1] += gravity * (-1 + 0.75*cosSq)
	if d[1] < 0 && horizLook > 0 {
		a := d[1] * -0.1 * cosSq
		d[0] += lx * a / horizLook
		d[1] += a
		d[2] += lz * a / horizLook
	}
	if pitchRad < 0 && horizLook > 0 {
		a := h * (-math.Sin(pitchRad)) * 0.04
		d[0] += -lx * a / horizLook
		d[1] += a * 3.2
		d[2] += -lz * a / horizLook
	}
	if horizLook > 0 {
		d[0] += (lx/horizLook*h - d[0]) * 0.1
		d[2] += (lz/horizLook*h - d[2]) * 0.1
	}
	d[0] *= 0.99
	d[1] *= 0.98
	d[2] *= 0.99
	return d
}

// sweepGlideBox axis-clamps a gliding player's AABB move the way
// Entity.collideWithShapes does: Y on the raw box, then X on the box
// moved by the resolved Y, then Z on the box moved by resolved X+Y —
// returning the clamped delta and whether either horizontal axis
// collided. The FALL_FLYING pose box is 0.6 wide and 0.6 high.
func (s *Server) sweepGlideBox(sess *Session, x, y, z, dx, dy, dz float64) (cx, cy, cz float64, hColl bool) {
	return s.sweepEntityBox(sess.world(), x, y, z, 0.3, 0.6, dx, dy, dz)
}

// sweepEntityBox resolves a bounding box move in vanilla's
// collideWithShapes order — Y on the raw box, X on the Y-displaced
// box, Z on the (X,Y)-displaced box — returning the clamped
// displacement and whether a horizontal axis bound.
func (s *Server) sweepEntityBox(wd *world.World, x, y, z, half, height, dx, dy, dz float64) (cx, cy, cz float64, hColl bool) {
	cy = s.clampBoxAxis(wd, [2]float64{y, y + height}, dy, 1, x-half, x+half, z-half, z+half)
	cx = s.clampBoxAxis(wd, [2]float64{x - half, x + half}, dx, 0, y+cy, y+height+cy, z-half, z+half)
	cz = s.clampBoxAxis(wd, [2]float64{z - half, z + half}, dz, 2, x-half+cx, x+half+cx, y+cy, y+height+cy)
	return cx, cy, cz, cx != dx || cz != dz
}

// clampBoxAxis returns the maximum displacement the box may move along
// one axis before a solid cell face blocks it — vanilla's
// Shapes.collide on unit cubes. Only boundaries ahead of the box face
// bind: a face already inside a cell's region slides free (existing
// overlap is tolerated) and crossing into a cell stops exactly at its
// entry face. span is the box's extent on the moving axis; o1/o2 are
// its extents on the other two axes (displaced by resolved axes).
// axis: 0=x, 1=y, 2=z.
func (s *Server) clampBoxAxis(wd *world.World, span [2]float64, d float64, axis int, o1min, o1max, o2min, o2max float64) float64 {
	if d == 0 {
		return 0
	}
	face := span[1]
	if d < 0 {
		face = span[0]
	}
	blocked := func(c int) bool {
		for b1 := int(math.Floor(o1min)); float64(b1) < o1max; b1++ {
			for b2 := int(math.Floor(o2min)); float64(b2) < o2max; b2++ {
				var p world.Pos
				switch axis {
				case 0:
					p = world.Pos{X: c, Y: b1, Z: b2}
				case 1:
					p = world.Pos{X: b1, Y: c, Z: b2}
				default:
					p = world.Pos{X: b1, Y: b2, Z: c}
				}
				if b := wd.GetBlock(p); !world.NonSolid(b) && !moveNoCollision(b) {
					return true
				}
			}
		}
		return false
	}
	// Vanilla binds a shape only when its entry face is at or ahead of
	// the box face (minX >= box.maxX), so a face already inside a cell
	// slides free — crossing into a cell stops exactly at the entry.
	if d > 0 {
		for b := int(math.Ceil(face)); float64(b) <= face+d; b++ {
			if blocked(b) {
				return math.Min(d, float64(b)-face)
			}
		}
		return d
	}
	for b := int(math.Floor(face)); float64(b) > face+d; b-- {
		if blocked(b - 1) {
			return math.Max(d, float64(b)-face)
		}
	}
	return d
}
