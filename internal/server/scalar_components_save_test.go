package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestNativeScalarComponentsSaveRestart(t *testing.T) {
	dir := t.TempDir()
	saved := New("unused")
	t.Cleanup(saved.Stop)
	saved.WorldDir = dir
	pos := world.Pos{X: 3, Y: -60, Z: 3}
	saved.setSilentBlock(saved.dims[dimOverworld], pos, world.BlockStateMust("chest"))
	mapID, _ := world.ItemID("filled_map")
	stoneID, _ := world.ItemID("stone")
	helmetID, _ := world.ItemID("leather_helmet")
	boxID, _ := world.ItemID("shulker_box")
	wants := []ItemStack{
		{ID: mapID, Count: 1, MapID: 5}, {ID: mapID, Count: 1, MapID: 1}, {ID: mapID, Count: 1},
		{ID: stoneID, Count: 1, BottleAmpSet: true, BottleAmp: 4}, {ID: stoneID, Count: 1, BottleAmpSet: true}, {ID: stoneID, Count: 1},
		{ID: helmetID, Count: 1, DyedColor: 1193046}, {ID: helmetID, Count: 1},
	}
	chest := saved.chestAt(saved.dims[dimOverworld], pos)
	for slot, it := range wants {
		chest.slots[slot] = it
	}
	nested := new([shulkerSlots]ItemStack)
	nested[0] = wants[1]
	nested[1] = wants[4]
	nested[2] = wants[6]
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
		if got.ID != want.ID || got.Count != want.Count || got.MapID != want.MapID || got.BottleAmpSet != want.BottleAmpSet || got.BottleAmp != want.BottleAmp || got.DyedColor != want.DyedColor {
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
	for slot, source := range []int{1, 4, 6} {
		check(slot, box.Contents[slot], wants[source])
	}
}
