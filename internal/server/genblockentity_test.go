package server

import (
	"bytes"
	"testing"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Every state with a block entity resolves to a 26.2 block entity type,
// and no other state does.
func TestBlockEntityTypeCoverage(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < world.StateCount(); i++ {
		b := world.Block(i)
		name, _ := world.StateOwner(b)
		if seen[name] {
			continue
		}
		seen[name] = true
		spec, ok := blockEntityOf(b)
		if has := worldgen.HasBlockEntity(b); has != ok {
			t.Errorf("%s: hasBlockEntity %v, resolved %v (%v)", name, has, ok, spec)
		}
	}
	for _, c := range []struct {
		block, id string
		reg       int32
	}{
		{"spawner", "minecraft:mob_spawner", 9}, {"bee_nest", "minecraft:beehive", 33},
		{"end_gateway", "minecraft:end_gateway", 22}, {"potent_sulfur", "minecraft:potent_sulfur", 48},
		{"oxidized_copper_chest", "minecraft:chest", 1}, {"suspicious_gravel", "minecraft:brushable_block", 40},
		{"sculk_catalyst", "minecraft:sculk_catalyst", 36}, {"creaking_heart", "minecraft:creaking_heart", 10},
		{"chest", "minecraft:chest", 1}, {"sculk_sensor", "minecraft:sculk_sensor", 34},
	} {
		b, _ := world.BlockStateOf(c.block, nil)
		spec, _ := blockEntityOf(b)
		if spec.name != c.id || spec.id != c.reg {
			t.Errorf("%s: got %v, want %s/%d", c.block, spec, c.id, c.reg)
		}
	}
}

// The seeded fill is a function of the seed, stays in the container,
// and refuses (seed 0, unmodelled tables) rather than guessing.
func TestLootFillExact(t *testing.T) {
	free := func(int) bool { return false }
	a, ok := lootFillExact("minecraft:chests/simple_dungeon", 490450898064816428, chestSlots, free)
	if !ok || len(a) == 0 {
		t.Fatalf("simple_dungeon fill: %v %v", a, ok)
	}
	b, _ := lootFillExact("minecraft:chests/simple_dungeon", 490450898064816428, chestSlots, free)
	for i, st := range a {
		if i < 0 || i >= chestSlots || st.Empty() || b[i] != st {
			t.Errorf("slot %d: %v vs %v", i, st, b[i])
		}
	}
	if _, ok := lootFillExact("minecraft:chests/simple_dungeon", 0, chestSlots, free); ok {
		t.Error("seed 0 filled")
	}
	if _, ok := lootFillExact("minecraft:chests/no_such_table", 5, chestSlots, free); ok {
		t.Error("unknown table filled")
	}
}

// A spawner's update tag is its saved fields without SpawnPotentials; a
// catalyst sends nothing.
func TestBlockEntityUpdateTag(t *testing.T) {
	f := genBlockEntityDefaults("mob_spawner")
	f["SpawnData"] = map[string]any{"entity": map[string]any{"id": "minecraft:zombie"}}
	raw := blockEntityUpdateTag("mob_spawner", f)
	v, err := nbt.ReadUnnamed(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	m := v.(map[string]any)
	if _, ok := m["SpawnPotentials"]; ok || m["Delay"] != int16(20) || m["SpawnData"] == nil {
		t.Errorf("spawner update tag %v", m)
	}
	if blockEntityUpdateTag("sculk_catalyst", genBlockEntityDefaults("sculk_catalyst")) != nil {
		t.Error("catalyst sends a tag")
	}
}
