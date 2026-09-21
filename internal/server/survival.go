package server

import (
	"math"

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

// digTicks is how many game ticks a break takes, straight from the
// public breaking formula: per-tick progress is speed/hardness/30 when
// the block can be harvested with what is held and /100 when it cannot,
// with a bare hand's speed of 1 and every result quantized to a whole
// tick. Verified against the real server: grass and dirt bare-handed
// take 18 and 15 ticks, stone bare-handed 150.
func digTicks(info blockHardness) int {
	if info.hardness < 0 {
		return -1
	}
	div := 30.0
	if info.requiresTool {
		// Without the right tool the player cannot harvest the block,
		// which makes every tick of progress that much smaller.
		div = 100.0
	}
	n := int(math.Ceil(info.hardness * div))
	if n < 1 {
		n = 1
	}
	return n
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

// handleDigSurvival implements the survival branch of Player Action.
// Vanilla's start records the block the player set out to mine, the
// server accumulates one tick of progress per game tick (digTickStep)
// and never trusts the client's idea of elapsed time, and the client's
// finish only commits a dig whose progress the server measured itself.
// Verified against the real server with a scripted client: a start on
// its own never destroys anything, a start plus finish destroys the
// block after exactly the hardness-derived number of ticks counted from
// the start, an abort discards the dig, and a second start on the same
// block restarts the timer and is answered with a resync of the true
// block; the real client also retries finish while a dig is latched.
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
		sess.digMu.Lock()
		restart := sess.digActive && sess.digPos == pos
		sess.digPos = pos
		sess.digBlock = blk
		sess.digTicks = 0
		sess.digNeeded = digTicks(info)
		sess.digConfirmed = false
		sess.digActive = true
		sess.digMu.Unlock()
		if restart {
			// Vanilla answers a redundant start with the truth about
			// the block, which is what resynchronizes a client that
			// predicted a break the server did not agree to.
			s.sendBlockResync(sess, pos)
		}
	case digCancel:
		sess.digMu.Lock()
		sess.digActive = false
		sess.digTicks = 0
		sess.digConfirmed = false
		sess.digMu.Unlock()
	case digFinish:
		// A finish only commits the dig it belongs to, and the
		// destruction itself happens on the tick that completes the
		// progress (digTickStep), so a client that stops early cannot
		// buy itself a faster break.
		sess.digMu.Lock()
		if sess.digActive && sess.digPos == pos {
			sess.digConfirmed = true
		}
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

// digTickStep advances the survival dig of every playing session by one
// tick and destroys the blocks whose progress completes, the server
// being the authority on break timing exactly as vanilla is.
func (s *Server) digTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dead || sess.mode() != GameModeSurvival {
			return true
		}
		sess.digMu.Lock()
		if !sess.digActive {
			sess.digMu.Unlock()
			return true
		}
		pos, want, need := sess.digPos, sess.digBlock, sess.digNeeded
		sess.digTicks++
		done := sess.digConfirmed && sess.digTicks >= need
		if done {
			sess.digActive = false
			sess.digTicks = 0
			sess.digConfirmed = false
		}
		sess.digMu.Unlock()

		if cur := sess.world().GetBlock(pos); cur != want {
			// Somebody else changed the block: the dig is stale.
			sess.digMu.Lock()
			sess.digActive = false
			sess.digMu.Unlock()
			return true
		}
		if !done {
			return true
		}
		affected := s.breakBlock(sess, pos, hardnessOf(want))
		s.sendLightUpdate(sess.dimension(), pos.X>>4, pos.Z>>4, affected)
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
	// A shulker box drops itself with everything inside as one stack
	// (see removeContainer), so the plain block item would duplicate it.
	if isShulkerBoxBlock(broken) {
		return affected
	}
	// Tool-aware drops arrive with the tool milestone; a bare hand
	// cannot harvest requires-tool blocks, exactly like vanilla.
	if info.drop == "" || info.requiresTool {
		return affected
	}
	id, ok := world.ItemID(info.drop)
	if !ok {
		return affected
	}
	s.spawnItemDrop(sess.dimName(), pos, ItemStack{ID: id, Count: 1})
	return affected
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

// damage applies survival damage and syncs health; a lethal hit runs
// the death flow. Creative and spectator players are immune.
func (s *Server) damage(sess *Session, amount float32, message component, src damageSource) {
	if amount <= 0 || sess.mode() != GameModeSurvival || sess.dead {
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
	if sess.dimension() != overworld {
		// Cross-dimension respawn: entity tracking, chunks, and
		// dimension bookkeeping all run through changeDimension.
		x, y, z := s.WorldSpawn()
		s.changeDimension(sess, overworld, x, y, z, 0)
		_ = s.sendContainerContent(sess)
		return
	}

	_ = s.sendRespawn(sess, overworld, 0, 0)

	x, y, z := s.WorldSpawn()
	sess.setPos(x, y, z, 0, 0, false)
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
