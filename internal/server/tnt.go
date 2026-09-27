package server

import (
	"math"
	"math/rand"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// tntFuseTicks is PrimedTnt's default fuse — the constructor sets
// DATA_FUSE_ID to 80, and TntBlock.prime/wasExploded start from it.
const tntFuseTicks = 80

// primedTntExplosionY is the vertical offset vanilla's PrimedTnt.explode
// applies to its position before handing it to level.explode.
const primedTntExplosionY = 0.0625

// primedTntPower is explosionPower — vanilla fixes it at 4.0.
const primedTntPower float32 = 4.0

// primeTntAt is TntBlock.prime + TntBlock.wasExploded's shared tail:
// spawn a PrimedTnt entity at the position with `fuse` ticks and the
// constructor's random horizontal nudge, announce it with the
// TNT_PRIMED block sound. tnt_explodes=false means no entity.
func (s *Server) primeTntAt(d *dimension, x, y, z float64, fuse int32) *Entity {
	if !s.gameRuleBool("minecraft:tnt_explodes") {
		return nil
	}
	// PrimedTnt's constructor nudges horizontally by
	// (-sin θ · 0.02, 0.2, -cos θ · 0.02), θ uniform over [0,2π).
	theta := rand.Float64() * 2 * math.Pi
	e := s.entities.Spawn(&Entity{
		Type:    mustEntityType("tnt"),
		Dim:     d.name,
		X:       x,
		Y:       y,
		Z:       z,
		MotionX: -math.Sin(theta) * 0.02,
		MotionY: 0.2,
		MotionZ: -math.Cos(theta) * 0.02,
		Fuse:    fuse,
	})
	s.broadcastEntitySpawn(nil, *e)
	s.broadcastEntityMetadata(e.ID, encodePrimedTntMetadata(fuse))
	// TNT_PRIMED goes out at SoundSource.BLOCKS (4), volume 1, pitch 1.
	s.playSoundSrc(d.name, x, y, z, 1623, 4, 1, 1, nil)
	// TntBlock.explode posts PRIME_FUSE at the primed entity's spot.
	s.emitGameEvent(d, "prime_fuse", x, y, z, nil, "")
	return e
}

// primeTntBlock is TntBlock.prime for a cell: the entity spawns at
// (x+0.5, y, z+0.5) — not centred vertically.
func (s *Server) primeTntBlock(d *dimension, pos world.Pos, fuse int32) *Entity {
	return s.primeTntAt(d, float64(pos.X)+0.5, float64(pos.Y), float64(pos.Z)+0.5, fuse)
}

// encodePrimedTntMetadata is the PrimedTnt SynchedEntityData pack:
// fuse at index 8 (INT), block_state at index 9 (BLOCK_STATE — the
// default TNT state id). Vanilla sends both on spawn; the chain-primed
// short fuse only reaches the client through this entry.
func encodePrimedTntMetadata(fuse int32) []byte {
	var b protocol.Buffer
	b.Byte(8)
	b.Byte(1) // INT serializer
	b.VarInt(fuse)
	b.Byte(9)
	b.Byte(14) // BLOCK_STATE serializer
	st, _ := world.BlockStateOf("tnt", nil)
	b.VarInt(int32(st))
	b.Byte(0xFF)
	return b.Bytes()
}

// tntShortFuse is wasExploded's getRandomShortFuse:
// nextInt(max(1, fuse/4)) + fuse/8 → 10..29 for the default 80.
func tntShortFuse(fuse int32) int32 {
	return int32(rand.Intn(int(max(fuse/4, 1)))) + fuse/8
}

// tntTickStep is PrimedTnt.tick: applyGravity, move, air drag, the
// grounded bounce, then the fuse countdown that ends in explode.
func (s *Server) tntTickStep() {
	tntType := mustEntityType("tnt")
	s.entities.mu.RLock()
	var ents []*Entity
	for _, e := range s.entities.byID {
		if e.Type == tntType && !e.Dying {
			ents = append(ents, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range ents {
		s.tntEntityTick(e)
	}
}

func (s *Server) tntEntityTick(e *Entity) {
	w := s.dimWorld(e.Dim)
	if w == nil {
		return
	}
	// applyGravity: PrimedTnt's getGravity is 0.04.
	e.MotionY -= 0.04
	nx, ny, nz := e.X+e.MotionX, e.Y+e.MotionY, e.Z+e.MotionZ
	// move() with SELF pusher: the 0.98-wide box lands when its bottom
	// would sink into a solid cell. Horizontal drift is tiny (0.02 max)
	// so only the down axis is resolved.
	onGround := false
	if e.MotionY < 0 {
		floorY := math.Floor(ny)
		feet := world.Pos{X: int(math.Floor(nx)), Y: int(floorY) - 1, Z: int(math.Floor(nz))}
		if ny < math.Floor(e.Y) && !world.NonSolid(w.GetBlock(feet)) {
			ny = math.Floor(e.Y)
			onGround = true
		}
	}
	// getAirDrag — 0.98 always (no water path modeled).
	e.MotionX *= 0.98
	e.MotionY *= 0.98
	e.MotionZ *= 0.98
	if onGround {
		e.MotionX *= 0.7
		e.MotionY *= -0.5
		e.MotionZ *= 0.7
	}
	s.entities.Update(e.ID, nx, ny, nz, e.Yaw, e.Pitch, onGround)
	e.Fuse--
	if e.Fuse <= 0 {
		s.entities.Remove(e.ID)
		s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
		s.tntExplode(e, e.X, e.Y+primedTntExplosionY, e.Z)
	}
}

// tntExplode wraps explodePower at TNT's power — 4 by default, the
// summon tag's explosion_power when one was given. Vanilla's TNT
// interaction reads tnt_explodes for its destroy type.
func (s *Server) tntExplode(src *Entity, x, y, z float64) {
	power := primedTntPower
	if src != nil && src.BlastPower > 0 {
		power = float32(src.BlastPower)
	}
	s.explodePower(src, x, y, z, power, s.gameRuleBool("minecraft:tnt_explodes"))
}

// explodePower is ServerLevel.explode for a MOB interaction: the block
// sampler runs with the real per-block/fluid explosion resistance,
// hurtEntities damages and knocks back entities in the 2·radius box,
// interactWithBlocks runs onExplosionHit per sampled cell
// (DESTROY_WITH_DECAY semantics — 1/radius drop decay, TNT cells
// chain-prime), and one explode packet per in-range player carries
// that player's knockback inline.
// grief is the interaction's destroy-type gate — mob_griefing for a
// mob blast, tnt_explodes for TNT. When false nothing carves and the
// packet's block count is 0 (KEEP).
func (s *Server) explodePower(src *Entity, x, y, z float64, radius float32, grief bool) {
	s.explodePowerFire(src, x, y, z, radius, grief, false)
}

// explodePowerFire is explodePower with vanilla's setFire arg: the
// LargeFireball onHit blast passes MOB_GRIEFING there, letting the
// carve light 1-in-3 air-over-solid cells on fire in the finalize
// pass (finalizedExplosion's second loop).
func (s *Server) explodePowerFire(src *Entity, x, y, z float64, radius float32, grief, setFire bool) {
	d := s.dims[src.Dim]
	if d == nil {
		return
	}
	// ServerExplosion.explode posts EXPLODE at the blast centre.
	s.emitGameEvent(d, "explode", x, y, z, nil, "")
	var positions []world.Pos
	if grief {
		positions = s.explodeBlockPositionsRes(src.Dim, x, y, z, radius, explosionResistFor)
	}
	blockCount := len(positions)

	// hurtEntities: every living entity inside 2r takes
	// ((impact²+impact)/2 · 7 · 2r + 1) damage where
	// impact = (1-dist/2r)·seenPercent (seen is 1 in the flat test
	// world — getSeenPercent multiplies both damage and push), plus
	// the normalized eye-direction push scaled by the same factor —
	// knockback multiplier 1.0, no upward bias on 26.2.
	doubleR := 2 * float64(radius)
	hitPlayers := map[int32][3]float64{}
	var pushedMobs []struct {
		eid int32
		kb  [3]float64
	}
	s.entities.mu.RLock()
	var mobs []*Entity
	for _, e := range s.entities.byID {
		if e.Living && !e.Dying && e.Dim == src.Dim && e.ID != src.ID {
			mobs = append(mobs, e)
		}
	}
	s.entities.mu.RUnlock()
	push := func(eid int32, ex, ey, ez, eyeY float64, kbResist float64) (float64, [3]float64, bool) {
		dist := math.Sqrt((ex-x)*(ex-x)+(ey-y)*(ey-y)+(ez-z)*(ez-z)) / doubleR
		if dist > 1 {
			return 0, [3]float64{}, false
		}
		dx, dy, dz := ex-x, eyeY-y, ez-z
		n := math.Sqrt(dx*dx + dy*dy + dz*dz)
		if n == 0 {
			dy, n = 1, 1
		}
		impact := 1 - dist
		power := impact * (1 - kbResist)
		vx, vy, vz := dx/n*power, dy/n*power, dz/n*power
		s.entities.SetMotion(eid, vx, vy, vz)
		return impact, [3]float64{vx, vy, vz}, true
	}
	for _, e := range mobs {
		eyeY := e.Y + mobBounds(e.Type)[1]*0.85
		impact, kb, ok := push(e.ID, e.X, e.Y, e.Z, eyeY, mobExplosionKBResist(e.Type))
		if !ok {
			continue
		}
		dmg := float32((impact*impact+impact)/2*7*doubleR + 1)
		res := s.entities.Damage(e.ID, dmg, "explosion", 0)
		s.mobHurtWireFX(e, res)
		if res.Health <= 0 {
			// hurtServer's lethal arm: die() (silent on a graced
			// hit), then the killer's killedEntity — a powered
			// creeper banks the victim's head.
			s.mobDie(e, nil, res.Graced)
			s.chargedCreeperSkull(src, e)
		}
		pushedMobs = append(pushedMobs, struct {
			eid int32
			kb  [3]float64
		}{e.ID, kb})
	}
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay || sess.dimName() != src.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		impact, kb, ok := push(sess.entityID.Load(), px, py, pz, py+1.62, 0)
		if !ok {
			return true
		}
		sess.explodeImpact = &[3]float64{px, py, pz}
		sess.explodeCause = src.Type
		if sess.mode() != GameModeSpectator && !(sess.mode() == GameModeCreative && sess.flying) {
			hitPlayers[sess.entityID.Load()] = kb
			dmg := float32((impact*impact+impact)/2*7*doubleR + 1)
			s.damageWith(sess, dmg, translatable("death.attack.explosion",
				playerChatComponent(sess)), damageSource{typeName: "explosion", cause: src.ID})
		}
		return true
	})

	// interactWithBlocks, DESTROY_WITH_DECAY — the vanilla order is
	// shuffle → per-cell onExplosionHit → popResource per dropped stack.
	s.explodeDestroyBlocks(src, positions, radius)

	// finalizeExplosion's fire pass (setFire): for every destroyed
	// cell a 1-in-3 roll puts base fire where the cell is now air
	// and the block below renders solid.
	if setFire {
		for _, pos := range positions {
			if rand.Intn(3) != 0 {
				continue
			}
			cur := d.w.GetBlock(pos)
			if cur != world.Air {
				continue
			}
			below := pos
			below.Y--
			if bn, ok := blockNameAt(d, below); ok && bn != "air" {
				s.setNaturalBlock(d, pos, plainFire)
			}
		}
	}

	// One explode packet per player within 64 blocks; the victim's own
	// packet carries the knockback vector, everyone else's omits it.
	// isSmall picks the single puff below radius 2, emitter at/above.
	particleID := int32(30) // explosion
	if radius >= 2.0 {
		particleID = 29 // explosion_emitter
	}
	s.sessions.Range(func(_, v any) bool {
		sess, ok := v.(*Session)
		if !ok || sess.state.Load() != StatePlay || sess.dimName() != src.Dim {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if (px-x)*(px-x)+(py-y)*(py-y)+(pz-z)*(pz-z) >= 4096 {
			return true
		}
		var b protocol.Buffer
		b.Double(x)
		b.Double(y)
		b.Double(z)
		b.Float(radius)
		b.Int(int32(blockCount))
		if kb, ok := hitPlayers[sess.entityID.Load()]; ok {
			b.Bool(true)
			b.Double(kb[0])
			b.Double(kb[1])
			b.Double(kb[2])
		} else {
			b.Bool(false)
		}
		b.VarInt(particleID)
		b.VarInt(699 + 1) // entity.generic.explode holder
		b.VarInt(0)       // empty block-particles weighted list
		_ = sess.write(sess.ids().PlayExplode, b.Bytes())
		return true
	})
	sendMotion := func(eid int32, kb [3]float64, skipEID int32) {
		body := encodeEntityMotion(eid, kb[0], kb[1], kb[2])
		s.sessions.Range(func(_, v any) bool {
			sess := v.(*Session)
			if sess.state.Load() != StatePlay || sess.dimName() != src.Dim || sess.entityID.Load() == skipEID {
				return true
			}
			_ = sess.write(sess.ids().PlaySetEntityMotion, body)
			return true
		})
	}
	for _, pm := range pushedMobs {
		sendMotion(pm.eid, pm.kb, -1)
	}
	for eid, kb := range hitPlayers {
		sendMotion(eid, kb, eid)
	}
}

// explodeDestroyBlocks is ServerExplosion.interactWithBlocks for a
// DESTROY_WITH_DECAY explosion: positions are shuffled, each non-air
// cell's onExplosionHit drops loot through the table with
// EXPLOSION_RADIUS set (survives_explosion → 1/radius), writes air
// with flag 3, and calls wasExploded — TNT cells chain-prime with the
// short fuse.
func (s *Server) explodeDestroyBlocks(src *Entity, positions []world.Pos, radius float32) {
	d := s.dims[src.Dim]
	if d == nil {
		return
	}
	for i := len(positions) - 1; i > 0; i-- {
		j := rand.Intn(i + 1)
		positions[i], positions[j] = positions[j], positions[i]
	}
	var drops []struct {
		pos   world.Pos
		stack ItemStack
	}
	var chains []world.Pos
	for _, pos := range positions {
		cur := d.w.GetBlock(pos)
		name, ok := world.StateOwner(cur)
		if !ok || cur == world.Air {
			continue
		}
		// dropFromExplosion — TNT overrides it false; the cell clears
		// and chains instead of rolling its loot table.
		isTnt := name == "tnt"
		if !isTnt {
			for _, st := range rollExplosionLoot("blocks/"+name, cur, pos, d, radius) {
				// addOrAppendStack: the incoming stack pours into the
				// first collectors that still merge (each capped at
				// 16 via ItemEntity.merge); only overflow founds a new
				// collector at this cell.
				for i := range drops {
					dr := &drops[i]
					if !dr.stack.Stackable(st) || dr.stack.Count >= 16 {
						continue
					}
					move := 16 - dr.stack.Count
					if move > st.Count {
						move = st.Count
					}
					dr.stack.Count += move
					st.Count -= move
					if st.Count <= 0 {
						break
					}
				}
				if st.Count > 0 {
					drops = append(drops, struct {
						pos   world.Pos
						stack ItemStack
					}{pos, st})
				}
			}
		}
		s.setNaturalBlock(d, pos, world.Air)
		if isBeeNestBlock(cur) {
			// BeehiveBlock.onExplosionHit tail: the 8x6x8 box sweep
			// runs unconditionally. The occupants themselves vanish
			// with the block entity — BeehiveBlockEntity overrides
			// no preRemoveSideEffects and onExplosionHit never calls
			// emptyAllLivingFromHive — so only free bees in the box
			// get a target.
			s.angerNearbyBees(d, pos)
		}
		if isTnt {
			chains = append(chains, pos)
		}
	}
	// popResource per collected stack — item entities spawn after their
	// block's air write, matching the StackCollector tail.
	for _, dr := range drops {
		s.spawnItemDrop(d.name, dr.pos, dr.stack)
	}
	for _, pos := range chains {
		s.primeTntBlock(d, pos, tntShortFuse(tntFuseTicks))
	}
}

// explosionResistFor is ExplosionDamageCalculator.getBlockExplosionResistance:
// absent when the cell is air holding no fluid, otherwise the max of
// the block's and the fluid's resistance (water/lava carry 100).
func explosionResistFor(w *world.World, pos world.Pos) (float32, bool) {
	blk := w.GetBlock(pos)
	kind, _, hasFluid := fluidOf(blk)
	if blk == world.Air && !hasFluid {
		return 0, false
	}
	name, _ := world.StateOwner(blk)
	res := blastResistance[name]
	var fres float32
	if hasFluid && (kind == fluidWater || kind == fluidLava) {
		fres = 100
	}
	if fres > res {
		res = fres
	}
	return res, true
}

// tntOnPlace is TntBlock.onPlace: a TNT written next to a signal primes
// and removes itself inside the write (callers re-read the cell after
// writeOnPlace, so the world shows air).
func (s *Server) tntOnPlace(d *dimension, pos world.Pos, blk world.Block) {
	name, _ := world.StateOwner(blk)
	if name != "tnt" {
		return
	}
	if !rsHasNeighborSignal(d, pos) {
		return
	}
	d.w.SetBlock(pos, world.Air)
	s.primeTntBlock(d, pos, tntFuseTicks)
}

// tntNeighborChanged is TntBlock.neighborChanged: any write next to a
// TNT re-evaluates hasNeighborSignal and primes+removes it.
func (s *Server) tntNeighborChanged(d *dimension, pos world.Pos) {
	cur := d.w.GetBlock(pos)
	name, _ := world.StateOwner(cur)
	if name != "tnt" {
		return
	}
	if !rsHasNeighborSignal(d, pos) {
		return
	}
	s.setNaturalBlock(d, pos, world.Air)
	s.primeTntBlock(d, pos, tntFuseTicks)
}

// useTnt is TntBlock.useItemOn: flint_and_steel and fire_charge prime
// the block — it clears with flag 11 (the |8 bit also runs the
// removeBlock path), the flint wears 1 point, the charge shrinks by
// one (creative keeps the item), and ITEM_USED lands for the held item.
// Returns false when the held item is neither.
func (s *Server) useTnt(sess *Session, d *dimension, pos world.Pos) bool {
	held := sess.heldStack()
	iname, ok := itemNameOf(held.ID)
	if !ok {
		return false
	}
	flint := iname == "flint_and_steel"
	if !flint && iname != "fire_charge" {
		return false
	}
	if s.primeTntBlock(d, pos, tntFuseTicks) == nil {
		// tnt_explodes=false: vanilla shows the disabled overlay and
		// returns InteractionResult.PASS — the use falls through to the
		// item's own useOn, so flint_and_steel still places fire.
		s.sendActionBar(sess, component{translate: "block.minecraft.tnt.disabled"})
		return false
	}
	s.setNaturalBlock(d, pos, world.Air)
	if flint {
		s.wearHeld(sess, 1)
	} else if sess.mode() != GameModeCreative {
		sess.setStack(invSlotForHotbar(sess.held.Load()), held.withCount(held.Count-1))
	}
	sess.addItemStat(statUsed, held.ID, 1)
	return true
}
