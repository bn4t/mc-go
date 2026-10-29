package server

// Fishing rods and the bobber entity. Use Item on a fishing_rod casts
// when the player has no bobber and retrieves when one is out — vanilla
// FishingRodItem.use is exactly that two-branch (player.fishing null
// check). The bobber flies with the arrow-style integrator at gravity
// 0.03/drag 0.92, parks on the first water surface it touches, and runs
// the vanilla catch state machine: lured (100-600 ticks minus the lure
// speed bonus) -> hooked (20-80, the fish-trail particle run) -> nibble
// (20-40, splash sound + the DATA_BITING metadata flag) -> reset and
// re-roll. Reeling during the nibble rolls the gameplay/fishing loot
// table, fires fishing_rod_hooked for every caught stack, launches the
// catch at the player, and costs 1 rod durability; an entity hooked on
// the line is pulled and costs 3 (item) or 5 (other) instead.

import (
	"math"
	"math/rand"
	"strconv"
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Bobber physics and timers, all measured from FishingHook bytecode:
// air gravity -0.03/tick and 0.92 drag, the lured/hooked/nibble
// countdowns, and the bite window.
const (
	fishGravity      = 0.03
	fishDrag         = 0.92
	fishCastSpeed    = 0.6
	fishLuredMin     = 100
	fishLuredMax     = 600
	fishHookedMin    = 20
	fishHookedMax    = 80
	fishNibbleMin    = 20
	fishNibbleMax    = 40
	fishingRodItemID = 1082

	sndBobberSplash   = 640
	sndBobberThrow    = 641
	sndBobberRetrieve = 639
)

// fishBobber is the per-entity catch state hanging off Entity.
type fishBobber struct {
	owner      *Session
	state      fishState
	lured      int
	hooked     int
	nibble     int
	surfaceY   float64  // water surface the bobber parked on
	hookedIn   int32    // eid of the entity the bobber rides, 0 none
	hookedSess *Session // hooked player (players are sessions, not entities)
	grounded   bool     // landed on solid blocks — retrieve costs 2
	lureSpeed  int      // lure level × 5 — subtracted from every lured roll
	luck       int      // luck_of_the_sea level — shifts loot weights via quality
	openWater  bool     // vanilla's field: latched false during hooked/nibble
	outOfWater int      // outOfWaterTime — ticks spent outside water (cap 10)
	fishAngle  float32  // approach-angle for the FISHING particle stream
}

type fishState int

const (
	fishFly fishState = iota // still in flight / falling
	fishBob                  // floating on water, waiting
)

// useFishingRod runs FishingRodItem.use: cast when no bobber is out,
// retrieve the running one otherwise. The cast's rotation comes from
// the use packet's floats (ServerGamePacketListenerImpl.handleUseItem
// applies them before the item dispatch), not the tracked position.
func (s *Server) useFishingRod(sess *Session, yaw, pitch float32) {
	if sess.fishingBobber != 0 {
		s.retrieveBobber(sess)
		return
	}
	x, y, z, _, _, _ := sess.Pos()
	s.castBobber(sess, x, y, z, yaw, pitch)
	sess.addItemStat(statUsed, fishingRodItemID, 1)
}

// castBobber spawns the bobber on the vanilla spawn point — the eye
// point shifted -0.3 on the pure-yaw horizontal components — with the
// ctor's per-component triangle-scaled throw vector, and plays
// entity.fishing_bobber.throw.
func (s *Server) castBobber(sess *Session, x, y, z float64, yaw, pitch float32) {
	yawR := float64(yaw) * math.Pi / 180
	pitchR := float64(pitch) * math.Pi / 180
	// FishingHook's ctor: the throw vector is pure-yaw x/z plus the
	// clamped pitch tangent — (-sinYaw, clamp(-tanPitch,±5), cosYaw) —
	// and each component is scaled by its OWN triangle draw centered
	// on 0.5, so the mean per-component factor is 0.6/dist + 0.5.
	mx := -math.Sin(yawR)
	my := math.Max(-5, math.Min(5, -math.Tan(pitchR)))
	mz := math.Cos(yawR)
	dist := math.Sqrt(mx*mx + my*my + mz*mz)
	vx := mx * (fishCastSpeed/dist + randTriangle(0.5, 0.0103365))
	vy := my * (fishCastSpeed/dist + randTriangle(0.5, 0.0103365))
	vz := mz * (fishCastSpeed/dist + randTriangle(0.5, 0.0103365))

	etype, ok := EntityTypeID("fishing_bobber")
	if !ok {
		return
	}
	// Spawn: eye point shifted -0.3 on the pure-yaw horizontal
	// components; rotation comes from the post-jitter velocity.
	ey := y + 1.62 // standing eye height
	e := s.entities.Spawn(&Entity{
		Type:    etype,
		Dim:     sess.dimName(),
		X:       x - math.Sin(yawR)*0.3,
		Y:       ey,
		Z:       z - math.Cos(yawR)*0.3,
		Yaw:     float32(math.Atan2(vx, vz) * 180 / math.Pi),
		Pitch:   float32(math.Atan2(vy, math.Sqrt(vx*vx+vz*vz)) * 180 / math.Pi),
		MotionX: vx, MotionY: vy, MotionZ: vz,
		VX: int16(vx * 8000), VY: int16(vy * 8000), VZ: int16(vz * 8000),
		Data: sess.entityID.Load(), // spawn packet owner field
	})
	rod := sess.heldStack()
	fb := &fishBobber{
		owner:     sess,
		lureSpeed: enchLevel(rod.Ench, "lure") * 5,
		luck:      enchLevel(rod.Ench, "luck_of_the_sea"),
		openWater: true,
	}
	e.fishing = fb
	sess.fishingBobber = e.ID
	s.broadcastEntitySpawn(nil, *e)
	s.playSoundAt(e.Dim, e.X, e.Y, e.Z, sndBobberThrow, 0.5, 0.4/(0.4*float32(rand.Float64())+0.8))
}

// randTriangle draws from vanilla RandomSource.triangle(center, spread):
// sum of two uniform(-spread, spread) deviates around center.
func randTriangle(center, spread float64) float64 {
	return center + spread*(rand.Float64()-rand.Float64())
}

// Vanilla emits each synched field as its own packet —
// entityData.set(DATA_HOOKED_ENTITY, v) and set(DATA_BITING, v) are
// separate dirty-flagged entries, never bundled. Index 8 hooked
// entity id (varint, 0 none), index 9 biting bool. Wire types:
// varint=1, boolean=8.
func encodeBobberHooked(hookedID int32) []byte {
	var b protocol.Buffer
	b.Byte(8)
	b.Byte(1) // varint
	b.VarInt(hookedID)
	b.Byte(0xFF)
	return b.Bytes()
}

func encodeBobberBiting(biting bool) []byte {
	var b protocol.Buffer
	b.Byte(9)
	b.Byte(8) // boolean
	if biting {
		b.Byte(1)
	} else {
		b.Byte(0)
	}
	b.Byte(0xFF)
	return b.Bytes()
}

// enchLevel reads "<name>:<level>" out of the canonical Ench
// component string ("name:lvl,name:lvl", name sorted).
func enchLevel(ench, name string) int {
	for _, part := range strings.Split(ench, ",") {
		if strings.HasPrefix(part, name+":") {
			if lv, err := strconv.Atoi(part[len(name)+1:]); err == nil {
				return lv
			}
		}
	}
	return 0
}

// playSoundAt broadcasts a positioned Sound packet at fractional
// coordinates (bobber sounds are entity-positioned NEUTRAL events).
func (s *Server) playSoundAt(dim string, x, y, z float64, soundID int32, vol, pitch float32) {
	s.playSoundSrc(dim, x, y, z, soundID, 6, vol, pitch, nil)
}

// playSoundSrc is playSoundAt with an explicit SoundSource id and an
// optional exclusion — vanilla's playSound(except, ...) leaves the
// causer's own client to play the sound locally.
func (s *Server) playSoundSrc(dim string, x, y, z float64, soundID, src int32, vol, pitch float32, except *Session) {
	var b protocol.Buffer
	b.VarInt(soundID + 1)
	b.VarInt(src)
	b.Int(int32(x * 8))
	b.Int(int32(y * 8))
	b.Int(int32(z * 8))
	b.Float(vol)
	b.Float(pitch)
	b.Long(0)
	body := b.Bytes()
	// sendToAllNearExcept: the sound only ships to listeners within
	// 16*max(1,volume) blocks of the position.
	r := 16.0
	if vol > 1 {
		r = 16 * float64(vol)
	}
	r2 := r * r
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other == except || other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		px, py, pz, _, _, _ := other.Pos()
		dx, dy, dz := x-px, y-py, z-pz
		if dx*dx+dy*dy+dz*dz >= r2 {
			return true
		}
		_ = other.write(other.ids().PlaySound, body)
		return true
	})
}

// playSoundEntity broadcasts a Sound Entity packet anchored on
// entityID — vanilla's entity-positioned playSound path — excluding
// `except`.
func (s *Server) playSoundEntity(dim string, entityID int32, soundID, src int32, vol, pitch float32, except *Session) {
	var b protocol.Buffer
	b.VarInt(soundID + 1)
	b.VarInt(src)
	b.VarInt(entityID)
	b.Float(vol)
	b.Float(pitch)
	b.Long(0)
	body := b.Bytes()
	x, y, z := float64(0), float64(0), float64(0)
	if e, ok := s.entities.Get(entityID); ok {
		x, y, z = e.X, e.Y, e.Z
	} else if p := s.sessionByEntityID(entityID); p != nil {
		// Players are not in the entity manager: an entity-anchored
		// sound on a player (a firework's launch on its shooter) is
		// heard around the player's own position.
		x, y, z, _, _, _ = p.Pos()
	}
	r := 16.0
	if vol > 1 {
		r = 16 * float64(vol)
	}
	r2 := r * r
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other == except || other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		px, py, pz, _, _, _ := other.Pos()
		dx, dy, dz := x-px, y-py, z-pz
		if dx*dx+dy*dy+dz*dz >= r2 {
			return true
		}
		_ = other.write(other.ids().PlaySoundEntity, body)
		return true
	})
}

// fishTickStep advances every live bobber one tick: flight physics
// until it parks on water, then the lured -> hooked -> nibble
// countdowns. Vanilla FishingHook.tick is the same walk.
func (s *Server) fishTickStep() {
	s.entities.mu.RLock()
	hooks := make([]*Entity, 0, 4)
	for _, e := range s.entities.byID {
		if e.Dying || e.fishing == nil {
			continue
		}
		hooks = append(hooks, e)
	}
	s.entities.mu.RUnlock()
	for _, e := range hooks {
		fb := e.fishing
		owner := fb.owner
		// shouldStopFishing: owner gone, dead, holding no rod, or the
		// line stretched past 32 blocks — the bobber discards.
		if owner == nil || owner.state.Load() != StatePlay || owner.dead || !bobberHeldBy(owner) {
			s.discardBobber(e)
			continue
		}
		ox, oy, oz, _, _, _ := owner.Pos()
		if dx, dy, dz := e.X-ox, e.Y-oy, e.Z-oz; dx*dx+dy*dy+dz*dz > 32*32 {
			s.discardBobber(e)
			continue
		}
		d := s.dimensionByName(e.Dim)
		if d == nil {
			continue
		}
		if fb.hookedIn != 0 || fb.hookedSess != nil {
			s.fishHookedTick(e, fb)
		} else if fb.state == fishFly {
			s.fishFlyTick(e, d, fb)
		} else {
			s.fishBobTick(e, d, fb)
		}
		s.broadcastEntityTeleport(nil, e.ID, e.X, e.Y, e.Z, e.Yaw, e.Pitch, false)
	}
}

// bobberHeldBy reports whether the owner still holds a fishing_rod.
func bobberHeldBy(sess *Session) bool {
	held := sess.heldStack()
	return held.ID == fishingRodItemID && !held.Empty()
}

// fishHookedTick is FishingHook.FishHookState.HOOKED_IN_ENTITY: the
// bobber rides the hooked entity at getY(0.8) — entity Y + 0.8·height
// — until the entity leaves or dies, which unhooks back to FLYING
// with DATA_HOOKED_ENTITY cleared to 0.
func (s *Server) fishHookedTick(e *Entity, fb *fishBobber) {
	if hs := fb.hookedSess; hs != nil {
		if hs.state.Load() == StatePlay && hs.dimName() == e.Dim {
			hx, hy, hz, _, _, _ := hs.Pos()
			e.X, e.Y, e.Z = hx, hy+1.44, hz // player height 1.8 · 0.8
		} else {
			fb.hookedSess = nil
			s.broadcastEntityMetadata(e.ID, encodeBobberHooked(0))
		}
		return
	}
	he, ok := s.entities.GetPtr(fb.hookedIn)
	if ok && !he.Dying && he.Dim == e.Dim {
		bb := entityBBDims(he)
		e.X, e.Y, e.Z = he.X, he.Y+bb[1]*0.8, he.Z
		return
	}
	fb.hookedIn = 0
	s.broadcastEntityMetadata(e.ID, encodeBobberHooked(0))
}

// bobberSweepHit is FishingHook.checkCollision's entity arm:
// ProjectileUtil.getHitResultOnMoveVector sweeps the movement ray
// against every entity canHitEntity allows — living mobs, live
// ItemEntity stacks, and other players, the owner excluded —
// and returns the closest hit along the ray.
func (s *Server) bobberSweepHit(e *Entity, fb *fishBobber) (*Entity, *Session) {
	ox, oy, oz := e.X, e.Y, e.Z
	ex, ey, ez := ox+e.MotionX, oy+e.MotionY, oz+e.MotionZ
	m := projectilePickMargin(e.TicksLived)
	var best *Entity
	var bestSess *Session
	bt := 2.0
	s.entities.mu.RLock()
	list := make([]*Entity, 0, len(s.entities.byID))
	for _, t := range s.entities.byID {
		list = append(list, t)
	}
	s.entities.mu.RUnlock()
	for _, t := range list {
		if t.ID == e.ID || t.Dim != e.Dim || t.Dying {
			continue
		}
		name, ok := EntityTypeName(t.Type)
		if !ok || (!t.Living && name != "item") {
			continue
		}
		bb := entityBBDims(t)
		if t0, hit := segmentHitsBoxT(ox, oy, oz, ex, ey, ez, t.X, t.Y, t.Z, bb[0], bb[1], m); hit && t0 < bt {
			best, bestSess, bt = t, nil, t0
		}
	}
	s.sessions.Range(func(_, v any) bool {
		hs := v.(*Session)
		if hs == fb.owner || hs.state.Load() != StatePlay || hs.dimName() != e.Dim {
			return true
		}
		hx, hy, hz, _, _, _ := hs.Pos()
		if t0, hit := segmentHitsBoxT(ox, oy, oz, ex, ey, ez, hx, hy, hz, 0.6, 1.8, m); hit && t0 < bt {
			best, bestSess, bt = nil, hs, t0
		}
		return true
	})
	return best, bestSess
}

// sendLevelParticles broadcasts a vanilla sendParticles packet for a
// data-less particle type to every play-state session in dim.
func (s *Server) sendLevelParticles(dim, ptype string, x, y, z, xd, yd, zd, speed float64, count int32) {
	pid, ok := ParticleTypeID(ptype)
	if !ok {
		return
	}
	var b protocol.Buffer
	b.Byte(0) // overrideLimiter
	b.Byte(0) // alwaysShow
	b.Double(x)
	b.Double(y)
	b.Double(z)
	b.Float(float32(xd))
	b.Float(float32(yd))
	b.Float(float32(zd))
	b.Float(float32(speed))
	b.Int(count)
	b.VarInt(pid)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != dim {
			return true
		}
		_ = other.write(other.ids().PlayLevelParticles, body)
		return true
	})
}

// fishFlyTick integrates the bobber's flight: gravity 0.03, air drag
// 0.92, and parking the first time it enters water. The parked y is
// the water surface (top of the block minus the bobber's half-height
// sink, vanilla rides ~0.15 below surface).
func (s *Server) fishFlyTick(e *Entity, d *dimension, fb *fishBobber) {
	// Projectile's checkCollision: the first living or item entity
	// the movement ray clips becomes hookedIn — DATA_HOOKED_ENTITY
	// (idx8) emits eid+1 and the delta zeroes.
	if hit, hs := s.bobberSweepHit(e, fb); hit != nil || hs != nil {
		eid := int32(0)
		if hit != nil {
			fb.hookedIn = hit.ID
			eid = hit.ID
		} else {
			fb.hookedSess = hs
			eid = hs.entityID.Load()
		}
		s.broadcastEntityMetadata(e.ID, encodeBobberHooked(eid+1))
		e.MotionX, e.MotionY, e.MotionZ = 0, 0, 0
		return
	}
	e.X += e.MotionX
	e.Y += e.MotionY
	e.Z += e.MotionZ
	e.MotionY -= fishGravity
	e.MotionX *= fishDrag
	e.MotionY *= fishDrag
	e.MotionZ *= fishDrag
	p := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
	blk := d.w.GetBlock(p)
	if k, level, ok := fluidOf(blk); ok && k == fluidWater {
		// Level is 0 for a source; surface sits at (pos + 1 - level/8)
		// minus the bobber's resting offset — vanilla keeps the bobber
		// just under the surface line.
		surface := float64(p.Y) + 1.0 - float64(level)/8.0 - 0.15
		fb.surfaceY = surface
		fb.state = fishBob
		e.Y = surface
		e.MotionX, e.MotionY, e.MotionZ = 0, 0, 0
		fb.lured = fishLuredMin + rand.Intn(fishLuredMax-fishLuredMin+1)
		return
	}
	// Solid ground under the flight: vanilla lands the bobber and it
	// sits waiting (the 1200-tick life cap is folded into state=Bob
	// with lured counting — matches the dead-stick case).
	if _, has := blockNameAt(d, p); has && !world.NonSolid(d.w.GetBlock(p)) {
		fb.state = fishBob
		fb.grounded = true
		fb.lured = fishLuredMin + rand.Intn(fishLuredMax-fishLuredMin+1)
		e.MotionX, e.MotionY, e.MotionZ = 0, 0, 0
		e.Y = float64(p.Y) + 1.0
	}
}

// fishBobTick runs the countdown chain while the bobber sits: lured
// ticks down (with the sky/rain modifier — a flat test world sees sky,
// no rain), then hooked, then the nibble window with the splash and
// DATA_BITING set; on expiry it re-rolls lured like vanilla's
// timeUntilLured<=0 branch.
func (s *Server) fishBobTick(e *Entity, d *dimension, fb *fishBobber) {
	inWater := false
	p := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
	if k, _, ok := fluidOf(d.w.GetBlock(p)); ok && k == fluidWater {
		inWater = true
	}
	// outOfWaterTime: -1 while wet, +1 while dry, cap 10 — vanilla
	// counts it inside the bob branch, before the catch logic.
	if inWater {
		if fb.outOfWater > 0 {
			fb.outOfWater--
		}
		fb.grounded = false
	} else {
		if fb.outOfWater < 10 {
			fb.outOfWater++
		}
		// Vanilla's onGround is not latched — Entity.move() recomputes
		// it every tick: a dry-landed bobber has zero delta, makes no
		// vertical collision, so the flag clears one tick, gravity
		// re-collides it the next. retrieve() reads whichever phase
		// it lands on, so the rod wear from a grounded retrieve is
		// genuinely 0-or-2.
		fb.grounded = !fb.grounded
	}
	// openWater resets while idle and latch-ANDs during hooked/nibble —
	// one tick out of open water (or 10+ ticks dry) closes it.
	if fb.nibble <= 0 && fb.hooked <= 0 {
		fb.openWater = true
	} else {
		fb.openWater = fb.openWater && fb.outOfWater < 10 && bobberOpenWater(d, p)
	}
	if !inWater {
		// catchingFish only runs while the bobber is in water — a
		// dry-landed bobber never ticks its timers.
		return
	}
	// fishingSpeed: 1 + 1 if raining at the above block (25% roll) - 1
	// if the column can't see sky (50% roll). mc-go has no weather, so
	// only the sky check applies — same integer semantics.
	fishingSpeed := 1
	if rand.Float64() < 0.5 && !bobberSeesSky(d, p) {
		fishingSpeed--
	}
	if fb.nibble > 0 {
		fb.nibble--
		if fb.nibble <= 0 {
			// Missed window: DATA_BITING false + both timers zero —
			// the idle branch re-rolls lured next tick.
			fb.nibble = 0
			fb.hooked = 0
			fb.lured = 0
			s.broadcastEntityMetadata(e.ID, encodeBobberBiting(false))
		}
		return
	}
	if fb.hooked > 0 {
		fb.hooked -= fishingSpeed
		if fb.hooked > 0 {
			// The approach stream: fishAngle wanders by triangle(0,
			// 9.188)°/tick, two FISHING particles cross toward the
			// bobber at the remaining-distance point, plus a 15%
			// BUBBLE — all only when that point is in water.
			fb.fishAngle += float32(randTriangle(0.0, 9.188))
			angle := float64(fb.fishAngle) * math.Pi / 180
			as, ac := math.Sin(angle), math.Cos(angle)
			fishX := e.X + as*float64(fb.hooked)*0.1
			fishY := float64(math.Floor(e.Y)) + 1.0
			fishZ := e.Z + ac*float64(fb.hooked)*0.1
			bp := world.Pos{X: int(math.Floor(fishX)), Y: int(math.Floor(fishY - 1.0)), Z: int(math.Floor(fishZ))}
			if k, lv, ok := fluidOf(d.w.GetBlock(bp)); ok && k == fluidWater && lv == 0 {
				if rand.Float64() < 0.15 {
					s.sendLevelParticles(e.Dim, "bubble", fishX, fishY-0.1, fishZ,
						as, 0.1, ac, 0, 1)
				}
				pxm, pzm := as*0.04, ac*0.04
				s.sendLevelParticles(e.Dim, "fishing", fishX, fishY, fishZ,
					pzm, 0.01, -pxm, 1.0, 0)
				s.sendLevelParticles(e.Dim, "fishing", fishX, fishY, fishZ,
					-pzm, 0.01, pxm, 1.0, 0)
			}
		} else {
			// The bite: splash sound, BUBBLE + FISHING bursts at
			// (1+width·20) count, DATA_BITING, nibble 20-40.
			fb.hooked = 0
			y := e.Y + 0.5
			cnt := int32(1 + 0.25*20)
			s.playSoundAt(e.Dim, e.X, e.Y, e.Z, sndBobberSplash,
				0.25, 1.0+0.4*float32(rand.Float64()-rand.Float64()))
			s.sendLevelParticles(e.Dim, "bubble", e.X, y, e.Z,
				0.25, 0, 0.25, 0.2, cnt)
			s.sendLevelParticles(e.Dim, "fishing", e.X, y, e.Z,
				0.25, 0, 0.25, 0.2, cnt)
			s.broadcastEntityMetadata(e.ID, encodeBobberBiting(true))
			fb.nibble = fishNibbleMin + rand.Intn(fishNibbleMax-fishNibbleMin+1)
		}
		return
	}
	if fb.lured > 0 {
		fb.lured -= fishingSpeed
		// Tease SPLASH: 0.15 base, ramping as the timer runs out —
		// only when the random splash point is actually water.
		tc := 0.15
		if fb.lured < 20 {
			tc += float64(20-fb.lured) * 0.05
		} else if fb.lured < 40 {
			tc += float64(40-fb.lured) * 0.02
		} else if fb.lured < 60 {
			tc += float64(60-fb.lured) * 0.01
		}
		if rand.Float64() < tc {
			angle := rand.Float64() * 360 * math.Pi / 180
			dist := 25.0 + rand.Float64()*35.0 // nextFloat(25, 60)
			fishX := e.X + math.Sin(angle)*dist*0.1
			fishY := float64(math.Floor(e.Y)) + 1.0
			fishZ := e.Z + math.Cos(angle)*dist*0.1
			bp := world.Pos{X: int(math.Floor(fishX)), Y: int(math.Floor(fishY - 1.0)), Z: int(math.Floor(fishZ))}
			if k, lv, ok := fluidOf(d.w.GetBlock(bp)); ok && k == fluidWater && lv == 0 {
				s.sendLevelParticles(e.Dim, "splash", fishX, fishY, fishZ,
					0.1, 0, 0.1, 0, int32(2+rand.Intn(2)))
			}
		}
		if fb.lured <= 0 {
			fb.lured = 0
			fb.fishAngle = float32(rand.Float64() * 360)
			fb.hooked = fishHookedMin + rand.Intn(fishHookedMax-fishHookedMin+1)
		}
		return
	}
	// Idle in water: re-roll lured, minus the rod's Lure.
	fb.lured = fishLuredMin + rand.Intn(fishLuredMax-fishLuredMin+1) - fb.lureSpeed
}

// bobberSeesSky reports whether the column above p is open to the sky:
// air and fluids don't block; any block with collision does.
func bobberSeesSky(d *dimension, p world.Pos) bool {
	for y := p.Y + 1; y <= 200; y++ {
		b := d.w.GetBlock(world.Pos{X: p.X, Y: y, Z: p.Z})
		if b == world.Air {
			continue
		}
		if _, _, ok := fluidOf(b); ok {
			continue
		}
		if world.NonSolid(b) {
			continue
		}
		return false
	}
	return true
}

// retrieveBobber runs FishingHook.retrieve: entity-hooked pulls cost
// 3/5 durability, a biting bobber rolls the fishing loot table and
// fires fishing_rod_hooked per caught stack, then the bobber discards
// with the retrieve sound. Returns nothing; the wear lands via
// wearHeld like every other tool.
func (s *Server) retrieveBobber(sess *Session) {
	e, ok := s.entities.GetPtr(sess.fishingBobber)
	if !ok || e.fishing == nil {
		sess.fishingBobber = 0
		return
	}
	fb := e.fishing
	cost := 0
	if fb.hookedIn != 0 || fb.hookedSess != nil {
		// FishingHook.retrieve's hooked branch: pull the entity toward
		// the owner, fire fishing_rod_hooked with an empty caught
		// list, entity_event 31 on the bobber; the wear is 3 for an
		// ItemEntity and 5 for anything else.
		cost = 5
		if he, ok := s.entities.GetPtr(fb.hookedIn); ok && !he.Dying {
			s.pullHookedEntity(sess, e, he)
			if name, _ := EntityTypeName(he.Type); name == "item" {
				cost = 3
			}
		} else if hs := fb.hookedSess; hs != nil && hs.state.Load() == StatePlay {
			s.pullHookedSession(sess, e, hs)
		}
		s.fireFishingRodHooked(sess, e, nil)
		s.broadcastEntityEvent(e.ID, 31)
	} else if fb.nibble > 0 {
		cost = 1
		// in_open_water is the latched field — vanilla keeps
		// evaluating it every hooked/nibble tick, not once at the
		// retrieve.
		drops := rollFishingLoot(fb.openWater, fb.luck)
		s.fireFishingRodHooked(sess, e, drops)
		px, py, pz, _, _, _ := sess.Pos()
		for _, it := range drops {
			s.spawnCaughtItem(e.Dim, e.X, e.Y, e.Z, px, py, pz, it)
			// ExperienceOrb: one orb worth 1-6 per rolled stack at the
			// owner's feet-half-block.
			s.spawnXPOrb(e.Dim, px, py+0.5, pz+0.5, int32(1+rand.Intn(6)))
			if name, ok := world.ItemName(it.ID); ok && fishingCaughtFish[name] {
				sess.addCustomStat("fish_caught", 1)
			}
		}
	}
	if fb.grounded {
		cost = 2 // vanilla's onGround arm overrides the roll
	}
	s.playSoundAt(e.Dim, e.X, e.Y, e.Z, sndBobberRetrieve, 1.0,
		0.4/(0.4*float32(rand.Float64())+0.8))
	s.discardBobber(e)
	if cost > 0 && sess.mode() == GameModeSurvival {
		s.wearHeld(sess, int32(cost))
	}
}

// fishingCaughtFish is the fishing pool's share of ItemTags.FISHES —
// the four fish items the catch table can roll, for Stats.FISH_CAUGHT.
var fishingCaughtFish = map[string]bool{
	"cod": true, "salmon": true, "pufferfish": true, "tropical_fish": true,
}

// pullHookedEntity is FishingHook.pullEntity on a hooked mob or item:
// the entity's delta gains (owner − bobber) · 0.1 and the new delta
// rides the next motion emit.
func (s *Server) pullHookedEntity(sess *Session, bobber, he *Entity) {
	ox, oy, oz, _, _, _ := sess.Pos()
	he.MotionX += (ox - bobber.X) * 0.1
	he.MotionY += (oy - bobber.Y) * 0.1
	he.MotionZ += (oz - bobber.Z) * 0.1
	he.VX = int16(he.MotionX * 8000)
	he.VY = int16(he.MotionY * 8000)
	he.VZ = int16(he.MotionZ * 8000)
	he.NeedsSync = true
}

// pullHookedSession is pullEntity on a hooked player: the delta emits
// as set_entity_motion to every tracker including the victim —
// vanilla's client-side pull rides the bobber's entity_event 31, so
// the wire carries the same new-delta packet the tracker sends.
func (s *Server) pullHookedSession(sess *Session, bobber *Entity, hs *Session) {
	ox, oy, oz, _, _, _ := sess.Pos()
	s.broadcastEntityMotion(bobber.Dim, hs.entityID.Load(),
		(ox-bobber.X)*0.1, (oy-bobber.Y)*0.1, (oz-bobber.Z)*0.1)
}

// discardBobber removes the bobber entity and clears the owner link.
func (s *Server) discardBobber(e *Entity) {
	if e.fishing != nil && e.fishing.owner != nil && e.fishing.owner.fishingBobber == e.ID {
		e.fishing.owner.fishingBobber = 0
	}
	e.fishing = nil
	s.entities.Remove(e.ID)
	s.broadcastEntityRemove([]int32{e.ID})
}

// spawnCaughtItem launches a caught stack from the bobber toward the
// player with vanilla's (dx,dy,dz)*0.1 + |d|*0.08-up launch.
func (s *Server) spawnCaughtItem(dim string, bx, by, bz, px, py, pz float64, it ItemStack) {
	itemType, ok := EntityTypeID("item")
	if !ok {
		return
	}
	dx, dy, dz := px-bx, py-by, pz-bz
	dist := math.Sqrt(dx*dx + dy*dy + dz*dz)
	e := s.entities.Spawn(&Entity{
		Type: itemType,
		Dim:  dim,
		X:    bx, Y: by, Z: bz,
		MotionX: dx * 0.1, MotionY: dy*0.1 + math.Sqrt(dist)*0.08, MotionZ: dz * 0.1,
		Item:        it,
		PickupDelay: 0, // vanilla sets none; the owner picks up at contact
	})
	e.VX = int16(e.MotionX * 8000)
	e.VY = int16(e.MotionY * 8000)
	e.VZ = int16(e.MotionZ * 8000)
	s.broadcastEntitySpawn(nil, *e)
}

// fishingTable is gameplay/fishing's fish pool flattened to the 85%
// fish branch (junk and treasure are the other two pools: junk 10,
// treasure 5 with in_open_water — a test pond qualifies).
type fishEntry struct {
	item   string
	weight int
}

var fishingPool = []struct {
	table  string
	weight int
}{
	{"fish", 85},
	{"junk", 10},
	{"treasure", 5},
}

var fishingFish = []fishEntry{
	{"cod", 60},
	{"salmon", 25},
	{"pufferfish", 13},
	{"tropical_fish", 2},
}

var fishingJunk = []fishEntry{
	{"lily_pad", 17},
	{"leather_boots", 10},
	{"leather", 10},
	{"bone", 10},
	{"potion", 10},
	{"string", 5},
	{"fishing_rod", 2},
	{"bowl", 10},
	{"stick", 5},
	{"ink_sac", 1},
	{"tripwire_hook", 10},
	{"rotten_flesh", 10},
	{"bamboo", 10},
}

var fishingTreasure = []fishEntry{
	{"name_tag", 1},
	{"saddle", 1},
	{"bow", 1},
	{"fishing_rod", 1},
	{"book", 1},
	{"nautilus_shell", 1},
}

// bobberOpenWater evaluates vanilla's in_open_water check
// (FishingHook.calculateOpenWater): four 5x5 layers — one below the
// bobber's block, its own, and two above — each layer must be
// uniformly source water (no collision) or uniformly air/lily_pad,
// and a water layer may not sit above an air layer. The first layer
// may not be above-water. Anything else closes the water.
func bobberOpenWater(d *dimension, p world.Pos) bool {
	prev := 2 // OpenWaterType.INVALID seed
	for dy := -1; dy <= 2; dy++ {
		t := openWaterLayer(d, p, dy)
		switch t {
		case 2: // INVALID layer
			return false
		case 0: // ABOVE_WATER
			if prev == 2 {
				return false
			}
		case 1: // INSIDE_WATER
			if prev == 0 {
				return false
			}
		}
		prev = t
	}
	return true
}

// openWaterLayer classifies one 5x5 layer at p+dy: 0 above-water
// (air or lily_pad), 1 inside-water (water source with no collision),
// 2 invalid (anything else, or a mix).
func openWaterLayer(d *dimension, p world.Pos, dy int) int {
	seen := -1
	for dx := -2; dx <= 2; dx++ {
		for dz := -2; dz <= 2; dz++ {
			b := d.w.GetBlock(world.Pos{X: p.X + dx, Y: p.Y + dy, Z: p.Z + dz})
			t := 2
			switch {
			case b == world.Air:
				t = 0
			default:
				name, _ := world.StateOwner(b)
				if name == "lily_pad" {
					t = 0
				} else if k, level, ok := fluidOf(b); ok && k == fluidWater && level == 0 {
					t = 1
				}
			}
			if seen == -1 {
				seen = t
			} else if t != seen {
				seen = 2
			}
		}
	}
	return seen
}

// rollFishingLoot draws one stack from the fishing table: pool roll,
// then the weighted item in that pool, then the entry's datapack
// functions (set_count, set_potion, set_damage; enchant_with_levels
// is folded to the item conversion — the random enchantment itself
// is not modeled). open=false removes the treasure pool, matching
// vanilla's in_open_water loot condition.
func rollFishingLoot(open bool, luck int) []ItemStack {
	// gameplay/fishing.json's single pool applies quality·luck to each
	// entry's weight: junk 10-2·luck, treasure 5+2·luck (gated
	// in_open_water), fish 85-1·luck.
	junkW := 10 - 2*luck
	if junkW < 0 {
		junkW = 0
	}
	fishW := 85 - luck
	if fishW < 0 {
		fishW = 0
	}
	treasW := 0
	if open {
		treasW = 5 + 2*luck
	}
	pools := []struct {
		table  string
		weight int
	}{
		{"fish", fishW},
		{"junk", junkW},
	}
	if treasW > 0 {
		pools = append(pools, struct {
			table  string
			weight int
		}{"treasure", treasW})
	}
	pool := weightedFishEntry(pools)
	var table []fishEntry
	switch pool {
	case "junk":
		table = fishingJunk
	case "treasure":
		table = fishingTreasure
	default:
		table = fishingFish
	}
	name := weightedFishItem(table)
	it := ItemStack{}

	// set_potion: the junk table's water bottle.
	if name == "potion" {
		if id, ok := world.ItemIDs["potion"]; ok {
			it = ItemStack{ID: id, Count: 1, Potion: "water"}
		}
	} else {
		// enchant_with_levels on book produces an enchanted_book.
		lookup := name
		if name == "book" && pool == "treasure" {
			lookup = "enchanted_book"
		}
		id, ok := world.ItemIDs[lookup]
		if !ok {
			return nil
		}
		it = ItemStack{ID: id, Count: 1}
	}
	if it.ID == 0 {
		return nil
	}

	// set_count 10: junk ink sacs land as a stack of ten.
	if name == "ink_sac" {
		it.Count = 10
	}
	// set_damage: junk rolls 0..90% of max durability, treasure 0..25%.
	frac := 0.0
	switch name {
	case "leather_boots", "fishing_rod":
		if pool == "junk" {
			frac = 0.9
		}
	case "bow":
		frac = 0.25
	}
	if name == "fishing_rod" && pool == "treasure" {
		frac = 0.25
	}
	if frac > 0 {
		if maxDmg, ok := damageableItems[name]; ok {
			it.Damage = int32(rand.Float64() * float64(maxDmg) * frac)
		}
	}
	return []ItemStack{it}
}

func weightedFishEntry(pool []struct {
	table  string
	weight int
}) string {
	total := 0
	for _, p := range pool {
		total += p.weight
	}
	r := rand.Intn(total)
	for _, p := range pool {
		if r < p.weight {
			return p.table
		}
		r -= p.weight
	}
	return pool[0].table
}

func weightedFishItem(entries []fishEntry) string {
	total := 0
	for _, e := range entries {
		total += e.weight
	}
	r := rand.Intn(total)
	for _, e := range entries {
		if r < e.weight {
			return e.item
		}
		r -= e.weight
	}
	return entries[0].item
}
