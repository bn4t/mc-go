package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestNativeFireworkInstrumentSaveRestart(t *testing.T) {
	dir := t.TempDir()
	saved := New("unused")
	t.Cleanup(saved.Stop)
	saved.WorldDir = dir
	pos := world.Pos{X: 3, Y: -60, Z: 3}
	saved.setSilentBlock(saved.dims[dimOverworld], pos, world.BlockStateMust("chest"))
	stoneID, _ := world.ItemID("stone")
	hornID, _ := world.ItemID("goat_horn")
	boxID, _ := world.ItemID("shulker_box")
	wants := []ItemStack{
		{ID: stoneID, Count: 1, FireworkExp: "star|1,2|3|1|1"}, {ID: stoneID, Count: 1, FireworkExp: "small_ball|||0|0"}, {ID: stoneID, Count: 1},
		{ID: stoneID, Count: 1, Fireworks: "2;star|1,2|3|1|1;small_ball|4||0|0"}, {ID: stoneID, Count: 1, Fireworks: "0"}, {ID: stoneID, Count: 1},
		{ID: hornID, Count: 1, InstrumentRemoved: true}, {ID: hornID, Count: 1, Instrument: "seek_goat_horn"}, {ID: hornID, Count: 1},
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
		if got.ID != want.ID || got.Count != want.Count || got.FireworkExp != want.FireworkExp || got.Fireworks != want.Fireworks || got.Instrument != want.Instrument || got.InstrumentRemoved != want.InstrumentRemoved {
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
