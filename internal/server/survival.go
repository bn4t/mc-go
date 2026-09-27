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
func digTicks(info blockHardness, bname string, sess *Session) int {
	if info.hardness < 0 {
		return -1
	}
	tool := sess.heldStack()
	speed := toolDestroySpeed(tool, bname)
	if eff := tool.enchLevel("minecraft:efficiency"); eff > 0 && speed > 1 {
		speed += float64(eff*eff + 1)
	}
	_, _, _, _, _, onGround := sess.Pos()
	if !onGround {
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
func (s *Server) handleDigSurvival(sess *Session, pos world.Pos, status int32) []int {
	blk := sess.world().GetBlock(pos)
	info := hardnessOf(blk)
	switch status {
	case digStart:
		if info.hardness < 0 || blk == world.Air {
			return nil
		}
		if !withinReach(sess, pos, reachSurvival) {
			return nil
		}
		bname, _ := world.StateOwner(blk)
		need := digTicks(info, bname, sess)
		if need <= 1 {
			// destroyProgress >= 1 on the first tick of a dig means the
			// block breaks on the start action itself, no finish needed.
			affected := s.breakBlock(sess, pos, info)
			s.sendLightUpdate(sess.dimension(), pos.X>>4, pos.Z>>4, affected)
			return affected
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
			return nil
		}
		bname, _ := world.StateOwner(sess.digBlock)
		need := digTicks(info, bname, sess)
		if sess.digTicks+1 >= need {
			sess.digActive = false
			sess.digTicks = 0
			sess.digMu.Unlock()
			affected := s.breakBlock(sess, pos, info)
			s.sendLightUpdate(sess.dimension(), pos.X>>4, pos.Z>>4, affected)
			return affected
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
	return nil
}

// sendBlockResync tells one client what a block really is, vanilla's
// answer to a block action the server rejected.
func (s *Server) sendBlockResync(sess *Session, pos world.Pos) {
	blk := sess.world().GetBlock(pos)
	body := encodeBlockUpdate(int32(pos.X), int32(pos.Y), int32(pos.Z), blockState(blk))
	_ = sess.write(sess.ids().PlayBlockUpdateCB, body)
}

// digTickStep advances digging by one tick per playing session. As in
// vanilla, the loop only counts progress: a live dig never breaks here,
// but a delayed destroy armed by an early stop breaks once its tick
// total crosses the requirement (hasDelayedDestroy in
// ServerPlayerGameMode.tick).
func (s *Server) digTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dead || sess.mode() != GameModeSurvival {
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
				if need := digTicks(hardnessOf(cur), cname, sess); ticks+1 >= need {
					affected := s.breakBlock(sess, pos, hardnessOf(cur))
					s.sendLightUpdate(sess.dimension(), pos.X>>4, pos.Z>>4, affected)
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
func (s *Server) breakBlock(sess *Session, pos world.Pos, info blockHardness) []int {
	broken := sess.world().GetBlock(pos)
	if broken == world.Air {
		return nil
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
	s.fireBeeNestDestroyed(sess, sess.dimension(), pos, broken)
	affected := s.setBlockAndBroadcast(sess.getName(), pos, world.Air)
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
		return affected
	}
	bname, _ := world.StateOwner(broken)
	// Vanilla gates getDrops on hasCorrectToolForDrops: a
	// requires-tool block drops nothing without a matching tool.
	if blockRequiresTool[bname] && !toolCorrectForDrops(sess.heldStack(), bname) {
		return affected
	}
	if _, ok := data.LootTables()["blocks/"+bname]; ok {
		for _, st := range rollBlockLoot("blocks/"+bname, sess, pos, bname, broken, nestBees, beName) {
			s.spawnItemDrop(sess.dimName(), pos, st)
		}
		return affected
	}
	// Blocks outside the generated table set keep the hand-curated
	// drop column (e.g. monster spawner: no table, no drops).
	if info.drop == "" {
		return affected
	}
	id, ok := world.ItemID(info.drop)
	if !ok {
		return affected
	}
	s.spawnItemDrop(sess.dimName(), pos, ItemStack{ID: id, Count: 1})
	return affected
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
	if e, ok := s.entities.Get(eid); ok {
		dim = e.Dim
	}
	var b protocol.Buffer
	b.VarInt(eid)
	b.Raw(payload)
	body := b.Bytes()
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
		_ = sess.write(sess.ids().PlayEntityMetadata, body)
		return true
	})
}

// itemTickStep advances item entities one tick: gravity, pickup delay,
// player pickup, and position sync.
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
		if e.FakeItem {
			// makeFakeItem ages to 5999 so the entity discards on its
			// first tick.
			s.entities.Remove(e.ID)
			s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
			continue
		}
		if e.PickupDelay > 0 {
			e.PickupDelay--
		}
		// Simple gravity until the entity rests on a solid top. Block
		// coordinates floor (negative Y must not truncate toward zero).
		bx := int(math.Floor(e.X))
		bz := int(math.Floor(e.Z))
		// Sample just past the quarter-height: at restY the naive
		// -0.25 lands exactly on the cell boundary (floor sees air),
		// which flips grounded↔airborne every other tick and storms
		// set_entity_motion forever.
		below := world.Pos{X: bx, Y: int(math.Floor(e.Y - 0.26)), Z: bz}
		newY := e.Y + e.MotionY
		if w.GetBlock(below) == world.Air {
			e.Grounded = false
			e.MotionY -= 0.04
			if e.MotionY < -0.5 {
				e.MotionY = -0.5
			}
			newY = e.Y + e.MotionY
		} else {
			restY := float64(below.Y) + 1.25
			if newY < restY {
				newY = restY
			}
			e.MotionY = 0
			e.Grounded = true
		}
		e.Y = newY
		// Item entities and XP orbs sync through the same ServerEntity
		// pipeline as mobs — updateInterval 20 — not a teleport every
		// moved tick.
		s.mobSyncFlush(e)
		// Entity.moveTowardsClosestSpace: an item whose feet cell is a
		// full block slides out toward the least-distant open face —
		// the scan covers N/S/W/E/UP (never down) and the push is
		// 0.1-0.3 along that axis while the others keep 75% of motion.
		feet := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
		if cb := w.GetBlock(feet); cb != world.Air && !world.NonSolid(cb) {
			fx := e.X - float64(feet.X)
			fy := e.Y - float64(feet.Y)
			fz := e.Z - float64(feet.Z)
			best := math.MaxFloat64
			axis, sign := 'y', 1.0
			for _, d := range [5][4]int{{0, 0, -1, 'z'}, {0, 0, 1, 'z'}, {-1, 0, 0, 'x'}, {1, 0, 0, 'x'}, {0, 1, 0, 'y'}} {
				nb := world.Pos{X: feet.X + d[0], Y: feet.Y + d[1], Z: feet.Z + d[2]}
				if b := w.GetBlock(nb); b != world.Air && !world.NonSolid(b) {
					continue
				}
				var dist float64
				switch d[3] {
				case 'x':
					dist = fx
				case 'y':
					dist = fy
				default:
					dist = fz
				}
				s := 1.0
				if d[0]+d[1]+d[2] < 0 {
					s = -1.0
				} else {
					dist = 1.0 - dist
				}
				if dist < best {
					best = dist
					axis = rune(d[3])
					sign = s
				}
			}
			push := 0.1 + rand.Float64()*0.2
			e.MotionX *= 0.75
			e.MotionY *= 0.75
			e.MotionZ *= 0.75
			switch axis {
			case 'x':
				e.MotionX = push * sign
			case 'y':
				e.MotionY = push * sign
			default:
				e.MotionZ = push * sign
			}
		}
		// Thrown items drift: horizontal motion applied each tick with
		// vanilla's decay (0.98 airborne, block slip ~0.55 grounded).
		// Each axis collides with walls independently — a sliding item
		// stops at the wall face instead of passing through.
		prevX, prevZ := e.X, e.Z
		if e.MotionX != 0 || e.MotionZ != 0 {
			if e.MotionX != 0 && w.GetBlock(world.Pos{X: int(math.Floor(e.X + e.MotionX)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}) == world.Air {
				e.X += e.MotionX
			} else {
				e.MotionX = 0
			}
			if e.MotionZ != 0 && w.GetBlock(world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z + e.MotionZ))}) == world.Air {
				e.Z += e.MotionZ
			} else {
				e.MotionZ = 0
			}
			fr := 0.98
			if e.MotionY == 0 {
				fr = 0.55
			}
			e.MotionX *= fr
			e.MotionZ *= fr
			if math.Abs(e.MotionX) < 0.01 && math.Abs(e.MotionZ) < 0.01 {
				e.MotionX, e.MotionZ = 0, 0
			}
		}
		if orb && e.OrbCount < 1 {
			e.OrbCount = 1 // orbs always carry a count of one from spawn
		}
		// ExperienceOrb.scanForMerges every 20 ticks: self absorbs
		// every orb whose box reaches the inflated-0.5 scan box, age
		// goes to the pair's minimum.
		if orb && e.ItemAge%20 == 0 {
			s.orbScanForMerges(e, items)
		}
		// ExperienceOrb.followNearbyPlayer every tick: keep the
		// latched player while non-spectator and within 8, else rescan
		// for the nearest non-spectator live player in 8; the pull is
		// normalize(dir to eye-centre) * (1-d/8)^2 * 0.1 onto motion.
		if orb {
			if t := e.OrbFollowing; t == 0 || !s.orbFollowingOK(e, t) {
				e.OrbFollowing = 0
				if p := s.orbNearestPlayer(e); p != nil {
					e.OrbFollowing = p.entityID.Load()
				}
			}
			if e.OrbFollowing != 0 {
				if p := s.sessionByEntityID(e.OrbFollowing); p != nil {
					px, py, pz, _, _, _ := p.Pos()
					dx, dy, dz := px-e.X, py+0.81-e.Y, pz-e.Z
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
		}
		// ItemEntity.mergeWithNeighbours: every 2 ticks when the item
		// crossed a block boundary this tick, else every 40 — the
		// bigger stack absorbs the smaller into its own entity.
		if !orb && e.Item.Empty() {
			continue // absorbed by a merge earlier this tick
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
		e.ItemAge++
		if e.ItemAge >= 6000 {
			s.entities.Remove(e.ID)
			s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
			continue
		}
		if e.PickupDelay <= 0 {
			if sess := s.pickupTarget(e, orb); sess != nil {
				if orb {
					// ExperienceOrb.playerTouch: the touch routes
					// value through repairPlayerItems first — mending
					// gear drinks it — then grants what's left, once
					// per takeXpDelay window, discarding at count 0.
					if sess.takeXpDelay > 0 {
						continue
					}
					sess.takeXpDelay = 2
					if remaining := s.repairPlayerItems(sess, e.OrbValue); remaining > 0 {
						sess.addXP(remaining)
					}
					e.OrbCount--
					if e.OrbCount <= 0 {
						s.entities.Remove(e.ID)
						s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
					}
					continue
				}
				remainder, picked := sess.addStack(e.Item)
				if picked {
					// ServerPlayer.take -> inventory_changed; recipe
					// unlocks (e.g. wolf_armor_dyed) gate on it.
					s.fireInventoryChanged(sess)
					took := e.Item.Count
					// ServerPlayer.onItemPickup: a picked-up item
					// whose owner resolves fires
					// thrown_item_picked_up_by_player for the picker
					// (allay_deliver_item_to_player).
					if e.Thrower != 0 {
						if oe, ok := s.entities.Get(e.Thrower); ok {
							if tname, ok := EntityTypeName(oe.Type); ok {
								s.fireThrownItemPickedUpByPlayer(sess, e.Item, tname)
							}
						} else if s.sessionByEntityID(e.Thrower) != nil {
							s.fireThrownItemPickedUpByPlayer(sess, e.Item, "player")
						}
					}
					// Vanilla tells the client the item flies to
					// the collector before it disappears.
					s.broadcastTakeItem(e.Dim, e.ID, sess.entityID.Load(), e.Item.Count)
					s.entities.Remove(e.ID)
					s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
					sess.addItemStat(statPickedUp, e.Item.ID, took)
				} else if remainder != e.Item {
					s.fireInventoryChanged(sess)
					sess.addItemStat(statPickedUp, e.Item.ID, e.Item.Count-remainder.Count)
					e.Item = remainder
					s.broadcastEntityMetadata(e.ID, encodeItemMetadata(remainder))
				}
			}
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
	// Vanilla's die() award order: killed_by[killer], then deaths,
	// then time_since_death resets.
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
	s.closeWindow(sess)
	// Survival drops the whole inventory at the death spot.
	if s.GameMode == GameModeSurvival {
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
	for i := 0; i < slotCount; i++ {
		if !sess.inv[i].Empty() {
			stacks = append(stacks, sess.inv[i])
			sess.inv[i] = ItemStack{}
		}
	}
	sess.carried = ItemStack{}
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
	_ = s.sendCarriedItem(sess, ItemStack{})
}

// respawnPlayer resets survival state and re-syncs the client: the
// Respawn packet, a fresh position, health, inventory, and the chunk
// window around spawn. Vanilla respawns dead players at their spawn
// point, which lives in the overworld, so dying elsewhere also crosses
// dimensions.
func (s *Server) respawnPlayer(sess *Session) {
	sess.dead = false
	s.closeWindow(sess)
	sess.health = defaultHealth
	sess.food = defaultFood
	sess.saturation = defaultSaturation
	sess.fallDistance = 0
	// Damage-source refs live on the dying entity — the respawned one
	// carries no attacker memory.
	sess.lastHurtBy = 0
	sess.lastHurtPlayerBy = [16]byte{}
	sess.lastHurtPlayerTicks = 0

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
		x, y, z = s.WorldSpawn()
	}
	if sess.dimension() != dest {
		// Cross-dimension respawn: entity tracking, chunks, and
		// dimension bookkeeping all run through changeDimension.
		s.changeDimension(sess, dest, x, y, z, 0)
		// changeDimension zeroes the view rotation; restore the spawn
		// point's angles on the session side for selectors/tracking.
		sess.setPos(x, y, z, spawnYaw, spawnPitch, false)
		_ = s.sendContainerContent(sess)
		return
	}

	_ = s.sendRespawn(sess, dest, 0, 0)

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
		if sess.dead {
			s.respawnPlayer(sess)
		}
	case 1: // request stats
		_ = s.sendStatistics(sess)
	case 2: // request game rule values
		_ = s.sendGameRules(sess)
	}
}

// applyFallDamage converts accumulated fall distance into damage on
// landing, matching vanilla's threshold of three blocks.
func (s *Server) applyFallDamage(sess *Session) {
	if sess.mode() != GameModeSurvival || sess.dead || sess.fallDistance <= 3.0 {
		sess.fallDistance = 0
		return
	}
	distance := sess.fallDistance
	sess.fallDistance = 0
	// LivingEntity.causeFallDamage's dmg>0 tail: the generic
	// Fallsounds pair (big when dmg > 4) then the landed block's
	// SoundType.fallSound — both at the player's SoundSource (7).
	x, y, z, _, _, _ := sess.Pos()
	dmg := distance - 3.0
	snd := int32(702) // entity.generic.small_fall
	if dmg > 4 {
		snd = 694 // entity.generic.big_fall
	}
	s.playSoundSrc(sess.dimName(), x, y, z, snd, 7, 1, 1, nil)
	s.playBlockFallSound(sess.dimName(), x, y, z, 7, world.Pos{
		X: int(math.Floor(x)), Y: int(math.Floor(y)) - 1, Z: int(math.Floor(z)),
	})
	s.damageWith(sess, float32(dmg), translatable("death.fell.accident.generic", literal(sess.getName())), srcFall)
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
		if sess.state.Load() != StatePlay || sess.dead {
			return true
		}
		if sess.takeXpDelay > 0 {
			sess.takeXpDelay--
		}
		if sess.mode() != GameModeSurvival {
			return true
		}
		changed := false
		// Exhaustion spends first: every four points take a saturation,
		// then a food point. The exhaustion a regen heal adds drains on
		// the NEXT tick, so its health packet still carries the old
		// saturation.
		for sess.exhaustion >= 4 {
			sess.exhaustion -= 4
			if sess.saturation > 0 {
				sess.saturation--
			} else if sess.food > 0 {
				sess.food--
			}
			changed = true
		}
		hurt := sess.health < defaultHealth
		if regen && hurt && sess.food >= 20 && sess.saturation > 0 {
			sess.foodTickTimer++
			if sess.foodTickTimer >= 10 {
				sess.foodTickTimer = 0
				amount := min(sess.saturation, 6)
				sess.health = min(sess.health+amount/6, defaultHealth)
				sess.addExhaustion(amount)
				changed = true
			}
		} else if regen && hurt && sess.food >= 18 {
			sess.foodTickTimer++
			if sess.foodTickTimer >= 80 {
				sess.foodTickTimer = 0
				sess.health++
				sess.addExhaustion(6)
				changed = true
			}
		} else {
			sess.foodTickTimer = 0
		}
		// Entity.baseTick's fire pass: each multiple of twenty ticks
		// deals a point of on_fire damage (armor + fire_protection
		// still apply) and the flags byte's on-fire bit drops at zero.
		if sess.fireTicks > 0 {
			if sess.fireTicks%20 == 0 {
				s.damageWith(sess, 1, translatable("death.attack.onFire", literal(sess.getName())), damageSource{typeName: "on_fire"})
			}
			sess.fireTicks--
			if sess.fireTicks <= 0 {
				sess.fireTicks = 0
				s.broadcastSharedFlags(sess)
			}
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

// pickupTarget returns a nearby player in e's dimension able to
// collect it.
func (s *Server) pickupTarget(e *Entity, orb bool) *Session {
	var target *Session
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		if sess.dimName() != e.Dim {
			return true
		}
		// Items: only survival and adventure players collect them.
		// Orbs: ExperienceOrb.playerTouch has no mode gate — creative
		// players absorb xp too; spectators are the only skip.
		if orb {
			if sess.mode() == GameModeSpectator {
				return true
			}
		} else if m := sess.mode(); m != GameModeSurvival && m != GameModeAdventure {
			return true
		}
		// ItemEntity.playerTouch: a targeted item (setTarget — /give
		// overflow) can only be picked up by its owner.
		if e.HasOwner && e.Owner != sess.getUUID() {
			return true
		}
		// Fake items (/give's visual toss) can never be picked up.
		if e.FakeItem {
			return true
		}
		x, y, z, _, _, _ := sess.Pos()
		// Vanilla collects an item when its 0.25-cube box touches the
		// player's bounding box inflated by (1.0, 0.5, 1.0): half-width
		// 0.3+1.0+0.125 horizontally, [y-0.75, y+2.3] for the item's
		// feet vertically.
		if abs(x-e.X) <= 1.425 && abs(z-e.Z) <= 1.425 && e.Y >= y-0.75 && e.Y <= y+2.3 {
			target = sess
			return false
		}
		return true
	})
	return target
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
