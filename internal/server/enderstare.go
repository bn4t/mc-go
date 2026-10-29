package server

import (
	"math"
	"math/rand"

	"mc-go/api/registry"
	"mc-go/internal/data"
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
			e.EnderTargetChange = 0
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

	// Pending window: canContinueToUse retests isAngerInducing
	// each tick — the stare OR an already-running isAngryAt keeps
	// the aggro alive; head tracks the pending target while the
	// countdown runs (movement is not gated).
	if e.EnderPending != [16]byte{} {
		t := s.sessionByUUID(e.EnderPending)
		if t == nil || !s.enderAngerInduces(e, t) {
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
				e.EnderTargetChange = e.TickCount
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

// enderDaylightFlee is EnderMan.customServerAiStep's bright-outside
// branch: while isBrightOutside (a skylight dimension inside the
// monster-burn day window) and 600+ ticks have passed since the last
// setTarget, a lightLevelDependentMagicValue over 0.5 at the mob cell
// plus open sky rolls rand·30 < (br−0.4)·2 → setTarget(null) +
// teleport(). The magic value is the v/(4−3v) curve on
// maxLocalRawBrightness/15 (the ambientLight lerp collapses to the
// curve); skyDarken ≈ 0 inside the bright window, so the raw
// brightness is max(blockLight, skyEffective) and cropLit stands in
// for canSeeSky — the same approximations sunBurnTick uses.
func (s *Server) enderDaylightFlee(e *Entity) {
	d := s.dims[e.Dim]
	if d == nil || !d.skylight {
		return
	}
	if e.TickCount < e.EnderTargetChange+600 {
		return
	}
	dt := s.dayTime() % 24000
	if dt < 0 {
		dt += 24000
	}
	if dt >= 12542 && dt <= 23459 {
		return
	}
	pos := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
	raw := int(d.blockLight.get(pos))
	if sk := skyEffective(d, pos); sk > raw {
		raw = sk
	}
	v := float64(raw) / 15.0
	br := v / (4.0 - 3.0*v)
	if br <= 0.5 || !s.cropLit(d, pos) || rand.Float64()*30.0 >= (br-0.4)*2.0 {
		return
	}
	// setTarget(null) — clears CREEPY and STARED_AT together and
	// resets targetChangeTime.
	if e.EnderStareTarget != [16]byte{} || e.EnderCreepy || e.EnderStared {
		e.EnderStareTarget = [16]byte{}
		e.EnderCreepy, e.EnderStared = false, false
		e.MetaDirty = true
	}
	e.EnderTargetChange = 0
	s.enderTeleportRandom(e)
}

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

// EndermanTakeBlockGoal + EndermanLeaveBlockGoal: while mob_griefing
// allows, a 1-in-20 tick roll takes a random reachable
// ENDERMAN_HOLDABLE cell into DATA_CARRY_STATE, and a 1-in-2000 roll
// places it back over a full-collision non-bedrock cell. The take
// strips to the block's default state; the placement must be air
// over a survivable support with no entity inside.
var enderHoldable = func() map[string]bool {
	m := map[string]bool{}
	for _, n := range data.BlockTags["enderman_holdable"] {
		m[n] = true
	}
	return m
}()

// enderCarryPlants are the holdables whose canSurvive wants soil —
// flowers, mushrooms, torchflower and the eyeblossoms. Everything
// else in the tag is a terrain block that survives anywhere.
var enderCarryPlants = map[string]bool{
	"allium": true, "azure_bluet": true, "blue_orchid": true,
	"brown_mushroom": true, "closed_eyeblossom": true, "cornflower": true,
	"dandelion": true, "golden_dandelion": true, "lily_of_the_valley": true,
	"open_eyeblossom": true, "orange_tulip": true, "oxeye_daisy": true,
	"pink_tulip": true, "poppy": true, "red_mushroom": true,
	"red_tulip": true, "torchflower": true, "white_tulip": true,
	"wither_rose": true,
}

var enderCarryNetherPlants = map[string]bool{
	"crimson_fungus": true, "warped_fungus": true,
	"crimson_roots": true, "warped_roots": true,
}

var enderCarrySoil = map[string]bool{
	"grass_block": true, "dirt": true, "podzol": true, "coarse_dirt": true,
	"rooted_dirt": true, "moss_block": true, "mud": true,
	"muddy_mangrove_roots": true, "farmland": true, "mycelium": true,
	"dirt_path": true, "pale_moss_block": true,
}

// enderCarrySurvives is carried.canSurvive against the support cell —
// nether plants want nylium or soul soil, cactus wants sand, the
// plants want soil, and the terrain blocks always pass.
func enderCarrySurvives(carried, below string) bool {
	switch carried {
	case "cactus", "cactus_flower":
		return below == "sand" || below == "red_sand" || below == "cactus"
	}
	if enderCarryNetherPlants[carried] {
		return below == "crimson_nylium" || below == "warped_nylium" || below == "soul_soil"
	}
	if enderCarryPlants[carried] {
		return enderCarrySoil[below]
	}
	return true
}

// collisionFullBlockAt is belowState.isCollisionShapeFullBlock: no
// table means the solid's whole cube collides; a table only passes
// when its single box is the unit cell.
func collisionFullBlockAt(w *world.World, pos world.Pos) bool {
	b := w.GetBlock(pos)
	if b == world.Air {
		return false
	}
	if boxes, has := collisionBoxesOf(b); has {
		return len(boxes) == 1 &&
			boxes[0][0] == 0 && boxes[0][1] == 0 && boxes[0][2] == 0 &&
			boxes[0][3] == 1 && boxes[0][4] == 1 && boxes[0][5] == 1
	}
	return !world.NonSolid(b) && !moveNoCollision(b)
}

// enderCellOccupied is the placement's AABB check — any other
// entity's box (mobs or players) intersecting the unit cell blocks
// the leave.
func (s *Server) enderCellOccupied(e *Entity, pos world.Pos) bool {
	x0, y0, z0 := float64(pos.X), float64(pos.Y), float64(pos.Z)
	inside := func(ex, ey, ez, half, h float64) bool {
		return ex+half > x0 && ex-half < x0+1 &&
			ey+h > y0 && ey < y0+1 &&
			ez+half > z0 && ez-half < z0+1
	}
	for _, o := range s.entities.All() {
		if o.ID == e.ID || o.Dim != e.Dim || o.Dying {
			continue
		}
		dims := entityBBDims(&o)
		if inside(o.X, o.Y, o.Z, dims[0]/2, dims[1]) {
			return true
		}
	}
	occupied := false
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dimName() != e.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if inside(px, py, pz, 0.3, 1.8) {
			occupied = true
			return false
		}
		return true
	})
	return occupied
}

// enderCarryStep runs both carry goals for the tick — take and leave
// are ordinary goals, so the step never gates movement.
func (s *Server) enderCarryStep(e *Entity) {
	if !s.gameRuleBool("minecraft:mob_griefing") {
		return
	}
	d := s.dims[e.Dim]
	if d == nil {
		return
	}
	if e.EnderCarryName == "" {
		s.enderTakeBlock(e, d)
	} else {
		s.enderLeaveBlock(e, d)
	}
}

// enderTakeBlock is EndermanTakeBlockGoal: a random cell in the
// x±2/y+0..2/z±2 box whose state is ENDERMAN_HOLDABLE and reachable —
// vanilla clips from the mob's own cell centre at the TARGET's height
// to the cell centre and requires that first hit to be the target.
func (s *Server) enderTakeBlock(e *Entity, d *dimension) {
	if rand.Float64() >= 0.05 {
		return
	}
	xt := int(math.Floor(e.X - 2.0 + rand.Float64()*4.0))
	yt := int(math.Floor(e.Y + rand.Float64()*3.0))
	zt := int(math.Floor(e.Z - 2.0 + rand.Float64()*4.0))
	pos := world.Pos{X: xt, Y: yt, Z: zt}
	name, ok := world.StateOwner(d.w.GetBlock(pos))
	if !ok || !enderHoldable[name] {
		return
	}
	bx, bz := int(math.Floor(e.X)), int(math.Floor(e.Z))
	hx, hy, hz, _, _, _, _, hit := s.clipSolidSegment(d,
		float64(bx)+0.5, float64(yt)+0.5, float64(bz)+0.5,
		float64(xt)+0.5, float64(yt)+0.5, float64(zt)+0.5)
	if hit && (hx != xt || hy != yt || hz != zt) {
		return
	}
	d.w.SetBlock(pos, world.Air)
	s.broadcastBlockState(d, pos, world.Air)
	// setCarriedBlock takes the default state — every property
	// the cell carried is stripped.
	e.EnderCarryName = name
	e.EnderCarryProps = nil
	e.MetaDirty = true
}

// enderLeaveBlock is EndermanLeaveBlockGoal: a random cell in the
// x±1/y+0..1/z±1 box — air over a full-collision non-bedrock support
// the carried block survives on, with no entity inside.
func (s *Server) enderLeaveBlock(e *Entity, d *dimension) {
	if rand.Float64() >= 0.0005 {
		return
	}
	xt := int(math.Floor(e.X - 1.0 + rand.Float64()*2.0))
	yt := int(math.Floor(e.Y + rand.Float64()*2.0))
	zt := int(math.Floor(e.Z - 1.0 + rand.Float64()*2.0))
	pos := world.Pos{X: xt, Y: yt, Z: zt}
	if d.w.GetBlock(pos) != world.Air {
		return
	}
	below := world.Pos{X: xt, Y: yt - 1, Z: zt}
	bn, _ := world.StateOwner(d.w.GetBlock(below))
	if d.w.GetBlock(below) == world.Air || bn == "bedrock" ||
		!collisionFullBlockAt(d.w, below) ||
		!enderCarrySurvives(e.EnderCarryName, bn) ||
		s.enderCellOccupied(e, pos) {
		return
	}
	if nb, ok := world.BlockStateOf(e.EnderCarryName, e.EnderCarryProps); ok {
		d.w.SetBlock(pos, nb)
		s.broadcastBlockState(d, pos, nb)
	}
	e.EnderCarryName = ""
	e.EnderCarryProps = nil
	e.MetaDirty = true
}

// enderCarryState is the DATA_CARRY_STATE wire value — the carried
// block's default-state id, or 0 (air) for the empty Optional.
func enderCarryState(e *Entity) int32 {
	mapping, _ := world.BlockMapping(776)
	wire, err := enderCarryStateFor(e, mapping)
	if err != nil {
		return 0
	}
	return wire
}

// The surrounding metadata schema is still776-specific. Empty is its Optional
// sentinel, not a canonical block handle and not a fallback for missing states.
func enderCarryStateFor(e *Entity, mapping *registry.BlockMapping) (int32, error) {
	if e.EnderCarryName == "" {
		return 0, nil
	}
	if state, ok := world.BlockStateOf(e.EnderCarryName, e.EnderCarryProps); ok {
		return mapping.Encode(state)
	}
	return 0, registry.ErrUnknownBlockState
}

// enderProjectileSource is DamageTypeTags.IS_PROJECTILE — a tagged
// source (or a thrown potion's direct entity) routes a hit into
// hurtServer's teleport-loop branch instead of the plain damage arm.
var enderProjectileSource = map[string]bool{
	"arrow":                 true,
	"trident":               true,
	"mob_projectile":        true,
	"unattributed_fireball": true,
	"fireball":              true,
	"wither_skull":          true,
	"thrown":                true,
	"wind_charge":           true,
}

// enderHurtServer is EnderMan.hurtServer: a projectile hit or a
// splash-potion contact rolls up to 64 teleports and lands no damage
// at all — except the water bottle's hurtWithCleanWater, applied
// BEFORE the teleport loop. Every other source damages normally,
// then rolls a 9/10 teleport when the source's getEntity() isn't a
// LivingEntity (drown, suffocation, fall, explosions...).
func (s *Server) enderHurtServer(e *Entity, amount float32, typeName string, killer *Session, info *damageInfo) (dealt float32, graced, died bool) {
	if e.Invulnerable {
		return 0, false, false
	}
	potion := info != nil && s.entityIsThrownPotion(info.direct)
	if !enderProjectileSource[typeName] && !potion {
		dealt, graced, died = s.mobHurtCore(e, amount, typeName, killer, info)
		if !s.causeIsLiving(killer, info) && rand.Intn(10) != 0 {
			s.enderTeleportRandom(e)
		}
		return dealt, graced, died
	}
	// hurtWithCleanWater runs BEFORE the teleport loop — only a
	// splash/lingering water bottle reaches super.hurtServer here.
	if potion && s.potionEntityIsWater(info.direct) {
		dealt, graced, died = s.mobHurtCore(e, amount, typeName, killer, info)
	}
	for i := 0; i < 64; i++ {
		if s.enderTeleportRandom(e) {
			return dealt, graced, died
		}
	}
	return dealt, graced, died
}

// causeIsLiving reports whether a damage source's getEntity() — the
// responsible entity — resolves to a LivingEntity: a spawned living
// mob, or the session the cause's entity id belongs to.
func (s *Server) causeIsLiving(killer *Session, info *damageInfo) bool {
	if killer != nil {
		return true
	}
	if info == nil || info.cause == 0 {
		return false
	}
	if e, ok := s.entities.Get(info.cause); ok && e.Living {
		return true
	}
	return s.sessionByEntityID(info.cause) != nil
}

// entityIsThrownPotion reports whether an entity id is a thrown
// splash/lingering potion — vanilla's AbstractThrownPotion check on
// the source's direct entity.
func (s *Server) entityIsThrownPotion(eid int32) bool {
	if e, ok := s.entities.Get(eid); ok {
		if n, ok := EntityTypeName(e.Type); ok {
			return n == "splash_potion" || n == "lingering_potion"
		}
	}
	return false
}

// potionEntityIsWater is thrownPotion.getItem() PotionContents.is(
// Potions.WATER) — the hurtWithCleanWater discriminator.
func (s *Server) potionEntityIsWater(eid int32) bool {
	if e, ok := s.entities.Get(eid); ok {
		return e.splashPotion == "water"
	}
	return false
}
