package server

import (
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"reflect"
	"testing"
)

func TestComponentSNBTStringTypes(t *testing.T) {
	value := map[string]any{"minecraft:key": nbt.List{Elem: nbt.TagString, Items: []any{"123", "true", "minecraft:speed", "plain", "1e2", "", `a\b"c`}}}
	raw := componentSNBT(value)
	tag, end, err := parseSNBTTag(raw, 0)
	if err != nil || end != len(raw) {
		t.Fatalf("invalid component SNBT %q: %v", raw, err)
	}
	if got := snbtToValue(tag); !reflect.DeepEqual(got, value) {
		t.Fatalf("type loss: got %#v want %#v", got, value)
	}
}
func TestBundlePotionNBTRoundTrip(t *testing.T) {
	child := ItemStack{ID: world.ItemIDs["potion"], Count: 1, Potion: "swiftness"}
	bundle := ItemStack{ID: world.ItemIDs["bundle"], Count: 1}
	bundleSetItems(&bundle, []ItemStack{child})
	restored := itemFromNBT(itemNBT(bundle))
	if restored.Bundle != bundle.Bundle {
		t.Fatalf("nested potion lost: got%q want%q", restored.Bundle, bundle.Bundle)
	}
}
