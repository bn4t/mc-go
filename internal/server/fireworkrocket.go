package server

// fireworkrocket.go — FireworkRocketItem + FireworkRocketEntity parity
// (measured on the vanilla 26.2 jar):
//   - use (right-click on air) fires only while the user is
//     fall-flying: leashes the player holds drop with LEAD_BREAK, an
//     attached rocket spawns at the player's position, the stack
//     consumes one (creative keeps it) and ITEM_USED awards.
//   - useOn (right-click on a block) is a PASS while gliding;
//     otherwise a free-flight rocket spawns at the click point pushed
//     0.15 toward the face and the stack shrinks one in EVERY gamemode
//     (vanilla's ItemStack.shrink, not the creative-safe consume).
//   - Rocket tick: life==0 fires the FIREWORK_ROCKET_LAUNCH ambient
//     sound once; an attached rocket copies its owner's position every
//     tick (the client's own rocket applies the acceleration); a free
//     rocket's horizontal delta scales ×1.15/tick with a +0.04 y-draft
//     (shot-at-angle rockets fly fixed); life > lifetime explodes.
//   - explode: entity-event 17, EXPLODE game event, damage = 5+2*n per
//     the explosions list, attached entity takes the full amount,
//     living entities within 5 blocks take
//     dmg·sqrt((5-dist)/5), then discard.

import (
	"math"
	"math/rand"
	"strconv"
	"strings"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// srcAmbient is SoundSource.AMBIENT — firework launch sounds ride it.
const srcAmbient int32 = 8

// rocketFlight reads the FIREWORKS component's flight_duration —
// "flight;exp;exp;..." — 0 when absent (vanilla adds it onto a base
// of 1: lifetime = 10·(1+flight) + rand6 + rand7).
func rocketFlight(st ItemStack) int {
	if st.Fireworks == "" {
		return 0
	}
	n, _ := strconv.Atoi(strings.SplitN(st.Fireworks, ";", 2)[0])
	return n
}

// rocketExplosionCount counts the entries in the FIREWORKS
// component's explosions list (the ";" separators).
func rocketExplosionCount(st ItemStack) int {
	return strings.Count(st.Fireworks, ";")
}

// rocketLifetime is FireworkRocketEntity's lifetime roll:
// 10·(1+flightDuration) + nextInt(6) + nextInt(7).
func rocketLifetime(st ItemStack) int32 {
	return int32(10*(1+rocketFlight(st)) + rand.Intn(6) + rand.Intn(7))
}

// encodeRocketMetadata builds the Set Entity Data body for a
// firework_rocket: index 8 slot = the fireworks item stack, index 9
// optional_unsigned_int = the attached entity id (VarInt eid+1, 0
// absent — elided while default like vanilla's non-default packing),
// index 10 boolean = shot-at-angle (likewise elided while false).
// Serializer ids follow the 26.2 registration order: 7 = ITEM_STACK,
// 19 = OPTIONAL_UNSIGNED_INT, 8 = BOOLEAN.
func encodeRocketMetadata(e *Entity) []byte {
	var b protocol.Buffer
	b.Byte(8)
	b.Byte(7) // ITEM_STACK
	st := e.Item
	if st.ID == 0 {
		// DATA_ID_FIREWORKS_ITEM defaults to the item's default stack.
		if id, ok := world.ItemID("firework_rocket"); ok {
			st = ItemStack{ID: id, Count: 1}
		}
	}
	writeSlot(&b, st)
	if e.AttachedTo != 0 {
		b.Byte(9)
		b.Byte(19) // OPTIONAL_UNSIGNED_INT
		b.VarInt(e.AttachedTo + 1)
	}
	if e.ShotAtAngle {
		b.Byte(10)
		b.Byte(8) // BOOLEAN
		b.Byte(1)
	}
	b.Byte(0xFF)
	return b.Bytes()
}

// spawnFireworkRocket mints a rocket entity: the vanilla ctor's tiny
// triangle-jitter initial delta, lifetime from the stack's flight
// duration, the held stack copied into entity data. attachedTo is
// the entity id a boost rocket rides (0 for free-flight spawns).
func (s *Server) spawnFireworkRocket(d *dimension, st ItemStack, x, y, z float64, attachedTo, owner int32) *Entity {
	const j = 0.002297
	e := s.entities.Spawn(&Entity{
		Type:         mustEntityType("firework_rocket"),
		Dim:          d.name,
		X:            x,
		Y:            y,
		Z:            z,
		MotionX:      (rand.Float64() - rand.Float64()) * j,
		MotionY:      0.05 + (rand.Float64()-rand.Float64())*j,
		MotionZ:      (rand.Float64() - rand.Float64()) * j,
		Item:         st,
		AttachedTo:   attachedTo,
		Fuse:         rocketLifetime(st),
		RocketLife:   0,
		arrowShooter: owner,
	})
	s.broadcastEntitySpawn(nil, *e)
	s.broadcastEntityMetadata(e.ID, encodeRocketMetadata(e))
	return e
}

// useFireworkBoost is FireworkRocketItem.use — reached only while the
// sender is fall-flying (the handler gates it): leashes drop with
// LEAD_BREAK, an attached rocket spawns at the player's position, the
// stack consumes one (creative keeps it), ITEM_USED awards.
func (s *Server) useFireworkBoost(sess *Session, st ItemStack, hand int32) {
	eid := sess.entityID.Load()
	x, y, z, _, _, _ := sess.Pos()
	d := sess.dimension()
	// Entity.dropAllLeashConnections(player): every leashable the
	// player holds drops — LEAD_BREAK plays only when one was held.
	s.dropPlayerLeashes(sess)
	s.spawnFireworkRocket(d, st, x, y, z, eid, eid)
	// ItemStack.consume(1, player): hasInfiniteMaterials keeps the
	// stack — only non-creative modes shrink.
	if sess.mode() != GameModeCreative {
		_, slot := sess.stackForHand(hand)
		s.consumeHandSlot(sess, slot, 1)
	}
	sess.addItemStat(statUsed, st.ID, 1)
}

// dropPlayerLeashes drops every leashable held by the player
// (LeashHolder == the player's entity id) — the player's side of
// Entity.dropAllLeashConnections.
func (s *Server) dropPlayerLeashes(sess *Session) bool {
	eid := sess.entityID.Load()
	var held []*Entity
	s.entities.mu.RLock()
	for _, l := range s.entities.byID {
		if l.LeashHolder == eid {
			held = append(held, l)
		}
	}
	s.entities.mu.RUnlock()
	for _, l := range held {
		s.leashDrop(l, true)
		// Entity.dropLeash's LEASH_BREAK plays on each dropped mob.
		s.playSoundEntity(l.Dim, l.ID, soundEventIDs["item.lead.break"], srcNeutral, 1.0, 1.0, nil)
	}
	return len(held) > 0
}

// consumeHandSlot shrinks a hand slot by n — vanilla's bare
// ItemStack.shrink (creative pays too); the creative-safe paths gate
// on the caller's mode instead.
func (s *Server) consumeHandSlot(sess *Session, slot int16, n int32) {
	sess.invMu.Lock()
	it := sess.inv[slot]
	it.Count -= n
	if it.Count <= 0 {
		it = ItemStack{}
	}
	sess.inv[slot] = it
	sess.invState.Add(1)
	sess.invMu.Unlock()
	_ = s.sendContainerSlot(sess, slot, it)
}

// useFireworkPlace is FireworkRocketItem.useOn: a gliding user gets
// vanilla's PASS (silent); otherwise a free rocket spawns at the
// click location pushed 0.15 toward the face and the stack shrinks
// one regardless of gamemode.
func (s *Server) useFireworkPlace(sess *Session, hand int32, x, y, z int32, dx, dy, dz int32, cursor [3]float64) bool {
	st, slot := sess.stackForHand(hand)
	if st.Empty() {
		return false
	}
	if un, ok := itemNameOf(st.ID); !ok || un != "firework_rocket" {
		return false
	}
	if sess.fallFlying {
		// useOn returns PASS while gliding, which lets the caller's
		// fallback run ItemStack.use — the boost fires all the same.
		s.useFireworkBoost(sess, st, hand)
		return true
	}
	px := float64(x) + cursor[0] + float64(dx)*0.15
	py := float64(y) + cursor[1] + float64(dy)*0.15
	pz := float64(z) + cursor[2] + float64(dz)*0.15
	s.spawnFireworkRocket(sess.dimension(), st, px, py, pz, 0, sess.entityID.Load())
	s.consumeHandSlot(sess, slot, 1)
	return true
}

// fireworkTick is FireworkRocketEntity.tick, replacing the generic
// projectile move. Vanilla's order, measured on the 26.2 jar:
//   - attached rockets ride the owner: while the owner is
//     fall-flying the owner's delta gains look·0.1 + (look·1.5−d)·0.5
//     and the rocket sits at the getHandHoldingItemAngle offset
//     (view(0, yaw±80°)·0.5); the rocket's delta copies the owner's.
//   - free rockets draft ×1.15 horizontal / +0.04 vertical unless
//     shot-at-angle; a latched horizontal collision drops the factor
//     to 1.0; the unclamped delta is restored after move.
//   - BOTH branches run ProjectileUtil.getHitResultOnMoveVector: the
//     move-vector segment clipped to the first block, then the
//     nearest entity box (inflated by computeMargin, ramping 0→0.3
//     over 22 ticks) along it. Entity hits explode unconditionally;
//     block hits explode only when the rocket has explosions.
//   - life > lifetime explodes.
func (s *Server) fireworkTick(e *Entity) {
	if e.RocketLife == 0 && !e.Silent {
		// FireworkRocketEntity.tick's first tick: level.playSound(null,
		// getX(), getY(), getZ(), …) — a positional AMBIENT sound at the
		// rocket itself, heard by everyone.
		s.playSoundSrc(e.Dim, e.X, e.Y, e.Z, soundEventIDs["entity.firework_rocket.launch"], srcAmbient, 3.0, 1.0, nil)
	}
	e.RocketLife++
	var hit rocketHit
	if e.AttachedTo != 0 {
		if attached := s.sessionByEntityID(e.AttachedTo); attached != nil {
			x, y, z, yaw, pitch, _ := attached.Pos()
			var ox, oy, oz float64
			if attached.fallFlying {
				yr := float64(yaw) * (math.Pi / 180)
				pr := float64(pitch) * (math.Pi / 180)
				lx := -math.Sin(yr) * math.Cos(pr)
				ly := -math.Sin(pr)
				lz := math.Cos(yr) * math.Cos(pr)
				d := attached.glideDelta
				attached.glideDelta = [3]float64{
					d[0] + lx*0.1 + (lx*1.5-d[0])*0.5,
					d[1] + ly*0.1 + (ly*1.5-d[1])*0.5,
					d[2] + lz*0.1 + (lz*1.5-d[2])*0.5,
				}
				ox, oy, oz = s.rocketHandOffset(attached)
			}
			e.X, e.Y, e.Z = x+ox, y+oy, z+oz
			if attached.fallFlying {
				d := attached.glideDelta
				e.MotionX, e.MotionY, e.MotionZ = d[0], d[1], d[2]
			} else {
				// A non-gliding owner has no stored delta.
				e.MotionX, e.MotionY, e.MotionZ = 0, 0, 0
			}
			s.broadcastEntityTeleport(nil, e.ID, e.X, e.Y, e.Z, e.Yaw, e.Pitch, false)
		}
		hit = s.rocketHitScan(e)
	} else {
		if !e.ShotAtAngle || e.rocketHColl {
			f := 1.15
			if e.rocketHColl {
				f = 1.0
			}
			e.MotionX *= f
			e.MotionZ *= f
			e.MotionY += 0.04
		}
		ox, oy, oz := e.X, e.Y, e.Z
		hit = s.rocketHitScan(e)
		d := s.dimensionByName(e.Dim)
		if d != nil {
			cx, cy, cz, hColl := s.sweepEntityBox(d.w, e.X, e.Y, e.Z, 0.125, 0.25, e.MotionX, e.MotionY, e.MotionZ)
			e.rocketHColl = hColl
			e.X, e.Y, e.Z = ox+cx, oy+cy, oz+cz
		} else {
			e.X += e.MotionX
			e.Y += e.MotionY
			e.Z += e.MotionZ
		}
		s.broadcastEntityTeleport(nil, e.ID, e.X, e.Y, e.Z, e.Yaw, e.Pitch, false)
	}
	if hit.sess != nil || hit.mob != nil {
		// onHitEntity: explode unconditionally — even starless
		// rockets pop on an entity.
		s.rocketExplode(e)
		return
	}
	if hit.block {
		if d := s.dimensionByName(e.Dim); d != nil {
			if name, ok := blockNameAt(d, world.Pos{X: hit.bx, Y: hit.by, Z: hit.bz}); ok && name == "target" {
				if shooter := s.sessionByEntityID(e.arrowShooter); shooter != nil {
					s.fireTargetHit(shooter, e, targetSignal(hit.axis, hit.hx, hit.hy, hit.hz))
				}
			}
		}
		// onHitBlock: only a rocket with explosions detonates on
		// impact; a starless rocket keeps pushing into the block
		// until its fuse ends (its delta is never stopped).
		if rocketExplosionCount(e.Item) > 0 {
			s.rocketExplode(e)
			return
		}
	}
	if e.RocketLife > e.Fuse {
		s.rocketExplode(e)
	}
}

// rocketHit describes one getHitResultOnMoveVector outcome.
type rocketHit struct {
	sess             *Session
	mob              *Entity
	block            bool
	bx, by, bz, axis int
	hx, hy, hz       float64
}

// rocketHitScan is ProjectileUtil.getHitResultOnMoveVector for the
// rocket: clip the pos→pos+delta segment against collider blocks, then
// find the nearest hittable entity box (inflated by computeMargin)
// along the clipped segment.
func (s *Server) rocketHitScan(e *Entity) (hit rocketHit) {
	d := s.dimensionByName(e.Dim)
	if d == nil {
		return
	}
	ex, ey, ez := e.X+e.MotionX, e.Y+e.MotionY, e.Z+e.MotionZ
	bx, by, bz, axis, hx, hy, hz, bhit := s.clipSolidSegment(d, e.X, e.Y, e.Z, ex, ey, ez)
	if bhit {
		ex, ey, ez = hx, hy, hz
		hit.block = true
		hit.bx, hit.by, hit.bz, hit.axis = bx, by, bz, axis
		hit.hx, hit.hy, hit.hz = hx, hy, hz
	}
	// computeMargin: the target inflation ramps 0→0.3 over the
	// projectile's first 22 ticks.
	margin := float64(e.RocketLife-2) / 20
	if margin > 0.3 {
		margin = 0.3
	}
	if margin < 0 {
		margin = 0
	}
	best := math.MaxFloat64
	var bSess *Session
	var bMob *Entity
	leftOwner := s.rocketOwnerLeftOwner(e)
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dead || sess.dimName() != e.Dim {
			return true
		}
		seid := sess.entityID.Load()
		if seid == e.ID {
			return true
		}
		if seid == e.arrowShooter && !leftOwner {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		dist := segmentBoxT(e.X, e.Y, e.Z, ex, ey, ez,
			px-0.3-margin, py-margin, pz-0.3-margin,
			px+0.3+margin, py+1.8+margin, pz+0.3+margin)
		if dist >= 0 && dist < best {
			best, bSess, bMob = dist, sess, nil
		}
		return true
	})
	s.entities.mu.RLock()
	for _, m := range s.entities.byID {
		if !m.Living || m.Dying || m.Dim != e.Dim || m.ID == e.ID {
			continue
		}
		if m.ID == e.arrowShooter && !leftOwner {
			continue
		}
		b := entityBBDims(m)
		dist := segmentBoxT(e.X, e.Y, e.Z, ex, ey, ez,
			m.X-b[0]/2-margin, m.Y-margin, m.Z-b[0]/2-margin,
			m.X+b[0]/2+margin, m.Y+b[1]+margin, m.Z+b[0]/2+margin)
		if dist >= 0 && dist < best {
			best, bSess, bMob = dist, nil, m
		}
	}
	s.entities.mu.RUnlock()
	hit.sess, hit.mob = bSess, bMob
	return hit
}

// rocketOwnerLeftOwner is Projectile.isOutsideOwnerCollisionRange for
// the rocket: the owner can be hit only once the rocket's box,
// expanded along its move vector and inflated 1 block, no longer
// overlaps the owner's box. While attached the rocket rides inside
// the owner so it never hits them.
func (s *Server) rocketOwnerLeftOwner(e *Entity) bool {
	if e.arrowShooter == 0 {
		return true
	}
	lo := func(v, d float64) float64 {
		if d < 0 {
			return v + d
		}
		return v
	}
	hi := func(v, d float64) float64 {
		if d > 0 {
			return v + d
		}
		return v
	}
	sx1 := lo(e.X-0.125, e.MotionX) - 1
	sy1 := lo(e.Y, e.MotionY) - 1
	sz1 := lo(e.Z-0.125, e.MotionZ) - 1
	sx2 := hi(e.X+0.125, e.MotionX) + 1
	sy2 := hi(e.Y+0.25, e.MotionY) + 1
	sz2 := hi(e.Z+0.125, e.MotionZ) + 1
	ox, oy, oz, hw, h := float64(0), float64(0), float64(0), 0.0, 0.0
	found := false
	if sess := s.sessionByEntityID(e.arrowShooter); sess != nil {
		ox, oy, oz, _, _, _ = sess.Pos()
		hw, h = 0.3, 1.8
		found = true
	} else {
		s.entities.mu.RLock()
		if m, ok := s.entities.byID[e.arrowShooter]; ok {
			b := entityBBDims(m)
			ox, oy, oz = m.X, m.Y, m.Z
			hw, h = b[0]/2, b[1]
			found = true
		}
		s.entities.mu.RUnlock()
	}
	if !found {
		return true
	}
	return sx1 >= ox+hw || sx2 <= ox-hw || sz1 >= oz+hw || sz2 <= oz-hw || sy1 >= oy+h || sy2 <= oy
}

// rocketHandOffset is Entity.getHandHoldingItemAngle: the point 0.5
// out the holding arm, view(0°, yaw±80°)·0.5 — +80° for the main arm
// (mc-go doesn't parse client_information's main_arm; RIGHT is the
// default) and −80° when only the offhand still holds a rocket.
func (s *Server) rocketHandOffset(sess *Session) (ox, oy, oz float64) {
	_, _, _, yaw, _, _ := sess.Pos()
	main, _ := sess.stackForHand(0)
	off, _ := sess.stackForHand(1)
	mainName, _ := itemNameOf(main.ID)
	offName, _ := itemNameOf(off.ID)
	ang := 80.0
	if offName == "firework_rocket" && mainName != "firework_rocket" {
		ang = -80.0
	}
	yr := (float64(yaw) + ang) * (math.Pi / 180)
	return -math.Sin(yr) * 0.5, 0, math.Cos(yr) * 0.5
}

// clipSolidSegment walks a segment and returns the first solid cell's
// entry position — vanilla's clip(..., COLLIDER, NONE) clipped against
// the cell's collision boxes rather than a unit cube, so a ray clears
// the empty half of a slab, snow's missing layers, an open fence gate,
// or the cavity inside a cauldron.
func (s *Server) clipSolidSegment(d *dimension, ox, oy, oz, ex, ey, ez float64) (bx, by, bz, axis int, hx, hy, hz float64, ok bool) {
	dx, dy, dz := ex-ox, ey-oy, ez-oz
	length := math.Sqrt(dx*dx + dy*dy + dz*dz)
	if length == 0 {
		return
	}
	nx, ny, nz := dx/length, dy/length, dz/length
	steps := int(length/0.05) + 1
	prev := world.Pos{X: int(math.Floor(ox)), Y: int(math.Floor(oy)), Z: int(math.Floor(oz))}
	// The origin cell itself can contain the ray's start inside a
	// collision box — a clip at distance 0 (an eye already touching
	// the plate sees nothing past it).
	if ent, _ := s.clipCellEntry(d, prev, ox, oy, oz, ex, ey, ez, length); ent >= 0 {
		bx, by, bz = prev.X, prev.Y, prev.Z
		hx, hy, hz = ox, oy, oz
		ok = true
		return
	}
	for i := 1; i <= steps; i++ {
		t := length * float64(i) / float64(steps)
		px, py, pz := ox+nx*t, oy+ny*t, oz+nz*t
		p := world.Pos{X: int(math.Floor(px)), Y: int(math.Floor(py)), Z: int(math.Floor(pz))}
		if p == prev {
			continue
		}
		prev = p
		ent, hb := s.clipCellEntry(d, p, ox, oy, oz, ex, ey, ez, length)
		if ent < 0 {
			continue
		}
		bx, by, bz = p.X, p.Y, p.Z
		hx, hy, hz = ox+nx*ent, oy+ny*ent, oz+nz*ent
		switch {
		case math.Abs(hx-float64(bx)-hb[0]) < 1e-6 || math.Abs(hx-float64(bx)-hb[3]) < 1e-6:
			axis = 0
		case math.Abs(hy-float64(by)-hb[1]) < 1e-6 || math.Abs(hy-float64(by)-hb[4]) < 1e-6:
			axis = 1
		default:
			axis = 2
		}
		ok = true
		return
	}
	return
}

// clipCellEntry returns the earliest entry distance at which the
// segment touches one of cell p's collision boxes — <0 when the cell
// has no collision (air, powder_snow, an open gate) or every box
// misses. A segment starting inside a box reports 0.
func (s *Server) clipCellEntry(d *dimension, p world.Pos, ox, oy, oz, ex, ey, ez, length float64) (float64, cellBox) {
	if _, has := blockNameAt(d, p); !has {
		return -1, cellBox{}
	}
	blk := d.w.GetBlock(p)
	boxes, has := collisionBoxesOf(blk)
	switch {
	case has && len(boxes) == 0:
		return -1, cellBox{} // empty collision: powder_snow, an open gate
	case has:
	case world.NonSolid(blk):
		return -1, cellBox{} // no table entry and no collision at all
	default:
		boxes = []cellBox{{0, 0, 0, 1, 1, 1}}
	}
	ent := -1.0
	var hb cellBox
	for _, b := range boxes {
		mnx, mny, mnz := float64(p.X)+b[0], float64(p.Y)+b[1], float64(p.Z)+b[2]
		mxx, mxy, mxz := float64(p.X)+b[3], float64(p.Y)+b[4], float64(p.Z)+b[5]
		e := segmentBoxT(ox, oy, oz, ex, ey, ez, mnx, mny, mnz, mxx, mxy, mxz)
		if e < 0 && ox >= mnx && ox <= mxx && oy >= mny && oy <= mxy && oz >= mnz && oz <= mxz {
			e = 0 // a ray starting inside the box clips at the origin
		}
		if e >= 0 && e <= length && (ent < 0 || e < ent) {
			ent, hb = e, b
		}
	}
	return ent, hb
}

// segmentBoxT clips a segment against an AABB, returning the entry
// distance along the segment or -1 on a miss — vanilla's
// AABB.clip used by ProjectileUtil's entity scan.
func segmentBoxT(sx, sy, sz, ex, ey, ez, minx, miny, minz, maxx, maxy, maxz float64) float64 {
	dx, dy, dz := ex-sx, ey-sy, ez-sz
	t0, t1 := 0.0, 1.0
	axes := [][2]float64{{sx, dx}, {sy, dy}, {sz, dz}}
	mins := [3]float64{minx, miny, minz}
	maxs := [3]float64{maxx, maxy, maxz}
	for i, a := range axes {
		if a[1] == 0 {
			if a[0] < mins[i] || a[0] > maxs[i] {
				return -1
			}
			continue
		}
		ta := (mins[i] - a[0]) / a[1]
		tb := (maxs[i] - a[0]) / a[1]
		if ta > tb {
			ta, tb = tb, ta
		}
		if ta > t0 {
			t0 = ta
		}
		if tb < t1 {
			t1 = tb
		}
		if t0 > t1 {
			return -1
		}
	}
	return t0 * math.Sqrt(dx*dx+dy*dy+dz*dz)
}

// rocketExplode is FireworkRocketEntity.explode + dealExplosionDamage:
// entity-event 17 and discard always; with explosions the attached
// entity takes 5+2·n and living entities within 5 blocks take the
// sqrt falloff — gated by vanilla's two-ray LOS check (rocket →
// victim feet and rocket → victim mid, either clear counts).
func (s *Server) rocketExplode(e *Entity) {
	s.broadcastEntityEventTo(nil, e.Dim, e.ID, 17)
	n := rocketExplosionCount(e.Item)
	if n > 0 {
		dmg := float32(5 + n*2)
		src := damageSource{typeName: "fireworks", cause: e.arrowShooter, direct: e.ID}
		d := s.dimensionByName(e.Dim)
		los := func(tx, ty, tz float64) bool {
			if d == nil {
				return true
			}
			_, _, _, _, _, _, _, hit := s.clipSolidSegment(d, e.X, e.Y, e.Z, tx, ty, tz)
			return !hit
		}
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() != StatePlay || sess.dimName() != e.Dim {
				return true
			}
			seid := sess.entityID.Load()
			px, py, pz, _, _, _ := sess.Pos()
			if seid == e.AttachedTo {
				// The attached entity takes the flat full damage.
				s.damageWith(sess, dmg, s.fireworksDeathMsg(sess, e), src)
				return true
			}
			dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
			if dx*dx+dy*dy+dz*dz > 25 {
				return true
			}
			height := 1.8
			if sess.fallFlying {
				height = 0.6
			}
			if !los(px, py, pz) && !los(px, py+height*0.5, pz) {
				return true
			}
			f := dmg * float32(math.Sqrt((5.0-math.Sqrt(dx*dx+dy*dy+dz*dz))/5.0))
			s.damageWith(sess, f, s.fireworksDeathMsg(sess, e), src)
			return true
		})
		s.entities.mu.RLock()
		mobs := make([]*Entity, 0, len(s.entities.byID))
		for _, m := range s.entities.byID {
			mobs = append(mobs, m)
		}
		s.entities.mu.RUnlock()
		for _, m := range mobs {
			if !m.Living || m.Dim != e.Dim || m.ID == e.AttachedTo {
				continue
			}
			dx, dy, dz := m.X-e.X, m.Y-e.Y, m.Z-e.Z
			d2 := dx*dx + dy*dy + dz*dz
			if d2 > 25 {
				continue
			}
			if !los(m.X, m.Y, m.Z) && !los(m.X, m.Y+entityBBDims(m)[1]*0.5, m.Z) {
				continue
			}
			f := dmg * float32(math.Sqrt((5.0-math.Sqrt(d2))/5.0))
			var killer *Session
			if e.arrowShooter != 0 {
				killer = s.sessionByEntityID(e.arrowShooter)
			}
			s.damageMob(m, f, "fireworks", killer, &damageInfo{
				typeName: "fireworks", cause: e.arrowShooter, direct: e.ID,
			})
		}
	}
	s.entities.Remove(e.ID)
	s.broadcastEntityRemove([]int32{e.ID})
}

// fireworksDeathMsg picks the fireworks death message: "went off
// with a bang" alone, or "while fighting" when the rocket's owner
// is another player (the named-item .item variant is a GOAL gap).
func (s *Server) fireworksDeathMsg(victim *Session, e *Entity) component {
	if owner := s.sessionByEntityID(e.arrowShooter); owner != nil && owner != victim {
		return translatable("death.attack.fireworks.player",
			playerChatComponent(victim), playerChatComponent(owner))
	}
	return translatable("death.attack.fireworks", playerChatComponent(victim))
}

// applyRocketSummonNBT reads the rocket's /summon keys — Life,
// LifeTime, ShotAtAngle, FireworksItem ({id,count,components}). Only
// the fireworks component parses inside the item compound.
func applyRocketSummonNBT(e *Entity, tag *snbtTag) {
	if v, ok := tag.vals["Life"]; ok && v.kind == snbtNumber {
		e.RocketLife = int32(snbtNum(v))
	}
	if v, ok := tag.vals["LifeTime"]; ok && v.kind == snbtNumber {
		e.Fuse = int32(snbtNum(v))
	}
	if v, ok := tag.vals["ShotAtAngle"]; ok && (v.num == "1b" || v.num == "1" || v.str == "true") {
		e.ShotAtAngle = true
	}
	if v, ok := tag.vals["FireworksItem"]; ok && v.kind == snbtCompound {
		e.Item = snbtItemCost(v)
		if e.Item.ID == 0 {
			if id, ok := world.ItemID("firework_rocket"); ok {
				e.Item.ID = id
			}
		}
		if comps, ok := v.vals["components"]; ok && comps.kind == snbtCompound {
			for key, cv := range comps.vals {
				short := strings.TrimPrefix(key, "minecraft:")
				if short != "fireworks" || cv.kind != snbtCompound {
					continue
				}
				fv, ok := cv.vals["flight_duration"]
				if !ok || fv.kind != snbtNumber {
					continue
				}
				var b strings.Builder
				b.WriteString(strconv.Itoa(int(snbtNum(fv))))
				if ev, ok := cv.vals["explosions"]; ok && ev.kind == snbtList {
					for _, it := range ev.items {
						exp, ok2 := parseFireworkExplosionSNBT(it)
						if !ok2 {
							continue
						}
						b.WriteString(";" + exp)
					}
				}
				e.Item.Fireworks = b.String()
			}
		}
	}
}
