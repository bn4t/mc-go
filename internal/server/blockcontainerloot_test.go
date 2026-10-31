package server

import (
	"mc-go/internal/world"
	"testing"
)

func TestBlockContainerLootCommandOrigin(t *testing.T) {
	s := nativeSaveTestServer(t)
	const seed = int64(-653164966778954613)
	for _, dimension := range []string{dimOverworld, dimNether} {
		for _, kind := range []string{"chest", "barrel"} {
			for _, biome := range []string{"minecraft:plains", "minecraft:sulfur_caves"} {
				for _, write := range []bool{false, true} {
					d := s.dims[dimension]
					pos := world.Pos{X: 8, Y: 0, Z: 8}
					d.biomesMu.Lock()
					if d.biomes == nil {
						d.biomes = map[[3]int]string{}
					}
					for x := -2; x < 6; x++ {
						for y := -2; y < 6; y++ {
							for z := -2; z < 6; z++ {
								d.biomes[[3]int{x, y, z}] = biome
							}
						}
					}
					d.biomesMu.Unlock()
					block, _ := world.BlockStateOf(kind, nil)
					d.w.SetBlock(pos, block)
					s.restoreBlockEntity(d, map[string]any{"id": "minecraft:" + kind, "x": int32(pos.X), "y": int32(pos.Y), "z": int32(pos.Z), "LootTable": "minecraft:chests/abandoned_mineshaft", "LootTableSeed": seed})
					before, _ := s.blockEntitySaveMap(d, pos)
					if before["LootTableSeed"] != seed || before["LootTable"] != "minecraft:chests/abandoned_mineshaft" {
						t.Fatal("loot was not latent", before)
					}
					if write {
						stone, _ := world.ItemID("stone")
						s.writeItemBlock(d, pos, 26, ItemStack{ID: stone, Count: 1})
					} else {
						item, _, ok := s.readItemBlock(d, pos, 26)
						want := "activator_rail"
						if biome == "minecraft:sulfur_caves" {
							want = "gold_ingot"
						}
						id, _ := world.ItemID(want)
						if !ok || item.ID != id || item.Count != 2 {
							t.Fatalf("dimension%s block%s biome%s read%v want%s/2", dimension, kind, biome, item, want)
						}
					}
					saved, _ := s.blockEntitySaveMap(d, pos)
					if saved["LootTable"] != nil || saved["LootTableSeed"] != nil {
						t.Fatal("command left loot armed", saved)
					}
					slots := make([]ItemStack, 27)
					itemsInto(saved["Items"], slots)
					occupied := 0
					for _, item := range slots {
						if !item.Empty() {
							occupied++
						}
					}
					wantCount, wantPick := 15, 1
					if biome == "minecraft:sulfur_caves" {
						wantCount, wantPick = 13, 8
					}
					pick, _ := world.ItemID("iron_pickaxe")
					if occupied != wantCount || slots[wantPick].ID != pick || slots[wantPick].Count != 1 {
						t.Fatalf("dimension%s block%s biome%s count%d pick%v", dimension, kind, biome, occupied, slots[wantPick])
					}
				}
			}
		}
	}
}
