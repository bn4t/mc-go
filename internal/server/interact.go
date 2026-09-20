package server

import (
	"math"

	"mc-go/internal/protocol"
)

// Entity animation ids (Entity Animation packet).
const (
	animSwingMainArm   = 0
	animSwingOffhand   = 3
	animCriticalEffect = 4
	animMagicCritical  = 5
)

// handleSwing broadcasts the arm swing animation to other players.
func (s *Server) handleSwing(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	hand, err := r.VarInt()
	if err != nil {
		return
	}
	anim := byte(animSwingMainArm)
	if hand == 1 {
		anim = animSwingOffhand
	}
	s.broadcastEntityAnimation(sess, sess.entityID.Load(), anim)
}

// broadcastEntityAnimation sends one Entity Animation to every other
// spawned play-stage session in the subject's dimension.
func (s *Server) broadcastEntityAnimation(except *Session, eid int32, anim byte) {
	if eid == 0 {
		return
	}
	dim := dimOverworld
	if e, ok := s.entities.Get(eid); ok {
		dim = e.Dim
	}
	var b protocol.Buffer
	b.VarInt(eid)
	b.Byte(anim)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess == except || sess.state.Load() != StatePlay || !sess.spawned.Load() {
			return true
		}
		if sess.dimName() != dim {
			return true
		}
		_ = sess.write(sess.ids().PlayEntityAnimation, body)
		return true
	})
}

// Player Action statuses that are not block breaking.
const (
	actionDropStack = 3
	actionDropItem  = 4
	actionSwapHands = 6
)

// handlePlayerActionExtras handles the non-digging Player Action
// statuses: drop item/stack and swap with offhand.
func (s *Server) handlePlayerActionExtras(sess *Session, p protocol.Packet) bool {
	r := protocol.NewPacketReader(p.Data)
	status, err := r.VarInt()
	if err != nil {
		return false
	}
	switch status {
	case actionDropStack, actionDropItem:
		if _, _, _, err := r.Position(); err != nil {
			return false
		}
		whole := status == actionDropStack
		s.dropHeld(sess, whole)
		return true
	case actionSwapHands:
		s.swapHands(sess)
		return true
	}
	return false
}

// dropHeld throws the selected hotbar stack — all of it, or a single
// item — spawning the item entity and counting the drop stats.
func (s *Server) dropHeld(sess *Session, whole bool) {
	slot := invSlotForHotbar(sess.held.Load())
	sess.invMu.Lock()
	it := sess.inv[slot]
	if it.Empty() {
		sess.invMu.Unlock()
		return
	}
	out := it
	if !whole {
		out = it.withCount(1)
	}
	sess.inv[slot].Count -= out.Count
	if sess.inv[slot].Count <= 0 {
		sess.inv[slot] = ItemStack{}
	}
	updated := sess.inv[slot]
	sess.invState.Add(1)
	s.dropItemLocked(sess, out)
	sess.invMu.Unlock()
	_ = s.sendContainerSlot(sess, slot, updated)
	s.fireInventoryChanged(sess)
}

// swapHands swaps the held hotbar stack with the offhand stack.
func (s *Server) swapHands(sess *Session) {
	slot := invSlotForHotbar(sess.held.Load())
	sess.invMu.Lock()
	sess.inv[slot], sess.inv[slotOffhand] = sess.inv[slotOffhand], sess.inv[slot]
	main := sess.inv[slot]
	off := sess.inv[slotOffhand]
	sess.invMu.Unlock()
	_ = s.sendContainerSlot(sess, slot, main)
	_ = s.sendContainerSlot(sess, slotOffhand, off)
}

// attackReach is the vanilla entity-interaction range in survival.
const attackReach = 3.0

// handleAttack applies a melee attack to another player: damage, hurt
// animation, and knockback (motion sync). Entity types beyond players
// arrive with the entity milestone.
func (s *Server) handleAttack(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	targetID, err := r.VarInt()
	if err != nil {
		return
	}
	s.attackEntity(sess, targetID)
}

// attackEntity applies a melee hit to one entity id, shared by the
// Attack packet and the Interact packet's attack action so both routes
// produce identical damage, combat, and animation packets.
func (s *Server) attackEntity(sess *Session, targetID int32) {
	if sess.mode() != GameModeSurvival || sess.dead {
		return
	}
	if targetID == 0 || targetID == sess.entityID.Load() {
		return
	}
	target := s.sessionByEntityID(targetID)
	if target == nil {
		// Not a player: attack a living mob if one has this id.
		if e, ok := s.entities.Get(targetID); ok && e.Living {
			s.broadcastDamageEvent(e.ID, damageInfo{
				typeName: "player_attack",
				cause:    sess.entityID.Load(),
				direct:   sess.entityID.Load(),
			})
			s.damageMob(&e, 1.0)
			s.broadcastEntityEventDamage(nil, e.ID)
			sess.addCustomStat("damage_dealt", 10)
			if _, still := s.entities.Get(targetID); !still {
				sess.addStat(statKilled, e.Type, 1)
				sess.addCustomStat("mob_kills", 1)
			}
		}
		return
	}
	if target.dead {
		return
	}
	ax, ay, az, _, _, _ := sess.Pos()
	tx, ty, tz, _, _, _ := target.Pos()
	dx, dy, dz := ax-tx, ay-ty, az-tz
	if dx*dx+dy*dy+dz*dz > (attackReach+0.5)*(attackReach+0.5) {
		return
	}
	sess.addExhaustion(0.1) // attacking costs hunger
	s.startCombat(sess)
	s.startCombat(target)
	// Weapon damage reduced by the victim's armor; the attack-cooldown
	// scaling and enchantments ride a later combat milestone.
	damage := reduceDamage(sess.heldAttackDamage(), target.armorDefense())
	sess.addCustomStat("damage_dealt", int32(damage*10))
	s.damageWith(target, damage, sess.getName()+" was slain by "+target.getName(), damageSource{
		typeName: "player_attack",
		cause:    sess.entityID.Load(),
		direct:   sess.entityID.Load(),
	})
	s.sendHurtAnimation(target, hurtYaw(tx, tz, ax, az))
	// Everyone except the victim sees the hurt entity event, matching
	// vanilla (the victim gets Hurt Animation instead).
	s.broadcastEntityEventDamage(target, targetID)
	_ = dy
}

// hurtYaw is the yaw of the damage direction (from the victim toward
// the attacker), matching the Hurt Animation convention.
func hurtYaw(tx, tz, ax, az float64) float32 {
	dx, dz := ax-tx, az-tz
	if dx == 0 && dz == 0 {
		return 0
	}
	// Minecraft yaw: 0 faces +Z, increasing toward -X.
	return float32(math.Atan2(-dx, dz) * 180 / math.Pi)
}

func (s *Server) sendHurtAnimation(sess *Session, yaw float32) {
	var b protocol.Buffer
	b.VarInt(sess.entityID.Load())
	b.Float(yaw)
	_ = sess.write(sess.ids().PlayHurtAnimation, b.Bytes())
}

// broadcastEntityEventDamage plays the hurt entity event for everyone
// in the victim's dimension except the victim (who receives Hurt
// Animation instead).
func (s *Server) broadcastEntityEventDamage(except *Session, eid int32) {
	dim := dimOverworld
	if e, ok := s.entities.Get(eid); ok {
		dim = e.Dim
	}
	var b protocol.Buffer
	b.Int(eid)
	b.Byte(2) // status: hurt
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other == except || other.state.Load() != StatePlay || !other.spawned.Load() {
			return true
		}
		if other.dimName() != dim {
			return true
		}
		_ = other.write(other.ids().PlayEntityEvent, body)
		return true
	})
}

// Player Input flag bits: 0x20 sneak, 0x40 sprint.
const (
	inputSneak  = 0x20
	inputSprint = 0x40
)

// Living-entity metadata index for the pose, and the pose values we
// broadcast. The index is verified against the real server by
// TestSneakPoseParity.
const (
	metaPoseIndex = 6
	metaPoseType  = 20
	poseStanding  = 0
	poseSneaking  = 5
)

// handlePlayerInput tracks the client's movement input flags and
// broadcasts pose metadata when sneaking starts or stops.
func (s *Server) handlePlayerInput(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	flags, err := r.Byte()
	if err != nil {
		return
	}
	sneak := flags&inputSneak != 0
	sprint := flags&inputSprint != 0
	jump := flags&0x10 != 0
	sess.sprinting = sprint
	if jump && !sess.jumping {
		_, _, _, _, _, onGround := sess.Pos()
		if onGround && !sess.flying {
			sess.addCustomStat("jump", 1)
		}
		if s.GameMode == GameModeSurvival {
			sess.addExhaustion(0.05)
		}
	}
	sess.jumping = jump
	if sneak == sess.sneaking {
		return
	}
	sess.sneaking = sneak
	pose := int32(poseStanding)
	if sneak {
		pose = poseSneaking
	}
	s.broadcastPose(sess, pose)
}

// broadcastPose sends the player's pose metadata to every play-stage
// session in their dimension (including the subject, like vanilla).
func (s *Server) broadcastPose(sess *Session, pose int32) {
	eid := sess.entityID.Load()
	if eid == 0 {
		return
	}
	dim := sess.dimName()
	var b protocol.Buffer
	b.VarInt(eid)
	b.Byte(metaPoseIndex)
	b.Byte(metaPoseType)
	b.VarInt(pose)
	b.Byte(0xFF)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() {
			return true
		}
		if other.dimName() != dim {
			return true
		}
		_ = other.write(other.ids().PlayEntityMetadata, body)
		return true
	})
}

// handlePlayerAbilities applies the client's ability flag update
// (flight toggle) and echoes the server-authoritative flags, exactly
// like vanilla. Modes without flight cannot start flying.
func (s *Server) handlePlayerAbilities(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	flags, err := r.Byte()
	if err != nil {
		return
	}
	flying := flags&abilityFlagFlying != 0
	if flying && sess.mode() != GameModeCreative && sess.mode() != GameModeSpectator {
		flying = false
	}
	if flying == sess.flying {
		return
	}
	sess.flying = flying
	_ = s.sendAbilities(sess)
}

// handleChunkBatchReceived applies the client's chunk-batch
// acknowledgement: the chunks-per-tick rate it asks for, which the
// streaming throttle spends on ten batches from the next tick on. The
// real server floors the request to a whole number of chunks and clamps
// it to [1, 64], and ignores acknowledgements that do not parse.
//
// The first of the ten batches goes out at once rather than on a tick, as
// it does in the real server, so a client that acknowledges every batch
// keeps its window streaming without waiting for a tick boundary.
func (s *Server) handleChunkBatchReceived(sess *Session, p protocol.Packet) {
	rate, err := protocol.NewPacketReader(p.Data).Float()
	if err != nil {
		return
	}
	sess.sentMu.Lock()
	sess.rate = chunkRate(rate)
	sess.interval = chunkInterval(rate)
	sess.burst = burstBatches
	sess.cool = 0
	sess.sentMu.Unlock()
	_ = s.pumpChunks(sess)
}

// clientInfo is the subset of Client Information the server keeps.
type clientInfo struct {
	Locale       string
	ViewDistance byte
	ChatColors   bool
	SkinParts    byte
	MainHand     int32
}

// parseClientInfo decodes a Client Information body.
func parseClientInfo(data []byte) (clientInfo, bool) {
	r := protocol.NewPacketReader(data)
	locale, err := r.String()
	if err != nil || len(locale) > 64 {
		return clientInfo{}, false
	}
	view, err := r.Byte()
	if err != nil || view > 32 {
		return clientInfo{}, false
	}
	chatMode, err := r.VarInt()
	if err != nil || chatMode < 0 || chatMode > 2 {
		return clientInfo{}, false
	}
	chatColors, err := r.Bool()
	if err != nil {
		return clientInfo{}, false
	}
	skin, err := r.Byte()
	if err != nil {
		return clientInfo{}, false
	}
	main, err := r.VarInt()
	if err != nil {
		return clientInfo{}, false
	}
	// Trailing options (text filtering, server listings, particle
	// status) are parsed when a feature needs them.
	return clientInfo{
		Locale:       locale,
		ViewDistance: view,
		ChatColors:   chatColors,
		SkinParts:    skin,
		MainHand:     main,
	}, true
}

// handleClientInfo stores the client's settings packet.
//
// A view distance that changes mid-game widens or narrows the window the
// player is being sent, and the real server acts on it at once:
// measured on 26.2, a client that has announced nothing is sent the
// default window and is given the whole of a larger one the moment its
// Client Information arrives (329 columns for a view distance of 8,
// where the default window is 49), and asking for a smaller distance
// again takes the excess away.
func (s *Server) handleClientInfo(sess *Session, p protocol.Packet) {
	info, ok := parseClientInfo(p.Data)
	if !ok {
		return
	}
	before := sess.viewRadius(s.ViewDistance)
	sess.infoMu.Lock()
	sess.info = info
	sess.infoMu.Unlock()
	if sess.viewRadius(s.ViewDistance) == before || sess.state.Load() != StatePlay {
		return
	}
	x, _, z, _, _, _ := sess.Pos()
	_ = s.sendWindow(sess, int(x)>>4, int(z)>>4)
}

// viewRadius returns the effective streamed-window radius: the server's
// view distance capped by the client's requested distance.
//
// A client that never sent Client Information is read as asking for the
// smallest distance the protocol allows, which is what the real server
// falls back to: measured on 26.2, a scripted client that announces
// nothing is sent the same 49 columns as one that asks for a view
// distance of 2, and a client asking for 3 is sent 81 - so the fallback
// is 2 and not the smallest window mc-go used to send.
func (sess *Session) viewRadius(serverVD int) int {
	sess.infoMu.Lock()
	v := int(sess.info.ViewDistance)
	sess.infoMu.Unlock()
	if v < minViewDistance {
		v = minViewDistance
	}
	if serverVD > 0 && v > serverVD {
		v = serverVD
	}
	if v < 1 {
		v = 1
	}
	return v
}
