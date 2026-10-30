package server

import (
	"mc-go/internal/world"
	"reflect"
	"testing"
)

func nativeOminousRecordFixture(t *testing.T) map[string]any {
	t.Helper()
	tag, end, err := parseSNBTTag(jukeboxOminousRecord, 0)
	if err != nil || end != len(jukeboxOminousRecord) {
		t.Fatalf("native fixture parse%d %v", end, err)
	}
	return snbtToValue(tag).(map[string]any)
}

func TestNativeOminousItemRestore(t *testing.T) {
	record := nativeOminousRecordFixture(t)
	got := itemFromNBT(record)
	if got.ID != world.ItemIDs["white_banner"] || got.Count != 2 || !got.Ominous || got.BannerPatterns != "" {
		t.Fatalf("full native patch restored%#v", got)
	}
	delete(record["components"].(map[string]any), "minecraft:item_name")
	partial := itemFromNBT(record)
	if partial.Ominous || partial.BannerPatterns == "" {
		t.Fatalf("partial patch reclassified%#v", partial)
	}
}

func TestNativeOminousItemSaveRestart(t *testing.T) {
	dir := t.TempDir()
	saved := New("unused")
	t.Cleanup(saved.Stop)
	saved.WorldDir = dir
	pos := world.Pos{X: 3, Y: -60, Z: 3}
	saved.setSilentBlock(saved.dims[dimOverworld], pos, world.BlockStateMust("chest"))
	banner := ItemStack{ID: world.ItemIDs["white_banner"], Count: 2, Ominous: true}
	plain := ItemStack{ID: banner.ID, Count: 1}
	chest := saved.chestAt(saved.dims[dimOverworld], pos)
	chest.slots[0], chest.slots[1] = banner, plain
	nested := new([shulkerSlots]ItemStack)
	nested[0], nested[1] = banner, plain
	chest.slots[2] = ItemStack{ID: world.ItemIDs["shulker_box"], Count: 1, Contents: nested}
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
	check := func(got, want ItemStack) {
		t.Helper()
		if got.ID != want.ID || got.Count != want.Count || got.Ominous != want.Ominous || got.BannerPatterns != "" {
			t.Fatalf("restored%#v want%#v", got, want)
		}
		if want.Ominous {
			expected := nativeOminousRecordFixture(t)["components"]
			if actual := itemComponentsNBT(got); !reflect.DeepEqual(actual, expected) {
				t.Fatalf("persisted components%#v want%#v", actual, expected)
			}
		}
	}
	check(restored.slots[0], banner)
	check(restored.slots[1], plain)
	if restored.slots[2].Contents == nil {
		t.Fatal("nested contents absent")
	}
	check(restored.slots[2].Contents[0], banner)
	check(restored.slots[2].Contents[1], plain)
}
