package server

import (
	"bytes"
	"testing"
)

func TestCatNativeVariantHydration(t *testing.T) {
	s := simulationTestServer(t)
	cat, ok := s.summonMobRaw(s.dims[dimOverworld], "cat", 0, -60, 0, false, false)
	if !ok {
		t.Fatal("cat constructor")
	}
	// The generated native hut record saves both names, including royal sound.
	s.applyEntitySave(cat, map[string]any{"variant": "minecraft:all_black", "sound_variant": "minecraft:royal"})
	if cat.VariantName != "all_black" || cat.SoundVariant != "royal" || !cat.MetaDirty {
		t.Fatal("native holder fields", cat.VariantName, cat.SoundVariant, cat.MetaDirty)
	}
	saved := entitySaveMap(cat, s.clock.count, s.entities).m
	if saved["variant"] != "minecraft:all_black" || saved["sound_variant"] != "minecraft:royal" {
		t.Fatal("holder round trip", saved["variant"], saved["sound_variant"])
	}
	s.applyEntitySave(cat, map[string]any{})
	if cat.VariantName != "all_black" || cat.SoundVariant != "royal" {
		t.Fatal("absent holders replaced existing values")
	}
	fresh, ok := s.summonMobRaw(s.dims[dimOverworld], "cat", 2, -60, 0, false, false)
	if !ok {
		t.Fatal("fresh cat constructor")
	}
	s.applyEntitySave(fresh, map[string]any{})
	saved = entitySaveMap(fresh, s.clock.count, s.entities).m
	if saved["variant"] != "minecraft:black" || saved["sound_variant"] != "minecraft:classic" {
		t.Fatal("defined absent defaults", saved["variant"], saved["sound_variant"])
	}
}

// BLACK is the native define default (holder1); TABBY is a non-default
// holder9 and must therefore be packed when explicitly selected.
func TestCatNativeDefaultMetadata(t *testing.T) {
	s := simulationTestServer(t)
	cat, ok := s.summonMobRaw(s.dims[dimOverworld], "cat", 0, -60, 0, false, false)
	if !ok {
		t.Fatal("cat constructor")
	}
	defined := encodeLivingMetadata(cat, nil)
	if bytes.Contains(defined, []byte{20, 21}) {
		t.Fatal("defined black holder redundantly packed", defined)
	}
	cat.VariantName = "black"
	if got := encodeLivingMetadata(cat, nil); !bytes.Equal(got, defined) {
		t.Fatal("explicit black differs from defined holder", got, defined)
	}
	cat.VariantName = "tabby"
	if got := encodeLivingMetadata(cat, nil); !bytes.Contains(got, []byte{20, 21, 9}) {
		t.Fatal("explicit tabby holder omitted", got)
	}
}
