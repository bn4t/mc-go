package server

import (
	"mc-go/internal/data"
	"mc-go/internal/world"
	"testing"
)

func TestLootBiomeConditions(t *testing.T) {
	conditions := []any{map[string]any{"condition": "minecraft:location_check", "predicate": map[string]any{"biomes": "minecraft:sulfur_caves"}}}
	for _, tc := range []struct {
		biome        string
		match, known bool
	}{{"sulfur_caves", true, true}, {"minecraft:plains", false, true}, {"", false, false}} {
		match, known := lootBiomeConditions(conditions, tc.biome)
		if match != tc.match || known != tc.known {
			t.Errorf("biome%q got%v/%v want%v/%v", tc.biome, match, known, tc.match, tc.known)
		}
	}
	conditions[0].(map[string]any)["offsetY"] = float64(1)
	if _, known := lootBiomeConditions(conditions, "sulfur_caves"); known {
		t.Fatal("unsupported location offset accepted")
	}
	if _, known := lootBiomeConditions("bad", "sulfur_caves"); known {
		t.Fatal("malformed conditions accepted")
	}
}

// Biome origin belongs to the container, independently of the player used
// for advancement credit. Surround the cart with a different biome than
// the opener and repeat in both dimensions to expose context substitution.
func TestVehicleLootBiomeOrigin(t *testing.T) {
	s := nativeSaveTestServer(t)
	player, _ := enforceSession(t, s, "BiomeLoot")
	player.setPos(100.5, 0, 100.5, 0, 0, true)
	table := "test/vehicle_biome_origin"
	tables := data.LootTables()
	old, had := tables[table]
	defer func() {
		if had {
			tables[table] = old
		} else {
			delete(tables, table)
		}
	}()
	tables[table] = map[string]any{"pools": []any{map[string]any{"rolls": float64(1), "entries": []any{map[string]any{"type": "minecraft:item", "name": "minecraft:diamond", "conditions": []any{map[string]any{"condition": "minecraft:location_check", "predicate": map[string]any{"biomes": "minecraft:sulfur_caves"}}}}}}}}
	for _, dimension := range []string{dimOverworld, dimNether} {
		d := s.dims[dimension]
		if d == nil {
			t.Fatal("test dimension missing", dimension)
		}
		if d.biomes == nil {
			d.biomes = make(map[[3]int]string)
		}
		for _, biome := range []string{"minecraft:sulfur_caves", "minecraft:plains"} {
			for x := -2; x <= 2; x++ {
				for y := -2; y <= 2; y++ {
					for z := -2; z <= 2; z++ {
						d.biomes[[3]int{x, y, z}] = biome
					}
				}
			}
			cart := s.entities.Spawn(&Entity{Type: mustEntityType("chest_minecart"), Dim: dimension, X: -0.5, Y: 0, Z: -0.5, ExtraNBT: map[string]any{"LootTable": "minecraft:" + table, "LootTableSeed": int64(31)}})
			inventory := s.vehicleCargoInventory(cart)
			if inventory.lootSeed != 31 || inventory.lootTable == "" {
				t.Fatal("loot was not latent")
			}
			s.unpackVehicleCargo(cart, inventory, player)
			count := int32(0)
			diamond, _ := world.ItemID("diamond")
			for _, item := range inventory.slots {
				if !item.Empty() {
					if item.ID != diamond {
						t.Fatal("unexpected item", item)
					}
					count += item.Count
				}
			}
			want := int32(0)
			if biome == "minecraft:sulfur_caves" {
				want = 1
			}
			if count != want || inventory.lootTable != "" || inventory.lootSeed != 0 {
				t.Errorf("dimension%s biome%s count%d want%d latent%s/%d", dimension, biome, count, want, inventory.lootTable, inventory.lootSeed)
			}
		}
	}
}
