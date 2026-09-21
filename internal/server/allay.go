package server

import (
	"strconv"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

// Allay constants measured from vanilla 26.2 (Allay/AllayAi/
// GoAndGiveItemsToTarget): noteblocks are heard within 30 blocks and
// remembered for 600 ticks; the deposit target is the noteblock's
// above() while the memory is fresh and the block still stands (within
// 1024 blocks), else the liked player's position; the allay throws one
// inventory item per tick once its eye position is within 3.0 of the
// target. Item pickup reach is a 1-block box (ITEM_PICKUP_REACH).
const (
	allayNoteblockRadius = 30.0
	allayNoteblockForget = 600
	allayNoteblockRange  = 1024.0
	allayThrowDist       = 3.0
	allayPickupReach     = 1.5
)

func isNoteBlock(b world.Block) bool {
	name, ok := world.StateOwner(b)
	return ok && name == "note_block"
}

func isAllay(e *Entity) bool {
	name, ok := EntityTypeName(e.Type)
	return ok && name == "allay"
}

// noteBlockUse implements NoteBlock.useWithoutItem: cycle the note
// property, play it, count tune_noteblock, and post the
// NOTE_BLOCK_PLAY game event every allay within 30 blocks hears.
func (s *Server) noteBlockUse(sess *Session, d *dimension, pos world.Pos) {
	blk := d.w.GetBlock(pos)
	if !isNoteBlock(blk) {
		return
	}
	if v, ok := world.StateProperty(blk, "note"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			if nb, ok := world.WithProperty(blk, "note", strconv.Itoa((n+1)%25)); ok {
				s.setBlockAndBroadcast(sess.getName(), pos, nb)
			}
		}
	}
	sess.addCustomStat("tune_noteblock", 1)
	s.noteBlockPlay(d, pos)
}

// noteBlockPlay marks every allay within 30 blocks of a played
// noteblock as having heard it — the LIKED_NOTEBLOCK_POSITION memory.
func (s *Server) noteBlockPlay(d *dimension, pos world.Pos) {
	s.entities.mu.Lock()
	defer s.entities.mu.Unlock()
	for _, e := range s.entities.byID {
		if e.Dim != d.name || !isAllay(e) {
			continue
		}
		dx, dy, dz := e.X-float64(pos.X)-0.5, e.Y-float64(pos.Y)-0.5, e.Z-float64(pos.Z)-0.5
		if dx*dx+dy*dy+dz*dz > allayNoteblockRadius*allayNoteblockRadius {
			continue
		}
		e.HeardNote = pos
		e.HeardNoteAt = s.TickCount()
		e.HasHeardNote = true
	}
}

// allayEquip broadcasts the allay's MAIN_HAND slot.
func (s *Server) allayEquip(e *Entity, it ItemStack) {
	var b protocol.Buffer
	b.VarInt(e.ID)
	b.Byte(equipMainHand)
	writeSlot(&b, it)
	body := b.Bytes()
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		if other.state.Load() != StatePlay || !other.spawned.Load() || other.dimName() != e.Dim {
			return true
		}
		_ = other.write(other.ids().PlaySetEquipment, body)
		return true
	})
}

// giveAllay implements Allay.mobInteract's item hand-off: an empty
// allay hand takes one of the player's held stack into the MAINHAND
// equipment slot and latches the player as LIKED_PLAYER. Note this
// does NOT fill the allay's inventory — vanilla keeps the two
// separate, and only inventory items are thrown to a deposit target.
func (s *Server) giveAllay(sess *Session, eid int32, hand int32) {
	s.entities.mu.RLock()
	e, ok := s.entities.byID[eid]
	s.entities.mu.RUnlock()
	if !ok || !isAllay(e) || !e.HeldItem.Empty() {
		return
	}
	held, hslot := sess.stackForHand(hand)
	if held.Empty() {
		return
	}
	one := held
	one.Count = 1
	s.entities.mu.Lock()
	e.HeldItem = one
	e.LikedPlayer = sess.uuid
	e.HasLikedPlayer = true
	s.entities.mu.Unlock()
	if sess.mode() == GameModeSurvival || sess.mode() == GameModeAdventure {
		sess.consumeSlot(hslot, 1)
	}
	s.allayEquip(e, one)
	s.broadcastEntityAnimation(sess, sess.entityID.Load(), 0)
}

// allayTickStep runs the allay's observable AI: pick up a nearby item
// entity matching the held stack (GoToWantedItem's ground truth once
// reached), then throw one inventory item per tick at the deposit
// target once inside GoAndGiveItemsToTarget's 3-block gate.
func (s *Server) allayTickStep() {
	now := s.TickCount()
	if now%100 == 0 {
	}
	s.entities.mu.Lock()
	var allays []*Entity
	for _, e := range s.entities.byID {
		if !isAllay(e) {
			continue
		}
		allays = append(allays, e)
	}
	s.entities.mu.Unlock()
	for _, e := range allays {
		if now%40 == 0 {
		}
		s.allayPickUpStep(e)
		s.allayThrowStep(e, now)
	}
}

// allayPickUpStep picks up the first matching item entity in the
// 1-block reach: wantsToPickUp requires the hand to be empty or to
// hold an equal stack that the single inventory slot can take.
func (s *Server) allayPickUpStep(e *Entity) {
	if !e.HeldItem.Empty() && (e.HeldItem.ID != 0 && !e.InvItem.Empty() && e.InvItem.ID != e.HeldItem.ID) {
		return
	}
	s.entities.mu.RLock()
	var item *Entity
	for _, it := range s.entities.byID {
		if it.Dim != e.Dim || it.Item.Empty() || it.FakeItem {
			continue
		}
		if it.PickupDelay > 0 {
			continue
		}
		name, ok := EntityTypeName(it.Type)
		if !ok || name != "item" {
			continue
		}
		dx, dy, dz := it.X-e.X, it.Y-e.Y, it.Z-e.Z
		if dx*dx+dy*dy+dz*dz > allayPickupReach*allayPickupReach {
			continue
		}
		if !e.HeldItem.Empty() && it.Item.ID != e.HeldItem.ID {
			continue
		}
		if !e.InvItem.Empty() && it.Item.ID != e.InvItem.ID {
			continue
		}
		item = it
		break
	}
	s.entities.mu.RUnlock()
	if item == nil {
		return
	}
	s.entities.mu.Lock()
	if e.InvItem.Empty() {
		e.InvItem = item.Item
	} else {
		e.InvItem.Count += item.Item.Count
	}
	e.HeldItem = e.InvItem
	s.entities.mu.Unlock()
	s.entities.Remove(item.ID)
	s.broadcastEntityRemoveIn(e.Dim, nil, []int32{item.ID})
	s.allayEquip(e, e.HeldItem)
}

// allayThrowStep evaluates one allay's deposit target and throws one
// inventory item toward it.
func (s *Server) allayThrowStep(e *Entity, now int64) {
	if e.InvItem.Empty() {
		return
	}
	if now-e.LastThrow < 20 {
		return
	}
	d := s.dims[e.Dim]
	if d == nil {
		return
	}
	var tx, ty, tz float64
	var targetBlock world.Pos
	if e.HasHeardNote && now-e.HeardNoteAt < allayNoteblockForget && isNoteBlock(d.w.GetBlock(e.HeardNote)) {
		dx, dy, dz := e.X-float64(e.HeardNote.X)-0.5, e.Y-float64(e.HeardNote.Y)-0.5, e.Z-float64(e.HeardNote.Z)-0.5
		if dx*dx+dy*dy+dz*dz <= allayNoteblockRange*allayNoteblockRange {
			targetBlock = world.Pos{X: e.HeardNote.X, Y: e.HeardNote.Y + 1, Z: e.HeardNote.Z}
			tx, ty, tz = float64(targetBlock.X)+0.5, float64(targetBlock.Y)+0.5, float64(targetBlock.Z)+0.5
		} else {
			e.HasHeardNote = false
		}
	}
	if targetBlock == (world.Pos{}) && e.HasLikedPlayer {
		if sess := s.sessionByUUID(e.LikedPlayer); sess != nil && sess.dimName() == e.Dim {
			px, py, pz, _, _, _ := sess.Pos()
			tx, ty, tz = px, py, pz
			targetBlock = world.Pos{X: int(tx), Y: int(ty), Z: int(tz)}
		}
	}
	if targetBlock == (world.Pos{}) {
		return
	}
	// GoAndGiveItemsToTarget: dist(target, eyePos) < 3 throws.
	ex, ey, ez := e.X, e.Y+0.6, e.Z
	dx, dy, dz := tx-ex, ty-ey, tz-ez
	if dx*dx+dy*dy+dz*dz >= allayThrowDist*allayThrowDist {
		return
	}
	s.entities.mu.Lock()
	e.LastThrow = now
	thrown := e.InvItem
	thrown.Count = 1
	e.InvItem.Count--
	if e.InvItem.Count <= 0 {
		e.InvItem = ItemStack{}
	}
	e.HeldItem = e.InvItem
	s.entities.mu.Unlock()

	s.allayEquip(e, e.HeldItem)

	// Vanilla tosses the item entity from the allay's position with a
	// (0.2,0.3,0.2) drift — it never teleports to the target.
	it := &Entity{
		Type: mustEntityType("item"),
		Dim:  e.Dim,
		X:    e.X, Y: e.Y + 0.6, Z: e.Z,
		Item:        thrown,
		PickupDelay: 60, // ITEM_PICKUP_COOLDOWN on a thrown item
	}
	s.entities.Spawn(it)
	s.broadcastEntitySpawn(nil, *it)
	s.broadcastEntityMetadata(it.ID, encodeItemMetadata(thrown))

	// The trigger's block pos is the target's block position's below:
	// for a noteblock deposit that resolves to the noteblock itself.
	below := world.Pos{X: targetBlock.X, Y: targetBlock.Y - 1, Z: targetBlock.Z}
	if e.HasLikedPlayer {
		if sess := s.sessionByUUID(e.LikedPlayer); sess != nil {
			blkName, _ := world.StateOwner(d.w.GetBlock(below))
			itemName, _ := itemNameOf(thrown.ID)
			s.fireAllayDropOnBlock(sess, blkName, itemName)
		}
	}
}
