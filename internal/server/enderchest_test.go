package server

import (
	"testing"

	"mc-go/internal/world"
)

// The ender menu opens over the player's own storage: a stack moved
// into it is visible from a second chest across the map, and breaking
// the block drops no contents.
func TestEnderChestOpenAndTransfer(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Trader", 0.5, 0.5, 0.5)
	pos := world.Pos{X: 4, Y: 4, Z: 4}
	ender, _ := world.BlockState("ender_chest")
	s.setBlockAndBroadcast("Trader", pos, ender)
	sess.setStack(invSlotForHotbar(0), ItemStack{ID: world.ItemIDs["dirt"], Count: 2})

	s.openEnderChest(sess, pos)
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || w.ender == nil {
		t.Fatal("ender window not open")
	}

	// Pick the hotbar stack (menu slot 54) onto the cursor, then place
	// it in the first ender slot.
	s.handleChestClick(sess, w, 54, 0, 0, true)
	s.handleChestClick(sess, w, 0, 0, 0, true)
	if got := sess.carried; !got.Empty() {
		t.Fatalf("carried after place = %+v", got)
	}
	if got := (*w.ender)[0]; got != (ItemStack{ID: world.ItemIDs["dirt"], Count: 2}) {
		t.Fatalf("ender slot = %+v", got)
	}

	// A second chest shows the same storage: it follows the player,
	// not the block.
	pos2 := world.Pos{X: 9, Y: 4, Z: 9}
	s.setBlockAndBroadcast("Trader", pos2, ender)
	s.openEnderChest(sess, pos2)
	sess.windowMu.Lock()
	w2 := sess.window
	sess.windowMu.Unlock()
	if w2 == nil || w2.ender == nil {
		t.Fatal("second ender window not open")
	}
	if got := (*w2.ender)[0]; got != (ItemStack{ID: world.ItemIDs["dirt"], Count: 2}) {
		t.Fatalf("second chest shows %+v", got)
	}
}
