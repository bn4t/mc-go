package server

import (
	"math/rand"

	"mc-go/internal/world"
)

// bucketContents names the fluid a plain BucketItem carries ("" for the
// empty bucket). Mob buckets (MobBucketItem) also carry water but spawn
// their mob; mc-go leaves them to the default path.
var bucketContents = map[string]string{
	"bucket":       "",
	"water_bucket": "water",
	"lava_bucket":  "lava",
}

// useBucket is BucketItem.use through ServerPlayerGameMode.useItem.
// The POV clip (SOURCE_ONLY for the empty bucket, NONE otherwise)
// misses → PASS. A hit fails when the level or the player may not act
// at the hit / the cell against its face. The empty bucket picks up a
// BucketPickup block at the hit (FAIL when it yields nothing); a full
// one empties into the hit cell when that is a water container and the
// bucket holds water, else into the cell against the face. A changed
// hand stack is written back and the whole inventory resent
// (sendAllDataToRemote).
func (s *Server) useBucket(sess *Session, hand int32, yaw, pitch float32, item string) {
	content := bucketContents[item]
	d := sess.dimension()
	if d == nil || sess.mode() == GameModeSpectator {
		return
	}
	mode := clipFluidNone
	if content == "" {
		mode = clipFluidSourceOnly
	}
	hit := s.playerPOVHit(sess, d, mode, yaw, pitch)
	if hit.miss {
		return
	}
	pos := hit.pos
	against := hit.face.rel(pos)
	if !s.mayInteract(sess, d, pos) || !bucketMayUseAt(sess) {
		return
	}
	stack, slot := sess.stackForHand(hand)
	if content == "" {
		st := d.w.GetBlock(pos)
		filled, sound := s.bucketPickup(sess, d, pos, st)
		if filled == "" {
			return
		}
		sess.addItemStat(statUsed, stack.ID, 1)
		if id, ok := soundEventIDs[sound]; ok {
			x, y, z, _, _, _ := sess.Pos()
			s.playSoundSrc(d.name, x, y, z, id, srcPlayers, 1, 1, sess)
		}
		fid, _ := world.ItemID(filled)
		s.bucketResult(sess, slot, stack, ItemStack{ID: fid, Count: 1})
		return
	}
	target := against
	if s.liquidContainer(d.w.GetBlock(pos)) && content == "water" {
		target = pos
	}
	if !s.bucketEmpty(sess, d, target, &hit, content) {
		return
	}
	sess.addItemStat(statUsed, stack.ID, 1)
	empty := stack
	if sess.mode() != GameModeCreative {
		bid, _ := world.ItemID("bucket")
		empty = ItemStack{ID: bid, Count: 1}
	}
	s.bucketResult(sess, slot, stack, empty)
}

// bucketMayUseAt is Player.mayUseItemAt for a bucket: abilities.mayBuild
// (not adventure or spectator; a bucket carries no can_place_on).
func bucketMayUseAt(sess *Session) bool {
	m := sess.mode()
	return m != GameModeAdventure && m != GameModeSpectator
}

// bucketResult is ItemUtils.createFilledResult(stack, player, filled)
// and the held-item write-back: infinite materials keep the stack and
// add the result only when no equal stack is held anywhere; otherwise
// one is consumed, an emptied stack becomes the result, else the result
// goes to the inventory (or is dropped). A changed hand stack resends
// the inventory.
func (s *Server) bucketResult(sess *Session, slot int16, stack, filled ItemStack) {
	if sess.mode() == GameModeCreative {
		if filled.ID != stack.ID && !sess.inventoryContains(filled.ID) {
			sess.addStack(filled)
		}
		return
	}
	stack.Count--
	if stack.Count <= 0 {
		sess.setStack(slot, filled)
	} else {
		sess.setStack(slot, stack)
		if rest, _ := sess.addStack(filled); !rest.Empty() {
			s.dropItemLocked(sess, rest)
		}
	}
	_ = s.sendContainerContent(sess)
}

// liquidContainer reports a LiquidBlockContainer block.
func (s *Server) liquidContainer(st world.Block) bool {
	return vanillaBucketBlocks[bstate(st)].place != ""
}

// bucketPickup is BucketPickup.pickupBlock: the filled bucket's item
// name ("" for ItemStack.EMPTY) and the block's pickup sound.
//   - LiquidBlock: a source (level 0) becomes air (flags 11).
//   - BubbleColumnBlock: air, a water bucket.
//   - PowderSnowBlock: air plus the 2001 break event.
//   - SimpleWaterloggedBlock: a waterlogged state loses the water
//     (flags 3), then breaks with drops if it no longer survives;
//     BarrierBlock only for a creative player.
func (s *Server) bucketPickup(sess *Session, d *dimension, pos world.Pos, st world.Block) (string, string) {
	name := bstate(st)
	bb := vanillaBucketBlocks[name]
	switch bb.pickup {
	case "LiquidBlock":
		if lv, _ := world.StateProperty(st, "level"); lv != "0" {
			return "", ""
		}
		s.setNaturalBlock(d, pos, world.Air)
		return name + "_bucket", bb.sound
	case "BubbleColumnBlock":
		s.setNaturalBlock(d, pos, world.Air)
		return "water_bucket", bb.sound
	case "PowderSnowBlock":
		s.setNaturalBlock(d, pos, world.Air)
		s.broadcastLevelEvent(d.name, 2001, pos, int32(st))
		return "powder_snow_bucket", bb.sound
	case "BarrierBlock":
		if sess.mode() != GameModeCreative {
			return "", ""
		}
		fallthrough
	case "SimpleWaterloggedBlock":
		if !propIs(st, "waterlogged", "true") {
			return "", ""
		}
		dry, _ := world.WithProperty(st, "waterlogged", "false")
		s.setNaturalBlock(d, pos, dry)
		if !blockCanSurvive(s, d, dry, pos) {
			s.destroyBlockNatural(d, pos, dry)
		}
		return "water_bucket", bb.sound
	}
	return "", ""
}

// bucketEmpty is BucketItem.emptyContents(player, level, pos, hit). The
// cell takes the fluid when it is air, or replaceable by it (or a
// container that accepts it) — except for a sneaking player's first
// try, which moves on; a refused cell retries once at the cell against
// the hit face. An evaporating dimension fizzes water away
// (block.fire.extinguish, BLOCKS, 0.5, 2.6 + (r1 - r2) * 0.8, plus
// eight large smoke particles); a container takes water through
// placeLiquid; else a replaceable non-liquid breaks with drops and the
// fluid's source block is written (flags 11), succeeding when the
// write lands or the cell already held a source.
func (s *Server) bucketEmpty(sess *Session, d *dimension, pos world.Pos, hit *blockHit, content string) bool {
	st := d.w.GetBlock(pos)
	row := stateShapeRow(st)
	replace := row.flags&shapeReplaceWater != 0
	placeFlag := uint8(shapePlaceWater)
	if content == "lava" {
		replace = row.flags&shapeReplaceLava != 0
		placeFlag = shapePlaceLava
	}
	container := s.liquidContainer(st) && s.canPlaceLiquid(sess, st, placeFlag)
	sneak := sess.sneaking
	ok := st == world.Air || (replace || container) && !(sneak && hit != nil)
	if !ok {
		return hit != nil && s.bucketEmpty(sess, d, hit.face.rel(hit.pos), nil, content)
	}
	if d.ultraWarm && content == "water" {
		if id, ok := soundEventIDs["block.fire.extinguish"]; ok {
			pitch := 2.6 + (rand.Float32()-rand.Float32())*0.8
			s.playSoundSrc(d.name, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, id, srcBlocks, 0.5, pitch, sess)
		}
		return true
	}
	if s.liquidContainer(st) && content == "water" {
		s.placeLiquid(sess, d, pos, st)
		s.bucketEmptySound(sess, d, pos, content)
		return true
	}
	if replace && !worldIsLiquid(st) {
		s.destroyBlockNatural(d, pos, st)
	}
	src, _ := world.BlockState(content)
	wasSource := false
	if _, lv, fok := fluidOf(st); fok && lv == 0 {
		wasSource = true
	}
	// Level.setBlock answers false for an unchanged state, which
	// writes (and sends) nothing.
	changed := d.w.GetBlock(pos) != src
	if changed {
		s.setNaturalBlock(d, pos, src)
	}
	if changed || wasSource {
		s.bucketEmptySound(sess, d, pos, content)
		return true
	}
	return false
}

// canPlaceLiquid is LiquidBlockContainer.canPlaceLiquid: the measured
// empty-getter answer, with BarrierBlock's creative-only gate.
func (s *Server) canPlaceLiquid(sess *Session, st world.Block, flag uint8) bool {
	if vanillaBucketBlocks[bstate(st)].pickup == "BarrierBlock" && sess.mode() != GameModeCreative {
		return false
	}
	return stateShapeRow(st).flags&flag != 0
}

// placeLiquid is LiquidBlockContainer.placeLiquid with a water source:
//   - SimpleWaterloggedBlock (SlabBlock: not a double slab): a dry state
//     waterlogs (flags 3) and the water's tick is scheduled.
//   - CandleBlock: waterlogged, and a lit candle is extinguished
//     (lit=false, block.candle.extinguish BLOCKS 1 1).
//   - CampfireBlock: a lit campfire plays block.generic.extinguish_fire
//     and goes out; waterlogged and unlit.
//   - DriedGhastBlock: waterlogged, block.dried_ghast.place_in_water.
//   - Kelp and seagrass refuse.
func (s *Server) placeLiquid(sess *Session, d *dimension, pos world.Pos, st world.Block) {
	bb := vanillaBucketBlocks[bstate(st)]
	if propIs(st, "waterlogged", "true") {
		return
	}
	if bb.place == "SlabBlock" && propIs(st, "type", "double") {
		return
	}
	switch bb.place {
	case "SimpleWaterloggedBlock", "SlabBlock", "CandleBlock", "CampfireBlock", "DriedGhastBlock":
	default:
		return
	}
	wet, ok := world.WithProperty(st, "waterlogged", "true")
	if !ok {
		return
	}
	sound := ""
	switch bb.place {
	case "CandleBlock":
		if propIs(st, "lit", "true") {
			wet, _ = world.WithProperty(wet, "lit", "false")
			sound = "block.candle.extinguish"
		}
	case "CampfireBlock":
		if propIs(st, "lit", "true") {
			s.blockSoundAt(d, pos, "block.generic.extinguish_fire", 1, 1)
			wet, _ = world.WithProperty(wet, "lit", "false")
		}
	case "DriedGhastBlock":
		sound = "block.dried_ghast.place_in_water"
	}
	s.setNaturalBlock(d, pos, wet)
	s.scheduleFluidTick(d, pos, fluidWater.tickDelay(d))
	if sound != "" {
		s.blockSoundAt(d, pos, sound, 1, 1)
	}
}

// bucketEmptySound is BucketItem.playEmptySound: bucket.empty_lava or
// bucket.empty at the cell, BLOCKS 1 1, to everyone but the user.
func (s *Server) bucketEmptySound(sess *Session, d *dimension, pos world.Pos, content string) {
	name := "item.bucket.empty"
	if content == "lava" {
		name = "item.bucket.empty_lava"
	}
	if id, ok := soundEventIDs[name]; ok {
		s.playSoundSrc(d.name, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, id, srcBlocks, 1, 1, sess)
	}
}

// blockSoundAt plays a BLOCKS sound at the cell centre for everyone.
func (s *Server) blockSoundAt(d *dimension, pos world.Pos, name string, vol, pitch float32) {
	if id, ok := soundEventIDs[name]; ok {
		s.playSoundSrc(d.name, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5, id, srcBlocks, vol, pitch, nil)
	}
}

// worldIsLiquid is BlockStateBase.liquid(): a LiquidBlock state.
func worldIsLiquid(st world.Block) bool {
	n := bstate(st)
	return n == "water" || n == "lava"
}

// useGlassBottle is BottleItem.use: a live area effect cloud a dragon
// owns within two blocks of the player's box shrinks its radius by 0.5
// and fills the bottle with dragon_breath (item.bottle.fill_dragonbreath,
// NEUTRAL, heard by everyone); otherwise a SOURCE_ONLY POV clip whose
// hit cell holds water fills a water potion (item.bottle.fill, NEUTRAL,
// for everyone but the user). The result goes through
// createFilledResult and counts item_used; anything else passes.
func (s *Server) useGlassBottle(sess *Session, hand int32, yaw, pitch float32) {
	d := sess.dimension()
	if d == nil || sess.mode() == GameModeSpectator {
		return
	}
	stack, slot := sess.stackForHand(hand)
	x, y, z, _, _, _ := sess.Pos()
	cloudType := mustEntityType("area_effect_cloud")
	for _, e := range s.entities.All() {
		if e.Type != cloudType || e.Dim != d.name || e.Dying {
			continue
		}
		owner, ok := s.entities.Get(e.CloudOwnerID)
		if !ok || owner.Type != mustEntityType("ender_dragon") {
			continue
		}
		r := float64(e.CloudRadius)
		if !(e.X+r > x-2.3 && e.X-r < x+2.3 && e.Y+0.5 > y-2 && e.Y < y+3.8 && e.Z+r > z-2.3 && e.Z-r < z+2.3) {
			continue
		}
		if p, ok := s.entities.GetPtr(e.ID); ok {
			p.CloudRadius -= 0.5
			if meta := encodeCloudMeta(p); len(meta) > 0 {
				s.broadcastEntityMetadata(p.ID, meta)
			}
		}
		if id, ok := soundEventIDs["item.bottle.fill_dragonbreath"]; ok {
			s.playSoundSrc(d.name, x, y, z, id, srcNeutral, 1, 1, nil)
		}
		bid, _ := world.ItemID("dragon_breath")
		sess.addItemStat(statUsed, stack.ID, 1)
		s.bucketResult(sess, slot, stack, ItemStack{ID: bid, Count: 1})
		return
	}
	hit := s.playerPOVHit(sess, d, clipFluidSourceOnly, yaw, pitch)
	if hit.miss || !s.mayInteract(sess, d, hit.pos) {
		return
	}
	if k, _, ok := fluidOf(d.w.GetBlock(hit.pos)); !ok || k != fluidWater {
		return
	}
	if id, ok := soundEventIDs["item.bottle.fill"]; ok {
		s.playSoundSrc(d.name, x, y, z, id, srcNeutral, 1, 1, sess)
	}
	pid, _ := world.ItemID("potion")
	sess.addItemStat(statUsed, stack.ID, 1)
	s.bucketResult(sess, slot, stack, ItemStack{ID: pid, Count: 1, Potion: "water"})
}
