package server

import (
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"testing"
)

// The same native-observed boundary runs through the world-tick consumer
// with no admitted sessions. Hopper state and payload are explicitly set
// for each phase; this does not claim redstone neighbor-update parity.
func TestHopperContainerLootWithoutPlayers(t *testing.T) {
	for _, kind := range []string{"chest", "barrel"} {
		for _, sulfur := range []bool{false, true} {
			for _, push := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/sulfur%v/push%v", kind, sulfur, push), func(t *testing.T) {
					s := nativeSaveTestServer(t)
					s.WorldSeed = 12345
					d := s.dims[dimOverworld]
					d.minY, d.height = -64, 384
					d.biomes = map[[3]int]string{}
					for x := 0; x < 6; x++ {
						for y := -16; y < -11; y++ {
							for z := 0; z < 6; z++ {
								d.biomes[[3]int{x, y, z}] = "minecraft:plains"
							}
						}
					}
					if sulfur {
						d.biomes[[3]int{2, -15, 2}] = "minecraft:sulfur_caves"
					}
					p := world.Pos{X: 8, Y: -58, Z: 11}
					h := p
					face := "down"
					if push {
						h.X--
						face = "east"
					} else {
						h.Y--
					}
					wantBiome := "minecraft:plains"
					if sulfur {
						wantBiome = "minecraft:sulfur_caves"
					}
					if s.spawnBiomeAt(d, p) != wantBiome || s.spawnBiomeAt(d, h) != "minecraft:plains" {
						t.Fatal("actual container/hopper biome fixture invalid")
					}
					block, _ := world.BlockStateOf(kind, nil)
					d.w.SetBlock(p, block)
					s.restoreBlockEntity(d, map[string]any{"id": "minecraft:" + kind, "x": int32(p.X), "y": int32(p.Y), "z": int32(p.Z), "LootTable": "minecraft:chests/abandoned_mineshaft", "LootTableSeed": int64(-653164966778954613)})
					inject := func(enabled bool, full bool, input int32) {
						b, _ := world.BlockStateOf("hopper", map[string]string{"facing": face, "enabled": fmt.Sprint(enabled)})
						d.w.SetBlock(h, b)
						var items []any
						if full {
							for slot := 0; slot < 5; slot++ {
								items = append(items, map[string]any{"Slot": int8(slot), "id": "minecraft:stone", "count": int32(64)})
							}
						} else if input > 0 {
							items = append(items, map[string]any{"Slot": int8(0), "id": "minecraft:stone", "count": input})
						}
						s.restoreBlockEntity(d, map[string]any{"id": "minecraft:hopper", "x": int32(h.X), "y": int32(h.Y), "z": int32(h.Z), "Items": nbt.List{Elem: nbt.TagCompound, Items: items}, "TransferCooldown": int32(0)})
					}
					tick := func() {
						players := 0
						s.sessions.Range(func(_, _ any) bool { players++; return true })
						if players != 0 || s.nextWindow.Load() != 0 {
							t.Fatal("player-free fixture admitted session/menu")
						}
						before := s.clock.count
						s.GameTick()
						if s.clock.count != before+1 {
							t.Fatal("world tick did not complete exactly once")
						}
					}
					latent := func() {
						save, _ := s.blockEntitySaveMap(d, p)
						if save["LootTable"] != "minecraft:chests/abandoned_mineshaft" || save["LootTableSeed"] != int64(-653164966778954613) || save["Items"] != nil {
							t.Fatal("negative phase prematurely unpacked loot", save)
						}
					}
					input := int32(0)
					if push {
						input = 2
					}
					inject(false, false, input)
					tick()
					latent()
					hopper := s.hopperAt(d, h)
					if hopper.cooldown != 0 || hopper.slots[0].Count != input {
						t.Fatal("disabled hopper changed input/cooldown", hopper)
					}
					inject(true, !push, 0)
					tick()
					latent()
					hopper = s.hopperAt(d, h)
					if hopper.cooldown != 0 {
						t.Fatal("blocked hopper got cooldown")
					}
					for _, item := range hopper.slots {
						want := int32(0)
						if !push {
							want = 64
						}
						if item.Count != want {
							t.Fatal("blocked hopper changed inventory", hopper)
						}
					}
					inject(true, false, input)
					tick()
					save, _ := s.blockEntitySaveMap(d, p)
					if save["LootTable"] != nil || save["LootTableSeed"] != nil {
						t.Fatal("eligible hopper did not consume table/seed", save)
					}
					slots := make([]ItemStack, 27)
					itemsInto(save["Items"], slots)
					occupied := 0
					for _, item := range slots {
						if !item.Empty() {
							occupied++
						}
					}
					wantCount := 14
					wantItem := "iron_pickaxe"
					if sulfur {
						wantCount = 13
						wantItem = "iron_ingot"
					}
					if push {
						wantCount += 2
						if sulfur {
							wantCount--
						}
						wantItem = "stone"
						stone, _ := world.ItemID("stone")
						if slots[0].ID != stone || slots[0].Count != 1 {
							t.Fatal("insertion target missing stone")
						}
					} else if sulfur {
						if slots[3].Count != 1 {
							t.Fatal("pull source count did not decrement")
						}
					} else if !slots[1].Empty() {
						t.Fatal("pull source did not retire one-count stack")
					}
					hopper = s.hopperAt(d, h)
					id, _ := world.ItemID(wantItem)
					if occupied != wantCount || hopper.slots[0].ID != id || hopper.slots[0].Count != 1 || hopper.cooldown != 8 {
						t.Fatalf("count%d want%d hopper%v", occupied, wantCount, hopper)
					}
				})
			}
		}
	}
}
