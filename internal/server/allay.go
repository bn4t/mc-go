package server

import (
	"math/rand"
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
	// allayJukeboxRadius is GameEvent.JUKEBOX_PLAY's notificationRadius
	// — the JukeboxListener only hears plays within 10 blocks.
	allayJukeboxRadius = 10.0
)

// allayThrowPitches is Allay.THROW_SOUND_PITCHES — the pitch pool
// ALLAY_THROW samples from on each throw (Util.getRandom).
var allayThrowPitches = []float32{
	0.5, 0.5625, 0.625, 0.75, 0.9375, 1, 1, 1, 1.125, 1.25,
	1.5, 1.875, 2, 2.25, 2.5, 3, 3.75, 4,
}

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

// giveAllay implements Allay.mobInteract's item hand-off both ways:
// an empty allay hand takes one of the player's held stack into the
// MAINHAND equipment slot (sound ITEM_GIVEN, latches LIKED_PLAYER);
// an empty player hand on an allay holding an item takes it back
// (sound ITEM_TAKEN, allay swing, inventory items thrown out,
// LIKED_PLAYER erased, the hand item addItem'ed). Note the give path
// does NOT fill the allay's inventory — vanilla keeps the two
// separate, and only inventory items are thrown to a deposit target.
func (s *Server) giveAllay(sess *Session, eid int32, hand int32) {
	s.entities.mu.RLock()
	e, ok := s.entities.byID[eid]
	s.entities.mu.RUnlock()
	if !ok || !isAllay(e) {
		return
	}
	held, hslot := sess.stackForHand(hand)
	if e.HeldItem.Empty() {
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
		// playSound(player, allay, ITEM_GIVEN, NEUTRAL, 2.0, 1.0) —
		// the causer's client plays it locally, everyone else hears
		// the packet.
		s.playSoundEntity(e.Dim, e.ID, 4, 6, 2.0, 1.0, sess)
		s.broadcastEntityAnimation(sess, sess.entityID.Load(), 0)
		return
	}
	// Take-back: player empty hand, MAIN_HAND only, allay holding.
	if !held.Empty() || hand != 0 {
		return
	}
	s.entities.mu.Lock()
	handItem := e.HeldItem
	e.HeldItem = ItemStack{}
	thrown := e.InvItem
	e.InvItem = ItemStack{}
	e.HasLikedPlayer = false
	s.entities.mu.Unlock()
	s.allayEquip(e, ItemStack{})
	s.playSoundEntity(e.Dim, e.ID, 5, 6, 2.0, 1.0, sess)
	// The allay swings MAIN_HAND (vanilla's swing on take-back) —
	// broadcast to every tracker.
	s.broadcastEntityAnimation(nil, e.ID, 0)
	// removeAllItems throws each inventory stack at the allay's own
	// position (BehaviorUtils.throwItem).
	if !thrown.Empty() {
		it := &Entity{
			Type: mustEntityType("item"),
			Dim:  e.Dim,
			X:    e.X, Y: e.Y, Z: e.Z,
			Item:        thrown,
			PickupDelay: 40,
			Thrower:     e.ID,
		}
		s.entities.Spawn(it)
		s.broadcastEntitySpawn(nil, *it)
	}
	// player.addItem(handItem): merge-or-new into the inventory;
	// whatever still doesn't fit drops at the player's feet.
	if rem, _ := sess.addStack(handItem); !rem.Empty() {
		px, py, pz, _, _, _ := sess.Pos()
		it := &Entity{
			Type: mustEntityType("item"),
			Dim:  e.Dim,
			X:    px, Y: py, Z: pz,
			Item:        rem,
			PickupDelay: 40,
		}
		s.entities.Spawn(it)
		s.broadcastEntitySpawn(nil, *it)
	}
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
		s.allayDanceStep(e, now)
	}
}

// allayJukeboxEvent is Allay$JukeboxListener.handleGameEvent: a
// JUKEBOX_PLAY at pos starts the dance on every allay inside the
// event's 10-block notificationRadius (and remembers that pos); a
// JUKEBOX_STOP_PLAY clears the dance only on allays whose remembered
// pos matches the event's (or who remember none — that arm covers
// the load-from-save allay whose listener never saw a play).
// Jukebox/stop events aren't in the vibrations or warden_can_listen
// tags, so no sculk dispatch rides this — the allay listener is the
// only ear.
func (s *Server) allayJukeboxEvent(d *dimension, pos world.Pos, playing bool) {
	s.entities.mu.Lock()
	defer s.entities.mu.Unlock()
	cx, cy, cz := float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5
	for _, e := range s.entities.byID {
		if e.Dim != d.name || !isAllay(e) {
			continue
		}
		dx, dy, dz := e.X-cx, e.Y-cy, e.Z-cz
		if dx*dx+dy*dy+dz*dz > allayJukeboxRadius*allayJukeboxRadius {
			continue
		}
		if playing {
			// setJukeboxPlaying(pos,true): only an allay that is NOT
			// already dancing takes the new pos — the listener does
			// not re-aim a dancing allay. The pos is stored regardless
			// of the flag write, so a NoAI allay keeps re-aiming.
			if e.Dancing {
				continue
			}
			e.JukeboxPos, e.HasJukeboxPos = pos, true
			// setDancing gates on isEffectiveAi (NoAI blocks the flag
			// write) and isPanicking — a NoAI/panicking allay stores
			// the pos but never flips DATA_DANCING.
			if e.NoAI || e.PanicActive {
				continue
			}
			e.Dancing = true
		} else {
			if e.HasJukeboxPos && e.JukeboxPos != pos {
				continue
			}
			e.HasJukeboxPos = false
			if e.NoAI || e.PanicActive {
				continue
			}
			e.Dancing = false
		}
		e.MetaDirty = true
	}
}

// allayDanceStep is Allay.aiStep's server arm: heal 1.0 every 10
// ticks while alive, run the %20 shouldStopDancing check (no
// remembered pos, jukebox center beyond notificationRadius, or the
// block no longer a jukebox — the disc itself need not still play),
// and count down duplicationCooldown (DATA_CAN_DUPLICATE flips back
// true at 0).
func (s *Server) allayDanceStep(e *Entity, now int64) {
	if now%10 == 0 && e.Health > 0 {
		s.entities.Heal(e.ID, 1)
	}
	if e.Dancing && now%20 == 0 {
		stop := !e.HasJukeboxPos
		if !stop {
			cx, cy, cz := float64(e.JukeboxPos.X)+0.5, float64(e.JukeboxPos.Y)+0.5, float64(e.JukeboxPos.Z)+0.5
			dx, dy, dz := e.X-cx, e.Y-cy, e.Z-cz
			if dx*dx+dy*dy+dz*dz > allayJukeboxRadius*allayJukeboxRadius {
				stop = true
			}
		}
		if !stop {
			d := s.dims[e.Dim]
			if d == nil {
				stop = true
			} else if n, _ := world.StateOwner(d.w.GetBlock(e.JukeboxPos)); n != "jukebox" {
				stop = true
			}
		}
		if stop {
			s.entities.mu.Lock()
			e.Dancing, e.HasJukeboxPos = false, false
			e.MetaDirty = true
			s.entities.mu.Unlock()
		}
	}
	if e.DupCooldown > 0 {
		s.entities.mu.Lock()
		e.DupCooldown--
		if e.DupCooldown == 0 {
			e.MetaDirty = true
		}
		s.entities.mu.Unlock()
	}
}

// allayDuplicate is Allay.mobInteract's first arm: while the allay
// is dancing and DATA_CAN_DUPLICATE is set, a held #duplicates_allays
// item (amethyst_shard) click spawns a copy at its position —
// snapTo's position and rotation, persistenceRequired on both, the
// 6000-tick duplicationCooldown reset on both — then broadcast
// entity event 18 and the amethyst chime to everyone but the
// interactor, and one shard consumed (creative keeps its stack).
func (s *Server) allayDuplicate(sess *Session, eid int32, hand int32) bool {
	s.entities.mu.RLock()
	e, ok := s.entities.byID[eid]
	s.entities.mu.RUnlock()
	if !ok || !isAllay(e) || !e.Dancing || e.DupCooldown > 0 {
		return false
	}
	held, slot := sess.stackForHand(hand)
	if iname, _ := itemNameOf(held.ID); iname != "amethyst_shard" {
		return false
	}
	d := s.dims[e.Dim]
	if d == nil {
		return false
	}
	ne, nok := s.summonMobInit(d, "allay", e.X, e.Y, e.Z, e.NoAI, true)
	if nok {
		s.entities.mu.Lock()
		ne.Yaw, ne.Pitch, ne.HeadYaw = e.Yaw, e.Pitch, e.HeadYaw
		ne.PersistenceRequired = true
		ne.DupCooldown = 6000
		ne.MetaDirty = true
		e.PersistenceRequired = true
		e.DupCooldown = 6000
		e.MetaDirty = true
		s.entities.mu.Unlock()
	}
	s.broadcastEntityEvent(e.ID, 18)
	s.playSoundSrc(e.Dim, e.X, e.Y, e.Z,
		soundEventIDs["block.amethyst_block.chime"], srcNeutral, 2.0, 1.0, sess)
	if sess.mode() != GameModeCreative {
		sess.consumeSlot(slot, 1)
	}
	return true
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
	s.entities.mu.Unlock()
	s.entities.Remove(item.ID)
	s.broadcastEntityRemoveIn(e.Dim, nil, []int32{item.ID})
	// Picked items land in the inventory only — MAINHAND keeps the
	// liked-give stack, so no equipment emit (InventoryCarrier.
	// pickUpItem writes the container, never the hand).
}

// allayThrowStep evaluates one allay's deposit target and throws one
// inventory item toward it.
func (s *Server) allayThrowStep(e *Entity, now int64) {
	if e.InvItem.Empty() {
		return
	}
	// GoAndGiveItemsToTarget.tick removes one inventory item per
	// behavior tick — the throw rate is one item per game tick.
	if now == e.LastThrow {
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
	s.entities.mu.Unlock()

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

	// AllayAi.onItemThrown: gameTime%7==0 && random<0.9 plays
	// ALLAY_THROW at a pitch drawn from THROW_SOUND_PITCHES.
	if now%7 == 0 && rand.Float64() < 0.9 {
		s.playSoundEntity(e.Dim, e.ID, 6, 6, 1.0, allayThrowPitches[rand.Intn(len(allayThrowPitches))], nil)
	}

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
