package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestNativePaletteComponentsSaveRestart(t *testing.T) {
	dir := t.TempDir()
	saved := New("unused")
	t.Cleanup(saved.Stop)
	saved.WorldDir = dir
	pos := world.Pos{X: 3, Y: -60, Z: 3}
	saved.setSilentBlock(saved.dims[dimOverworld], pos, world.BlockStateMust("chest"))
	stoneID, _ := world.ItemID("stone")
	bannerID, _ := world.ItemID("white_banner")
	boxID, _ := world.ItemID("shulker_box")
	wants := []ItemStack{
		{ID: stoneID, Count: 1, BaseColor: 1}, {ID: stoneID, Count: 1, BaseColor: 15}, {ID: stoneID, Count: 1},
		{ID: stoneID, Count: 1, Dye: 1}, {ID: stoneID, Count: 1, Dye: 15}, {ID: stoneID, Count: 1},
		{ID: bannerID, Count: 1, BannerPatterns: "stripe_bottom:red,circle:white"}, {ID: bannerID, Count: 1},
	}
	chest := saved.chestAt(saved.dims[dimOverworld], pos)
	for slot, it := range wants {
		chest.slots[slot] = it
	}
	nested := new([shulkerSlots]ItemStack)
	nested[0] = wants[0]
	nested[1] = wants[3]
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
		if got.ID != want.ID || got.Count != want.Count || got.BaseColor != want.BaseColor || got.Dye != want.Dye || got.BannerPatterns != want.BannerPatterns {
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
	for slot, source := range []int{0, 3, 6} {
		check(slot, box.Contents[slot], wants[source])
	}
}
