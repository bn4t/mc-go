package server

import (
	"math"
	"time"

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
}

// hardnessOf returns the break rules for a block state.
func hardnessOf(b world.Block) blockHardness {
	name, ok := world.BlockName(b)
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

// breakSeconds is how long a bare-handed break takes. Vanilla's
// progress formula is (1/hardness)/100 per tick without the proper
// tool, i.e. hardness * 5 seconds; a matching tool would use
// hardness * 1.5 and arrives with the tool milestone.
func breakSeconds(info blockHardness) float64 {
	if info.hardness < 0 {
		return -1
	}
	return info.hardness * 5
}

// digState tracks an in-progress survival break.
type digState struct {
	pos   world.Pos
	start time.Time
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

// targetedBlock returns the first non-air block along the player's
// look vector within reach, vanilla's interaction target.
func (s *Server) targetedBlock(sess *Session, reach float64) (world.Pos, bool) {
	x, y, z, yaw, pitch, _ := sess.Pos()
	dx, dy, dz := lookVector(yaw, pitch)
	ex, ey, ez := x, y+1.62, z
	for t := 0.0; t <= reach; t += 0.05 {
		px := int(math.Floor(ex + dx*t))
		py := int(math.Floor(ey + dy*t))
		pz := int(math.Floor(ez + dz*t))
		p := world.Pos{X: px, Y: py, Z: pz}
		if sess.world().GetBlock(p) != world.Air {
			return p, true
		}
	}
	return world.Pos{}, false
}

// handleDigSurvival implements the survival branch of Player Action:
// start records the dig, cancel clears it, finish breaks the block when
// enough time has passed. Tool speed and the block-destruction animation
// ride the tool milestone.
func (s *Server) handleDigSurvival(sess *Session, pos world.Pos, status int32) []int {
	blk := sess.world().GetBlock(pos)
	info := hardnessOf(blk)
	switch status {
	case digStart:
		if info.hardness < 0 {
			return nil
		}
		// Vanilla only starts digs on the block the player is looking
		// at; a scripted or desynced client cannot break around corners.
		if hit, ok := s.targetedBlock(sess, reachSurvival); !ok || hit != pos {
			return nil
		}
		sess.digMu.Lock()
		if !sess.digActive || sess.digPos != pos {
			sess.digPos = pos
			sess.digStart = time.Now()
			sess.digActive = true
		}
		sess.digMu.Unlock()
	case digCancel:
		sess.digMu.Lock()
		sess.digActive = false
		sess.digMu.Unlock()
	case digFinish:
		sess.digMu.Lock()
		active := sess.digActive && sess.digPos == pos
		started := sess.digStart
		sess.digActive = false
		sess.digMu.Unlock()
		if !active {
			// Vanilla only completes a dig it saw start for; a
			// finish without a matching start is ignored (the client
			// would resync).
			return nil
		}
		if time.Since(started) < 50*time.Millisecond {
			// The server ticks dig progress once per tick; a finish
			// in the same tick as the start never completes in
			// vanilla either.
			return nil
		}
		// Vanilla trusts the client's finish once the start was
		// accepted (verified against the real server); break speed is
		// enforced client-side. Server-side timing returns with the
		// anticheat milestone.
		return s.breakBlock(sess, pos, info)
	}
	return nil
}

// breakBlock removes a block and spawns its drops.
func (s *Server) breakBlock(sess *Session, pos world.Pos, info blockHardness) []int {
	if sess.world().GetBlock(pos) == world.Air {
		return nil
	}
	affected := s.setBlockAndBroadcast(sess.getName(), pos, world.Air)
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
					// Vanilla tells the client the item flies to
					// the collector before it disappears.
					s.broadcastTakeItem(e.Dim, e.ID, sess.entityID.Load(), e.Item.Count)
					s.entities.Remove(e.ID)
					s.broadcastEntityRemoveIn(e.Dim, nil, []int32{e.ID})
				} else if remainder != e.Item {
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
func (s *Server) damage(sess *Session, amount float32, message string) {
	if amount <= 0 || sess.mode() != GameModeSurvival || sess.dead {
		return
	}
	sess.health -= amount
	if sess.health <= 0 {
		sess.health = 0
		s.killPlayer(sess, message)
		return
	}
	_ = s.sendHealth(sess)
}

// killPlayer runs the death flow: health 0, combat death notice, and
// the dead state that blocks further actions until respawn.
func (s *Server) killPlayer(sess *Session, message string) {
	if sess.dead {
		return
	}
	sess.dead = true
	sess.health = 0
	s.closeWindow(sess)
	// Survival drops the whole inventory at the death spot.
	if s.GameMode == GameModeSurvival {
		s.dropInventory(sess)
	}
	_ = s.sendHealth(sess)
	var b protocol.Buffer
	b.VarInt(sess.entityID.Load())
	b.Raw(textComponentContent(message))
	_ = sess.write(sess.ids().PlayCombatDeath, b.Bytes())
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
		x, y, z := s.world.Spawn()
		s.changeDimension(sess, overworld, x, y, z, 0)
		_ = s.sendContainerContent(sess)
		return
	}

	_ = s.sendRespawn(sess, overworld, 0, 0)

	x, y, z := s.world.Spawn()
	sess.setPos(x, y, z, 0, 0, false)
	_ = s.sendSyncPos(sess, x, y, z)
	_ = s.sendHealth(sess)
	_ = s.sendContainerContent(sess)
	sess.sentMu.Lock()
	sess.sent = nil
	sess.sentMu.Unlock()
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
	s.damageWith(sess, float32(distance-3.0), "Fell from a high place", srcFall)
}

// addExhaustion accumulates activity exhaustion; every four points
// consume one saturation, or one food point when saturation is gone.
func (sess *Session) addExhaustion(amount float32) bool {
	if amount <= 0 {
		return false
	}
	sess.exhaustion += amount
	changed := false
	for sess.exhaustion >= 4 {
		sess.exhaustion -= 4
		if sess.saturation > 0 {
			sess.saturation--
			changed = true
		} else if sess.food > 0 {
			sess.food--
			changed = true
		}
	}
	return changed
}

// hungerTickStep advances the hunger simulation one tick: natural
// regeneration while well fed and starvation damage when empty.
func (s *Server) hungerTickStep() {
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dead || sess.mode() != GameModeSurvival {
			return true
		}
		changed := false
		if sess.food >= 18 && sess.health < defaultHealth {
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
				s.broadcastDamageEvent(sess.entityID.Load(), damageInfo{typeName: "starve"})
				sess.health--
				changed = true
			}
		} else {
			sess.starveTimer = 0
		}
		if changed {
			_ = s.sendHealth(sess)
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
		x, y, z, _, _, _ := sess.Pos()
		if abs(x-e.X) <= 1.2 && abs(z-e.Z) <= 1.2 && abs(y-e.Y) <= 2.0 {
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
