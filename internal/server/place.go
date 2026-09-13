package server

import (
	"math"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Creative block placement (Use Item On) plus the held-item tracking it
// resolves against. Item ids come from the official item registry
// (world.ItemIDs); every block-item places its default block state.

// Item ids named for tests and builtin tooling; values are the exact
// protocol ids from the official report.
var (
	ItemStone = world.ItemIDs["stone"]
	ItemDirt  = world.ItemIDs["dirt"]
	ItemGrass = world.ItemIDs["grass_block"]
)

// itemBlock maps a held item to the block it places.
func itemBlock(id int32) (world.Block, bool) {
	return world.ItemBlockState(id)
}

// faceOffset resolves a clicked face to the adjacent cell, following
// the public protocol docs: 0=-Y 1=+Y 2=-Z 3=+Z 4=-X 5=+X.
func faceOffset(face int32) (dx, dy, dz int32, ok bool) {
	switch face {
	case 0:
		return 0, -1, 0, true
	case 1:
		return 0, 1, 0, true
	case 2:
		return 0, 0, -1, true
	case 3:
		return 0, 0, 1, true
	case 4:
		return -1, 0, 0, true
	case 5:
		return 1, 0, 0, true
	}
	return 0, 0, 0, false
}

// handleHeld tracks the selected hotbar slot (0-8); anything else is
// ignored without touching state.
func (s *Server) handleHeld(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	slot, err := r.Short()
	if err != nil || slot < 0 || slot > 8 {
		return
	}
	sess.held.Store(int32(slot))
	s.broadcastEquipment(sess)
}

// handleCreativeSlot tracks one creative-inventory slot: Short slot,
// then the 1.20.5+ Slot shape (VarInt count, VarInt item id when
// count>0). Component data after the item id varies by version and is
// never parsed: packet framing is length-prefixed, so the tail is
// safely ignored once the id is read.
func (s *Server) handleCreativeSlot(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	slot, err := r.Short()
	if err != nil {
		return
	}
	count, err := r.VarInt()
	if err != nil {
		return
	}
	if count <= 0 {
		sess.setSlot(slot, 0, 0)
		return
	}
	item, err := r.VarInt()
	if err != nil {
		return
	}
	sess.setSlot(slot, item, count)
}

// placeReachM mirrors the dig-side reach: eye to target-center.
const placeReachM = 6.5

// handleUseItem places the held block against the clicked face in
// creative mode: hand, BlockPos, face, 3 cursor floats, inside-block,
// then a version-varying tail whose LAST varint is the prediction
// sequence (a Bool border-hit flag, when present, harmlessly decodes
// as a one-byte varint first). Denials still ack the sequence so the
// client retires its prediction instead of desyncing.
func (s *Server) handleUseItem(sess *Session, p protocol.Packet) {
	r := protocol.NewPacketReader(p.Data)
	if _, err := r.VarInt(); err != nil { // hand
		return
	}
	x, y, z, err := r.Position()
	if err != nil {
		return
	}
	face, err := r.VarInt()
	if err != nil {
		return
	}
	for i := 0; i < 3; i++ {
		if _, err := r.Float(); err != nil { // cursor
			return
		}
	}
	if _, err := r.Bool(); err != nil { // inside block
		return
	}
	seq := int32(-1)
	for r.Remaining() > 0 {
		v, err := r.VarInt()
		if err != nil {
			break
		}
		seq = v
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
	dx, dy, dz, ok := faceOffset(face)
	if !ok {
		return
	}
	clicked := world.Pos{X: int(x), Y: int(y), Z: int(z)}
	cur := d.w.GetBlock(clicked)
	if cur == world.Air {
		return // no block targeted: ack only
	}
	// Interacting with a sign opens its editor instead of placing.
	if isSignBlock(cur) {
		s.sendOpenSignEditor(sess, clicked, true)
		acked = true
		if seq >= 0 {
			s.sendBlockAck(sess, seq)
		}
		return
	}
	// Chests open their container window.
	if isChestBlock(cur) {
		s.openChest(sess, clicked)
		acked = true
		if seq >= 0 {
			s.sendBlockAck(sess, seq)
		}
		return
	}
	// Hoppers open their five-slot menu.
	if isHopperBlock(cur) {
		s.openHopper(sess, clicked)
		acked = true
		if seq >= 0 {
			s.sendBlockAck(sess, seq)
		}
		return
	}
	// Crafting tables open the 3x3 crafting menu.
	if isCraftingTableBlock(cur) {
		s.openCraftingTable(sess, clicked)
		acked = true
		if seq >= 0 {
			s.sendBlockAck(sess, seq)
		}
		return
	}
	// Furnaces open their smelting window.
	if isFurnaceBlock(cur) {
		s.openFurnace(sess, clicked)
		acked = true
		if seq >= 0 {
			s.sendBlockAck(sess, seq)
		}
		return
	}
	target := world.Pos{X: int(x + dx), Y: int(y + dy), Z: int(z + dz)}
	if target.Y < d.minY || target.Y > d.minY+d.height-1 {
		return
	}
	if cur := d.w.GetBlock(target); cur != world.Air {
		return // occupied: creative never overwrites solid
	}
	// Reach, measured like the anticheat break check: eye to
	// target-center, so the detector and the server agree.
	px, py, pz, _, _, _ := sess.Pos()
	ddx := px - (float64(target.X) + 0.5)
	ddy := (py + 1.62) - (float64(target.Y) + 0.5)
	ddz := pz - (float64(target.Z) + 0.5)
	if math.Sqrt(ddx*ddx+ddy*ddy+ddz*ddz) > placeReachM {
		return
	}
	item, ok := sess.heldItem()
	if !ok {
		return // empty hand: ack only
	}
	blk, ok := itemBlock(item)
	if !ok {
		return // not a placeable block-item: ack only
	}
	affected := s.setBlockAndBroadcast(sess.getName(), target, blk)
	acked = true
	if seq >= 0 {
		s.sendBlockAck(sess, seq)
	}
	s.sendLightUpdate(d, target.X>>4, target.Z>>4, affected)
	// Survival consumes one item and syncs the slot; creative keeps
	// the stack, matching vanilla.
	if sess.mode() != GameModeCreative {
		sess.consumeHeld(1)
	}
}

// consumeHeld removes n from the held stack and syncs the slot.
func (sess *Session) consumeHeld(n int32) {
	slot := invSlotForHotbar(sess.held.Load())
	sess.invMu.Lock()
	it := sess.inv[slot]
	if it.Empty() {
		sess.invMu.Unlock()
		return
	}
	it.Count -= n
	if it.Count <= 0 {
		it = ItemStack{}
	}
	sess.inv[slot] = it
	sess.invMu.Unlock()
	_ = sess.server.sendContainerSlot(sess, slot, it)
}
