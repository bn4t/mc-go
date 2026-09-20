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

// itemBlock maps a held item to the block it places. Most block items
// resolve through ItemBlockState; items whose block carries a different
// name (seeds → crops, redstone → wire, ...) go through the alias table.
func itemBlock(id int32) (world.Block, bool) {
	if b, ok := world.ItemBlockState(id); ok {
		return b, true
	}
	if name, ok := itemNameOf(id); ok {
		if alias, ok := itemBlockAlias[name]; ok {
			return world.BlockState(alias)
		}
	}
	return 0, false
}

// itemBlockAlias is the vanilla item-name → block-name map for items
// that place a differently-named block (BlockItem States only cover
// the same-name set).
var itemBlockAlias = map[string]string{
	"wheat_seeds":       "wheat",
	"beetroot_seeds":    "beetroots",
	"melon_seeds":       "melon_stem",
	"pumpkin_seeds":     "pumpkin_stem",
	"torchflower_seeds": "torchflower_crop",
	"pitcher_pod":       "pitcher_crop",
	"nether_wart":       "nether_wart",
	"carrot":            "carrots",
	"potato":            "potatoes",
	"cocoa_beans":       "cocoa",
	"sweet_berries":     "sweet_berry_bush",
	"glow_berries":      "cave_vines",
	"redstone":          "redstone_wire",
	"string":            "tripwire",
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

// useMenuReach is how far the eyes may be from the clicked block's
// nearest point for a use to open its menu. Measured on vanilla 26.2
// with a chest shelf (opened through 5.93, silent from 6.86) and a
// vertical column (opened through 5.5): a 6-block limit fits every
// cell.
const useMenuReach = 6.0

// opensMenu reports whether using the block opens a menu instead of
// placing. It mirrors openBlockMenu's dispatch without side effects,
// so the use gates (reach, teleport confirmation) apply to exactly
// the blocks that open.
func opensMenu(blk world.Block) bool {
	return isChestBlock(blk) || isEnderChestBlock(blk) || isHopperBlock(blk) || isCraftingTableBlock(blk) || isFurnaceBlock(blk) || isContainerBlock(blk)
}

// useMenuReachOK reports whether the session's eyes are within reach
// of the clicked block's nearest point.
func useMenuReachOK(sess *Session, pos world.Pos) bool {
	px, py, pz, _, _, _ := sess.Pos()
	ex, ey, ez := px, py+1.62, pz
	nx := min(max(ex, float64(pos.X)), float64(pos.X)+1)
	ny := min(max(ey, float64(pos.Y)), float64(pos.Y)+1)
	nz := min(max(ez, float64(pos.Z)), float64(pos.Z)+1)
	dx, dy, dz := ex-nx, ey-ny, ez-nz
	return dx*dx+dy*dy+dz*dz <= useMenuReach*useMenuReach
}

// teleportPending reports whether the server sent the session a
// teleport it never confirmed. Vanilla 26.2 answers an in-reach use
// that arrives first with the build.tooHigh action bar instead of
// opening the menu; with no teleport issued yet there is nothing to
// confirm.
func teleportPending(sess *Session) bool {
	last := sess.teleportNext.Load() - 1
	return last > 0 && sess.teleportAck.Load() != last
}

// sendBuildTooHigh answers a use that arrived before the client
// confirmed its teleports: the action-bar "Height limit for building
// is 319 blocks". Measured byte for byte on vanilla 26.2; the limit
// is one below the dimension's top (minY+height-1).
func (s *Server) sendBuildTooHigh(sess *Session, d *dimension) {
	s.sendActionBar(sess, component{
		translate: "build.tooHigh",
		color:     "red",
		with:      []any{int32(d.minY + d.height - 1)},
	})
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
	// A use in the first moments of a session, before the client has
	// reported its terrain loaded, is dropped whole: no placement, no
	// editor, no acknowledgement.
	if !sess.interactionsOpen(s.TickCount()) {
		return
	}
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
	// Interacting with a sign dyes it, waxes it, makes its text glow,
	// or opens its editor - it never places.
	if isSignLikeBlock(cur) {
		s.useSign(sess, d, clicked, cur)
		acked = true
		if seq >= 0 {
			s.sendBlockAck(sess, seq)
		}
		return
	}
	// Container blocks open their menu instead of placing, but only
	// in reach and only once every server teleport is confirmed:
	// out-of-reach uses die silently, unconfirmed ones get the
	// build.tooHigh action bar. Measured on vanilla 26.2; the
	// denials still ack the sequence.
	if opensMenu(cur) {
		if !useMenuReachOK(sess, clicked) {
			acked = true
			if seq >= 0 {
				s.sendBlockAck(sess, seq)
			}
			return
		}
		if teleportPending(sess) {
			acked = true
			if seq >= 0 {
				s.sendBlockAck(sess, seq)
			}
			s.sendBuildTooHigh(sess, d)
			return
		}
	}
	if s.openBlockMenu(sess, cur, clicked) {
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
	sess.addItemStat(statUsed, item, 1)
	if bname, ok := world.StateOwner(blk); ok {
		s.firePlacedBlock(sess, bname)
	}
	// A shulker box placed out of a stack that came with contents (it
	// was broken with items inside) restores them, so the box keeps its
	// contents across a move.
	if isShulkerBoxBlock(blk) {
		if held := sess.heldStack(); held.Contents != nil {
			if c := s.containerAt(sess.dimension(), target); c != nil {
				sess.dimension().containers.mu.Lock()
				copy(c.slots, held.Contents[:])
				sess.dimension().containers.mu.Unlock()
			}
		}
	}
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
