package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestPickItemFullHotbarPreservesDisplacedStack(t *testing.T) {
	s := New("unused")
	sess := placeSession(t, s, "Picker", 0, 70, 0)
	sess.setMode(GameModeCreative)
	dirt := world.ItemIDs["dirt"]
	stone := world.ItemIDs["stone"]
	for i := int16(0); i < 9; i++ {
		sess.setStack(slotHotbarStart+i, ItemStack{ID: dirt, Count: int32(i + 1), Ench: "unbreaking:1"})
	}
	sess.setStack(slotHotbarStart+7, ItemStack{ID: dirt, Count: 12})
	sess.held.Store(5)
	cursor := ItemStack{ID: dirt, Count: 42}
	sess.carriedMuSet(cursor)
	sess.pickItem(stone)
	if sess.held.Load() != 7 || sess.stackAt(slotHotbarStart+7) != (ItemStack{ID: stone, Count: 1}) {
		t.Fatal("did not prefer unenchanted slot")
	}
	if sess.stackAt(slotMainStart) != (ItemStack{ID: dirt, Count: 12}) {
		t.Fatal("displaced stack lost")
	}
	if sess.carriedBuffer() != cursor {
		t.Fatal("pick changed cursor")
	}
}
