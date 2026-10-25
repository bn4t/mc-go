package server

import (
	"sort"
	"strings"

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

// broadcastBlockState records pos as changed in this tick's per-section
// batch — vanilla's ChunkMap.blockChanged, which accumulates positions
// and lets ChunkHolder.broadcastChanges emit them at tick end. A
// section with one change sends block_update; several changes ride a
// section_blocks_update carrying each position's state at flush time.
// The blk argument is kept so call sites read as a state write.
func (s *Server) broadcastBlockState(d *dimension, pos world.Pos, blk world.Block) {
	if d == nil {
		return
	}
	if !s.ticking.Load() {
		// No game loop means no end-of-tick flush will ever run — write
		// the update right away (unit tests driving a server without
		// Serve rely on this).
		cx, cz := pos.X>>4, pos.Z>>4
		s.broadcastProjectedBlocks(d, cx, cz, false, func(profile *javaProfile) ([]byte, error) {
			return profile.encodeBlockUpdate(int32(pos.X), int32(pos.Y), int32(pos.Z), blk)
		})
		return
	}
	key := blockSectionKey{dim: d.name, x: pos.X >> 4, y: pos.Y >> 4, z: pos.Z >> 4}
	lp := uint16((pos.X&15)<<8 | (pos.Z&15)<<4 | (pos.Y & 15))
	s.dirtyMu.Lock()
	if s.dirtyBlocks[key] == nil {
		s.dirtyBlocks[key] = map[uint16]bool{}
	}
	s.dirtyBlocks[key][lp] = true
	s.dirtyMu.Unlock()
}

// blockSectionKey identifies one 16x16x16 chunk section in a dimension;
// y is the section coordinate (block y >> 4), not an index above minY.
type blockSectionKey struct {
	dim     string
	x, y, z int
}

// blockChangesFlushStep emits the tick's accumulated block changes —
// vanilla's ChunkHolder.broadcastChanges running per ticking chunk. One
// changed position in a section sends a plain block_update; more send a
// section_blocks_update: section pos long, VarInt count, then a VarLong
// per change packing (state id << 12) | 12-bit local position. The state
// is read at flush, so multiple writes to one block within a tick
// collapse to the final state — same as vanilla's ShortSet dedup.
func (s *Server) blockChangesFlushStep() {
	s.dirtyMu.Lock()
	dirty := s.dirtyBlocks
	s.dirtyBlocks = map[blockSectionKey]map[uint16]bool{}
	s.dirtyMu.Unlock()
	for key, set := range dirty {
		d := s.dims[key.dim]
		if d == nil {
			continue
		}
		locals := make([]uint16, 0, len(set))
		for lp := range set {
			locals = append(locals, lp)
		}
		sort.Slice(locals, func(i, j int) bool { return locals[i] < locals[j] })
		cx, cz := key.x, key.z
		if len(locals) == 1 {
			lp := locals[0]
			pos := world.Pos{X: cx*16 + int(lp>>8&15), Y: key.y*16 + int(lp&15), Z: cz*16 + int(lp>>4&15)}
			state := d.w.GetBlock(pos)
			s.broadcastProjectedBlocks(d, cx, cz, false, func(profile *javaProfile) ([]byte, error) {
				return profile.encodeBlockUpdate(int32(pos.X), int32(pos.Y), int32(pos.Z), state)
			})
			s.piggybackBlockEntity(d, pos)
			continue
		}
		states := make([]javaSectionBlock, 0, len(locals))
		for _, lp := range locals {
			pos := world.Pos{X: cx*16 + int(lp>>8&15), Y: key.y*16 + int(lp&15), Z: cz*16 + int(lp>>4&15)}
			states = append(states, javaSectionBlock{Local: lp, State: d.w.GetBlock(pos)})
		}
		s.broadcastProjectedBlocks(d, cx, cz, true, func(profile *javaProfile) ([]byte, error) {
			return profile.encodeSectionBlocks(key.x, key.y, key.z, states)
		})
		for _, lp := range locals {
			pos := world.Pos{X: cx*16 + int(lp>>8&15), Y: key.y*16 + int(lp&15), Z: cz*16 + int(lp>>4&15)}
			s.piggybackBlockEntity(d, pos)
		}
	}

	// Block entities dirtied without a block change — /data merge on a
	// sign — ride the same flush, vanilla's sendBlockUpdated marking
	// the chunk dirty for ChunkHolder.broadcastChanges. They land after
	// the command's chat reply, which already went out.
	s.dirtyMu.Lock()
	dirtyBEs, order := s.dirtyBEs, s.dirtyBEOrder
	s.dirtyBEs = map[dirtyBEKey]bool{}
	s.dirtyBEOrder = nil
	s.dirtyMu.Unlock()
	for _, key := range order {
		if !dirtyBEs[key] {
			continue
		}
		d := s.dims[key.dim]
		if d == nil {
			continue
		}
		s.piggybackBlockEntity(d, key.pos)
	}
}

// dirtyBEKey is one block entity waiting on the tick-end broadcast.
type dirtyBEKey struct {
	dim string
	pos world.Pos
}

// queueBlockEntityDirty schedules pos's block entity data for the
// tick-end flush. A server that is not ticking sends it right away —
// without a loop there is no later flush to catch it.
func (s *Server) queueBlockEntityDirty(d *dimension, pos world.Pos) {
	if !s.ticking.Load() {
		s.piggybackBlockEntity(d, pos)
		return
	}
	key := dirtyBEKey{dim: d.name, pos: pos}
	s.dirtyMu.Lock()
	if !s.dirtyBEs[key] {
		s.dirtyBEs[key] = true
		s.dirtyBEOrder = append(s.dirtyBEOrder, key)
	}
	s.dirtyMu.Unlock()
}

// piggybackBlockEntity emits block_entity_data for a changed position
// riding the flush — vanilla ChunkHolder.broadcastChanges sends it
// right after the block update for any changed state whose block
// entity produces an update packet. On the dedicated server that is
// only signs: chests and furnaces produce no placement packet.
func (s *Server) piggybackBlockEntity(d *dimension, pos world.Pos) {
	state := d.w.GetBlock(pos)
	s.broadcastLegacyBedData(d, pos, state)
	if spec, ok := signSpecOf(state); ok {
		if st, ok := s.signAt(d, pos); ok {
			s.broadcastBlockEntityData(d, pos, spec.id, signNBT(st))
		}
	}
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
// world update, BlockChange event, and Block Update fan-out. Light
// changes accumulate in the dimension's dirty maps so callers emit
// the Light Update after their acknowledgement, matching vanilla's
// order. The dimension is the acting player's; nameless actors land
// in the overworld.
func (s *Server) setBlockAndBroadcast(player string, pos world.Pos, blk world.Block) {
	s.setBlockAndBroadcastNotify(player, pos, blk, false)
}

// setBlockAndBroadcastNotify is setBlockAndBroadcast with the
// neighbourChanged flavour picked: gameplay writes (dig, place) hand
// neighbours the OLD state; command writes (/setblock — vanilla's
// updateNeighboursOnBlockSet) hand them the NEW one.
func (s *Server) setBlockAndBroadcastNotify(player string, pos world.Pos, blk world.Block, commandWrite bool) {
	s.setBlockAndBroadcastInOwned(s.dimOf(player), player, pos, blk, commandWrite)
}

// setBlockAndBroadcastInOwned is the existing write pipeline with an explicit
// dimension. Typed privileged edits never infer a dimension from a player name.
func (s *Server) setBlockAndBroadcastInOwned(d *dimension, player string, pos world.Pos, blk world.Block, commandWrite bool) {
	oldTop := columnTop(d, pos.X, pos.Z)
	old := d.w.GetBlock(pos)
	notify := blk
	if !commandWrite {
		notify = old
	}
	d.w.SetBlock(pos, blk)
	// While the write runs, updateNeighborsAt fan-outs only enqueue
	// (rsNeighborFanOut); the outermost write drains them once its own
	// neighbour pass and shape fold have landed.
	d.rsWriteDepth++
	s.writeOnPlace(d, pos, blk, old, commandWrite)
	// onPlace may have rewritten the cell (TNT primed on a signal
	// self-removes) — everything downstream reads the final state.
	blk = d.w.GetBlock(pos)
	// BlockInput.place runs updateFromNeighbourShapes first: the
	// written block folds itself against all six neighbours.
	if commandWrite {
		s.selfFoldShapes(d, pos)
		blk = d.w.GetBlock(pos)
	}
	if commandWrite && blk == old {
		// LevelChunk.setBlockState early-outs on an identical state:
		// the fold above already ran, but no block update, neighbour
		// fan-out, or broadcast ever fires. (A stone→stone setblock
		// is NOT an update — measured.)
		d.rsWriteDepth--
		return
	}
	// See setSilentBlock for the skip rule: the second scan only runs
	// when the write removed the top cell itself.
	var newTop int32
	switch {
	case int32(pos.Y) < oldTop || (blocksSky(blk) && int32(pos.Y) == oldTop):
		newTop = oldTop
	case int32(pos.Y) > oldTop:
		if blocksSky(blk) {
			newTop = int32(pos.Y)
		} else {
			newTop = oldTop
		}
	default:
		newTop = columnTop(d, pos.X, pos.Z)
	}
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
	s.invalidateRandomTickSection(d, pos)
	// Placing or breaking a block changes what the fluids around it can
	// do, so they are re-evaluated on their own next tick.
	s.wakeFluids(d, pos)
	// updateNeighborsAt: every face neighbour re-evaluates its
	// redstone signal (lamp, rails, torches, doors, TNT, ...).
	s.rsNeighborFanOut(d, pos)
	// updateNeighbourForOutputSignal: a write to an analog block
	// (flag&1, after the ring) wakes comparators reading it — one
	// cell adjacent, or two through a conductor.
	if rsHasAnalog(blk) {
		s.rsOutputSignalWake(d, pos)
	}
	// updateShape: a change re-checks support for pos's neighbors; the
	// placed block self-checks only in the scheduleTick family (the
	// BlockInput.place fold's surviving side-effect).
	s.neighborSurvival(d, pos, notify)
	s.selfCheckSurvival(d, pos, blk)
	if d.rsWriteDepth == 1 {
		// Drain the collected neighbour updates FIFO — chained
		// updates append mid-drain, matching the updater's layers.
		s.rsDrainPending(d)
	}
	d.rsWriteDepth--
	// The queued updates may have rewritten the cell again — the
	// light side reads the final state like everything downstream.
	blk = d.w.GetBlock(pos)
	s.blockLightChanged(d, pos, old, blk)
	s.skyLightChanged(d, pos, old, blk, oldTop, newTop)
}

// syncBlockEntities keeps the per-dimension block-entity stores in step
// with a block write: a block that owns a block entity gets its state
// created and announced, and one that does not has any stale state
// dropped. Shared by /setblock, digging, and fluid flow.
func (s *Server) syncBlockEntities(d *dimension, pos world.Pos, blk world.Block) {
	if spec, ok := signSpecOf(blk); ok {
		s.ensureSign(d, pos)
		if !s.ticking.Load() {
			// No end-of-tick flush will run — emit the piggybacked
			// block-entity update immediately, after the block update.
			if st, ok := s.signAt(d, pos); ok {
				s.broadcastBlockEntityData(d, pos, spec.id, signNBT(st))
			}
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
	// BaseEntityBlock preRemoveSideEffects: the old cell's block entity
	// state dies on every write. CustomName clears here (after
	// syncContainerBlock's removeContainer, which may still need it to
	// copy a broken shulker's name onto the dropped box item).
	delete(d.beNames, pos)
	if isBeeNestBlock(blk) {
		// BeehiveBlockEntity.setChanged: every mark-dirty re-checks
		// isFireNearby — a nest written while a FireBlock sits in
		// its 3x3x3 evacuates every occupant EMERGENCY. The player
		// is null on this path: release only, nobody angers.
		if d.bees[pos] > 0 && beeFireNearby(d, pos) {
			s.releaseBeeOccupants(d, pos, blk, d.bees[pos], beeReleaseEmergency)
			delete(d.bees, pos)
		}
	} else {
		// BeehiveBlockEntity has no preRemoveSideEffects: a nest
		// removed by any path that never calls
		// emptyAllLivingFromHive (explosion, setblock, creative
		// dig) loses its occupants silently — they never spawn.
		delete(d.bees, pos)
	}
	if isFireBlock(blk) {
		// The neighbour-write half of the same rule: a FireBlock
		// appearing in a nest's 3x3x3 marks that block entity
		// changed → the same EMERGENCY evacuation.
		for dx := -1; dx <= 1; dx++ {
			for dy := -1; dy <= 1; dy++ {
				for dz := -1; dz <= 1; dz++ {
					npos := world.Pos{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz}
					nblk := d.w.GetBlock(npos)
					if isBeeNestBlock(nblk) && d.bees[npos] > 0 {
						s.releaseBeeOccupants(d, npos, nblk, d.bees[npos], beeReleaseEmergency)
						delete(d.bees, npos)
					}
				}
			}
		}
	}
	// ComparatorBlockEntity: a comparator's stored output lives and
	// dies with the block.
	if rsIsComparator(blk) {
		if d.comparatorOut == nil {
			d.comparatorOut = map[world.Pos]int{}
		}
		if _, ok := d.comparatorOut[pos]; !ok {
			d.comparatorOut[pos] = 0
		}
	} else {
		delete(d.comparatorOut, pos)
	}
}

// isBeeNestBlock reports whether blk is a bee_nest or beehive — the
// two blocks that hold a Bees occupant list.
func isBeeNestBlock(blk world.Block) bool {
	name, ok := world.StateOwner(blk)
	return ok && (name == "bee_nest" || name == "beehive")
}

// isFireBlock mirrors instanceof FireBlock: plain fire only —
// SoulFireBlock extends BaseFireBlock, not FireBlock, so soul fire
// does not evacuate nests.
func isFireBlock(blk world.Block) bool {
	name, ok := world.StateOwner(blk)
	return ok && name == "fire"
}

// beeFireNearby is BeehiveBlockEntity.isFireNearby: any FireBlock in
// the nest's pos±1 (3x3x3) neighbourhood.
func beeFireNearby(d *dimension, pos world.Pos) bool {
	for dx := -1; dx <= 1; dx++ {
		for dy := -1; dy <= 1; dy++ {
			for dz := -1; dz <= 1; dz++ {
				if isFireBlock(d.w.GetBlock(world.Pos{X: pos.X + dx, Y: pos.Y + dy, Z: pos.Z + dz})) {
					return true
				}
			}
		}
	}
	return false
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
		// The occupants vanish with the block entity: nothing calls
		// emptyAllLivingFromHive on this path.
		s.setBlockAndBroadcast(sess.getName(), pos, world.Air)
		// ServerLevel.destroyBlock posts BLOCK_DESTROY at the broken
		// position with the old state as context — wool muffles it.
		oldName, _ := world.StateOwner(broken)
		s.emitGameEvent(d, "block_destroy",
			float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5,
			sess, strings.TrimPrefix(oldName, "minecraft:"))
		acked = true
		if seq >= 0 {
			s.sendBlockAck(sess, seq)
		}
		s.emitLightUpdate(d, pos)
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

// broadcastProjectedBlocks encodes once per immutable profile, rather than
// sharing a numeric state payload across versions. Missing exact mappings are
// terminal for that viewer; silently emitting a different state's ID would
// corrupt its world view. Authoritative blocks are never replaced by fallback.
func (s *Server) broadcastProjectedBlocks(d *dimension, cx, cz int, section bool, encode func(*javaProfile) ([]byte, error)) {
	type result struct {
		body []byte
		err  error
	}
	payloads := map[*javaProfile]result{}
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if sess.state.Load() != StatePlay || sess.dimName() != d.name || !sess.hasChunk(cx, cz) {
			return true
		}
		profile, err := sess.javaProfile()
		if err != nil {
			sess.closeQueue()
			return true
		}
		packet, ok := payloads[profile]
		if !ok {
			packet.body, packet.err = encode(profile)
			payloads[profile] = packet
		}
		if packet.err != nil {
			sess.closeQueue()
			return true
		}
		id := sess.ids().PlayBlockUpdateCB
		if section {
			id = sess.ids().PlaySectionBlocksUpdate
		}
		_ = sess.write(id, packet.body)
		return true
	})
}
