package server

import (
	"math"
	"strings"
	"time"

	"mc-go/api/frontend"
	"mc-go/internal/event"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// handlePlay runs the Play stage: Join Game + Synchronize Player
// Position + spawn chunks, then holds the connection open.
// Chat/keep-alive handling lands with the signed-chat milestone;
// unknown play packets are ignored so the session stays up.
func (s *Server) handlePlay(sess *Session) {
	if !s.joinPlayPrepared(sess) {
		return
	}
	stopKA := sess.startKeepAlive()
	defer close(stopKA)
	for {
		p, err := sess.pr.ReadPacket()
		if err != nil {
			return
		}
		// Any packet proves liveness; keep-alive echoes feed the
		// timeout watchdog explicitly.
		sess.markAlive()
		// Every serverbound play packet the 776 client can send has a
		// route (see playServerbound); an id outside the table is not
		// a 776 packet and is dropped, which is vanilla's behavior for
		// an unknown id.
		profile, profileErr := sess.javaProfile()
		if profileErr != nil {
			return
		}
		if h, ok := profile.playHandler(p.ID); ok {
			sess.busy.Add(1)
			s.simulationAction(func() { h(s, sess, p) })
			sess.busy.Add(-1)
			// A handler can take a while — /tick step runs its ticks
			// inline, the way vanilla blocks its server thread — and by
			// the time it returns the client's keep-alive is already
			// sitting in the socket. That silence is ours, not the
			// client's, so refresh liveness rather than let the
			// watchdog read it as a dead peer.
			sess.markAlive()
		}
	}
}

// joinPlayOwned initializes and publishes a player atomically with respect to
// gameplay actions and ticks. The packet reader begins only after this completes.
func (s *Server) joinPlayOwned(sess *Session) bool {
	if s.ShuttingDown() {
		return false
	}
	overworld := s.dims[dimOverworld]
	sess.setDimension(overworld)
	// Joining opens the interaction gate; the client lifts it by
	// reporting its terrain loaded.
	sess.joinTick.Store(s.TickCount())
	x, y, z := s.playerSpawn()
	sess.setPos(x, y, z, 0, 0, false)
	sess.initSurvival()
	s.applySavedPlayer(sess)
	// A returning player resumes where they logged out; the spawn
	// position, entity record, and teleport all follow the restored
	// coordinates, not the world spawn.
	x, y, z, _, _, _ = sess.Pos()
	sess.setEntityID(s.entities.Spawn(&Entity{
		UUID: sess.getUUID(), Type: mustEntityType("player"),
		Dim: sess.dimName(),
		X:   x, Y: y, Z: z,
	}).ID)
	s.applySavedPlayerAttributes(sess)
	if err := s.sendJoinGame(sess); err != nil {
		return false
	}
	// Publish broadcast eligibility only after Join Game occupies its FIFO
	// position. Configuration completion can wait behind another owned action;
	// advertising Play before this point lets a time/chat broadcast precede
	// the packet that establishes the client's play state.
	sess.state.Store(StatePlay)
	// Stateless join sync in vanilla's order.
	if err := s.sendDifficulty(sess); err != nil {
		return false
	}
	if err := s.sendAbilities(sess); err != nil {
		return false
	}
	if err := s.sendSetHeldSlot(sess, 0); err != nil {
		return false
	}
	if err := s.sendUpdateRecipes(sess); err != nil {
		return false
	}
	// sendPlayerPermissionLevel on join: vanilla reports the joining
	// player's own op level, never a flat byte.
	if err := s.sendEntityEvent(sess, sess.entityID.Load(), opStatus(sess.opLevel())); err != nil {
		return false
	}
	if err := s.sendCommands(sess); err != nil {
		return false
	}
	if err := s.sendRecipeBookSettings(sess); err != nil {
		return false
	}
	if err := s.sendRecipeBookAdd(sess); err != nil {
		return false
	}
	if err := s.sendSyncPos(sess, x, y, z); err != nil {
		return false
	}
	if err := s.sendServerData(sess); err != nil {
		return false
	}
	// Tab list: vanilla first sends an all-actions packet with no
	// entries, then the joiner's own row, then rows for players
	// already online.
	if err := sess.write(sess.ids().PlayPlayerInfoUpd, encodePlayerInfoAdd(nil)); err != nil {
		return false
	}
	mine := selfEntry(sess)
	if err := sess.write(sess.ids().PlayPlayerInfoUpd, encodePlayerInfoAdd([]playerEntry{mine})); err != nil {
		return false
	}
	if err := s.sendPlayerInfoTo(sess); err != nil {
		return false
	}
	if err := s.sendScoreboardSnapshot(sess); err != nil {
		return false
	}
	if err := s.sendInitializeBorder(sess); err != nil {
		return false
	}
	if err := s.sendUpdateTime(sess); err != nil {
		return false
	}
	if err := s.sendDefaultSpawn(sess); err != nil {
		return false
	}
	if err := s.sendWeatherState(sess); err != nil {
		return false
	}
	if err := s.sendGameEvent(sess, 13, 0); err != nil {
		return false
	}
	if err := s.sendTickingState(sess); err != nil {
		return false
	}
	if err := s.sendTickingStep(sess, 0); err != nil {
		return false
	}
	// The chunk-cache center the client culls render distance around,
	// then the player window state and inventory.
	scx, scz := int(x)>>4, int(z)>>4
	s.sendCenterChunk(sess, scx, scz)
	if err := s.sendContainerContent(sess); err != nil {
		return false
	}
	if err := s.sendEntityHealthMetadata(sess, sess.entityID.Load(), 20); err != nil {
		return false
	}
	if err := s.sendRecipeBookUnlock(sess); err != nil {
		return false
	}
	if err := s.sendUpdateAdvancements(sess); err != nil {
		return false
	}
	if err := s.sendHealth(sess); err != nil {
		return false
	}
	// Experience rides right behind health, then the opening chunk
	// batch, then the attribute sync - vanilla's order.
	if err := s.sendSetExperience(sess, sess.xpProgress, sess.xpLevel, sess.xpTotal); err != nil {
		return false
	}
	// Spawn columns around the player so a real client has ground
	// underneath it; further columns stream in as it moves.
	if err := s.sendWindow(sess, scx, scz); err != nil {
		return false
	}
	if err := s.sendUpdateAttributes(sess); err != nil {
		return false
	}
	s.sendSavedPlayerEffects(sess)
	// Remote-player visibility: everyone already online spawns for the
	// joiner right after their tab-list rows arrive, along with their
	// equipment.
	if err := s.sendEntitySpawnsTo(sess); err != nil {
		return false
	}
	if err := s.sendEquipmentTo(sess); err != nil {
		return false
	}
	// Preserve the older incarnation until every required initial packet
	// has successfully entered this connection's bounded FIFO.
	s.replacePlayersOwned(&sess.playerRecord)
	s.commitPlayerAdmissionOwned(&sess.playerRecord)
	s.broadcastSystemChatComponentExcept(sess, component{
		color:     "yellow",
		translate: "multiplayer.player.joined",
		with:      []any{playerChatComponent(sess)},
	})
	s.broadcastPlayerInfoAdd(sess, []playerEntry{mine})
	// ...and the joiner spawns for everyone already online.
	if e, ok := playerEntity(sess); ok {
		s.broadcastEntitySpawn(sess, e)
	}
	// The burst is complete: leave fan-out may now reference us.
	sess.spawned.Store(true)
	s.broadcastEquipment(sess)
	// Vanilla marks every stat dirty when the player is added; the
	// first stats request then dumps the whole table.
	sess.markAllStatsDirty()
	sess.markAlive()
	sess.teleportAck.Store(-1)
	return true
}

// PlayerMove is published on TopicPlayerMove for plugins (movement
// anticheat is the main consumer). At is the server receipt time, From
// is the pre-packet position for speed math, and Latency is the last
// keep-alive round trip so detectors can excuse laggy clients.
type PlayerMove = event.PlayerMove

// handleMove applies a movement packet to the session position. All
// 776 movement packets carry a flags byte (0x01 on ground, 0x02
// pushing against wall) instead of a boolean. Truncated packets are ignored;
// invalid numeric fields disconnect before shared movement validation.
// The shared rule handles border, collision and plugin decisions.
func (s *Server) handleMove(sess *Session, p protocol.Packet) {
	ids := sess.ids()
	r := protocol.NewPacketReader(p.Data)
	x, y, z, yaw, pitch, _ := sess.Pos()
	var flags byte
	var err error
	switch p.ID {
	case ids.PlayMovePosSB:
		if x, err = r.Double(); err != nil {
			return
		}
		if y, err = r.Double(); err != nil {
			return
		}
		if z, err = r.Double(); err != nil {
			return
		}
		if flags, err = r.Byte(); err != nil {
			return
		}
	case ids.PlayMovePosRotSB:
		if x, err = r.Double(); err != nil {
			return
		}
		if y, err = r.Double(); err != nil {
			return
		}
		if z, err = r.Double(); err != nil {
			return
		}
		if yaw, err = r.Float(); err != nil {
			return
		}
		if pitch, err = r.Float(); err != nil {
			return
		}
		if flags, err = r.Byte(); err != nil {
			return
		}
	case ids.PlayMoveRotSB:
		if yaw, err = r.Float(); err != nil {
			return
		}
		if pitch, err = r.Float(); err != nil {
			return
		}
		if flags, err = r.Byte(); err != nil {
			return
		}
	case ids.PlayMoveStatusSB:
		if flags, err = r.Byte(); err != nil {
			return
		}
	}
	hasPosition := p.ID == ids.PlayMovePosSB || p.ID == ids.PlayMovePosRotSB
	hasRotation := p.ID == ids.PlayMoveRotSB || p.ID == ids.PlayMovePosRotSB
	if invalidPlayerPacketNumbers(hasPosition, hasRotation, x, y, z, yaw, pitch) {
		s.disconnectWithComponent(sess, translatable("multiplayer.disconnect.invalid_player_movement"))
		return
	}
	s.applyPlayerMoveOwned(sess, frontend.Movement{
		X: x, Y: y, Z: z, Yaw: yaw, Pitch: pitch, OnGround: flags&0x01 != 0,
	})
}

// applyPlayerMoveOwned adapts an admitted Java player's actual record to the
// shared movement action used by neutral avatar connections.
func (s *Server) applyPlayerMoveOwned(sess *Session, move frontend.Movement) bool {
	return s.applyPlayerRecordMoveOwned(&sess.playerRecord, move)
}

// finishJavaPlayerMoveOwned retains Java's existing survival/statistics behavior.
// Neutral presence avatars deliberately participate only in movement/visibility.
func (s *Server) finishJavaPlayerMoveOwned(sess *Session, from, move frontend.Movement, at time.Time) {
	ox, oy, oz, og := from.X, from.Y, from.Z, from.OnGround
	x, y, z, onGround := move.X, move.Y, move.Z, move.OnGround
	// handleMovePlayer's player.move applies the inside-block scan on
	// every accepted move packet — a same-position resend still scans
	// the occupied cells (measured: a teleport echo into lava lands
	// the ignite and the four-damage hit).
	s.playerInsideBlocks(sess)
	// ChunkMap.updatePlayerPos: a position packet re-evaluates the
	// entity map against this player.
	s.retrackSession(sess)
	// location_changed fires on a real position change — a same-pos
	// resend does not retrigger frost walker (measured).
	if x != ox || y != oy || z != oz {
		s.frostWalkerStep(sess, x, y, z, onGround)
	}
	sess.setKnownMovement([3]float64{x - ox, y - oy, z - oz})
	s.awardMoveStats(sess, ox, oy, oz, x, y, z, onGround)
	// Vanilla's entity tick runs physics between move packets: a
	// resting entity settles onto its support and the onGround flag
	// resolves from that collision — so once a tick has elapsed, the
	// flag the jump check reads is support-derived, not the previous
	// packet's claim.
	if at.Sub(sess.lastMoveAt) >= 50*time.Millisecond {
		og = s.supportedBelow(sess, ox, oy, oz)
	}
	sess.lastMoveAt = at
	// handleMovePlayer's jump check: the server-side onGround flag was
	// up, the packet reports airborne, and the move rose — that's the
	// client's jump, so jumpFromGround banks the jump stat. Motion
	// stays client-driven; only the counter applies.
	if og && !onGround && y > oy {
		sess.addCustomStat("jump", 1)
	}
	// Sneaking drops step emissions at the source (getMovementEmission
	// is NONE), but checkFallDamage posts HIT_GROUND unconditionally on
	// landing — a sensor in earshot dampens it and the sneak earns
	// avoid_vibration.
	if onGround && sess.fallDistance > 0 {
		// Entity.checkFallDamage posts HIT_GROUND on landing — a
		// sneaking faller's emission is dampened inside the listener.
		s.emitGameEvent(sess.dimension(), "hit_ground", x, y, z, sess, "")
	}
	// Fall-distance tracking: Entity.checkFallDamage accumulates
	// downward travel only outside water (wasTouchingWater), whatever
	// the packet's grounded claim — even the landing packet's own
	// descent counts before the edge resets it.
	fallFeetName, _ := world.StateOwner(sess.world().GetBlock(world.Pos{
		X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z)),
	}))
	if y < oy && fallFeetName != "water" {
		if !sess.fallStart {
			sess.fallStart = true
			sess.fallStartY = oy
			// Vanilla's trackStartFallingPosition: an explosion impact
			// latched at or below the apex fires fall_after_explosion,
			// then both fields clear either way.
			if sess.explodeImpact != nil {
				if sess.explodeImpact[1] <= oy {
					s.fireFallAfterExplosion(sess, sess.explodeImpact[1], oy, sess.explodeCause)
				}
				sess.explodeImpact = nil
				sess.explodeCause = 0
			}
		}
		sess.fallDistance += oy - y
		s.fireSlideDown(sess, x, y, z)
	}
	if onGround {
		if sess.fallDistance > 0 && sess.fallStart {
			s.fireFallFromHeight(sess, sess.fallStartY, y)
		}

		sess.fallStart = false
		// checkFallDamage hands the support block to fallOn before
		// causeFallDamage — on farmland the trample roll (rand <
		// fallDist-0.5, bbW²·bbH > 0.512) turns it to dirt first; a
		// player always passes the mobGriefing gate. Pose sets the
		// box height (sneak 1.5, else stand 1.8).
		if sess.fallDistance > 0 {
			if dm := s.dims[sess.dimName()]; dm != nil {
				sup := world.Pos{
					X: int(math.Floor(x)),
					Y: int(math.Floor(y - 1e-5)),
					Z: int(math.Floor(z)),
				}
				switch n, _ := blockNameAt(dm, sup); n {
				case "farmland":
					h := 1.8
					if sess.sneaking {
						h = 1.5
					}
					s.farmlandTrample(dm, sup, x, y, z, true,
						0.6*0.6*h, sess.fallDistance)
				case "turtle_egg":
					// TurtleEggBlock.fallOn — a player is never a
					// Zombie, so the landing always rolls
					// destroyEgg(3).
					s.turtleEggDestroy(dm, sup, nil, sess, 3)
				}
			}
		}
		s.applyFallDamage(sess)
	} else if y > oy {
		sess.fallDistance = 0
		sess.fallStart = false
	}
}

// awardMoveStats buckets the travelled distance into the *_one_cm
// counters the way vanilla's checkMovementStatistics does. The 26.2
// branch order measured on the jar: a passenger returns early (its
// distance lands via checkRidingStatistics), then the swimming flag
// (updateSwimming's enter/stay asymmetry — enter only while
// submerged), eyes under water, feet in water, climbable-up,
// grounded sprint/crouch/walk (sprint wins over crouch), fall-flying —
// and every airborne remainder banks fly_one_cm on a 2D XZ leg
// over 25cm — the fly arm ignores dY, so a pure rise banks nothing
// (fall_one_cm lives in causeFallDamage on the landing edge, not
// here). The water arms and the sprint arm also credit food
// exhaustion.
//
// playerBoxCollides tests the player's current pose box against positioned
// block shapes, retaining vanilla's already-colliding-shape exemption.
func (s *Server) playerBoxCollides(sess *Session, ox, oy, oz, nx, ny, nz float64) bool {
	if sess.mode() == GameModeSpectator {
		return false
	}
	half, height := s.playerCollisionDims(&sess.playerRecord)
	return s.playerBoxCollidesInDimension(sess.dimension(), ox, oy, oz, nx, ny, nz, half, height)
}

// playerBoxCollidesIn is the standing-avatar entry point for callers without
// a player record. It consumes block storage, never a transport or session.
func playerBoxCollidesIn(w *world.World, ox, oy, oz, nx, ny, nz float64) bool {
	half, height := playerPoseDims(0)
	return playerBoxCollidesWithDims(w, ox, oy, oz, nx, ny, nz, half, height)
}

// supportedBelow reports whether a thin slab under the player's box
// bottom overlaps collision — the state vanilla's tick-resolved
// onGround flag settles into once physics has had a tick to land the
// entity on its footing.
func (s *Server) supportedBelow(sess *Session, x, y, z float64) bool {
	const half = 0.3
	by := int(math.Floor(y - 1e-4))
	if float64(by+1) < y-1e-4 {
		return false
	}
	for bx := int(math.Floor(x - half)); float64(bx) < x+half; bx++ {
		for bz := int(math.Floor(z - half)); float64(bz) < z+half; bz++ {
			b := sess.world().GetBlock(world.Pos{X: bx, Y: by, Z: bz})
			if !world.NonSolid(b) && !moveNoCollision(b) {
				return true
			}
		}
	}
	return false
}

// moveNoCollision adds the empty-collision-shape blocks the NonSolid
// table doesn't cover: fluids, portals, and the attachables a player
// moves straight through.
func moveNoCollision(b world.Block) bool {
	name, ok := world.StateOwner(b)
	if !ok {
		return false
	}
	switch name {
	case "water", "lava", "bubble_column", "nether_portal", "end_portal",
		"end_gateway", "cobweb", "powder_snow", "light", "structure_void":
		return true
	}
	return strings.HasSuffix(name, "_sign") ||
		strings.HasSuffix(name, "_hanging_sign") ||
		strings.HasSuffix(name, "_banner") ||
		strings.HasSuffix(name, "_button") ||
		strings.HasSuffix(name, "_pressure_plate") ||
		strings.HasSuffix(name, "_sapling") ||
		name == "attached_melon_stem" || name == "attached_pumpkin_stem" ||
		name == "melon_stem" || name == "pumpkin_stem"
}

func (s *Server) awardMoveStats(sess *Session, ox, oy, oz, x, y, z float64, onGround bool) {
	if s.GameMode == GameModeSpectator || sess.riding.Load() != 0 {
		return
	}
	dx, dy, dz := x-ox, y-oy, z-oz
	cmXZ := int32(math.Round(math.Sqrt(dx*dx+dz*dz) * 100))
	cm3 := int32(math.Round(math.Sqrt(dx*dx+dy*dy+dz*dz) * 100))
	if cmXZ == 0 && cm3 == 0 {
		return
	}
	bx, bz := int(math.Floor(x)), int(math.Floor(z))
	feetName, _ := world.StateOwner(sess.world().GetBlock(world.Pos{X: bx, Y: int(math.Floor(y)), Z: bz}))
	eyeName, _ := world.StateOwner(sess.world().GetBlock(world.Pos{X: bx, Y: int(math.Floor(y + 1.62)), Z: bz}))
	// Entity.updateSwimming: entering the swim flag needs sprint +
	// eyes under water + water at the feet cell (a shallow sprint
	// stays on the walk_on_water arm); staying in it only needs
	// sprint + water at the feet.
	if sess.swimming {
		sess.swimming = sess.sprinting && feetName == "water"
	} else {
		sess.swimming = sess.sprinting && eyeName == "water" && feetName == "water"
	}
	// causeFoodExhaustion is gated by invulnerable in vanilla —
	// creative and spectator never accumulate exhaustion; spectator
	// already returned above.
	exhaust := s.GameMode != GameModeCreative
	switch {
	case sess.swimming:
		sess.addCustomStat("swim_one_cm", cm3)
		if exhaust {
			sess.addExhaustion(0.01 * float32(cm3) * 0.01)
		}
	case eyeName == "water":
		sess.addCustomStat("walk_under_water_one_cm", cm3)
		if exhaust {
			sess.addExhaustion(0.01 * float32(cm3) * 0.01)
		}
	case feetName == "water":
		sess.addCustomStat("walk_on_water_one_cm", cmXZ)
		if exhaust {
			sess.addExhaustion(0.01 * float32(cmXZ) * 0.01)
		}
	case isClimbableBlock(feetName) && dy > 0:
		sess.addCustomStat("climb_one_cm", int32(math.Round(dy*100)))
	case onGround:
		switch {
		case sess.sprinting:
			sess.addCustomStat("sprint_one_cm", cmXZ)
			if exhaust {
				sess.addExhaustion(0.1 * float32(cmXZ) * 0.01)
			}
		case sess.sneaking:
			sess.addCustomStat("crouch_one_cm", cmXZ)
		default:
			sess.addCustomStat("walk_one_cm", cmXZ)
		}
	case sess.fallFlying:
		sess.addCustomStat("aviate_one_cm", cm3)
	case cmXZ > 25:
		// The fly arm reads only the horizontal leg — a straight
		// rise banks nothing (measured on vanilla).
		sess.addCustomStat("fly_one_cm", cmXZ)
	}
}

// isClimbableBlock names the blocks vanilla counts under climb_one_cm.
func isClimbableBlock(name string) bool {
	switch name {
	case "ladder", "vine", "scaffolding", "twisting_vines",
		"twisting_vines_plant", "weeping_vines", "weeping_vines_plant",
		"cave_vines", "cave_vines_plant":
		return true
	}
	return false
}

func badFloat(v float64) bool {
	return v != v || v > 1e308 || v < -1e308
}

// startKeepAlive schedules pings on Server.KeepAliveEvery. Its response
// timeout starts with the first unanswered ping actually written to the
// peer; any incoming packet resets it. The caller must close the returned
// channel when the session ends.
func (sess *Session) startKeepAlive() chan struct{} {
	every := sess.server.KeepAliveEvery
	if every <= 0 {
		every = 10 * time.Second
	}
	timeout := sess.server.KeepAliveTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				id := sess.kaNext.Add(1)
				sess.kaTime.Store(time.Now().UnixNano())
				var b protocol.Buffer
				b.Long(id)
				if err := sess.write(sess.ids().PlayKeepAliveCB, b.Bytes()); err != nil {
					return
				}
				// A queued ping may still be behind a detached chunk encoder.
				// Only an unanswered request actually written to the peer can
				// establish a client timeout; server work is not peer silence.
				awaiting := sess.kaAwaitingSince.Load()
				if awaiting != 0 && sess.busy.Load() == 0 && time.Since(time.Unix(0, awaiting)) > timeout && time.Since(sess.lastAlive()) > timeout {
					sess.server.SendPlayDisconnect(sess, "Timed out")
					// Let the writer flush the kick before the socket
					// goes away; the read loop exits on the close.
					time.Sleep(200 * time.Millisecond)
					_ = sess.conn.Close()
					return
				}
			}
		}
	}()
	return stop
}

// sendDifficulty syncs the server difficulty (normal, unlocked).
func (s *Server) sendDifficulty(sess *Session) error {
	s.rulesMu.RLock()
	d, locked := s.difficulty, s.difficultyLocked
	s.rulesMu.RUnlock()
	return s.sendDifficultyLocked(sess, d, locked)
}

// Player ability bit masks (clientbound flags).
const (
	abilityFlagFlying = 0x02
)

// abilityFlagsCreative advertises creative mode: invulnerable + may
// fly + instant break. The flying bit itself stays clear until takeoff.
const abilityFlagsCreative = 0x01 | 0x04 | 0x08

// abilityFlagsSpectator advertises spectator mode: invulnerable +
// flying + may fly, no instant break. Measured off an
// adventure-to-spectator /gamemode switch, which answers flags 0x07.
const abilityFlagsSpectator = 0x01 | 0x02 | 0x04

// sendAbilities syncs the game mode's ability flags and default speeds.
// The mode is the player's own, not the server default: /gamemode
// resends this on every switch. Per-mode flags measured off those
// switches: creative 0x0D (plus the flying bit while airborne),
// spectator 0x07, anything else 0x00.
func (s *Server) sendAbilities(sess *Session) error {
	flags := byte(0)
	switch sess.mode() {
	case GameModeCreative:
		flags = abilityFlagsCreative
		if sess.flying {
			flags |= abilityFlagFlying
		}
	case GameModeSpectator:
		flags = abilityFlagsSpectator
	}
	var b protocol.Buffer
	b.Byte(flags)
	b.Float(0.05)
	b.Float(sess.playerWalkingSpeed())
	return sess.write(sess.ids().PlayAbilitiesCB, b.Bytes())
}

// sendJoinGame captures authoritative fields; the selected profile owns layout.
func (s *Server) sendJoinGame(sess *Session) error {
	profile, err := sess.javaProfile()
	if err != nil {
		return err
	}
	d := sess.dimension()
	dimensionType, err := profile.projectRegistryID("minecraft:dimension_type", d.typeID)
	if err != nil {
		return err
	}
	join := javaJoinGame{
		EntityID:   sess.entityID.Load(),
		Dimensions: []string{dimOverworld, dimEnd, dimNether},
		MaxPlayers: int32(s.MaxPlayers), ViewDistance: int32(s.ViewDistance), SimulationDistance: int32(s.SimulationDistance),
		RespawnScreen: true, DimensionType: dimensionType, Dimension: d.name,
		HashedSeed: hashedSeed(s.worldSeed()), GameMode: sess.mode(), PreviousGameMode: 255, Flat: d.flat,
		SeaLevel: d.seaLevel,
	}
	return sess.write(sess.ids().PlayJoinGame, profile.encodeJoinGame(join))
}

// sendSyncPos teleports the player to x,y,z. Teleport flags 0 means
// absolute coordinates; each call issues a fresh teleport id (the spawn
// teleport is 1) for the client to confirm.
// syncRelRotDelta are the Relative flags a portal transition sends
// (Relative.ROTATION + Relative.DELTA: Y_ROT, X_ROT, DELTA_X/Y/Z,
// ROTATE_DELTA): the client keeps its rotation (turned by the yaw
// field) and its motion.
const syncRelRotDelta = 1<<3 | 1<<4 | 1<<5 | 1<<6 | 1<<7 | 1<<8

// syncRelPitchDelta are an end portal arrival's Relative flags
// (Relative.DELTA + X_ROT): an absolute yaw, the client's own pitch
// and motion.
const syncRelPitchDelta = 1<<4 | 1<<5 | 1<<6 | 1<<7 | 1<<8

// sendSyncPosRelative sends an absolute position with relative
// rotation (yaw/pitch deltas) and kept motion.
func (s *Server) sendSyncPosRelative(sess *Session, x, y, z float64, dyaw, dpitch float32) error {
	return s.sendSyncPosFlags(sess, x, y, z, dyaw, dpitch, syncRelRotDelta)
}

// sendSyncPosFlags sends a position sync with explicit Relative flags.
func (s *Server) sendSyncPosFlags(sess *Session, x, y, z float64, yaw, pitch float32, flags int32) error {
	id := sess.teleportNext.Add(1) - 1
	var b protocol.Buffer
	b.VarInt(id)
	b.Double(x)
	b.Double(y)
	b.Double(z)
	b.Double(0)
	b.Double(0)
	b.Double(0)
	b.Float(yaw)
	b.Float(pitch)
	b.Int(flags)
	return sess.write(sess.ids().PlaySyncPos, b.Bytes())
}

func (s *Server) sendSyncPos(sess *Session, x, y, z float64) error {
	_, _, _, yaw, pitch, _ := sess.Pos()
	id := sess.teleportNext.Add(1) - 1
	var b protocol.Buffer
	b.VarInt(id)
	b.Double(x)
	b.Double(y)
	b.Double(z)
	b.Double(0) // velocity x
	b.Double(0) // velocity y
	b.Double(0) // velocity z
	b.Float(yaw)
	b.Float(pitch)
	b.Int(0) // teleport flags: absolute
	return sess.write(sess.ids().PlaySyncPos, b.Bytes())
}

// SendPlayDisconnect kicks a play-stage session with a plain-text reason.
// The play disconnect payload is an NBT text component; a JSON string
// there fails the client's decode and hides the reason.
func (s *Server) SendPlayDisconnect(sess *Session, reason string) {
	sess.closing.Store(true)
	_ = sess.write(sess.ids().PlayDisconnect, literal(reason).content())
}
