package server

import (
	"mc-go/internal/event"
	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Dig statuses, clean-room from the public protocol docs (26.1.2 spec:
// 0=start 1=cancel 2=finish; 3/4 (drop stack/one) are handled by
// handlePlayerActionExtras, 5+ are ignored until that milestone).
const (
	digStart  = 0
	digCancel = 1
	digFinish = 2
)

// encodeBlockUpdate builds a Block Update body: packed BlockPos long +
// VarInt block state id. Field order follows the public 26.1.2 spec;
// VERIFY 26.2 by capture (see const.go).
func encodeBlockUpdate(x, y, z int32, state int32) []byte {
	var b protocol.Buffer
	b.Position(x, y, z)
	b.VarInt(state)
	return b.Bytes()
}

// broadcastBlockState sends a Block Update for pos to every play-stage
// session tracking its chunk in dimension d.
func (s *Server) broadcastBlockState(d *dimension, pos world.Pos, blk world.Block) {
	cx, cz := pos.X>>4, pos.Z>>4
	body := encodeBlockUpdate(int32(pos.X), int32(pos.Y), int32(pos.Z), blockState(blk))
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dimName() != d.name || !sess.hasChunk(cx, cz) {
			return true
		}
		_ = sess.write(sess.ids().PlayBlockUpdateCB, body)
		return true
	})
}

// broadcastBlockUpdateIn pushes a raw block-state change (dimension
// maintenance such as portal creation) to chunk trackers.
func (s *Server) broadcastBlockUpdateIn(dimName string, pos world.Pos, blk world.Block) {
	d := s.dims[dimName]
	if d == nil {
		return
	}
	s.broadcastBlockState(d, pos, blk)
}

// setBlockAndBroadcast is the shared write path for /setblock and digging:
// world update, BlockChange event, and Block Update fan-out. It returns
// the light sections the change affects so callers can emit the Light
// Update after their acknowledgement, matching vanilla's order. The
// dimension is the acting player's; nameless actors land in the
// overworld.
func (s *Server) setBlockAndBroadcast(player string, pos world.Pos, blk world.Block) []int {
	d := s.dimOf(player)
	oldTop := columnTop(d, pos.X, pos.Z)
	d.w.SetBlock(pos, blk)
	newTop := columnTop(d, pos.X, pos.Z)
	if s.bus != nil {
		// Snapshot the actor's position for reach checks; the
		// session read loop may move them before a plugin reacts.
		var px, py, pz float64
		if sess := s.findSessionByName(player); sess != nil {
			px, py, pz, _, _, _ = sess.Pos()
		}
		s.bus.Publish(event.TopicBlockChange, BlockChange{
			Player: player, X: pos.X, Y: pos.Y, Z: pos.Z, Block: blk,
			PX: px, PY: py, PZ: pz,
		})
	}
	s.broadcastBlockState(d, pos, blk)
	s.syncBlockEntities(d, pos, blk)
	s.checkSummonedEntity(d, pos, blk)
	s.invalidateRandomTickSection(d, pos)
	// Placing or breaking a block changes what the fluids around it can
	// do, so they are re-evaluated on their own next tick.
	s.wakeFluids(d, pos)
	return affectedSections(d, oldTop, newTop)
}

// syncBlockEntities keeps the per-dimension block-entity stores in step
// with a block write: a block that owns a block entity gets its state
// created and announced, and one that does not has any stale state
// dropped. Shared by /setblock, digging, and fluid flow.
func (s *Server) syncBlockEntities(d *dimension, pos world.Pos, blk world.Block) {
	if spec, ok := signSpecOf(blk); ok {
		s.ensureSign(d, pos)
		if st, ok := s.signAt(d, pos); ok {
			s.broadcastBlockEntityData(d, pos, spec.id, signNBT(st))
		}
	} else {
		s.clearSign(d, pos)
	}
	if isChestBlock(blk) {
		s.ensureChest(d, pos)
	} else {
		s.removeChest(d, pos)
	}
	if isFurnaceBlock(blk) {
		s.ensureFurnace(d, pos)
	} else {
		s.removeFurnace(d, pos)
	}
	if isHopperBlock(blk) {
		s.ensureHopper(d, pos)
	} else {
		s.removeHopper(d, pos)
	}
	s.syncContainerBlock(d, pos, blk)
	s.syncBeacon(d, pos, blk)
	if !isBeeNestBlock(blk) {
		delete(d.bees, pos)
	}
}

// isBeeNestBlock reports whether blk is a bee_nest or beehive — the
// two blocks that hold a Bees occupant list.
func isBeeNestBlock(blk world.Block) bool {
	name, ok := world.StateOwner(blk)
	return ok && (name == "bee_nest" || name == "beehive")
}

// syncContainerBlock keeps the generic container store in step with a
// block write. A block that is no longer a container, or a different
// container than the one already stored (a barrel swapped for a
// dispenser), drops what it held: the two menus have different slot
// counts, so carrying the old storage over would desync the window.
func (s *Server) syncContainerBlock(d *dimension, pos world.Pos, blk world.Block) {
	if _, ok := containerSpecFor(blk); !ok {
		s.removeContainer(d, pos)
		return
	}
	name, _ := world.StateOwner(blk)
	d.containers.mu.Lock()
	cur := d.containers.containers[pos]
	d.containers.mu.Unlock()
	if cur != nil && cur.block != name {
		s.removeContainer(d, pos)
	}
	s.ensureContainer(d, pos, blk)
}

// sendBlockAck retires one client prediction slot.
func (s *Server) sendBlockAck(sess *Session, seq int32) {
	var b protocol.Buffer
	b.VarInt(seq)
	_ = sess.write(sess.ids().PlayBlockChangedAck, b.Bytes())
}

// handleDig processes a Serverbound Player Action packet in creative
// instant-break mode: start (0) and finish (2) destroy the targeted
// block to air; cancel (1) and inventory statuses are acknowledged but
// change nothing. Only the leading status + position are interpreted;
// the face/sequence tail varies by version and is skipped best-effort,
// with the trailing VarInt echoed as the ack sequence when present.
func (s *Server) handleDig(sess *Session, p protocol.Packet) {
	// Mining in the first moments of a session is dropped with the rest
	// of a player's world interactions.
	if !sess.interactionsOpen(s.TickCount()) {
		return
	}
	r := protocol.NewPacketReader(p.Data)
	status, err := r.VarInt()
	if err != nil {
		return
	}
	x, y, z, err := r.Position()
	if err != nil {
		return
	}
	// Best-effort tail: face then sequence. Face values 0-5 decode
	// identically as Byte or VarInt, so one VarInt read skips either
	// framing; the last VarInt present is the prediction sequence.
	seq := int32(-1)
	if _, err := r.VarInt(); err == nil {
		if v, err := r.VarInt(); err == nil {
			seq = v
		}
	}
	acked := false
	defer func() {
		if seq >= 0 && !acked {
			s.sendBlockAck(sess, seq)
		}
	}()

	const border = 29999984
	if x < -border || x > border || z < -border || z > border {
		return
	}
	d := sess.dimension()
	if int(y) < d.minY || int(y) > d.minY+d.height-1 {
		return
	}
	if status != digStart && status != digFinish && status != digCancel {
		return // inventory statuses: ack only
	}
	pos := world.Pos{X: int(x), Y: int(y), Z: int(z)}
	switch sess.mode() {
	case GameModeCreative:
		// Creative destroys the block outright on start or finish; a
		// cancel is acknowledged and changes nothing.
		if status == digCancel {
			return
		}
		broken := d.w.GetBlock(pos)
		if broken == world.Air {
			return // already air: ack only, no event storm
		}
		// Vanilla's bee_nest_destroyed trigger lives in Block.playerDestroy,
		// which ServerPlayerGameMode.destroyBlock skips for players whose
		// mode prevents block drops (creative): a creative break never fires
		// the trigger — only the survival dig path does (breakBlock).
		affected := s.setBlockAndBroadcast(sess.getName(), pos, world.Air)
		acked = true
		if seq >= 0 {
			s.sendBlockAck(sess, seq)
		}
		s.sendLightUpdate(d, pos.X>>4, pos.Z>>4, affected)
		return
	case GameModeSurvival:
		// Survival digging is timed by the server's own tick loop, so
		// the action only records state here (see handleDigSurvival);
		// the block change, its drops, and the light update all come
		// from digTickStep on the tick that finishes the work.
		s.handleDigSurvival(sess, pos, status)
		acked = true
		if seq >= 0 {
			s.sendBlockAck(sess, seq)
		}
		return
	default:
		// Adventure and spectator players cannot destroy blocks; the
		// action is acknowledged and otherwise ignored.
		acked = true
		if seq >= 0 {
			s.sendBlockAck(sess, seq)
		}
		return
	}
}
