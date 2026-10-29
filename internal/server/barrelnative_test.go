package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestBarrelNativeAccessorUsesContainerStorage(t *testing.T) {
	s := simulationTestServer(t)
	d := s.dims[dimOverworld]
	p := world.Pos{X: 4, Y: -60, Z: 4}
	s.simulationAction(func() {
		block, _ := world.BlockState("barrel")
		s.setSilentBlock(d, p, block)
		c := s.containerAt(d, p)
		c.slots[0] = ItemStack{ID: world.ItemIDs["diamond"], Count: 3}
		tag, ok := s.blockEntitySaveMap(d, p)
		if !ok {
			t.Fatal("barrel native accessor did not find its live container")
		}
		var restored [27]ItemStack
		itemsInto(tag["Items"], restored[:])
		if restored[0].ID != world.ItemIDs["diamond"] || restored[0].Count != 3 {
			t.Fatalf("saved live slot %+v", restored[0])
		}
		c.slots[0].Count = 5
		itemsInto(tag["Items"], restored[:])
		if restored[0].Count != 3 {
			t.Fatal("saved tag aliases live slots")
		}
	})
}
