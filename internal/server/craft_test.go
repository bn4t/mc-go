package server

import (
	"testing"

	"mc-go/internal/world"
)

func TestCraftLogToPlanks(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Crafter", 0.5, 0.5, 0.5)
	sess.setStack(slotCraftStart, ItemStack{ID: world.ItemIDs["oak_log"], Count: 1})
	s.refreshCraftResult(sess)
	if got := sess.stackAt(slotCraftResult); got != (ItemStack{ID: world.ItemIDs["oak_planks"], Count: 4}) {
		t.Fatalf("result = %+v, want 4 oak planks", got)
	}
	// Take one craft: cursor gets the planks, grid clears.
	s.handleCraftResultClick(sess, 0, 0)
	if got := sess.carried; got != (ItemStack{ID: world.ItemIDs["oak_planks"], Count: 4}) {
		t.Fatalf("carried = %+v", got)
	}
	if !sess.stackAt(slotCraftStart).Empty() || !sess.stackAt(slotCraftResult).Empty() {
		t.Fatal("grid or result not consumed")
	}
}

func TestCraftSticks(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Crafter", 0.5, 0.5, 0.5)
	planks := world.ItemIDs["oak_planks"]
	sess.setStack(slotCraftStart, ItemStack{ID: planks, Count: 1})
	sess.setStack(slotCraftStart+2, ItemStack{ID: planks, Count: 1})
	s.refreshCraftResult(sess)
	if got := sess.stackAt(slotCraftResult); got != (ItemStack{ID: world.ItemIDs["stick"], Count: 4}) {
		t.Fatalf("sticks result = %+v", got)
	}
}

func TestCraftCraftingTable(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Crafter", 0.5, 0.5, 0.5)
	planks := world.ItemIDs["oak_planks"]
	for i := int16(slotCraftStart); i < slotCraftStart+4; i++ {
		sess.setStack(i, ItemStack{ID: planks, Count: 1})
	}
	s.refreshCraftResult(sess)
	if got := sess.stackAt(slotCraftResult); got != (ItemStack{ID: world.ItemIDs["crafting_table"], Count: 1}) {
		t.Fatalf("table result = %+v", got)
	}
}

func TestCraftNoMatchClearsResult(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Crafter", 0.5, 0.5, 0.5)
	sess.setStack(slotCraftResult, ItemStack{ID: world.ItemIDs["stick"], Count: 4})
	sess.setStack(slotCraftStart, ItemStack{ID: world.ItemIDs["stone"], Count: 1})
	s.refreshCraftResult(sess)
	if !sess.stackAt(slotCraftResult).Empty() {
		t.Fatalf("stale result = %+v", sess.stackAt(slotCraftResult))
	}
}

func TestCraftingTableMenu(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Crafter", 0.5, 0.5, 0.5)
	pos := world.Pos{X: 4, Y: 4, Z: 4}
	table, _ := world.BlockState("crafting_table")
	s.setBlockAndBroadcast("Crafter", pos, table)
	s.openCraftingTable(sess, pos)
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	if w == nil || w.craft == nil {
		t.Fatal("crafting window not open")
	}
	// 2x2 planks in the top-left of the 3x3 grid: place one per slot
	// with right-clicks from a carried stack.
	planks := world.ItemIDs["oak_planks"]
	grid := []int{1, 2, 4, 5}
	sess.carriedMuSet(ItemStack{ID: planks, Count: 4})
	for _, slot := range grid {
		s.handleCraftTableClick(sess, w, w.craft, int16(slot), 1, 0)
	}
	if got := w.craft.slots[0]; got != (ItemStack{ID: world.ItemIDs["crafting_table"], Count: 1}) {
		t.Fatalf("table result = %+v", got)
	}
	// Take the result.
	s.handleCraftTableClick(sess, w, w.craft, 0, 0, 0)
	if got := sess.carriedBuffer(); got != (ItemStack{ID: world.ItemIDs["crafting_table"], Count: 1}) {
		t.Fatalf("carried = %+v", got)
	}
	for _, slot := range grid {
		if !w.craft.slots[slot].Empty() {
			t.Fatalf("grid slot %d not consumed: %+v", slot, w.craft.slots[slot])
		}
	}
}

func TestCraftingTableFurnaceRecipe(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Crafter", 0.5, 0.5, 0.5)
	pos := world.Pos{X: 4, Y: 4, Z: 4}
	table, _ := world.BlockState("crafting_table")
	s.setBlockAndBroadcast("Crafter", pos, table)
	s.openCraftingTable(sess, pos)
	sess.windowMu.Lock()
	w := sess.window
	sess.windowMu.Unlock()
	cobble := world.ItemIDs["cobblestone"]
	for _, slot := range []int{1, 2, 3, 4, 6, 7, 8, 9} {
		w.craft.slots[slot] = ItemStack{ID: cobble, Count: 1}
	}
	s.refreshTableResult(sess, w.craft)
	if got := w.craft.slots[0]; got != (ItemStack{ID: world.ItemIDs["furnace"], Count: 1}) {
		t.Fatalf("furnace result = %+v", got)
	}
}
