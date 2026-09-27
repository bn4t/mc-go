package server

import (
	"math"
	"strings"
	"time"

	"mc-go/internal/event"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// handlePlay runs the Play stage: Join Game + Synchronize Player
// Position + spawn chunks, then holds the connection open.
// Chat/keep-alive handling lands with the signed-chat milestone;
// unknown play packets are ignored so the session stays up.
func (s *Server) handlePlay(sess *Session) {
	// A new login under a name already online disconnects the older
	// session, never the new one (PlayerList.placeNewPlayer).
	name := sess.getName()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other != sess && other.getName() == name {
			s.disconnectWithComponent(other, translatable("multiplayer.disconnect.duplicate_login"))
		}
		return true
	})
	overworld := s.dims[dimOverworld]
	sess.dim.Store(overworld)
	// Joining opens the interaction gate; the client lifts it by
	// reporting its terrain loaded.
	sess.joinTick.Store(s.TickCount())
	x, y, z := s.WorldSpawn()
	sess.setPos(x, y, z, 0, 0, false)
	sess.initSurvival()
	s.applySavedPlayer(sess)
	// A returning player resumes where they logged out; the spawn
	// position, entity record, and teleport all follow the restored
	// coordinates, not the world spawn.
	x, y, z, _, _, _ = sess.Pos()
	sess.entityID.Store(s.entities.Spawn(&Entity{
		UUID: sess.getUUID(), Type: sess.ids().EntityTypePlayer,
		Dim: sess.dimName(),
		X:   x, Y: y, Z: z,
	}).ID)
	if err := s.sendJoinGame(sess); err != nil {
		return
	}
	// Stateless join sync in vanilla's order.
	if err := s.sendDifficulty(sess); err != nil {
		return
	}
	if err := s.sendAbilities(sess); err != nil {
		return
	}
	if err := s.sendSetHeldSlot(sess, 0); err != nil {
		return
	}
	if err := s.sendUpdateRecipes(sess); err != nil {
		return
	}
	if err := s.sendEntityEvent(sess, sess.entityID.Load(), 24); err != nil {
		return
	}
	if err := s.sendCommands(sess); err != nil {
		return
	}
	if err := s.sendRecipeBookSettings(sess); err != nil {
		return
	}
	if err := s.sendRecipeBookAdd(sess); err != nil {
		return
	}
	if err := s.sendSyncPos(sess, x, y, z); err != nil {
		return
	}
	if err := s.sendServerData(sess); err != nil {
		return
	}
	// Tab list: vanilla first sends an all-actions packet with no
	// entries, then the joiner's own row, then rows for players
	// already online.
	if err := sess.write(sess.ids().PlayPlayerInfoUpd, encodePlayerInfoAdd(nil)); err != nil {
		return
	}
	mine := selfEntry(sess)
	if err := sess.write(sess.ids().PlayPlayerInfoUpd, encodePlayerInfoAdd([]playerEntry{mine})); err != nil {
		return
	}
	if err := s.sendPlayerInfoTo(sess); err != nil {
		return
	}
	if err := s.sendInitializeBorder(sess); err != nil {
		return
	}
	if err := s.sendUpdateTime(sess); err != nil {
		return
	}
	if err := s.sendDefaultSpawn(sess); err != nil {
		return
	}
	if err := s.sendGameEvent(sess, 13, 0); err != nil {
		return
	}
	if err := s.sendTickingState(sess); err != nil {
		return
	}
	if err := s.sendTickingStep(sess, 0); err != nil {
		return
	}
	// The chunk-cache center the client culls render distance around,
	// then the player window state and inventory.
	scx, scz := int(x)>>4, int(z)>>4
	s.sendCenterChunk(sess, scx, scz)
	if err := s.sendContainerContent(sess); err != nil {
		return
	}
	if err := s.sendEntityHealthMetadata(sess, sess.entityID.Load(), 20); err != nil {
		return
	}
	if err := s.sendRecipeBookUnlock(sess); err != nil {
		return
	}
	if err := s.sendUpdateAdvancements(sess); err != nil {
		return
	}
	if err := s.sendHealth(sess); err != nil {
		return
	}
	// Experience rides right behind health, then the opening chunk
	// batch, then the attribute sync - vanilla's order.
	if err := s.sendSetExperience(sess, sess.xpProgress, sess.xpLevel, sess.xpTotal); err != nil {
		return
	}
	// Spawn columns around the player so a real client has ground
	// underneath it; further columns stream in as it moves.
	if err := s.sendWindow(sess, scx, scz); err != nil {
		return
	}
	if err := s.sendUpdateAttributes(sess); err != nil {
		return
	}
	// Remote-player visibility: everyone already online spawns for the
	// joiner right after their tab-list rows arrive, along with their
	// equipment.
	if err := s.sendEntitySpawnsTo(sess); err != nil {
		return
	}
	if err := s.sendEquipmentTo(sess); err != nil {
		return
	}
	if s.bus != nil {
		s.bus.Publish(event.TopicPlayerJoin, map[string]string{"name": sess.getName()})
	}
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
		if h, ok := playServerbound[p.ID]; ok {
			sess.busy.Add(1)
			h(s, sess, p)
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

// PlayerMove is published on TopicPlayerMove for plugins (movement
// anticheat is the main consumer). At is the server receipt time, From
// is the pre-packet position for speed math, and Latency is the last
// keep-alive round trip so detectors can excuse laggy clients.
type PlayerMove struct {
	Name                string
	X, Y, Z             float64
	FromX, FromY, FromZ float64
	Yaw, Pitch          float32
	OnGround            bool
	At                  time.Time
	Latency             time.Duration
}

// handleMove applies a movement packet to the session position. All
// 776 movement packets carry a flags byte (0x01 on ground, 0x02
// pushing against wall) instead of a boolean. Malformed packets (bad
// floats, outside the world border) are dropped without touching
// state; speed enforcement lands with the physics milestone.
func (s *Server) handleMove(sess *Session, p protocol.Packet) {
	const border = 29999984.0
	ids := sess.ids()
	r := protocol.NewPacketReader(p.Data)
	ox, oy, oz, yaw, pitch, og := sess.Pos()
	x, y, z := ox, oy, oz
	at := time.Now()
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
	onGround := flags&0x01 != 0
	if badFloat(x) || badFloat(y) || badFloat(z) || badFloat(float64(yaw)) || badFloat(float64(pitch)) {
		return
	}
	if x < -border || x > border || z < -border || z > border || y < -2048 || y > 2048 {
		return
	}
	// A passenger's position is the vehicle's business: vanilla snaps
	// the packet position back to the current one and only keeps the
	// rotation (and the grounded flag) — no move handling at all.
	if sess.riding.Load() != 0 {
		sess.setPos(ox, oy, oz, yaw, pitch, onGround)
		if eid := sess.entityID.Load(); eid != 0 {
			s.entities.Update(eid, ox, oy, oz, yaw, pitch, onGround)
			s.broadcastEntityTeleport(sess, eid, ox, oy, oz, yaw, pitch, onGround)
		}
		return
	}
	// Vanilla's "moved wrongly" gate: a position packet whose target
	// AABB collides with a solid block it didn't already overlap is
	// rejected — the player is teleported back and none of the move
	// handling (stats, fall tracking, broadcast) runs.
	if x != ox || y != oy || z != oz {
		if s.playerBoxCollides(sess, ox, oy, oz, x, y, z) {
			if err := s.sendSyncPos(sess, ox, oy, oz); err != nil {
				return
			}
			return
		}
	}
	sess.setPos(x, y, z, yaw, pitch, onGround)
	// ChunkMap.updatePlayerPos: a position packet re-evaluates the
	// entity map against this player.
	s.retrackSession(sess)
	// location_changed fires on a real position change — a same-pos
	// resend does not retrigger frost walker (measured).
	if x != ox || y != oy || z != oz {
		s.frostWalkerStep(sess, x, y, z, onGround)
	}
	mdx, mdz := x-ox, z-oz
	sess.lastHorizSq = mdx*mdx + mdz*mdz
	sess.lastDelta = [3]float64{x - ox, y - oy, z - oz}
	s.awardMoveStats(sess, ox, oy, oz, x, y, z, onGround)
	// handleMovePlayer's jump check: the server-side onGround flag was
	// up, the packet reports airborne, and the move rose — that's the
	// client's jump, so jumpFromGround banks the jump stat. Motion
	// stays client-driven; only the counter applies.
	if og && !onGround && y > oy {
		sess.addCustomStat("jump", 1)
	}
	// Sprinting drains hunger by distance travelled.
	if sess.sprinting && s.GameMode == GameModeSurvival {
		dx, dz := x-ox, z-oz
		sess.addExhaustion(0.1 * float32(math.Sqrt(dx*dx+dz*dz)))
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
	// Fall-distance tracking for survival fall damage.
	if y < oy && !onGround {
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
	} else if onGround {
		if sess.fallDistance > 0 && sess.fallStart {
			s.fireFallFromHeight(sess, sess.fallStartY, y)
		}
		sess.fallStart = false
		s.applyFallDamage(sess)
	} else if y > oy {
		sess.fallDistance = 0
		sess.fallStart = false
	}
	// Track the move on the player's entity and fan it out: remote
	// clients render each other from these teleports.
	if eid := sess.entityID.Load(); eid != 0 {
		s.entities.Update(eid, x, y, z, yaw, pitch, onGround)
		s.broadcastEntityTeleport(sess, eid, x, y, z, yaw, pitch, onGround)
	}
	if s.bus != nil {
		s.bus.Publish(event.TopicPlayerMove, PlayerMove{
			Name: sess.getName(), X: x, Y: y, Z: z,
			FromX: ox, FromY: oy, FromZ: oz,
			Yaw: yaw, Pitch: pitch, OnGround: onGround,
			At: at, Latency: sess.latency(),
		})
	}
	// Stream newly entered columns; already-sent ones are skipped.
	// The cache-center notice follows the window so a far teleport
	// loads before it re-centers.
	_ = s.sendWindow(sess, int(x)>>4, int(z)>>4)
	s.sendCenterChunk(sess, int(x)>>4, int(z)>>4)
}

// awardMoveStats buckets the travelled distance into the *_one_cm
// counters the way vanilla's checkMovementStatistics does. The 26.2
// branch order measured on the jar: a passenger returns early (its
// distance lands via checkRidingStatistics), then swimming, eyes
// under water, feet in water, climbable-up, grounded, fall-flying —
// and every airborne remainder banks fly_one_cm once the horizontal
// leg passes 25cm (there is no fall_one_cm anymore).
//
// playerBoxCollides approximates vanilla's "moved wrongly" check
// (noCollision + isEntityCollidingWithAnythingNew): the 0.6×1.8
// player AABB at the packet's target position is scanned for solid
// cells that the box at the current position did not already
// overlap — dipping a toe into already-colliding blocks is
// tolerated, entering new ones rejects the whole move.
func (s *Server) playerBoxCollides(sess *Session, ox, oy, oz, nx, ny, nz float64) bool {
	const half = 0.3
	overlapsOld := func(bx, by, bz int) bool {
		return ox+half > float64(bx) && ox-half < float64(bx+1) &&
			oy+1.8 > float64(by) && oy < float64(by+1) &&
			oz+half > float64(bz) && oz-half < float64(bz+1)
	}
	for bx := int(math.Floor(nx - half)); float64(bx) < nx+half; bx++ {
		for bz := int(math.Floor(nz - half)); float64(bz) < nz+half; bz++ {
			for by := int(math.Floor(ny)); float64(by) < ny+1.8; by++ {
				b := sess.world().GetBlock(world.Pos{X: bx, Y: by, Z: bz})
				if world.NonSolid(b) || moveNoCollision(b) {
					continue
				}
				if !overlapsOld(bx, by, bz) {
					return true
				}
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
	switch {
	case sess.sprinting && eyeName == "water":
		sess.addCustomStat("swim_one_cm", cm3)
	case eyeName == "water":
		sess.addCustomStat("walk_under_water_one_cm", cm3)
	case feetName == "water":
		sess.addCustomStat("walk_on_water_one_cm", cmXZ)
	case isClimbableBlock(feetName) && dy > 0:
		sess.addCustomStat("climb_one_cm", int32(math.Round(dy*100)))
	case onGround:
		switch {
		case sess.sneaking:
			sess.addCustomStat("crouch_one_cm", cmXZ)
		case sess.sprinting:
			sess.addCustomStat("sprint_one_cm", cmXZ)
		default:
			sess.addCustomStat("walk_one_cm", cmXZ)
		}
	case sess.fallFlying:
		sess.addCustomStat("aviate_one_cm", cm3)
	case cmXZ > 25:
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

// startKeepAlive pings the client on Server.KeepAliveEvery and drops
// the session past KeepAliveTimeout without a response. The caller
// must close the returned channel when the session ends.
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
				if sess.busy.Load() == 0 && time.Since(sess.lastAlive()) > timeout {
					sess.server.SendPlayDisconnect(sess, `{"text":"Timed out"}`)
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
	b.Float(0.1)
	return sess.write(sess.ids().PlayAbilitiesCB, b.Bytes())
}

// sendJoinGame writes a 776 Join Game. Field order follows the public
// protocol docs; dimension names lists every dimension on the server.
func (s *Server) sendJoinGame(sess *Session) error {
	var b protocol.Buffer
	b.Int(sess.entityID.Load()) // entity id — vanilla sends the real one
	b.Bool(false)               // hardcore
	b.VarInt(3)                 // dimension names, in vanilla's Join Game order
	b.String("minecraft:overworld")
	b.String("minecraft:the_end")
	b.String("minecraft:the_nether")
	b.VarInt(int32(s.MaxPlayers))
	b.VarInt(int32(s.ViewDistance))       // view distance
	b.VarInt(int32(s.SimulationDistance)) // simulation distance
	b.Bool(false)                         // reduced debug info
	b.Bool(true)                          // enable respawn screen
	b.Bool(false)                         // do limited crafting
	d := sess.dimension()
	b.VarInt(d.typeID) // dimension type id
	b.String(d.name)
	b.Long(hashedSeed(s.worldSeed())) // obfuscated world seed
	b.Byte(sess.mode())
	b.Byte(255) // previous gamemode: none
	b.Bool(false)
	b.Bool(d.flat) // is flat
	b.Bool(false)
	b.VarInt(0) // portal cooldown
	b.VarInt(d.seaLevel)
	b.Bool(false) // online mode
	b.Bool(false) // enforces secure chat
	return sess.write(sess.ids().PlayJoinGame, b.Bytes())
}

// sendSyncPos teleports the player to x,y,z. Teleport flags 0 means
// absolute coordinates; each call issues a fresh teleport id (the spawn
// teleport is 1) for the client to confirm.
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

// SendPlayDisconnect kicks a play-stage session with a chat message.
func (s *Server) SendPlayDisconnect(sess *Session, reason string) {
	sess.closing.Store(true)
	var b protocol.Buffer
	b.String(reason)
	_ = sess.write(sess.ids().PlayDisconnect, b.Bytes())
}
