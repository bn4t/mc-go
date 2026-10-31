package server

import (
	"math"
	"math/rand"
	"strings"

	"mc-go/internal/data"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Game modes (Join Game byte values).
const (
	GameModeSurvival  = 0
	GameModeCreative  = 1
	GameModeAdventure = 2
	GameModeSpectator = 3
)

// blockHardness is the public vanilla hardness table for the blocks
// mc-go can generate or be asked to place; unknown blocks fall back to
// a breakable default. requiresTool mirrors vanilla's "needs correct
// tool for drops" flag.
type blockHardness struct {
	hardness     float64
	requiresTool bool
	drop         string
}

var blockHardnessTable = map[string]blockHardness{
	"bedrock":                 {hardness: -1},
	"barrier":                 {hardness: -1},
	"command_block":           {hardness: -1},
	"chain_command_block":     {hardness: -1},
	"repeating_command_block": {hardness: -1},
	"structure_block":         {hardness: -1},
	"jigsaw":                  {hardness: -1},
	"stone":                   {hardness: 1.5, requiresTool: true, drop: "cobblestone"},
	"cobblestone":             {hardness: 2.0, requiresTool: true, drop: "cobblestone"},
	"dirt":                    {hardness: 0.5, drop: "dirt"},
	"grass_block":             {hardness: 0.6, drop: "dirt"},
	"coarse_dirt":             {hardness: 0.5, drop: "coarse_dirt"},
	"podzol":                  {hardness: 0.5, drop: "podzol"},
	"sand":                    {hardness: 0.5, drop: "sand"},
	"gravel":                  {hardness: 0.6, drop: "gravel"},
	"oak_log":                 {hardness: 2.0, drop: "oak_log"},
	"oak_planks":              {hardness: 2.0, drop: "oak_planks"},
	"glass":                   {hardness: 0.3, drop: ""},
	// Bee nests/hives: strength 0.3/0.6 per Blocks.java, no
	// requiresCorrectToolForDrops — silk gating lives in the loot
	// table's match_tool pool instead.
	"bee_nest": {hardness: 0.3},
	"beehive":  {hardness: 0.6},
	// Storage blocks: chest family strength 2.5, shulker boxes 2.0,
	// dispenser/dropper/hopper family 3.5/3.5/3.0. The pickaxe-required
	// ones (dispenser, dropper, hopper, the furnaces, brewing_stand)
	// pick that up from blockRequiresTool — this table is strength only.
	"chest":         {hardness: 2.5},
	"trapped_chest": {hardness: 2.5},
	"barrel":        {hardness: 2.5},
	"dispenser":     {hardness: 3.5},
	"dropper":       {hardness: 3.5},
	"hopper":        {hardness: 3.0},
	"furnace":       {hardness: 3.5},
	"blast_furnace": {hardness: 3.5},
	"smoker":        {hardness: 3.5},
	"brewing_stand": {hardness: 0.5},
	"shulker_box":   {hardness: 2.0},
	// Ice family: strength 0.5, no requiresCorrectToolForDrops —
	// loot tables decide the drops (ice/frosted melt or silk only,
	// packed drops itself). Blue ice is 2.8 and drops itself.
	"ice":         {hardness: 0.5},
	"frosted_ice": {hardness: 0.5},
	"packed_ice":  {hardness: 0.5},
	"blue_ice":    {hardness: 2.8},
	// Instant breaks: destroySpeed 0 means no dig time and no tool
	// wear (vanilla only wears when destroySpeed != 0).
	"torch":          {hardness: 0, drop: "torch"},
	"redstone_torch": {hardness: 0, drop: "redstone_torch"},
	"soul_torch":     {hardness: 0, drop: "soul_torch"},
	"lever":          {hardness: 0, drop: "lever"},
	"oak_sapling":    {hardness: 0, drop: "oak_sapling"},
}

// hardnessOf returns the break rules for a block state.
func hardnessOf(b world.Block) blockHardness {
	name, ok := world.StateOwner(b)
	if !ok {
		return blockHardness{hardness: 1.0}
	}
	if info, ok := blockHardnessTable[name]; ok {
		return info
	}
	// Default: breakable, drops itself when the name is an item.
	drop := name
	if _, ok := world.ItemID(name); !ok {
		drop = ""
	}
	return blockHardness{hardness: 1.0, drop: drop}
}

// digTicks is how many game ticks a break takes, straight from
// vanilla's getDestroyProgress: per-tick progress is
// speed/hardness/30 when the held stack is the block's correct tool
// and /100 when it is not, quantized to a whole tick. A result of 1
// or less breaks on the action start itself, like vanilla's
// instabreak path (grass, bamboo with a sword).
func (s *Server) digTicks(info blockHardness, bname string, sess *Session) int {
	if info.hardness < 0 {
		return -1
	}
	tool := sess.heldStack()
	speed := toolDestroySpeed(tool, bname)
	if eff := tool.enchLevel("minecraft:efficiency"); eff > 0 && speed > 1 {
		speed += float64(eff*eff + 1)
	}
	x, y, z, _, _, og := sess.Pos()
	if !og {
		// Vanilla's onGround flag resolves on the entity's physics
		// tick, so a player parked on solid footing since the last
		// move packet still reads grounded; a free-floating digger
		// pays the /5 penalty.
		og = s.supportedBelow(sess, x, y, z)
	}
	if !og {
		speed /= 5
	}
	div := 30.0
	if blockRequiresTool[bname] && !toolCorrectForDrops(tool, bname) {
		// Only a requires-tool block punishes the wrong tool with the
		// /100 divisor; anything else keeps the /30 baseline.
		div = 100.0
	}
	n := int(math.Ceil(info.hardness * div / speed))
	if n < 1 {
		n = 1
	}
	return n
}

// toolDestroySpeed mirrors ItemStack.getDestroySpeed: the first Tool
// component rule matching the block wins, else the base speed 1. The
// diggers gate on their mineable tag for the tier's speed; swords
// have the cobweb and tag rules; shears their three tag tiers.
func toolDestroySpeed(tool ItemStack, bname string) float64 {
	if tool.Empty() {
		return 1
	}
	name, ok := world.ItemName(tool.ID)
	if !ok {
		return 1
	}
	switch {
	case name == "shears":
		switch {
		case bname == "cobweb" || blockInTag("shears_extreme_breaking_speed", bname):
			return 15
		case blockInTag("shears_major_breaking_speed", bname):
			return 5
		case blockInTag("shears_minor_breaking_speed", bname):
			return 2
		}
		return 1
	case strings.HasSuffix(name, "_sword"):
		switch {
		case bname == "cobweb":
			return 15
		case blockInTag("sword_instantly_mines", bname):
			return math.MaxFloat32
		case blockInTag("sword_efficient", bname):
			return 1.5
		}
		return 1
	}
	kind := ""
	for _, k := range []string{"pickaxe", "axe", "shovel", "hoe"} {
		if strings.HasSuffix(name, "_"+k) {
			kind = k
			break
		}
	}
	if kind == "" || !blockInTag("mineable/"+kind, bname) {
		return 1
	}
	return toolMaterialSpeed(name)
}

// toolMaterialSpeed is ToolMaterial.speed() by item-name tier.
func toolMaterialSpeed(itemName string) float64 {
	switch {
	case strings.HasPrefix(itemName, "wooden_"):
		return 2
	case strings.HasPrefix(itemName, "stone_"):
		return 4
	case strings.HasPrefix(itemName, "copper_"):
		return 5
	case strings.HasPrefix(itemName, "iron_"):
		return 6
	case strings.HasPrefix(itemName, "diamond_"):
		return 8
	case strings.HasPrefix(itemName, "golden_"):
		return 12
	case strings.HasPrefix(itemName, "netherite_"):
		return 9
	}
	return 1
}

// reachSurvival is the vanilla block-interaction reach in survival.
const reachSurvival = 4.5

// lookVector converts yaw/pitch degrees to a unit direction, matching
// the vanilla convention (yaw 0 faces +Z, pitch 90 looks straight down).
func lookVector(yaw, pitch float32) (float64, float64, float64) {
	ya := float64(yaw) * math.Pi / 180
	pa := float64(pitch) * math.Pi / 180
	cosP := math.Cos(pa)
	return -math.Sin(ya) * cosP, -math.Sin(pa), math.Cos(ya) * cosP
}

// withinReach reports whether pos is close enough to the session's eye
// to be acted on, vanilla's block interaction range of 4.5 blocks
// measured to the nearest point of the block.
func withinReach(sess *Session, pos world.Pos, reach float64) bool {
	x, y, z, _, _, _ := sess.Pos()
	dx := math.Max(0, math.Max(float64(pos.X)-(x), x-(float64(pos.X)+1)))
	dy := math.Max(0, math.Max(float64(pos.Y)-(y+1.62), (y+1.62)-(float64(pos.Y)+1)))
	dz := math.Max(0, math.Max(float64(pos.Z)-(z), z-(float64(pos.Z)+1)))
	return math.Sqrt(dx*dx+dy*dy+dz*dz) <= reach
}

// handleDigSurvival implements the survival branch of Player Action,
// mirroring vanilla's ServerPlayerGameMode.handleBlockBreakAction.
// A start insta-breaks when first-tick progress reaches 1, otherwise it
// latches the block and the server counts ticks (digTickStep) — the
// tick loop itself never destroys the block. A stop on the latched pos
// breaks it when accumulated progress completed, otherwise it arms a
// delayed destroy that fires on the first tick the progress reaches 1
// (vanilla's hasDelayedDestroy, which survives the dig being unlatched).
// An abort unlatches the dig; the delayed arm stays.
func (s *Server) handleDigSurvival(sess *Session, pos world.Pos, status int32) {
	blk := sess.world().GetBlock(pos)
	info := hardnessOf(blk)
	switch status {
	case digStart:
		if info.hardness < 0 || blk == world.Air {
			return
		}
		if !withinReach(sess, pos, reachSurvival) {
			return
		}
		bname, _ := world.StateOwner(blk)
		need := s.digTicks(info, bname, sess)
		if need <= 1 {
			// destroyProgress >= 1 on the first tick of a dig means the
			// block breaks on the start action itself, no finish needed.
			s.breakBlock(sess, pos, info)
			s.emitLightUpdate(sess.dimension(), pos)
			return
		}
		sess.digMu.Lock()
		restart := sess.digActive
		restartPos := sess.digPos
		sess.digPos = pos
		sess.digBlock = blk
		sess.digTicks = 0
		sess.digActive = true
		sess.digMu.Unlock()
		if restart {
			// A start while another dig was latched answers with a
			// resync of the block that was being destroyed.
			s.sendBlockResync(sess, restartPos)
		}
	case digCancel:
		sess.digMu.Lock()
		sess.digActive = false
		sess.digTicks = 0
		sess.digMu.Unlock()
	case digFinish:
		sess.digMu.Lock()
		if !sess.digActive || sess.digPos != pos {
			sess.digMu.Unlock()
			return
		}
		bname, _ := world.StateOwner(sess.digBlock)
		need := s.digTicks(info, bname, sess)
		if sess.digTicks+1 >= need {
			sess.digActive = false
			sess.digTicks = 0
			sess.digMu.Unlock()
			s.breakBlock(sess, pos, info)
			s.emitLightUpdate(sess.dimension(), pos)
			return
		}
		// Stopped early: arm the delayed destroy on the same tick
		// counter — it still breaks the block once the total reaches
		// the required ticks.
		if !sess.digDelayed {
			sess.digDelayed = true
			sess.digDelayedPos = pos
			sess.digDelayedTicks = sess.digTicks
		}
		sess.digActive = false
		sess.digTicks = 0
		sess.digMu.Unlock()
	}
	return
}

// sendBlockResync tells one client what a block really is, vanilla's
// answer to a block action the server rejected.
func (s *Server) sendBlockResync(sess *Session, pos world.Pos) {
	blk := sess.world().GetBlock(pos)
	_ = sess.sendProjectedBlockUpdate(pos, blk)
}

// digTickStep advances digging by one tick per playing session. As in
// vanilla, the loop only counts progress: a live dig never breaks here,
// but a delayed destroy armed by an early stop breaks once its tick
// total crosses the requirement (hasDelayedDestroy in
// ServerPlayerGameMode.tick).
func (s *Server) digTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if !sess.gameplayTickReady() || sess.dead || sess.mode() != GameModeSurvival {
			return true
		}
		sess.digMu.Lock()
		if sess.digDelayed {
			sess.digDelayedTicks++
			pos := sess.digDelayedPos
			ticks := sess.digDelayedTicks
			sess.digMu.Unlock()
			cur := sess.world().GetBlock(pos)
			if cur != world.Air {
				cname, _ := world.StateOwner(cur)
				// Vanilla re-reads the block state and the held tool
				// every tick, so a swap changes when it fires.
				if need := s.digTicks(hardnessOf(cur), cname, sess); ticks+1 >= need {
					s.breakBlock(sess, pos, hardnessOf(cur))
					s.emitLightUpdate(sess.dimension(), pos)
					sess.digMu.Lock()
					sess.digDelayed = false
					sess.digMu.Unlock()
				}
			} else {
				sess.digMu.Lock()
				sess.digDelayed = false
				sess.digMu.Unlock()
			}
			sess.digMu.Lock()
		}
		if !sess.digActive {
			sess.digMu.Unlock()
			return true
		}
		pos := sess.digPos
		sess.digTicks++
		sess.digMu.Unlock()
		if sess.world().GetBlock(pos) == world.Air {
			// The block under the pick is already gone.
			sess.digMu.Lock()
			sess.digActive = false
			sess.digMu.Unlock()
		}
		return true
	})
}

// breakBlock removes a block and spawns its drops.
func (s *Server) breakBlock(sess *Session, pos world.Pos, info blockHardness) {
	broken := sess.world().GetBlock(pos)
	if broken == world.Air {
		return
	}
	// BeehiveBlock.playerDestroy: a non-silk break empties the hive
	// EMERGENCY. The occupant count dies with the cell, so capture it
	// before the write; the release itself lands after the drops.
	nestBees := int32(0)
	if isBeeNestBlock(broken) {
		// The write below deletes the occupant record — the silk
		// drop's copy_components and the non-silk release both read
		// the count captured here.
		nestBees = sess.dimension().bees[pos]
	}
	// The same pre-write capture for the block entity's CustomName:
	// a break drops the block-item via the loot table's
	// copy_components[custom_name], which reads the dead cell's name.
	beName := sess.dimension().beNames[pos]
	// JukeboxBlock's contents drop with the block (popOutTheItem's
	// destroy arm): capture the disc + stop the song before the
	// write kills the block entity's pos record.
	if name, _ := world.StateOwner(broken); name == "jukebox" {
		s.jukeboxDrop(sess.dimension(), pos)
	}
	s.fireBeeNestDestroyed(sess, sess.dimension(), pos, broken)
	s.setBlockAndBroadcast(sess.getName(), pos, world.Air)
	// ServerLevel.destroyBlock posts BLOCK_DESTROY at the broken
	// position with the old state as context — wool muffles it.
	oldName, _ := world.StateOwner(broken)
	s.emitGameEvent(sess.dimension(), "block_destroy",
		float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5,
		sess, strings.TrimPrefix(oldName, "minecraft:"))
	if nestBees > 0 && sess.heldStack().enchLevel("silk_touch") <= 0 {
		defer s.beeNestDestroyed(sess.dimension(), pos, broken, nestBees, sess)
	}
	// Vanilla's destroyBlock wears the held tool and counts the use
	// (ItemStack.mineBlock) before the break statistics land
	// (Block.playerDestroy), so the used stat precedes mined here.
	s.mineWear(sess, info)
	sess.addBlockStat(statMined, broken, 1)
	s.iceMeltOnDestroy(sess, sess.dimension(), pos, broken)
	// A shulker box drops itself with everything inside as one stack
	// (see removeContainer), so the plain block item would duplicate it.
	if isShulkerBoxBlock(broken) {
		return
	}
	bname, _ := world.StateOwner(broken)
	// Vanilla gates getDrops on hasCorrectToolForDrops: a
	// requires-tool block drops nothing without a matching tool.
	if blockRequiresTool[bname] && !toolCorrectForDrops(sess.heldStack(), bname) {
		return
	}
	if _, ok := data.LootTables()["blocks/"+bname]; ok {
		for _, st := range rollBlockLoot("blocks/"+bname, sess, pos, bname, broken, nestBees, beName) {
			s.spawnItemDrop(sess.dimName(), pos, st)
		}
		return
	}
	// Blocks outside the generated table set keep the hand-curated
	// drop column (e.g. monster spawner: no table, no drops).
	if info.drop == "" {
		return
	}
	id, ok := world.ItemID(info.drop)
	if !ok {
		return
	}
	s.spawnItemDrop(sess.dimName(), pos, ItemStack{ID: id, Count: 1})
	return
}

// iceMeltOnDestroy is IceBlock.playerDestroy's tail — it runs only on
// the survival path (vanilla's destroyBlock returns before
// playerDestroy for players whose mode prevents drops). An ice or
// frosted_ice broken without silk touch refills the cell with water
// when the block below could hold it (blocksMotion or a liquid); in a
// WATER_EVAPORATES dimension the cell stays empty.
func (s *Server) iceMeltOnDestroy(sess *Session, d *dimension, pos world.Pos, broken world.Block) {
	name, _ := world.StateOwner(broken)
	if name != "ice" && name != "frosted_ice" {
		return
	}
	if sess.heldStack().enchLevel("silk_touch") > 0 {
		return
	}
	if d.ultraWarm {
		return
	}
	below := d.w.GetBlock(posBelow(pos, 1))
	if _, _, isFluid := fluidOf(below); !isFluid && world.NonSolid(below) {
		return
	}
	if w, ok := world.BlockStateOf("water", nil); ok {
		s.setNaturalBlock(d, pos, w)
	}
}

// spawnItemDrop is Block.popResource: the item entity spawns at the
// cell center scattered ±0.25 on each axis (item height 0.25 halves
// out of the y), tossed on a (rand·0.2−0.1, 0.2, rand·0.2−0.1)
// delta — the drift is what lets neighbour merges chain.
func (s *Server) spawnItemDrop(dim string, pos world.Pos, stack ItemStack) *Entity {
	if stack.Empty() {
		return nil
	}
	itemType, ok := EntityTypeID("item")
	if !ok {
		return nil
	}
	e := s.entities.Spawn(&Entity{
		Type:        itemType,
		Dim:         dim,
		X:           float64(pos.X) + 0.5 + rand.Float64()*0.5 - 0.25,
		Y:           float64(pos.Y) + 0.5 + rand.Float64()*0.5 - 0.25 - 0.125,
		Z:           float64(pos.Z) + 0.5 + rand.Float64()*0.5 - 0.25,
		VY:          0,
		MotionX:     rand.Float64()*0.2 - 0.1,
		MotionY:     0.2,
		MotionZ:     rand.Float64()*0.2 - 0.1,
		Item:        stack,
		PickupDelay: 10,
	})
	s.broadcastEntitySpawn(nil, *e)
	s.broadcastEntityMetadata(e.ID, encodeItemMetadata(e.Item))
	return e
}

// encodeItemMetadata builds the Set Entity Data body for an item
// entity: index 8, type 7 (slot), the stack, terminator.
func encodeItemMetadata(stack ItemStack) []byte {
	var b protocol.Buffer
	b.Byte(8)
	b.Byte(7)
	writeSlot(&b, stack)
	b.Byte(0xFF)
	return b.Bytes()
}

// broadcastEntityMetadata fans one metadata payload out to the spawned
// sessions in the entity's dimension.
func (s *Server) broadcastEntityMetadata(eid int32, payload []byte) {
	if eid == 0 || payload == nil {
		return
	}
	dim := dimOverworld
	entityType := int32(-1)
	if e, ok := s.entities.Get(eid); ok {
		dim = e.Dim
		entityType = e.Type
	}
	var b protocol.Buffer
	b.VarInt(eid)
	b.Raw(payload)
	body := b.Bytes()
	cache := javaEntityBodyCache{}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		if sess.dimName() != dim {
			return true
		}
		if !sess.tracksEntity(eid) && sess.entityID.Load() != eid {
			return true
		}
		_ = cache.writeMetadata(sess, entityType, body)
		return true
	})
}

// itemTickStep advances item entities one tick: gravity, pickup delay,
// experience-orb pickup, and position sync. Regular item touches run
// on the player connection tick, including while the world is frozen.
func (s *Server) itemTickStep() {
	orbType := mustEntityType("experience_orb")
	itemType := mustEntityType("item")
	s.entities.mu.RLock()
	items := make([]*Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		// Item physics belong to ItemEntity + XP orbs alone — a
		// thrown projectile (egg/snowball/potion) also carries an
		// Item field for rendering but flies on projectile rules.
		if e.Type == itemType || e.Type == orbType {
			items = append(items, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range items {
		if _, ok := s.entities.GetPtr(e.ID); !ok {
			continue // removed earlier in this pass (merge victim)
		}
		w := s.dimWorld(e.Dim)
		if w == nil {
			continue
		}
		orb := e.Type == orbType
		if !orb {
			e.TickCount++
			if e.FireTicks > 0 {
				if itemFireImmune(e) {
					e.FireTicks = 0
				} else {
					_, inLava, _, _, _, _ := entityFluidScan(w, e.X, e.Y, e.Z, 0.125, 0.25, fluidLava)
					if e.FireTicks%20 == 0 && !inLava {
						s.damageItemEntity(e, 1, "on_fire", 0)
					}
					e.FireTicks--
				}
			}
			if _, ok := s.entities.GetPtr(e.ID); !ok {
				continue
			}
		}
		if e.FakeItem {
			// makeFakeItem ages to 5999 so the entity discards on its
			// first tick.
			s.entities.Remove(e.ID)
			s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
			continue
		}
		if e.PickupDelay > 0 && (orb || e.PickupDelay != 32767) {
			e.PickupDelay--
		}
		// ItemEntity.tick snapshots getDeltaMovement at the head of the
		// tick; the tail flags needsSync when the net change exceeds 0.1.
		preDX, preDY, preDZ := e.MotionX, e.MotionY, e.MotionZ
		prevX, prevZ := e.X, e.Z
		// ItemEntity.tick's noPhysics branch: when the entity box —
		// shrunk by 1e-7 — overlaps a collision shape, vanilla calls
		// moveTowardsClosestSpace on the box center (not the feet cell)
		// before the item advances through its noPhysics move path.
		// An entity resting on a shape top
		// merely touches, so it never lands here.
		halfBox, heightBox := 0.125, 0.25
		if orb {
			halfBox, heightBox = 0.25, 0.5
		}
		inside := false
		if orb {
			inside = s.entityBoxInsideWorldCollision(s.dims[e.Dim], e.X, e.Y, e.Z, halfBox, heightBox)
		} else {
			inside = s.entityBoxInsideWorldCollision(s.dims[e.Dim], e.X, e.Y, e.Z, halfBox, heightBox)
			e.itemNoPhysics = inside
		}
		if inside && orb {
			s.setEmbeddedWorldEscapeVelocity(s.dims[e.Dim], e, heightBox)
			e.PosDirty = true
			e.NeedsSync = true
			s.moveExperienceOrb(e, w)
		}
		if orb && !inside {
			// ExperienceOrb.tick: Entity.tick's updateFluidInteraction
			// runs the current push, then the eye-in-water branch
			// replaces gravity — the orb formula damps xz 0.99 and
			// caps the rise at y 0.06; otherwise gravity (0.03).
			entityFluidTick(w, s.dims[e.Dim], e, 0.25, 0.5)
			_, eyeInWater, _, _, _, _ := entityFluidScan(
				w, e.X, e.Y+0.425, e.Z, 0, 0, fluidWater)
			if eyeInWater {
				e.MotionX *= 0.99
				e.MotionY = math.Min(e.MotionY+0.0005, 0.06)
				e.MotionZ *= 0.99
			} else if !e.NoGravity {
				e.MotionY -= 0.03
			}
			if e.OrbCount < 1 {
				e.OrbCount = 1 // orbs always carry a count of one from spawn
			}
			// scanForMerges: tickCount % 20 == 1 — self absorbs every
			// orb whose box reaches the inflated-0.5 scan box, age
			// goes to the pair's minimum.
			if e.ItemAge%20 == 1 {
				s.orbScanForMerges(e, items)
			}
			// followNearbyPlayer: keep the latched player while
			// non-spectator and within 8, else rescan for the nearest
			// non-spectator live player in 8; the pull is normalize
			// (dir to eye-centre) * (1-d/8)^2 * 0.1 onto motion.
			if t := e.OrbFollowing; t == 0 || !s.orbFollowingOK(e, t) {
				e.OrbFollowing = 0
				if p := s.orbNearestPlayer(e); p != nil {
					e.OrbFollowing = p.entityID.Load()
				}
			}
			if e.OrbFollowing != 0 {
				if p := s.sessionByEntityID(e.OrbFollowing); p != nil {
					px, py, pz, _, _, _ := p.Pos()
					eye := s.commandSourceEyeHeight(&dataEntity{sess: p})
					dx, dy, dz := px-e.X, py+eye/2-e.Y, pz-e.Z
					l := math.Sqrt(dx*dx + dy*dy + dz*dz)
					if l > 0 {
						k := 1 - l/8
						m := k * k * 0.1 / l
						e.MotionX += dx * m
						e.MotionY += dy * m
						e.MotionZ += dz * m
					}
				}
			}
			s.moveExperienceOrb(e, w)
		} else if !orb {
			// ItemEntity.tick: Entity.tick's updateFluidInteraction ran
			// its current push; then the submersion branch replaces
			// gravity — deeper than 0.1 in water or lava applies the
			// fluid formula (xz damped 0.95, y lifted +0.0005 while
			// under 0.06), else gravity (0.04) → the rest-skip gate →
			// move → applyEffectsFromBlocks → delta *= (f3, airDrag,
			// f3) where f3 is airDrag times the supporting block's
			// friction when grounded → the grounded rest-bounce: a
			// residual downward motion flips to -0.5×.
			wDepth, inW, lDepth, inL := entityFluidTick(
				w, s.dims[e.Dim], e, 0.125, 0.25)
			submerged := inW && wDepth > 0.10000000149011612 ||
				inL && lDepth > 0.10000000149011612
			if submerged {
				e.MotionX *= 0.95
				if e.MotionY < 0.05999999865889549 {
					e.MotionY += 0.0005
				}
				e.MotionZ *= 0.95
			} else if !e.NoGravity {
				e.MotionY -= 0.04
			}
			// Gravity/fluid acceleration precedes closest-space damping.
			if inside {
				s.setEmbeddedWorldEscapeVelocity(s.dims[e.Dim], e, heightBox)
			}
			resting := e.Grounded &&
				e.MotionX*e.MotionX+e.MotionZ*e.MotionZ <= 1e-5 &&
				(e.ItemAge+e.ID)%4 != 0
			if !resting {
				if inside {
					// Entity.move's noPhysics path advances without collision.
					e.X += e.MotionX
					e.Y += e.MotionY
					e.Z += e.MotionZ
				} else {
					s.entityMove(e, w, 0.125, 0.25, e.MotionX, e.MotionY, e.MotionZ, .04, float64(float32(.98)), false)
				}
				f3 := float32(.98)
				if e.Grounded {
					f3 *= float32(entityFrictionBelow(w, e.X, e.Y, e.Z))
				}
				e.MotionX *= float64(f3)
				e.MotionY *= float64(float32(.98))
				e.MotionZ *= float64(f3)
				if e.Grounded && e.MotionY < 0 {
					e.MotionY = -e.MotionY * 0.5
				}
			}
		}
		if !orb {
			// ItemEntity.tick's tail runs updateFluidInteraction a
			// second time — the current push applies again, and being
			// in any fluid sets needsSync directly.
			_, inW2, _, inL2 := entityFluidTick(
				w, s.dims[e.Dim], e, 0.125, 0.25)
			if inW2 || inL2 {
				e.PosDirty = true
				e.NeedsSync = true
			}
			// ItemEntity.tick's intra-tick needsSync: a >0.1 net delta
			// change bypasses the updateInterval gate and emits now.
			ddx, ddy, ddz := e.MotionX-preDX, e.MotionY-preDY, e.MotionZ-preDZ
			if ddx*ddx+ddy*ddy+ddz*ddz > 0.01 {
				e.PosDirty = true
				e.NeedsSync = true
			}
		}
		// Item entities and XP orbs sync through the same ServerEntity
		// pipeline as mobs — updateInterval 20 — not a teleport every
		// moved tick.
		s.mobSyncFlush(e)
		// ItemEntity.mergeWithNeighbours: every 2 ticks when the item
		// crossed a block boundary this tick, else every 40 — the
		// bigger stack absorbs the smaller into its own entity. An
		// empty stack is discarded outright (ItemEntity.tick's
		// isEmpty -> discard) — continuing here would leave a ghost
		// entity that never ages and never despawns.
		if !orb && e.Item.Empty() {
			s.entities.Remove(e.ID)
			s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
			continue
		}
		if !orb && itemMergable(e) {
			interval := int32(40)
			if int(math.Floor(e.X)) != int(math.Floor(prevX)) ||
				int(math.Floor(e.Z)) != int(math.Floor(prevZ)) {
				interval = 2
			}
			if e.ItemAge%interval == 0 {
				s.itemMergeWithNeighbours(e, items)
				if e.Item.Empty() {
					continue // absorbed into a neighbour
				}
			}
		}
		// ItemEntity.tick tail: age advances each tick and the item
		// discards at 6000 (five minutes).
		if orb || e.ItemAge != -32768 {
			e.ItemAge++
		}
		if e.ItemAge >= 6000 {
			s.entities.Remove(e.ID)
			s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
			continue
		}
	}
}

// itemMergable is ItemEntity.isMergable: alive, unpinned pickup delay,
// under the despawn age, and the stack has room.
func itemMergable(e *Entity) bool {
	return !e.Item.Empty() && e.PickupDelay != 32767 &&
		e.ItemAge < 6000 && e.Item.Count < maxStackOfID(e.Item.ID)
}

// itemMergeWithNeighbours is ItemEntity.mergeWithNeighbours: every
// mergable item entity whose bounding box inflated (0.5,0,0.5) reaches
// this one's gets tried — same item+components, combined count inside
// the stack cap, and an owner match (setTarget) — then the larger
// stack absorbs the smaller, ties going to the neighbour; the survivor
// takes max(pickupDelay) and min(age), the loser discards.
func (s *Server) itemMergeWithNeighbours(e *Entity, items []*Entity) {
	for _, o := range items {
		if o == e || !itemMergable(o) || o.Dim != e.Dim {
			continue
		}
		// inflate(0.5,0,0.5) on the 0.25-wide/tall item box reaches
		// neighbours ~0.75 across and half a block vertically.
		if math.Abs(o.X-e.X) > 0.75 || math.Abs(o.Z-e.Z) > 0.75 || math.Abs(o.Y-e.Y) > 0.5 {
			continue
		}
		if o.HasOwner != e.HasOwner || o.Owner != e.Owner {
			continue
		}
		if !o.Item.Stackable(e.Item) || o.Item.Count+e.Item.Count > maxStackOfID(e.Item.ID) {
			continue
		}
		keep, gone := e, o
		if o.Item.Count < e.Item.Count {
			keep, gone = e, o
		} else {
			keep, gone = o, e
		}
		keep.Item.Count += gone.Item.Count
		gone.Item = ItemStack{}
		keep.PickupDelay = max(keep.PickupDelay, gone.PickupDelay)
		keep.ItemAge = min(keep.ItemAge, gone.ItemAge)
		s.entities.Remove(gone.ID)
		s.broadcastEntityRemoveIn(e.Dim, nil, []int32{gone.ID})
		s.broadcastEntityMetadata(keep.ID, encodeItemMetadata(keep.Item))
		if gone == e {
			return // self absorbed — this entity is gone
		}
	}
}

// orbScanForMerges is ExperienceOrb.scanForMerges: each orb in this
// one's bounding box inflated by 0.5 is absorbed into self — count
// adds, age takes the minimum, the neighbour discards — but only for
// the merge bucket (orb.id - self.id) % 40 == 0 with equal values.
func (s *Server) orbScanForMerges(e *Entity, items []*Entity) {
	orbType := mustEntityType("experience_orb")
	for _, o := range items {
		if o == e || o.Type != orbType || o.Dim != e.Dim {
			continue
		}
		if (o.ID-e.ID)%40 != 0 || o.OrbValue != e.OrbValue {
			continue
		}
		if math.Abs(o.X-e.X) > 0.75 || math.Abs(o.Y-e.Y) > 0.75 || math.Abs(o.Z-e.Z) > 0.75 {
			continue
		}
		if _, ok := s.entities.GetPtr(o.ID); !ok {
			continue // canMerge's !other.isRemoved() gate
		}
		e.OrbCount += o.OrbCount
		e.ItemAge = min(e.ItemAge, o.ItemAge)
		s.entities.Remove(o.ID)
		s.broadcastEntityRemoveIn(e.Dim, nil, []int32{o.ID})
	}
}

// repairPlayerItems is ExperienceOrb.repairPlayerItems: pick a random
// damaged equipment item carrying mending (the REPAIR_WITH_XP slot set
// is both hands plus the four armor slots), repair
// min(value*durabilityPerXp, damage) — mending's component repairs 2
// durability per xp point — and recurse with what the orb has left.
// The slot update rides the same container_set_slot vanilla's
// inventory broadcast sends.
func (s *Server) repairPlayerItems(sess *Session, value int32) int32 {
	var candidates []int16
	for _, slot := range []int16{
		invSlotForHotbar(sess.held.Load()), slotOffhand,
		slotArmorStart, slotArmorStart + 1, slotArmorStart + 2, slotArmorStart + 3,
	} {
		it := sess.stackAt(slot)
		if !it.Empty() && it.Damage > 0 && it.enchLevel("mending") > 0 {
			candidates = append(candidates, slot)
		}
	}
	if len(candidates) == 0 {
		return value
	}
	slot := candidates[rand.Intn(len(candidates))]
	it := sess.stackAt(slot)
	modified := value * 2
	repaired := modified
	if it.Damage < repaired {
		repaired = it.Damage
	}
	it.Damage -= repaired
	sess.setStack(slot, it)
	if repaired <= 0 {
		return 0
	}
	sess.invState.Add(1)
	_ = s.sendContainerSlot(sess, slot, it)
	if remaining := value - repaired*value/modified; remaining > 0 {
		return s.repairPlayerItems(sess, remaining)
	}
	return 0
}

// orbFollowingOK mirrors the latch keep condition: the followed player
// stays while non-spectator and within 8 blocks (squared 64).
func (s *Server) orbFollowingOK(e *Entity, playerEID int32) bool {
	p := s.sessionByEntityID(playerEID)
	if p == nil || p.mode() == GameModeSpectator || p.dead {
		return false
	}
	px, py, pz, _, _, _ := p.Pos()
	dx, dy, dz := px-e.X, py-e.Y, pz-e.Z
	return dx*dx+dy*dy+dz*dz <= 64.0
}

// orbNearestPlayer is Level.getNearestPlayer(orb, 8.0): the nearest
// non-spectator, non-dead player within 8 blocks of the orb.
func (s *Server) orbNearestPlayer(e *Entity) *Session {
	var best *Session
	bestSq := 64.0
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() || sess.dead {
			return true
		}
		if sess.dimName() != e.Dim || sess.mode() == GameModeSpectator {
			return true
		}
		px, py, pz, _, _, _ := sess.Pos()
		d := (px-e.X)*(px-e.X) + (py-e.Y)*(py-e.Y) + (pz-e.Z)*(pz-e.Z)
		if d < bestSq {
			bestSq = d
			best = sess
		}
		return true
	})
	return best
}

// defaultHealth/defaultFood are vanilla survival starting values.
const (
	defaultHealth     = 20
	defaultFood       = 20
	defaultSaturation = 5
)

// initSurvival initializes a joining player's survival state.
func (sess *Session) initSurvival() {
	// A first join writes previousPlayerGameType -1 no matter the
	// server's default mode; seeding is not a switch.
	sess.prevGameMode = -1
	sess.gameMode = sess.server.GameMode
	sess.health = defaultHealth
	sess.food = defaultFood
	sess.saturation = defaultSaturation
	sess.dead = false
	sess.fallDistance = 0
	sess.airSupply = 300
	// Vanilla entities spawn with -20 remainingFireTicks — the
	// unlit baseline, not zero — and /data reads it back verbatim.
	sess.fireTicks = -20
}

// sendHealth syncs the current health, food, and saturation.
func (s *Server) sendHealth(sess *Session) error {
	return s.sendSetHealth(sess, sess.health, sess.food, sess.saturation)
}

// damage applies damage and syncs health; a lethal hit runs
// the death flow. Creative and spectator players are immune except to
// the bypasses_invulnerability types (void, /kill) — adventure players
// are hurt normally.
func (s *Server) damage(sess *Session, amount float32, message component, src damageSource) {
	if amount <= 0 || sess.dead || sessInvulnerableTo(sess, src.typeName) {
		return
	}
	// Absorption hearts soak damage before health — vanilla's absorb
	// step runs after armor/enchant reduction and before the death
	// check, so the totem only sees what survives the yellow hearts.
	if sess.absorb > 0 {
		soak := sess.absorb
		if soak > amount {
			soak = amount
		}
		sess.absorb -= soak
		amount -= soak
	}
	sess.health -= amount
	if sess.health <= 0 {
		// A totem absorbs the killing blow unless the source is in
		// bypasses_invulnerability (void, /kill).
		if !damageTypeBypassesInvulnerability(src.typeName) && s.tryTotem(sess) {
			return
		}
		sess.health = 0
		s.killPlayer(sess, message)
		return
	}
	sess.healthDirty.Store(true)
}

// killPlayer runs the death flow: health 0, combat death notice, and
// the dead state that blocks further actions until respawn.
func (s *Server) killPlayer(sess *Session, message component) {
	if sess.dead {
		return
	}
	sess.dead = true
	sess.health = 0
	// LivingEntity.die posts ENTITY_DEATH at the body's feet.
	{
		x, y, z, _, _, _ := sess.Pos()
		s.emitGameEvent(sess.dimension(), "entity_die", x, y, z, sess, "")
	}
	// Vanilla's die() award order: the deathCount score write rides
	// forAllObjectives ahead of the stats — then killed_by[killer],
	// the killer's awardKillScore, deaths, and the two time resets.
	s.bumpCriteria(sess.getName(), "deathCount", 1)
	if killer := s.entityByID(sess.lastHurtBy); killer != nil {
		sess.addStat(statKilledBy, killer.Type, 1)
	}
	// Vanilla resolves the kill credit — the last player to hurt inside
	// the 100-tick window — not the killing blow's source.
	if src := s.killCreditPlayer(sess); src != nil {
		src.addStat(statKilled, mustEntityType("player"), 1)
		// awardKillScore and its PLAYER_KILLED_ENTITY trigger skip
		// victim==killer.
		if src != sess {
			s.awardKillScore(src, sess.getName(), true)
			// player_killed_entity fires for player victims too — the
			// advancement's killed_something criterion matches any entity.
			vx, _, vz, _, _, _ := sess.Pos()
			ax, _, az, _, _, _ := src.Pos()
			s.firePlayerKilledEntity(src, "player", vx, vz, ax, az, false)
		}
	}
	// entity_killed_player is the victim-side trigger (killed_by_-
	// something on adventure/root): fires whenever an entity did the
	// killing, mob or player.
	if sess.lastHurtBy != 0 {
		vx, _, vz, _, _, _ := sess.Pos()
		if src := s.sessionByEntityID(sess.lastHurtBy); src != nil {
			ax, _, az, _, _, _ := src.Pos()
			s.fireEntityKilledPlayer(sess, "player", vx, vz, ax, az)
		} else if killer := s.entityByID(sess.lastHurtBy); killer != nil {
			if kn, ok := EntityTypeName(killer.Type); ok {
				s.fireEntityKilledPlayer(sess, kn, vx, vz, killer.X, killer.Z)
			}
		}
	}
	sess.addCustomStat("deaths", 1)
	sess.setStat(statCustom, int32(CustomStatIDs["time_since_death"]), 0)
	sess.setStat(statCustom, int32(CustomStatIDs["time_since_rest"]), 0)
	// Menus remain attached until player removal on respawn. Only the
	// equipped and stored inventory drops at death; cursor and menu inputs
	// retire later, even when keep_inventory preserves the inventory.
	if sess.mode() != GameModeSpectator && !s.gameRuleBool("minecraft:keep_inventory") {
		s.dropInventory(sess)
	}
	_ = s.sendHealth(sess)
	var b protocol.Buffer
	b.VarInt(sess.entityID.Load())
	b.Raw(message.content())
	_ = sess.write(sess.ids().PlayCombatDeath, b.Bytes())
	// Vanilla also fans the death message out as a system chat line to
	// every player (the same translatable combat_death carries).
	s.broadcastSystemChatComponent(message)
}

// dropInventory scatters every item stack at the player's death spot
// and clears the inventory, syncing the client.
func (s *Server) dropInventory(sess *Session) {
	x, y, z, _, _, _ := sess.Pos()
	sess.invMu.Lock()
	var stacks []ItemStack
	for i := slotArmorStart; i < slotCount; i++ {
		if !sess.inv[i].Empty() {
			stacks = append(stacks, sess.inv[i])
			sess.inv[i] = ItemStack{}
		}
	}
	sess.invMu.Unlock()
	for _, stack := range stacks {
		e := s.entities.Spawn(&Entity{
			Type:        mustEntityType("item"),
			Dim:         sess.dimName(),
			X:           x,
			Y:           y + 0.25,
			Z:           z,
			Item:        stack,
			PickupDelay: 40, // vanilla's death-drop pickup delay
		})
		s.broadcastEntitySpawn(nil, *e)
		s.broadcastEntityMetadata(e.ID, encodeItemMetadata(e.Item))
	}
	_ = s.sendContainerContent(sess)
}

// respawnPlayer resets survival state and re-syncs the client: the
// Respawn packet, a fresh position, health, inventory, and the chunk
// window around spawn. Vanilla respawns dead players at their spawn
// point, which lives in the overworld, so dying elsewhere also crosses
// dimensions.
func (s *Server) respawnPlayer(sess *Session) {
	// Respawn creates a fresh ServerPlayer; copied scale attributes refresh
	// its constructor bounding box on the next living tick.
	sess.collisionScale = 1
	newBodyHealth := s.resetPlayerBodyAttributes(sess)
	s.removePlayerMenus(sess)
	sess.dead = false
	sess.health = newBodyHealth
	sess.food = defaultFood
	sess.saturation = defaultSaturation
	sess.fallDistance = 0
	// Damage-source refs live on the dying entity — the respawned one
	// carries no attacker memory.
	sess.lastHurtBy = 0
	sess.lastHurtPlayerBy = [16]byte{}
	sess.lastHurtPlayerTicks = 0
	// The respawned entity is a fresh ServerPlayer — its shared flags
	// (shared flag 7 = elytra glide) don't carry over.
	if sess.fallFlying {
		sess.fallFlying = false
		s.broadcastSharedFlags(sess)
	}
	sess.fallFlyTicks = 0

	overworld := s.dims[dimOverworld]
	// A /spawnpoint (or bed/anchor) respawn wins over the world spawn;
	// it can also live in another dimension.
	sess.posMu.Lock()
	hasSpawn := sess.hasSpawnPos
	spawnPos := sess.spawnPos
	spawnYaw, spawnPitch := sess.spawnYaw, sess.spawnPitch
	spawnDim := sess.spawnDim
	sess.posMu.Unlock()
	dest := overworld
	var x, y, z float64
	yaw := float32(0)
	if hasSpawn {
		if d, ok := s.dims[spawnDim]; ok {
			dest = d
		}
		x, y, z = float64(spawnPos[0])+0.5, float64(spawnPos[1]), float64(spawnPos[2])+0.5
		yaw = spawnYaw
	} else {
		x, y, z = s.playerSpawn()
	}
	if sess.dimension() != dest {
		// Cross-dimension respawn: entity tracking, chunks, and
		// dimension bookkeeping all run through changeDimension.
		s.changeDimension(sess, dest, x, y, z, 0)
		_ = s.sendAbilities(sess)
		// changeDimension zeroes the view rotation; restore the spawn
		// point's angles on the session side for selectors/tracking.
		sess.setPos(x, y, z, spawnYaw, spawnPitch, false)
		_ = s.sendContainerContent(sess)
		return
	}

	_ = s.sendRespawn(sess, dest, 0, 0)
	_ = s.sendAbilities(sess)

	sess.setPos(x, y, z, yaw, spawnPitch, false)
	sess.trackMu.Lock()
	sess.trackedEnts = nil
	sess.trackMu.Unlock()
	_ = s.sendSyncPos(sess, x, y, z)
	_ = s.sendHealth(sess)
	_ = s.sendContainerContent(sess)
	sess.resetChunks()
	scx, scz := int(x)>>4, int(z)>>4
	s.sendCenterChunk(sess, scx, scz)
	_ = s.sendWindow(sess, scx, scz)
}

// handleClientStatus processes the client's Client Command packet.
// Action 0 (perform respawn) revives a dead player.
func (s *Server) handleClientStatus(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	action, err := r.VarInt()
	if err != nil {
		return
	}
	switch action {
	case 0: // perform respawn
		if sess.wonGame {
			s.returnThroughEnd(sess)
		} else if sess.dead {
			s.respawnPlayer(sess)
		}
	case 1: // request stats
		_ = s.sendStatistics(sess)
	case 2: // request game rule values
		_ = s.sendGameRules(sess)
	}
}

// applyFallDamage converts accumulated fall distance into damage on
// landing, using the player's current fall attributes.
func (s *Server) applyFallDamage(sess *Session) {
	// Landing block effects precede Player.causeFallDamage's mayfly gate;
	// adventure players share survival damage (26.2, JDK 25 javap).
	if sess.dead {
		sess.fallDistance = 0
		return
	}
	distance := sess.fallDistance
	sess.fallDistance = 0
	// Block.fallOn dispatches before the living damage tail. Its argument
	// multiplier is a float; retain that precision before attribute scaling.
	x, y, z, _, _, _ := sess.Pos()
	support := world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y - 0.2)), Z: int(math.Floor(z))}
	block := sess.world().GetBlock(support)
	name, _ := world.StateOwner(block)
	multiplier := float32(1)
	source := srcFall
	honey := false
	switch {
	case name == "slime_block":
		// SlimeBlock.fallOn has no damage tail in either shift-key branch;
		// suppression controls its client-side bounce, not landing damage.
		if !sess.sneaking {
			sess.awardFallDistance(distance)
		}
		return
	case name == "hay_block":
		multiplier = 0.2
	case name == "honey_block":
		honey = true
		multiplier = 0.2
		s.playSoundSrc(sess.dimName(), x, y, z, soundEventIDs["block.honey_block.slide"], 7, 1, 1, sess)
		s.broadcastEntityEvent(sess.entityID.Load(), 54)
	case strings.HasSuffix(name, "_bed"):
		distance *= 0.5
	case name == "pointed_dripstone":
		direction, _ := world.StateProperty(block, "vertical_direction")
		thickness, _ := world.StateProperty(block, "thickness")
		if direction == "up" && thickness == "tip" {
			distance += 2.5
			multiplier = 2
			source = damageSource{typeName: "stalagmite"}
		}
	case name == "powder_snow":
		if distance >= 4 {
			sound := int32(1318)
			if distance >= 7 {
				sound = 1308
			}
			s.playSoundSrc(sess.dimName(), x, y, z, sound, 7, 1, 1, sess)
		}
		return
	}
	if sess.mode() != GameModeSurvival && sess.mode() != GameModeAdventure {
		return
	}
	sess.awardFallDistance(distance)
	dmg := sess.playerFallDamageScaled(distance, multiplier)
	if dmg == 0 {
		return
	}
	snd := int32(1318)
	if dmg > 4 {
		snd = 1308
	}
	s.playSoundSrc(sess.dimName(), x, y, z, snd, 7, 1, 1, sess)
	if sound, ok := blockFallSounds[name]; ok {
		volume, pitch := float32(1), float32(1)
		if scale, ok := blockFallSoundScale[name]; ok {
			volume, pitch = scale[0], scale[1]
		}
		s.playSoundSrc(sess.dimName(), x, y, z, sound, 7, volume*0.5, pitch*0.75, sess)
	}
	message := translatable("death.fell.accident.generic", literal(sess.getName()))
	if source.typeName == "stalagmite" {
		message = translatable("death.attack.stalagmite", literal(sess.getName()))
	}
	s.damageWith(sess, float32(dmg), message, source)
	if honey {
		if sound, ok := blockFallSounds["honey_block"]; ok {
			s.playSoundSrc(sess.dimName(), x, y, z, sound, 7, 0.5, 0.75, sess)
		}
	}

}

// addExhaustion accumulates activity exhaustion. The saturation/food
// spend does not happen here: vanilla drains it at the top of the next
// food tick, which is what hungerTickStep does.
func (sess *Session) addExhaustion(amount float32) {
	if amount > 0 {
		sess.exhaustion += amount
	}
}

// hungerTickStep advances the hunger simulation one player tick:
// natural regeneration runs on two vanilla clocks — a heal of
// min(saturation,6)/6 every ten ticks while saturation lasts and the
// player is full, else one health every eighty ticks once food is at
// least eighteen — and starvation lands one damage every eighty ticks
// while empty. All of it sits under the natural_health_regeneration
// game rule for the heal side.
func (s *Server) hungerTickStep() {
	regen := s.gameRuleBool("minecraft:natural_health_regeneration")
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if !sess.gameplayTickReady() || sess.dead {
			return true
		}
		if sess.takeXpDelay > 0 {
			sess.takeXpDelay--
		}
		if m := sess.mode(); m != GameModeSurvival && m != GameModeAdventure {
			return true
		}
		changed := false
		// Native FoodData.tick spends at most one exhaustion budget per
		// player frame, strictly above four. A positive saturation pool
		// pays the budget and clamps at zero without spilling into food. The exhaustion a regen heal adds drains on
		// the NEXT tick, so its health packet still carries the old
		// saturation.
		if sess.exhaustion > 4 {
			sess.exhaustion -= 4
			if sess.saturation > 0 {
				sess.saturation = max(0, sess.saturation-1)
			} else if sess.food > 0 && s.difficulty != DifficultyPeaceful {
				sess.food--
			}
			changed = true
		}
		hurt := sess.health < s.playerMaxHealth(&sess.playerRecord)
		if regen && hurt && sess.food >= 20 && sess.saturation > 0 {
			sess.foodTickTimer++
			if sess.foodTickTimer >= 10 {
				sess.foodTickTimer = 0
				amount := min(sess.saturation, 6)
				sess.healPlayer(amount / 6)
				sess.addExhaustion(amount)
				changed = true
			}
		} else if regen && hurt && sess.food >= 18 {
			sess.foodTickTimer++
			if sess.foodTickTimer >= 80 {
				sess.foodTickTimer = 0
				sess.healPlayer(1)
				sess.addExhaustion(6)
				changed = true
			}
		} else {
			sess.foodTickTimer = 0
		}
		if sess.food == 0 && sess.health > 1 {
			sess.starveTimer++
			if sess.starveTimer >= 80 {
				sess.starveTimer = 0
				s.damageWith(sess, 1, translatable("death.attack.starve", literal(sess.getName())), srcStarve)
			}
		} else {
			sess.starveTimer = 0
		}
		if changed {
			sess.healthDirty.Store(true)
		}
		return true
	})
}

// playerFluidTickStep runs the player's fluid half of the living tick
// once per connection tick: LivingEntity.baseTick's water-air pass,
// then Entity.checkInsideBlocks' fluid cells — lava ignites and hits
// four, water extinguishes, fire blocks ignite and hit in_fire. Air
// changes resend metadata index 1 to every tracker, self included.
func (s *Server) playerFluidTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dead || !sess.spawned.Load() {
			return true
		}
		d := sess.dimension()
		if d == nil {
			return true
		}
		x, y, z, _, _, onGround := sess.Pos()
		// The player entity's tickCount — vanilla increments it in
		// Entity.baseTick ahead of the living pass below.
		sess.entityTicks++
		// LivingEntity.aiStep clears accumulated descent while either effect
		// is active, before travel. Packet movement can accumulate again
		// between ticks, so this is deliberately an entity-tick reset.
		sess.effMu.Lock()
		_, slow := sess.effects[effectSlowFalling]
		_, levitating := sess.effects[effectLevitation]
		sess.effMu.Unlock()
		if slow || levitating {
			sess.fallDistance = 0
		}

		// Entity.baseTick's onBelowWorld: one four-damage hit per
		// entity tick under the out-of-world band; the post-hit grace
		// paces the visible damage to roughly every ten.
		if y < float64(d.minY-64) {
			s.damageWith(sess, 4, translatable("death.attack.outOfWorld",
				playerChatComponent(sess)), srcVoid)
		}
		// LivingEntity.baseTick's air block: an eye-level water cell
		// (a bubble column counts as out-of-water here) drains
		// airSupply one per tick while the player can't breathe under
		// water — creative and spectator count as abilities.invulnerable
		// — while the refilling effects tick it back up instead.
		// Reaching -20 resets the supply to zero with the
		// drown-particle event (67) and 2 drown damage. Everywhere else
		// it climbs back by four up to the 300 max. decreaseAirSupply's
		// oxygen_bonus roll is unmodelled — players only get that
		// attribute from respiration, which has no pipeline yet.
		eye := 1.62
		if sess.sneaking {
			eye = 1.27
		}
		if sess.fallFlying {
			eye = 0.4
		}
		epos := world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y + eye)), Z: int(math.Floor(z))}
		eb := d.w.GetBlock(epos)
		kind, _, fluid := fluidOf(eb)
		eyeWater := fluid && kind == fluidWater
		eyeBubble := false
		if bn, ok := world.StateOwner(eb); ok && strings.TrimPrefix(bn, "minecraft:") == "bubble_column" {
			eyeBubble = true
		}
		// Entity.baseTick's fire pass: each multiple of twenty ticks
		// deals a point of on_fire damage (armor + fire_protection
		// still apply) and the flags byte's on-fire bit drops at zero.
		// It runs for every mode — the hit itself is invulnerability-
		// gated inside damageWith, while the countdown isn't. The
		// isInLava check skips the on_fire tick while inside lava:
		// the four-damage lava hit covers it there.
		if sess.fireTicks > 0 {
			if sess.fireTicks%20 == 0 && !sess.inLava {
				s.damageWith(sess, 1, translatable("death.attack.onFire", literal(sess.getName())), damageSource{typeName: "on_fire"})
			}
			sess.fireTicks--
			if sess.fireTicks <= 0 {
				sess.fireTicks = 0
				s.broadcastSharedFlags(sess)
			}
		}
		// ServerPlayer.tick's grace counters are entity-tick business:
		// they decay under sprint and step and keep running while the
		// world is frozen, exactly like the air pass beside them.
		if sess.invulnTicks > 0 {
			sess.invulnTicks--
		}
		if sess.lastHurtPlayerTicks > 0 {
			sess.lastHurtPlayerTicks--
		}
		air := sess.airSupply
		if eyeWater && !eyeBubble {
			invulnerable := sess.mode() == GameModeCreative || sess.mode() == GameModeSpectator
			refill := sessMobHasEff(sess, "water_breathing") ||
				sessMobHasEff(sess, "conduit_power") ||
				sessMobHasEff(sess, "breath_of_the_nautilus")
			if !invulnerable && !refill {
				air--
				if air <= -20 {
					air = 0
					s.broadcastEntityEvent(sess.entityID.Load(), 67)
					s.damageWith(sess, 2, translatable("death.attack.drown", literal(sess.getName())), srcDrown)
				}
			} else if air < 300 && refill {
				air = min(air+4, 300)
			}
		} else if air < 300 {
			air = min(air+4, 300)
		}
		if air != sess.airSupply {
			sess.airSupply = air
			if pe, ok := s.entities.GetPtr(sess.entityID.Load()); ok {
				pe.AirSupply = int16(air)
			}
			s.broadcastAirSupply(sess, air)
		}
		// ServerPlayer.doTick tails with the read-only criteria poll:
		// health/food/air/armor/xp/level each carry a last-recorded
		// snapshot and write one score packet per objective on change.
		s.criteriaFeed(sess, "health", "food", "air", "armor", "xp", "level")
		// aiStep's travel->move chain feeds the inside-block scan every
		// entity tick, not just on client packets — a player standing
		// still in lava still burns (measured: ~4 hits per 30 ticks).
		// The invulnerability window paces the hits; each accepted move
		// packet scans once more on its own.
		s.playerInsideBlocks(sess)
		// applyEffectsFromBlocks' stepOn arm: the support block's
		// stepOn runs every grounded tick on the getOnPosLegacy cell
		// (0.2 below the feet) — TurtleEggBlock.stepOn rolls
		// nextInt(100) == 0 unless the player is stepping carefully.
		if d != nil && onGround && !sess.sneaking {
			onPos := world.Pos{
				X: int(math.Floor(x)),
				Y: int(math.Floor(y - 0.2)),
				Z: int(math.Floor(z)),
			}
			if onName, _ := blockNameAt(d, onPos); onName == "turtle_egg" {
				s.turtleEggDestroy(d, onPos, nil, sess, 100)
			}
		}
		// LivingEntity.baseTick's suffocation arm: isInWall scans the
		// eye plane (a width*0.8 box one microblock tall, centered on
		// the eye position) for a non-air block that isSuffocating —
		// opaque full cubes only, slabs/leaves/glass don't count.
		if s.playerInWall(sess, d, x, y, z, eye) {
			s.damageWith(sess, 1, translatable("death.attack.inWall",
				playerChatComponent(sess)), srcInWall)
		}
		// Player freezing follows the admitted player frame, including while
		// world age is frozen. The living pass follows the inside pass above.
		if pe, ok := s.entities.GetPtr(sess.entityID.Load()); ok {
			s.playerPowderTick(sess, pe)
			if pe.TicksFrozen >= 140 && playerCanFreeze(sess) && sess.entityTicks%40 == 0 {
				s.damageWith(sess, 1, translatable("death.attack.frozen",
					playerChatComponent(sess)), srcFreeze)
			}
			// LivingEntity.baseTick's frost pass: the record's
			// MOVEMENT_SPEED gains -0.05*percentFrozen while frozen on
			// a non-air on-block; a change syncs to the client via
			// update_attributes.
			s.frostPass(pe, d.w)
			pm := pe.AttrMods["movement_speed"]
			mod, has := pm["minecraft:powder_snow"]
			if has != sess.frostActive || (has && mod.value != sess.frostSent) {
				_ = sess.writeAttributes776(updateAttributesSpeed(pe.ID, pm))
				sess.frostActive, sess.frostSent = has, mod.value
			}
			// ServerEntity.sendChanges' hurtMarked tail on the player
			// record: a landed hurt flushes the entity's delta
			// self-inclusively — vanilla's
			// sendToTrackingPlayersAndSelf reaches the victim's own
			// connection, drift gate bypassed.
			if pe.HurtMarked {
				pe.HurtMarked = false
				s.broadcastEntityMotion(pe.Dim, pe.ID, pe.MotionX, pe.MotionY, pe.MotionZ)
			}
		}
		// updateFluidInteraction — the LAVA tracker — is tick-driven
		// like the air pass; it feeds the fire pass's in-lava skip.
		sess.inLava = sessInFluidCell(sess, d, x, y, z, "lava", "lava_cauldron")
		return true
	})
}

// playerInWall mirrors Entity.isInWall: the probe is a plane centered
// on the eye position, width*0.8 = 0.48 across (0.6*0.8) and 1e-6
// tall, so a full-cube opaque block covering the eye cell suffocates.
func (s *Server) playerInWall(sess *Session, d *dimension, x, y, z, eye float64) bool {
	if d == nil {
		return false
	}
	const half = 0.24 // width 0.6 * 0.8 / 2
	ey := int(math.Floor(y + eye))
	for bx := int(math.Floor(x - half)); bx <= int(math.Floor(x+half)); bx++ {
		for bz := int(math.Floor(z - half)); bz <= int(math.Floor(z+half)); bz++ {
			b := d.w.GetBlock(world.Pos{X: bx, Y: ey, Z: bz})
			if b == world.Air {
				continue
			}
			if name, ok := world.StateOwner(b); ok && suffocatingBlocks[strings.TrimPrefix(name, "minecraft:")] {
				return true
			}
		}
	}
	return false
}

// sessInFluidCell reports whether any cell of the player's inside
// box holds one of the named block owners — the per-tick fluid
// tracker scan.
func sessInFluidCell(sess *Session, d *dimension, x, y, z float64, names ...string) bool {
	for bx := int(math.Floor(x - 0.3)); bx <= int(math.Floor(x+0.3)); bx++ {
		for by := int(math.Floor(y)); by <= int(math.Floor(y+1.8)); by++ {
			for bz := int(math.Floor(z - 0.3)); bz <= int(math.Floor(z+0.3)); bz++ {
				bn, ok := world.StateOwner(d.w.GetBlock(world.Pos{X: bx, Y: by, Z: bz}))
				if !ok {
					continue
				}
				for _, want := range names {
					if strings.TrimPrefix(bn, "minecraft:") == want {
						return true
					}
				}
			}
		}
	}
	return false
}

// playerInsideBlocks is Entity.move's applyEffectsFromBlocks for the
// player: vanilla players only run the inside-block scan when the
// client sends a move packet — handleMovePlayer calls player.move —
// so a stationary client takes no lava, fire or extinguish effect at
// all. Each call is one move()'s worth of effects: lava ignites
// fifteen seconds and lands the four-damage lavaHurt, fire blocks
// ignite eight and hit in_fire, water-family cells extinguish.
func (s *Server) playerInsideBlocks(sess *Session) {
	if sess.state.Load() != StatePlay || sess.dead || !sess.spawned.Load() {
		return
	}
	d := sess.dimension()
	if d == nil {
		return
	}
	x, y, z, _, _, _ := sess.Pos()
	beforeFire := sess.fireTicks
	s.playerPowderInside(sess, d, x, y, z)
	lava := false
	wet, burn := false, float32(0)
	for bx := int(math.Floor(x - 0.3)); bx <= int(math.Floor(x+0.3)); bx++ {
		for by := int(math.Floor(y)); by <= int(math.Floor(y+1.8)); by++ {
			for bz := int(math.Floor(z - 0.3)); bz <= int(math.Floor(z+0.3)); bz++ {
				bn, ok := world.StateOwner(d.w.GetBlock(world.Pos{X: bx, Y: by, Z: bz}))
				if !ok {
					continue
				}
				switch strings.TrimPrefix(bn, "minecraft:") {
				case "lava", "lava_cauldron":
					lava = true
				case "water", "bubble_column", "water_cauldron", "powder_snow_cauldron":
					wet = true
				case "fire":
					burn = 1
				case "soul_fire":
					burn = 2
				}
			}
		}
	}
	if lava {
		// lavaIgnite -> igniteForSeconds(15) -> igniteForTicks(300):
		// remainingFireTicks is set to 300 outright when shorter.
		if sess.fireTicks < 300 {
			sess.fireTicks = 300
			s.broadcastSharedFlags(sess)
		}
		s.damageWith(sess, 4, translatable("death.attack.lava", literal(sess.getName())), srcLava)
	}
	if burn > 0 {
		// FireBlock's entityInside -> igniteForSeconds(8) ->
		// igniteForTicks(160): the same plain set at 160.
		if sess.fireTicks < 160 {
			sess.fireTicks = 160
			s.broadcastSharedFlags(sess)
		}
		s.damageWith(sess, burn, translatable("death.attack.inFire", literal(sess.getName())), srcInFire)
	}
	if wet && sess.fireTicks > 0 {
		// EXTINGUISH -> Entity.clearFire: remainingFireTicks drops to
		// min(0, it) — a negative baseline stays untouched.
		sess.fireTicks = 0
		s.broadcastSharedFlags(sess)
	}
	sess.fireTicks = settledPlayerFire(beforeFire, sess.fireTicks)
}

// broadcastAirSupply resends metadata index 1 (the air supply int) to
// every session tracking the player, the subject included — vanilla's
// SynchedEntityData resends the entry each tick setAirSupply ran.
func (s *Server) broadcastAirSupply(sess *Session, air int32) {
	eid := sess.entityID.Load()
	if eid == 0 {
		return
	}
	var b protocol.Buffer
	b.VarInt(eid)
	b.Byte(1) // index: air supply
	b.Byte(1) // type: varint
	b.VarInt(air)
	b.Byte(0xFF)
	body := b.Bytes()
	cache := javaEntityBodyCache{}
	dim := sess.dimName()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() {
			return true
		}
		if other.dimName() != dim {
			return true
		}
		if !other.tracksEntity(eid) && other.entityID.Load() != eid {
			return true
		}
		_ = cache.writeMetadata(other, mustEntityType("player"), body)
		return true
	})
}

// broadcastTakeItem sends Take Item Entity for a collected drop to the
// item's dimension.
func (s *Server) broadcastTakeItem(dim string, itemID, collectorID, amount int32) {
	if itemID == 0 || collectorID == 0 || amount <= 0 {
		return
	}
	var b protocol.Buffer
	b.VarInt(itemID)
	b.VarInt(collectorID)
	b.VarInt(amount)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		if sess.dimName() != dim {
			return true
		}
		_ = sess.write(sess.ids().PlayTakeItem, body)
		return true
	})
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// playerFallDamageAmount measures LivingEntity.calculateFallDamage with
// default safe_fall_distance=3 and fall_damage_multiplier=1 (26.2, JDK 25
// javap). The epsilon protects integral landing distances from float drift.
func playerFallDamageAmount(distance float64) float64 {
	return math.Max(0, math.Floor(distance+1e-6-3))
}

// playerFallDamage uses the measured 26.2 attribute calculation. Jump Boost
// contributes an additive safe-distance modifier before the other passes.
func (sess *Session) playerFallDamage(distance float64) float64 {
	return sess.playerFallDamageScaled(distance, 1)
}

func (sess *Session) playerFallDamageScaled(distance float64, multiplier float32) float64 {
	base := playerAttrBases["safe_fall_distance"]
	mods := map[string]attrMod{}
	canonical := false
	if e, ok := sess.server.entities.Get(sess.entityID.Load()); ok {
		canonical = true
		if v, ok := e.AttrBase["safe_fall_distance"]; ok {
			base = v
		}
		for k, v := range e.AttrMods["safe_fall_distance"] {
			mods[k] = v
		}
	}
	// Detached callers have no attribute instance; admitted players use its
	// exact modifier set, including explicit command removal or replacement.
	if !canonical {
		sess.effMu.Lock()
		if id, ok := MobEffectID("jump_boost"); ok {
			if effect, active := sess.effects[id]; active {
				mods["minecraft:effect.jump_boost"] = attrMod{value: float64(effect.amplifier + 1)}
			}
		}
		sess.effMu.Unlock()
	}
	safe := attrRange("safe_fall_distance", attrValue(base, mods))
	return math.Max(0, math.Floor((distance+1e-6-safe)*float64(multiplier)*sess.sessAttr("fall_damage_multiplier", nil)))
}

// Player.causeFallDamage banks the distance passed by the landing block,
// before the damage calculation (including zero-multiplier slime landings).
func (sess *Session) awardFallDistance(distance float64) {
	if distance >= 2 && (sess.mode() == GameModeSurvival || sess.mode() == GameModeAdventure) {
		sess.addCustomStat("fall_one_cm", int32(math.Round(distance*100)))
	}
}
