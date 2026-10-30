package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestNativeStructuredComponentsSaveRestart(t *testing.T) {
	dir := t.TempDir()
	saved := New("unused")
	t.Cleanup(saved.Stop)
	saved.WorldDir = dir
	pos := world.Pos{X: 3, Y: -60, Z: 3}
	saved.setSilentBlock(saved.dims[dimOverworld], pos, world.BlockStateMust("chest"))
	stoneID, _ := world.ItemID("stone")
	boxID, _ := world.ItemID("shulker_box")
	wants := []ItemStack{
		{ID: stoneID, Count: 1, StewEffects: "night_vision:160,blindness:0"}, {ID: stoneID, Count: 1, StewEffects: "night_vision:160,blindness:200"}, {ID: stoneID, Count: 1},
		{ID: stoneID, Count: 1, PotDeco: "brick,angler_pottery_sherd,archer_pottery_sherd,skull_pottery_sherd"}, {ID: stoneID, Count: 1},
	}
	chest := saved.chestAt(saved.dims[dimOverworld], pos)
	for slot, it := range wants {
		chest.slots[slot] = it
	}
	nested := new([shulkerSlots]ItemStack)
	nested[0] = wants[0]
	nested[1] = wants[3]
	chest.slots[len(wants)] = ItemStack{ID: boxID, Count: 1, Contents: nested}
	if e := saved.SaveWorld(); e != nil {
		t.Fatal(e)
	}
	loaded := New("unused")
	t.Cleanup(loaded.Stop)
	loaded.WorldDir = dir
	if e := loaded.LoadWorld(); e != nil {
		t.Fatal(e)
	}
	restored := loaded.chestAt(loaded.dims[dimOverworld], pos)
	check := func(slot int, got, want ItemStack) {
		t.Helper()
		if got.ID != want.ID || got.Count != want.Count || got.StewEffects != want.StewEffects || got.PotDeco != want.PotDeco {
			t.Errorf("slot%d restored%#v want%#v", slot, got, want)
		}
	}
	for slot, want := range wants {
		check(slot, restored.slots[slot], want)
	}
	box := restored.slots[len(wants)]
	if box.Contents == nil {
		t.Fatal("nested contents missing")
	}
	for slot, source := range []int{0, 3} {
		check(slot, box.Contents[slot], wants[source])
	}
}
