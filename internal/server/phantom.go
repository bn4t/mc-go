package server

// Phantom AI, measured off the 26.2 jar (javap of Phantom and its
// AttackPhase, PhantomAttackStrategyGoal, PhantomSweepAttackGoal,
// PhantomCircleAroundAnchorGoal, PhantomAttackPlayerTargetGoal,
// PhantomMoveControl, PhantomBodyRotationControl and PhantomLookControl
// inner classes):
//
//   - registerGoals: AttackStrategy(1), SweepAttack(2, MOVE),
//     CircleAroundAnchor(3, MOVE); target AttackPlayerTarget(1). None
//     of the four requires an every-tick update, so all of them tick on
//     the even selector ticks only and their adjustedTickDelay /
//     reducedTickDelay figures are half the authored ticks.
//   - AttackPlayerTarget.canUse: nextScanTick (reducedTickDelay(20))
//     counts down per evaluation; at zero it re-arms
//     reducedTickDelay(60) and scans the players inside the box
//     inflated (16, 64, 16) within 64 blocks, highest getY first, and
//     targets the first that canAttack(DEFAULT) accepts.
//   - AttackStrategy (target attackable): start arms nextSweepTick
//     adjusted(10), CIRCLE, and re-anchors 20+nextInt(20) above the
//     target (clamped to seaLevel+1) — only when an anchor exists;
//     tick in CIRCLE counts it down, then SWOOP, re-anchor, re-arm
//     adjusted((8+nextInt(4))·20) and entity.phantom.swoop (vol 10,
//     pitch 0.95+nextFloat·0.1). stop re-anchors 10+nextInt(20) above
//     the MOTION_BLOCKING heightmap at the old anchor.
//   - SweepAttack (target, SWOOP): moveTargetPoint := the target's
//     (x, getY(0.5), z); the box inflated 0.2 touching the target's box
//     lands doHurtTarget, drops to CIRCLE and plays level event 1039
//     (unless silent); a horizontal collision or hurtTime > 0 also
//     drops to CIRCLE. canContinueToUse fails on a dead, creative or
//     spectator target, outside SWOOP, or (every 20 ticks) with a cat
//     inside 16 blocks. stop() clears the target and phase.
//   - CircleAroundAnchor (no target or CIRCLE): start rolls distance
//     5+10·r, height −4+9·r, clockwise ±1 and selectNext; tick re-rolls
//     height (1/adjusted(350)), widens the circle (1/adjusted(250),
//     past 15 back to 5 with the direction flipped), jumps the angle
//     (1/adjusted(450)), and re-aims when the point is touched (< 2
//     blocks) or a block sits right below/above in the travel
//     direction. selectNext: anchor := blockPosition when unset, angle
//     += clockwise·15°, point = anchor + (d·cos, −4+height, d·sin).
//   - PhantomMoveControl.tick (every tick): a horizontal collision
//     turns yRot 180 and resets speed to 0.1. Toward moveTargetPoint
//     the horizontal leg is shortened by |dy·0.7|/h, yRot approaches
//     the heading 4°/tick, speed approaches 1.8 by 0.005·1.8/speed
//     while within 3° of the heading else 0.2 by 0.025, xRot follows
//     the climb, and the delta moves 0.2 of the way to the wanted
//     velocity. travel is travelFlying(input, 0.2) with no input —
//     move, ×0.91 (×0.8 water, ×0.5 lava), no gravity.
//   - PhantomBodyRotationControl: yHeadRot = yBodyRot, yBodyRot = yRot;
//     PhantomLookControl.tick is empty.
//   - ID_SIZE (index 16, int, 0..64): the box scales by 1+0.15·size and
//     onSyncedDataUpdated rebases attack_damage to 6+size — a write of
//     the define default 0 never fires it, so a size-0 phantom keeps
//     the supplier's 2.
//   - finalizeSpawn (a bare summon) anchors 5 above the spawn block.
//     NBT: anchor_pos (BlockPos) and size.

import (
	"math"
	"math/rand"
	"sort"

	"mc-go/internal/world"
)

func init() {
	flyerMobs["phantom"] = true
}

// phantomSummonInit is Phantom.finalizeSpawn's anchor for the no-NBT
// summon overload.
func (s *Server) phantomSummonInit(e *Entity) {
	st := flyerOf(e)
	st.anchor = world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)) + 5, Z: int(math.Floor(e.Z))}
	st.hasAnchor = true
}

// phantomSummonNBT is Phantom.readAdditionalSaveData.
func (s *Server) phantomSummonNBT(e *Entity, tag *snbtTag) {
	st := flyerOf(e)
	if v, ok := tag.vals["size"]; ok {
		s.phantomSetSize(e, int32(snbtNum(v)))
	}
	if v, ok := tag.vals["anchor_pos"]; ok && len(v.items) == 3 {
		st.anchor = world.Pos{X: int(snbtNum(v.items[0])), Y: int(snbtNum(v.items[1])), Z: int(snbtNum(v.items[2]))}
		st.hasAnchor = true
	}
}

// phantomSetSize is setPhantomSize — clamp to 0..64; a changed value
// fires updatePhantomSizeInfo (attack_damage base 6+size).
func (s *Server) phantomSetSize(e *Entity, size int32) {
	st := flyerOf(e)
	if size < 0 {
		size = 0
	}
	if size > 64 {
		size = 64
	}
	if size == st.size {
		return
	}
	st.size = size
	st.sizeTouched = true
	if e.AttrBase == nil {
		e.AttrBase = map[string]float64{}
	}
	e.AttrBase["attack_damage"] = float64(6 + size)
	e.MetaDirty = true
}

// phantomDims is the size-scaled box (0.9 × 0.5 base).
func phantomDims(e *Entity) (half, height float64) {
	sc := 1.0
	if e.flyer != nil {
		sc = 1 + 0.15*float64(e.flyer.size)
	}
	return 0.9 * sc / 2, 0.5 * sc
}

// phantomTargetOK is canAttack(TargetingConditions.DEFAULT).
func (s *Server) phantomTargetOK(e *Entity, st *flyerState) *Session {
	t := s.flyerTargetSess(st)
	if t != nil && s.flyerPlayerOK(e, t) {
		return t
	}
	return nil
}

func (s *Server) phantomStep(e *Entity) bool {
	st := flyerOf(e)
	w := s.dimWorld(e.Dim)
	if w == nil {
		return true
	}
	if flyerEvenTick(e) {
		s.phantomSelectors(e, st)
	}
	s.phantomMoveControl(e, st)
	e.HeadYaw = e.Yaw
	e.LookYaw = e.Yaw
	half, h := phantomDims(e)
	s.flyerTravel(e, w, half, h)
	return true
}

// phantomSelectors is one even-tick pass of the target and goal
// selectors: stop what can't continue, start what can use, tick.
func (s *Server) phantomSelectors(e *Entity, st *flyerState) {
	// Target selector — PhantomAttackPlayerTargetGoal.
	if st.targetGoalOn && s.phantomTargetOK(e, st) == nil {
		st.targetGoalOn = false
	}
	if !st.targetGoalOn {
		if st.nextScan > 0 {
			st.nextScan--
		} else {
			st.nextScan = 30
			if t := s.phantomScan(e); t != nil {
				st.target = t.uuid
				st.targetGoalOn = true
			}
		}
	}
	t := s.phantomTargetOK(e, st)
	// Stops.
	if st.strategyOn && t == nil {
		st.strategyOn = false
		if st.hasAnchor {
			if d := s.dims[e.Dim]; d != nil {
				hy := d.w.TopMotionBlocking(st.anchor.X, st.anchor.Z, d.minY, d.minY+d.height-1) + 1
				st.anchor.Y = hy + 10 + rand.Intn(20)
			}
		}
	}
	if st.sweepOn && !s.phantomSweepContinue(e, st, t) {
		st.sweepOn = false
		st.target = [16]byte{}
		st.swoop = false
		t = nil
	}
	if st.circleOn && !(t == nil || !st.swoop) {
		st.circleOn = false
	}
	// Starts, priority order.
	if !st.strategyOn && t != nil {
		st.strategyOn = true
		st.nextSweep = 5
		st.swoop = false
		s.phantomAnchorAboveTarget(e, st, t)
	}
	if !st.sweepOn && t != nil && st.swoop {
		st.sweepOn = true
	}
	if !st.circleOn && !st.sweepOn && (t == nil || !st.swoop) {
		st.circleOn = true
		st.circleDist = 5 + rand.Float32()*10
		st.circleHeight = -4 + rand.Float32()*9
		if rand.Intn(2) == 0 {
			st.clockwise = 1
		} else {
			st.clockwise = -1
		}
		s.phantomSelectNext(e, st)
	}
	// Ticks, registration order.
	if st.strategyOn && !st.swoop {
		st.nextSweep--
		if st.nextSweep <= 0 {
			st.swoop = true
			s.phantomAnchorAboveTarget(e, st, t)
			st.nextSweep = int32((8+rand.Intn(4))*20+1) / 2
			if !e.Silent {
				if id, ok := SoundEventID("entity.phantom.swoop"); ok {
					s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, id, srcHostile, 10, 0.95+rand.Float32()*0.1, nil)
				}
			}
		}
	}
	if st.sweepOn && t != nil {
		s.phantomSweepTick(e, st, t)
	}
	if st.circleOn {
		s.phantomCircleTick(e, st)
	}
}

// phantomScan is AttackPlayerTarget's player scan.
func (s *Server) phantomScan(e *Entity) *Session {
	half, h := phantomDims(e)
	type cand struct {
		sess *Session
		y    float64
	}
	var cs []cand
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if !s.flyerPlayerOK(e, sess) {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		// getNearbyPlayers: the player's box meets the phantom's box
		// inflated (16, 64, 16), then the 64-block range test.
		if px+0.3 < e.X-half-16 || px-0.3 > e.X+half+16 ||
			py+1.8 < e.Y-64 || py > e.Y+h+64 ||
			pz+0.3 < e.Z-half-16 || pz-0.3 > e.Z+half+16 {
			return true
		}
		dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
		if dx*dx+dy*dy+dz*dz > 64*64 {
			return true
		}
		cs = append(cs, cand{sess, py})
		return true
	})
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].y > cs[j].y })
	if len(cs) == 0 {
		return nil
	}
	return cs[0].sess
}

// phantomAnchorAboveTarget is AttackStrategy.setAnchorAboveTarget.
func (s *Server) phantomAnchorAboveTarget(e *Entity, st *flyerState, t *Session) {
	if !st.hasAnchor || t == nil {
		return
	}
	px, py, pz, _, _, _ := t.Pos()
	st.anchor = world.Pos{X: int(math.Floor(px)), Y: int(math.Floor(py)) + 20 + rand.Intn(20), Z: int(math.Floor(pz))}
	if d := s.dims[e.Dim]; d != nil && st.anchor.Y < int(d.seaLevel) {
		st.anchor.Y = int(d.seaLevel) + 1
	}
}

// phantomSweepContinue is SweepAttack.canContinueToUse.
func (s *Server) phantomSweepContinue(e *Entity, st *flyerState, t *Session) bool {
	if t == nil || !st.swoop {
		return false
	}
	if e.TickCount > st.catSearchTick {
		st.catSearchTick = e.TickCount + 20
		half, h := phantomDims(e)
		scared := false
		catID, _ := EntityTypeID("cat")
		s.entities.mu.RLock()
		for _, c := range s.entities.byID {
			if c.Type != catID || c.Dying || c.Dim != e.Dim {
				continue
			}
			if c.X > e.X-half-16 && c.X < e.X+half+16 && c.Y > e.Y-16-0.7 && c.Y < e.Y+h+16 &&
				c.Z > e.Z-half-16 && c.Z < e.Z+half+16 {
				scared = true
			}
		}
		s.entities.mu.RUnlock()
		if scared {
			return false
		}
	}
	return true
}

// phantomSweepTick is SweepAttack.tick.
func (s *Server) phantomSweepTick(e *Entity, st *flyerState, t *Session) {
	px, py, pz, _, _, _ := t.Pos()
	st.moveTX, st.moveTY, st.moveTZ = px, py+0.9, pz
	half, h := phantomDims(e)
	half += 0.2
	if e.X-half < px+0.3 && e.X+half > px-0.3 &&
		e.Y-0.2 < py+1.8 && e.Y+h+0.2 > py &&
		e.Z-half < pz+0.3 && e.Z+half > pz-0.3 {
		dmg := float32(s.mobAttr(e, "phantom", "attack_damage", 2))
		s.damageWith(t, dmg, translatable("death.attack.mob",
			literal(t.getName()), literal(mobDisplayName("phantom"))), damageSource{
			typeName: "mob_attack", cause: e.ID, direct: e.ID,
		})
		st.swoop = false
		if !e.Silent {
			s.broadcastLevelEvent(e.Dim, 1039, world.Pos{
				X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z)),
			}, 0)
		}
	} else if e.HColl || e.HurtTime > 0 {
		st.swoop = false
	}
}

// phantomCircleTick is CircleAroundAnchor.tick.
func (s *Server) phantomCircleTick(e *Entity, st *flyerState) {
	if rand.Intn(175) == 0 {
		st.circleHeight = -4 + rand.Float32()*9
	}
	if rand.Intn(125) == 0 {
		st.circleDist++
		if st.circleDist > 15 {
			st.circleDist = 5
			st.clockwise = -st.clockwise
		}
	}
	if rand.Intn(225) == 0 {
		st.angle = rand.Float32() * 2 * math.Pi
		s.phantomSelectNext(e, st)
	}
	dx, dy, dz := st.moveTX-e.X, st.moveTY-e.Y, st.moveTZ-e.Z
	if dx*dx+dy*dy+dz*dz < 4 {
		s.phantomSelectNext(e, st)
	}
	w := s.dimWorld(e.Dim)
	if w == nil {
		return
	}
	bp := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
	if st.moveTY < e.Y && w.GetBlock(world.Pos{X: bp.X, Y: bp.Y - 1, Z: bp.Z}) != world.Air {
		st.circleHeight = float32(math.Max(1, float64(st.circleHeight)))
		s.phantomSelectNext(e, st)
	}
	if st.moveTY > e.Y && w.GetBlock(world.Pos{X: bp.X, Y: bp.Y + 1, Z: bp.Z}) != world.Air {
		st.circleHeight = float32(math.Min(-1, float64(st.circleHeight)))
		s.phantomSelectNext(e, st)
	}
}

// phantomSelectNext is CircleAroundAnchor.selectNext.
func (s *Server) phantomSelectNext(e *Entity, st *flyerState) {
	if !st.hasAnchor {
		st.anchor = world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
		st.hasAnchor = true
	}
	st.angle += st.clockwise * 15 * math.Pi / 180
	st.moveTX = float64(st.anchor.X) + float64(st.circleDist*float32(math.Cos(float64(st.angle))))
	st.moveTY = float64(st.anchor.Y) + float64(-4+st.circleHeight)
	st.moveTZ = float64(st.anchor.Z) + float64(st.circleDist*float32(math.Sin(float64(st.angle))))
}

// phantomMoveControl is PhantomMoveControl.tick.
func (s *Server) phantomMoveControl(e *Entity, st *flyerState) {
	if e.HColl {
		e.Yaw += 180
		st.moveSpeed = 0.1
	}
	dx, dy, dz := st.moveTX-e.X, st.moveTY-e.Y, st.moveTZ-e.Z
	h := math.Sqrt(dx*dx + dz*dz)
	if math.Abs(h) <= 1e-5 {
		return
	}
	f := 1 - math.Abs(dy*0.7)/h
	dx *= f
	dz *= f
	h = math.Sqrt(dx*dx + dz*dz)
	d3 := math.Sqrt(dx*dx + dz*dz + dy*dy)
	y0 := e.Yaw
	head := float32(math.Atan2(dz, dx))
	cur := wrapDeg(e.Yaw + 90)
	want := wrapDeg(head * 57.295776)
	e.Yaw = approachDeg(cur, want, 4) - 90
	if float32(math.Abs(float64(wrapDeg(e.Yaw-y0)))) < 3 {
		st.moveSpeed = approachF(st.moveSpeed, 1.8, 0.005*(1.8/st.moveSpeed))
	} else {
		st.moveSpeed = approachF(st.moveSpeed, 0.2, 0.025)
	}
	pitch := float32(-(math.Atan2(-dy, h) * 57.2957763671875))
	e.Pitch = pitch
	yr := float64(e.Yaw+90) * math.Pi / 180
	pr := float64(pitch) * math.Pi / 180
	sp := float64(st.moveSpeed)
	vx := float64(float32(sp*math.Cos(yr))) * math.Abs(dx/d3)
	vz := float64(float32(sp*math.Sin(yr))) * math.Abs(dz/d3)
	vy := float64(float32(sp*math.Sin(pr))) * math.Abs(dy/d3)
	e.MotionX += (vx - e.MotionX) * 0.2
	e.MotionY += (vy - e.MotionY) * 0.2
	e.MotionZ += (vz - e.MotionZ) * 0.2
}

// approachF is Mth.approach for floats.
func approachF(cur, target, step float32) float32 {
	if cur < target {
		return float32(math.Min(float64(cur+step), float64(target)))
	}
	return float32(math.Max(float64(cur-step), float64(target)))
}
