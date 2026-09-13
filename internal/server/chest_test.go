package server

import (
	"testing"

	"mc-go/internal/protocol"
	"mc-go/internal/world"
)

func clickWindowPacket(t *testing.T, window int32, slot int16, button byte, mode int32) protocol.Packet {
	t.Helper()
	var b protocol.Buffer
	b.VarInt(window)
	b.VarInt(0) // state
	b.Short(slot)
	b.Byte(button)
	b.VarInt(mode)
	b.VarInt(0) // changed slots
	b.VarInt(0) // carried item
	return protocol.Packet{ID: IDPlayClickContainerSB, Data: b.Bytes()}
}

func TestChestOpenAndTransfer(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Trader", 0.5, 0.5, 0.5)
	pos := world.Pos{X: 4, Y: 4, Z: 4}
	chest, _ := world.BlockState("chest")
	s.setBlockAndBroadcast("Trader", pos, chest)
	sess.setStack(invSlotForHotbar(0), ItemStack{ID: world.ItemIDs["dirt"], Count: 2})

	s.openChest(sess, pos)
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil {
		t.Fatal("window not open")
	}

	// Pick the hotbar stack (menu slot 54) onto the cursor.
	s.handleChestClick(sess, w, 54, 0, 0, true)
	if got := sess.carried; got != (ItemStack{ID: world.ItemIDs["dirt"], Count: 2}) {
		t.Fatalf("carried = %+v", got)
	}
	// Place it in the first chest slot.
	s.handleChestClick(sess, w, 0, 0, 0, true)
	if got := sess.carried; !got.Empty() {
		t.Fatalf("carried after place = %+v", got)
	}
	if got := s.menuStackForTest(sess, 0); got != (ItemStack{ID: world.ItemIDs["dirt"], Count: 2}) {
		t.Fatalf("chest slot = %+v", got)
	}
	if !sess.stackAt(invSlotForHotbar(0)).Empty() {
		t.Fatal("player slot still holds the items")
	}
}

// menuStackForTest reads menu slot 0 through the public path.
func (s *Server) menuStackForTest(sess *Session, slot int) ItemStack {
	return sess.menuStack(slot)
}

func TestChestDropsOnBreak(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Trader", 0.5, 0.5, 0.5)
	pos := world.Pos{X: 4, Y: 4, Z: 4}
	chest, _ := world.BlockState("chest")
	s.setBlockAndBroadcast("Trader", pos, chest)
	s.chestAt(s.dims[dimOverworld], pos).slots[3] = ItemStack{ID: world.ItemIDs["stone"], Count: 7}

	air := world.Air
	s.setBlockAndBroadcast("Trader", pos, air)
	found := ItemStack{}
	s.entities.mu.RLock()
	for _, e := range s.entities.byID {
		if e.Item.ID == world.ItemIDs["stone"] {
			found = e.Item
		}
	}
	s.entities.mu.RUnlock()
	if found.Count != 7 {
		t.Fatalf("chest drop = %+v", found)
	}
	_ = sess
}

func TestChestQuickMove(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Trader", 0.5, 0.5, 0.5)
	pos := world.Pos{X: 4, Y: 4, Z: 4}
	chest, _ := world.BlockState("chest")
	s.setBlockAndBroadcast("Trader", pos, chest)
	s.chestAt(s.dims[dimOverworld], pos).slots[0] = ItemStack{ID: world.ItemIDs["stone"], Count: 5}

	s.openChest(sess, pos)
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	// Quick-move chest slot 0 into the player inventory.
	s.handleChestClick(sess, w, 0, 0, 1, true)
	if got := sess.menuStack(0); !got.Empty() {
		t.Fatalf("chest slot after quick move = %+v", got)
	}
	// Vanilla quick-move scans the player main inventory first.
	if got := sess.stackAt(slotMainStart); got != (ItemStack{ID: world.ItemIDs["stone"], Count: 5}) {
		t.Fatalf("main inventory after quick move = %+v", got)
	}
}
