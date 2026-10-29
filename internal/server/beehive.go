package server

import (
	"math/rand"
	"strconv"

	"mc-go/internal/world"
)

// Bee harvest + occupant release. Vanilla paths measured on 26.2:
//
//   - BeehiveBlock.useItemOn (shears or glass_bottle, honey_level>=5):
//     drop the comb / fill the bottle, play the player-side sound,
//     then split on CampfireBlock.isSmokeyPos — a lit campfire within
//     five blocks below keeps the occupants inside (resetHoneyLevel);
//     otherwise releaseBeesAndResetHoneyLevel(EMERGENCY) runs, whose
//     releaseOccupant bypasses both the bees-stay-in-hive attribute
//     and a blocked exit.
//   - DispenseItemBehavior$9 (bottle) + ShearsDispenseItemBehavior:
//     releaseBeesAndResetHoneyLevel(BEE_RELEASED) — the same gates
//     apply, no campfire check, no player.
//   - BeehiveBlock.playerDestroy: without PREVENTS_BEE_SPAWNS_WHEN_MINING
//     (silk touch) the nest empties EMERGENCY.
//
// releaseOccupant per occupant: BEES_STAY_IN_HIVE (the day timeline's
// [12542,23460) window, or rain/thunder) keeps the bee unless
// EMERGENCY; a non-empty collision shape in the facing cell keeps it
// unless EMERGENCY. Survivors spawn at pos+0.5 stepped
// 0.55+bbWidth/2 out the face (0 when blocked), beehive_exit per bee.
//
// emptyAllLivingFromHive's anger pass: every released bee within
// 4 blocks of the harvester takes setTarget on him (mc-go's
// AngryAt open-ended — Bee's persistent-anger timer never arms on
// this path); a sedated nest (lit campfire under it, possible on a
// break) swaps anger for stayOutOfHiveCountdown=400. The block then
// layers angerNearbyBees on top: every targetless bee in the 8x6x8
// box around the nest takes a random in-box player — the use path
// only reaches it unsmoked, but playerDestroy and onExplosionHit run
// it unconditionally, so a campfire never protects a breaker.

type beeReleaseStatus int

const (
	beeReleaseEmergency beeReleaseStatus = iota
	beeReleaseReleased
)

// beeStayInHive is the environment attribute
// gameplay/bees_stay_in_hive: the overworld day timeline ORs it true
// on [12542,23460) and the rain/thunder weather maps set it.
func (s *Server) beeStayInHive() bool {
	dt := s.dayTime() % 24000
	if dt >= 12542 && dt < 23460 {
		return true
	}
	return s.weatherRaining.Load() || s.weatherThunder.Load()
}

// campfireSedated is CampfireBlock.isSmokeyPos: scan up to five
// blocks below; a lit campfire answers true, and the first block
// whose collision shape would block the smoke post ends the scan —
// the answer is then whether the block ONE further below is a lit
// campfire (a campfire under a solid floor still sedates).
func (s *Server) campfireSedated(d *dimension, pos world.Pos) bool {
	litCampfire := func(b world.Block) bool {
		name, ok := world.StateOwner(b)
		if !ok || (name != "campfire" && name != "soul_campfire") {
			return false
		}
		p, ok := world.StateProperty(b, "lit")
		return ok && p == "true"
	}
	for i := 1; i <= 5; i++ {
		b := d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y - i, Z: pos.Z})
		if litCampfire(b) {
			return true
		}
		// NonSolid approximates an empty collision shape; anything
		// else blocks the post.
		if !world.NonSolid(b) {
			return litCampfire(d.w.GetBlock(world.Pos{X: pos.X, Y: pos.Y - i - 1, Z: pos.Z}))
		}
	}
	return false
}

var beeFacingSteps = map[string][2]int{
	"north": {0, -1}, "south": {0, 1},
	"west": {-1, 0}, "east": {1, 0},
}

// releaseBeeOccupants is BeehiveBlockEntity.releaseAllOccupants over a
// count-only occupant list (mc-go stores the bees list's length, not
// its entities). Returns the spawned bees and the count left inside.
func (s *Server) releaseBeeOccupants(d *dimension, pos world.Pos, blk world.Block, count int32, status beeReleaseStatus) ([]*Entity, int32) {
	if count <= 0 {
		return nil, 0
	}
	stayIn := s.beeStayInHive()
	facing := rsPropsOf(blk)("facing")
	step, ok := beeFacingSteps[facing]
	if !ok {
		step = [2]int{0, 1} // undefined facing — vanilla defaults south
	}
	front := world.Pos{X: pos.X + step[0], Y: pos.Y, Z: pos.Z + step[1]}
	// getCollisionShape.isEmpty at the facing cell; the washed-away
	// set approximates empty collision.
	frontBlocked := !world.NonSolid(d.w.GetBlock(front))
	// Bee hitbox: 0.55 wide, 0.5 tall (measured against a vanilla
	// release — spawns 0.825 out the face, feet 0.25 below center).
	const beeW, beeH = 0.55, 0.5
	dist := 0.55 + beeW/2
	if frontBlocked {
		dist = 0
	}
	sx := float64(step[0])
	sz := float64(step[1])
	var released []*Entity
	for i := int32(0); i < count; i++ {
		if stayIn && status != beeReleaseEmergency {
			break
		}
		if frontBlocked && status != beeReleaseEmergency {
			break
		}
		e, ok := s.summonMobRaw(d, "bee",
			float64(pos.X)+0.5+dist*sx,
			float64(pos.Y)+0.5-beeH/2,
			float64(pos.Z)+0.5+dist*sz,
			false, false)
		if !ok {
			break
		}
		s.broadcastFreshMob(e)
		released = append(released, e)
		// beehive_exit per released occupant, BLOCKS source, at the
		// nest (not the exit cell).
		if snd, ok := soundEventIDs["block.beehive.exit"]; ok {
			s.playSoundSrc(d.name, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, snd, 4, 1.0, 1.0, nil)
		}
	}
	return released, count - int32(len(released))
}

// beeAngerSweep is the tail of BeehiveBlockEntity.emptyAllLivingFromHive:
// each released bee within 4 blocks (distanceToSqr ≤ 16) of the player
// gets setTarget on him — mc-go's open-ended AngryAt. A sedated nest
// (lit campfire under it) swaps anger for stayOutOfHiveCountdown=400;
// harvest never reaches this when sedated, but a nest break does.
func (s *Server) beeAngerSweep(d *dimension, released []*Entity, sess *Session, pos world.Pos) {
	if sess == nil || len(released) == 0 {
		return
	}
	sedated := s.campfireSedated(d, pos)
	px, py, pz, _, _, _ := sess.Pos()
	for _, be := range released {
		dx, dy, dz := be.X-px, be.Y-py, be.Z-pz
		if dx*dx+dy*dy+dz*dz > 16.0 {
			continue
		}
		if sedated {
			be.CannotEnterHive = 400
		} else {
			be.AngryAt = sess.uuid
			be.AngryUntil = 0
			be.MetaDirty = true
		}
	}
}

// angerNearbyBees is Beehive.angerNearbyBees: every bee in the
// AABB(pos).inflate(8,6,8) box that has no target takes a random
// in-box player as its target. Unsmoked harvest runs it only when
// the hive holds occupants; breaks and explosions run it
// unconditionally — the campfire gate lives on the use branch, so
// breaking under smoke still angers.
func (s *Server) angerNearbyBees(d *dimension, pos world.Pos) {
	minX, minY, minZ := float64(pos.X)-8, float64(pos.Y)-6, float64(pos.Z)-8
	maxX, maxY, maxZ := float64(pos.X)+9, float64(pos.Y)+7, float64(pos.Z)+9
	inBox := func(x, y, z float64) bool {
		return x >= minX && x < maxX && y >= minY && y < maxY && z >= minZ && z < maxZ
	}
	var players []*Session
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if !sess.spawned.Load() || sess.dimension().name != d.name {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		if inBox(px, py, pz) {
			players = append(players, sess)
		}
		return true
	})
	if len(players) == 0 {
		return
	}
	for _, snap := range s.entities.All() {
		if snap.Dim != d.name || snap.Health <= 0 {
			continue
		}
		if kn, _ := EntityTypeName(snap.Type); kn != "bee" {
			continue
		}
		if !inBox(snap.X, snap.Y, snap.Z) {
			continue
		}
		// Bee.getTarget()!=null skips a bee already aggroed; an
		// expired anger timer reads as no target here too.
		if snap.AngryAt != ([16]byte{}) && (snap.AngryUntil == 0 || s.gameTime() < snap.AngryUntil) {
			continue
		}
		if e, ok := s.entities.GetPtr(snap.ID); ok {
			target := players[rand.Intn(len(players))]
			e.AngryAt = target.uuid
			e.AngryUntil = 0
			e.MetaDirty = true
		}
	}
}

// releaseBeesAndResetHoneyLevel is BeehiveBlock.releaseBeesAndResetHoneyLevel:
// honey_level resets first (the rsWriteBlockFlag2 write carries the same
// client+neighbour surface vanilla's setBlock flag 3 does here), then the
// hive empties. sess is the harvester — the released bees run
// emptyAllLivingFromHive's anger sweep on him.
func (s *Server) releaseBeesAndResetHoneyLevel(d *dimension, pos world.Pos, blk world.Block, sess *Session, status beeReleaseStatus) {
	if nb, ok := world.WithProperty(blk, "honey_level", "0"); ok {
		s.rsWriteBlockFlag2(d, pos, nb)
	}
	count := d.bees[pos]
	if count <= 0 {
		return
	}
	released, left := s.releaseBeeOccupants(d, pos, blk, count, status)
	s.beeAngerSweep(d, released, sess, pos)
	if left <= 0 {
		delete(d.bees, pos)
	} else {
		d.bees[pos] = left
	}
}

// useBeehive is BeehiveBlock.useItemOn: honey_level>=5 with shears or
// a glass bottle harvests. Reports whether the use was consumed.
func (s *Server) useBeehive(sess *Session, d *dimension, pos world.Pos, blk world.Block) bool {
	return s.useBeehiveInHand(sess, d, pos, blk, 0)
}

func (s *Server) useBeehiveInHand(sess *Session, d *dimension, pos world.Pos, blk world.Block, hand int32) bool {
	hl, err := strconv.Atoi(rsPropsOf(blk)("honey_level"))
	if err != nil || hl < 5 {
		return false
	}
	held, slot := sess.stackForHand(hand)
	iname, _ := itemNameOf(held.ID)
	px, py, pz, _, _, _ := sess.Pos()
	switch iname {
	case "shears":
		// dropHoneycomb — the interact loot table yields 3 honeycomb.
		if hid, ok := world.ItemIDs["honeycomb"]; ok {
			s.spawnItemDrop(d.name, pos, ItemStack{ID: hid, Count: 3})
		}
		if snd, ok := soundEventIDs["block.beehive.shear"]; ok {
			s.playSoundSrc(d.name, px, py, pz, snd, 4, 1.0, 1.0, nil)
		}
		s.wearSlot(sess, slot, 1)
	case "glass_bottle":
		held.Count--
		if snd, ok := soundEventIDs["item.bottle.fill"]; ok {
			s.playSoundSrc(d.name, px, py, pz, snd, 4, 1.0, 1.0, nil)
		}
		var hb ItemStack
		if fid, ok := world.ItemIDs["honey_bottle"]; ok {
			hb = ItemStack{ID: fid, Count: 1}
		}
		if held.Count <= 0 {
			// shrink emptied the stack → setItemInHand(honey_bottle).
			sess.setStack(slot, hb)
			sess.invState.Add(1)
			_ = s.sendContainerSlot(sess, slot, hb)
		} else {
			sess.setStack(slot, held)
			sess.invState.Add(1)
			_ = s.sendContainerSlot(sess, slot, held)
			// inventory.add(honey_bottle) or Player.drop on overflow.
			if rem, _ := sess.addStack(hb); !rem.Empty() {
				s.dropItemLocked(sess, rem)
			}
		}
	default:
		return false
	}
	if s.campfireSedated(d, pos) {
		if nb, ok := world.WithProperty(blk, "honey_level", "0"); ok {
			s.rsWriteBlockFlag2(d, pos, nb)
		}
		return true
	}
	// No campfire: angerNearbyBees (only while the hive still holds
	// occupants), then releaseBeesAndResetHoneyLevel(EMERGENCY) —
	// release plus the anger sweep on the harvester.
	if d.bees[pos] > 0 {
		s.angerNearbyBees(d, pos)
	}
	s.releaseBeesAndResetHoneyLevel(d, pos, blk, sess, beeReleaseEmergency)
	return true
}

// beeNestDestroyed is BeehiveBlock.playerDestroy's non-silk tail:
// emptyAllLivingFromHive(EMERGENCY) — release plus the anger sweep on
// the breaker — then angerNearbyBees unconditionally: a campfire
// never protects the breaker. broken is the pre-write nest state;
// count was captured before the cell turned to air.
func (s *Server) beeNestDestroyed(d *dimension, pos world.Pos, broken world.Block, count int32, sess *Session) {
	released, _ := s.releaseBeeOccupants(d, pos, broken, count, beeReleaseEmergency)
	s.beeAngerSweep(d, released, sess, pos)
	s.angerNearbyBees(d, pos)
}
