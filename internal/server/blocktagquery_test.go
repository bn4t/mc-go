package server

import (
	"bytes"
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"testing"
)

func TestBlockTagQueryLeavesLootAndStorageMetadata(t *testing.T) {
	s := New("unused")
	t.Cleanup(s.Stop)
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 1, Y: -60, Z: 0}
	s.setSilentBlock(d, pos, world.BlockStateMust("chest"))
	chest := s.chestAt(d, pos)
	chest.lootTable, chest.lootSeed = "minecraft:chests/simple_dungeon", 42
	root, err := nbt.ReadUnnamed(bytes.NewReader(s.blockQueryTag(d, pos)))
	if err != nil {
		t.Fatal(err)
	}
	fields, ok := root.(map[string]any)
	if !ok {
		t.Fatalf("root %T", root)
	}
	for _, key := range []string{"id", "x", "y", "z", "keepPacked"} {
		if _, ok := fields[key]; ok {
			t.Errorf("query retained %s", key)
		}
	}
	if fields["LootTable"] != chest.lootTable || fields["LootTableSeed"] != int64(42) {
		t.Fatalf("loot fields %#v", fields)
	}
	if _, ok := fields["Items"]; ok {
		t.Fatal("query unpacked loot")
	}
	if chest.lootTable != "minecraft:chests/simple_dungeon" || chest.lootSeed != 42 {
		t.Fatal("query mutated live loot state")
	}
	stored, ok := s.blockEntitySaveMap(d, pos)
	if !ok || stored["id"] != "minecraft:chest" || stored["x"] != int32(1) || stored["keepPacked"] != int8(0) {
		t.Fatalf("storage metadata changed: %#v", stored)
	}
}
