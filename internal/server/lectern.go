package server

import (
	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

// Lectern behavior, measured on vanilla 26.2's LecternBlock and
// LecternBlockEntity. The stored book rides the generic per-position
// container store (spec "minecraft:lectern", one slot): slot 0 is the
// BE's Book, containerData.page the BE's Page. has_book/powered are
// block properties — written with the flag-3 setBlock the real block
// uses, i.e. rsWriteBlock with an updateNeighborsAt on the block
// below it.

// lecternSlot is the lectern menu's only slot — the book.
const lecternSlot = 0

// sndBookPut is item.book.put's sound-event registry id (26.2).
const sndBookPut = 193

// lecternBookTag reports whether the item is in #minecraft:lectern_books
// (writable_book and written_book) — the tag LecternBlock.useItemOn
// checks.
func lecternBookTag(name string) bool {
	return name == "writable_book" || name == "written_book"
}

func lecternHasBook(blk world.Block) bool {
	v, _ := world.StateProperty(blk, "has_book")
	return v == "true"
}

// lecternPageCount is LecternBlockEntity.getPageCount: the written
// content's page list wins over the writable one's, else 0.
func lecternPageCount(it ItemStack) int32 {
	v := bookContentValue(it, true)
	if v == nil {
		v = bookContentValue(it, false)
	}
	if v == nil {
		return 0
	}
	l, _ := v["pages"].(nbt.List)
	return int32(len(l.Items))
}

// lecternUse runs LecternBlock.useItemOn: a #lectern_books item used
// on a bookless lectern stores it (tryPlaceBook); any other interact
// on a bookless lectern is vanilla's useWithoutItem CONSUME — nothing
// opens and the held item does not place. A lectern already holding a
// book falls through to the menu open.
func (s *Server) lecternUse(d *dimension, sess *Session, pos world.Pos, cur world.Block, hand int32) bool {
	name, _ := world.StateOwner(cur)
	if name != "lectern" {
		return false
	}
	if lecternHasBook(cur) {
		return false
	}
	held, handSlot := sess.stackForHand(hand)
	if iname, ok := itemNameOf(held.ID); ok && lecternBookTag(iname) {
		s.lecternPlaceBook(d, sess, pos, cur, held, handSlot)
	}
	return true
}

// lecternPlaceBook is placeBook + resetBookState: the hand stack loses
// one (consumeAndReturn — creative keeps it), the book lands in the
// lectern's slot with Page reset to 0, the block writes
// powered=false,has_book=true and updateNeighborsAt the block below,
// and item.book.put plays to everyone but the placer.
func (s *Server) lecternPlaceBook(d *dimension, sess *Session, pos world.Pos, cur world.Block, held ItemStack, handSlot int16) {
	book := bookResolve(held.withCount(1))
	if sess.mode() != GameModeCreative {
		sess.consumeSlot(handSlot, 1)
	}
	c := s.ensureContainer(d, pos, cur)
	if c == nil {
		return
	}
	d.containers.mu.Lock()
	c.slots[lecternSlot] = book
	c.page = 0
	d.containers.mu.Unlock()
	nb, ok := world.WithProperty(cur, "has_book", "true")
	if !ok {
		return
	}
	nb, _ = world.WithProperty(nb, "powered", "false")
	s.lecternWrite(d, pos, nb)
	// playSound(null, pos, SoundEvents.BOOK_PUT, BLOCKS, 1.0, 1.0).
	s.playSoundSrc(d.name, float64(pos.X)+0.5, float64(pos.Y)+0.5, float64(pos.Z)+0.5,
		sndBookPut, 6, 1.0, 1.0, nil)
}

// lecternWrite is LecternBlock's flag-3 setBlock plus its
// updateNeighborsAt(pos.below()) — the write the place, take, and
// page-signal paths all share.
func (s *Server) lecternWrite(d *dimension, pos world.Pos, nb world.Block) {
	below := world.Pos{X: pos.X, Y: pos.Y - 1, Z: pos.Z}
	s.rsWriteBlock(d, pos, nb, nb, func() {
		s.rsNeighborFanOutNow(d, below, -1)
	})
}

// lecternSetPage is LecternMenu.setPage: the target page clamps into
// [0, pageCount-1]; a real change broadcasts the container property to
// every viewer and fires signalPageChange — the 2-tick powered pulse
// plus the level event the client turns into the page-turn sound.
func (s *Server) lecternSetPage(w *openWindow, pos world.Pos, want int32) {
	c := w.container
	d := w.dim
	d.containers.mu.Lock()
	pages := lecternPageCount(c.slots[lecternSlot])
	d.containers.mu.Unlock()
	np := want
	if np < 0 {
		np = 0
	}
	if np > pages-1 {
		np = pages - 1
	}
	d.containers.mu.Lock()
	if np == c.page {
		d.containers.mu.Unlock()
		return
	}
	c.page = np
	d.containers.mu.Unlock()
	// broadcastChanges: the page property reaches every session whose
	// open menu is this lectern.
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		other.windowMu.Lock()
		ow := other.window
		other.windowMu.Unlock()
		if ow != nil && ow.container == c {
			s.sendContainerProperty(other, ow.id, 0, np)
		}
		return true
	})
	// signalPageChange: changePowered(true), a 2-tick reset, then the
	// level event (1043 = lectern page turn).
	cur := d.w.GetBlock(pos)
	nb, ok := world.WithProperty(cur, "powered", "true")
	if ok {
		s.lecternWrite(d, pos, nb)
	}
	s.scheduleBlockTick(d, pos, 2)
	s.broadcastLevelEvent(d.name, 1043, pos, 0)
}

// lecternTick is LecternBlock.tick — the scheduled pulse end. Its
// changePowered(false) is unguarded in vanilla: the flag-3 setBlock
// runs (and broadcasts) even when powered is already false.
func (s *Server) lecternTick(d *dimension, pos world.Pos) {
	cur := d.w.GetBlock(pos)
	name, _ := world.StateOwner(cur)
	if name != "lectern" {
		return
	}
	nb, ok := world.WithProperty(cur, "powered", "false")
	if !ok {
		return
	}
	s.lecternWrite(d, pos, nb)
}

// lecternMenuBookCheck runs LecternBlockEntity.onBookItemRemove when a
// menu interaction emptied the book slot — the book's Page resets and
// the block writes has_book=false,powered=false. Vanilla's slot
// accepts nothing (canPlaceItem is a flat false), so the only
// transition the menu produces is the book leaving.
func (s *Server) lecternMenuBookCheck(w *openWindow) {
	c := w.container
	d := w.dim
	cur := d.w.GetBlock(w.pos)
	if !lecternHasBook(cur) {
		return
	}
	d.containers.mu.Lock()
	empty := c.slots[lecternSlot].Empty()
	if empty {
		c.page = 0
	}
	d.containers.mu.Unlock()
	if !empty {
		return
	}
	if nb, ok := world.WithProperty(cur, "has_book", "false"); ok {
		nb, _ = world.WithProperty(nb, "powered", "false")
		s.lecternWrite(d, w.pos, nb)
	}
	// Same stillValid close as the take button: the book slot went
	// empty under the open menu, so the menu closes — and removed()
	// runs placeItemBackInInventory on whatever rides the cursor.
	s.sessions.Range(func(_, v any) bool {
		other := v.(*Session)
		other.windowMu.Lock()
		ow := other.window
		other.windowMu.Unlock()
		if ow != nil && ow.container == c {
			s.sendContainerProperty(other, ow.id, 0, 0)
			s.closeWindow(other)
			s.menuRemoved(other, ow, false)
		}
		return true
	})
}

// lecternTakeBook is LecternMenu's take-book button: the book leaves
// the slot, the block writes has_book=false,powered=false, and the
// stack joins the player's inventory — dropping at their feet when it
// is full. mayBuild() gates spectators and adventure mode.
func (s *Server) lecternTakeBook(sess *Session, w *openWindow) {
	if sess.mode() == GameModeSpectator || sess.mode() == GameModeAdventure {
		return
	}
	c := w.container
	d := w.dim
	d.containers.mu.Lock()
	it := c.slots[lecternSlot]
	if it.Empty() {
		d.containers.mu.Unlock()
		return
	}
	c.slots[lecternSlot] = ItemStack{}
	c.page = 0
	d.containers.mu.Unlock()
	// onBookItemRemove runs inside removeItemNoUpdate — the state
	// write precedes the inventory add.
	cur := d.w.GetBlock(w.pos)
	if nb, ok := world.WithProperty(cur, "has_book", "false"); ok {
		nb, _ = world.WithProperty(nb, "powered", "false")
		s.lecternWrite(d, w.pos, nb)
	}
	remainder, _ := sess.addStack(it)
	s.setCarriedLocked(sess, remainder)
	s.syncContainerMenuSlots(sess, w, []int{lecternSlot})
	// LecternMenu.stillValid is hasBook: once the book leaves the
	// slot the menu stops being valid and vanilla closes it — the
	// page property resets to 0 on the way out, and removed()
	// returns the cursor stack to the inventory.
	s.sendContainerProperty(sess, w.id, 0, 0)
	s.closeWindow(sess)
	s.menuRemoved(sess, w, false)
}
