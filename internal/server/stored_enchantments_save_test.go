package server

import (
	"mc-go/internal/world"
	"reflect"
	"testing"
)

func TestNativeStoredEnchantmentsMapsRemainDistinct(t *testing.T) {
	components := itemComponentsNBT(ItemStack{Ench: "protection:1", StoredEnch: "mending:2"})
	if !reflect.DeepEqual(components["minecraft:enchantments"], map[string]any{"minecraft:protection": int32(1)}) || !reflect.DeepEqual(components["minecraft:stored_enchantments"], map[string]any{"minecraft:mending": int32(2)}) {
		t.Fatalf("components %#v", components)
	}
}

func TestNativeStoredEnchantmentsSaveRestart(t *testing.T) {
	dir := t.TempDir()
	saved := New("unused")
	t.Cleanup(saved.Stop)
	saved.WorldDir = dir
	pos := world.Pos{X: 3, Y: -60, Z: 3}
	saved.setSilentBlock(saved.dims[dimOverworld], pos, world.BlockStateMust("chest"))
	bookID, _ := world.ItemID("enchanted_book")
	helmetID, _ := world.ItemID("leather_helmet")
	boxID, _ := world.ItemID("shulker_box")
	book := ItemStack{ID: bookID, Count: 1, StoredEnch: "mending:1,unbreaking:2"}
	chest := saved.chestAt(saved.dims[dimOverworld], pos)
	chest.slots[0] = book
	chest.slots[1] = ItemStack{ID: helmetID, Count: 1, Ench: "protection:1", StoredEnch: "mending:2"}
	contents := new([shulkerSlots]ItemStack)
	contents[0] = book
	chest.slots[2] = ItemStack{ID: boxID, Count: 1, Contents: contents}
	if err := saved.SaveWorld(); err != nil {
		t.Fatal(err)
	}
	loaded := New("unused")
	t.Cleanup(loaded.Stop)
	loaded.WorldDir = dir
	if err := loaded.LoadWorld(); err != nil {
		t.Fatal(err)
	}
	restored := loaded.chestAt(loaded.dims[dimOverworld], pos)
	if got := restored.slots[0]; got.ID != bookID || got.StoredEnch != book.StoredEnch || got.Ench != "" {
		t.Fatalf("restored book %#v", got)
	}
	if got := restored.slots[1]; got.Ench != "protection:1" || got.StoredEnch != "mending:2" {
		t.Fatalf("restored dual item %#v", got)
	}
	if got := restored.slots[2]; got.Contents == nil || got.Contents[0].StoredEnch != book.StoredEnch {
		t.Fatalf("restored nested book %#v", got)
	}
}
