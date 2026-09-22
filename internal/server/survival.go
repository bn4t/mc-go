package server

import (
	"math"
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
	s.fireBeeNestDestroyed(sess, sess.dimension(), pos, broken)
	affected := s.setBlockAndBroadcast(sess.getName(), pos, world.Air)
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
		for _, st := range rollBlockLoot("blocks/"+bname, sess, pos, bname, broken) {
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

// spawnItemDrop creates an item entity above pos in dimension dim and
// fans it out.
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
		X:           float64(pos.X) + 0.5,
		Y:           float64(pos.Y) + 0.25,
		Z:           float64(pos.Z) + 0.5,
		VY:          0,
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
		_ = sess.write(sess.ids().PlayEntityMetadata, body)
		return true
	})
}

// itemTickStep advances item entities one tick: gravity, pickup delay,
// player pickup, and position sync.
func (s *Server) itemTickStep() {
	orbType := mustEntityType("experience_orb")
	s.entities.mu.RLock()
	items := make([]*Entity, 0, len(s.entities.byID))
	for _, e := range s.entities.byID {
		if e.Item.ID != 0 || e.Type == orbType {
			items = append(items, e)
		}
	}
	s.entities.mu.RUnlock()
	for _, e := range items {
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
		below := world.Pos{X: bx, Y: int(math.Floor(e.Y - 0.25)), Z: bz}
		newY := e.Y + e.MotionY
		if w.GetBlock(below) == world.Air {
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
		}
		moved := newY != e.Y
		e.Y = newY
		if moved {
			s.broadcastEntityTeleport(nil, e.ID, e.X, e.Y, e.Z, 0, 0, true)
		}
		// Thrown items drift: horizontal motion applied each tick with
		// vanilla's decay (0.98 airborne, block slip ~0.55 grounded).
		// Each axis collides with walls independently — a sliding item
		// stops at the wall face instead of passing through.
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
			} else {
				moved = true
			}
		}
		if e.PickupDelay <= 0 {
			if sess := s.pickupTarget(e); sess != nil {
				if orb {
					sess.addXP(e.Data)
					s.entities.Remove(e.ID)
					s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
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

// defaultHealth/defaultFood are vanilla survival starting values.
const (
	defaultHealth     = 20
	defaultFood       = 20
	defaultSaturation = 5
)

// initSurvival initializes a joining player's survival state.
func (sess *Session) initSurvival() {
	sess.setMode(sess.server.GameMode)
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
	if src := s.sessionByEntityID(sess.lastHurtBy); src != nil {
		src.addStat(statKilled, mustEntityType("player"), 1)
		src.addCustomStat("player_kills", 1)
		// player_killed_entity fires for player victims too — the
		// advancement's killed_something criterion matches any entity.
		vx, _, vz, _, _, _ := sess.Pos()
		ax, _, az, _, _, _ := src.Pos()
		s.firePlayerKilledEntity(src, "player", vx, vz, ax, az)
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
	s.damageWith(sess, float32(distance-3.0), translatable("death.fell.accident.generic", literal(sess.getName())), srcFall)
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
		if sess.state.Load() != StatePlay || sess.dead || sess.mode() != GameModeSurvival {
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
func (s *Server) pickupTarget(e *Entity) *Session {
	var target *Session
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		if sess.dimName() != e.Dim {
			return true
		}
		// Vanilla pickup rule: only survival and adventure players
		// collect item entities; creative and spectator never do.
		if m := sess.mode(); m != GameModeSurvival && m != GameModeAdventure {
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
