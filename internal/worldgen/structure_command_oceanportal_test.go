package worldgen

import (
	"mc-go/internal/world"
	"testing"
)

func TestCommandOceanPortalBiomeSource(t *testing.T) {
	gen, _ := world.FlatGen(&world.FlatPreset{Layers: []world.FlatLayer{{Block: "bedrock", Height: 1}, {Block: "stone", Height: 120}, {Block: "dirt", Height: 3}, {Block: "grass_block", Height: 1}}}, -64)
	for _, tc := range []struct {
		biome string
		cold  bool
	}{{"minecraft:snowy_plains", true}, {"minecraft:the_void", false}} {
		t.Run(tc.biome, func(t *testing.T) {
			source := CommandStructureSource{BaseBlock: func(x, y, z int32) world.Block { return gen(world.Pos{X: int(x), Y: int(y), Z: int(z)}) }, FlatBiome: tc.biome}
			plan, err := PlanCommandStructure(Vanilla(), 1, "ruined_portal_ocean", 0, 0, -64, 384, -63, func(int32, int32, int) int32 { return 61 }, source)
			if err != nil || plan == nil {
				t.Fatalf("plan=%v err=%v", plan, err)
			}
			p := plan.start.Pieces[0].(*ruinedPortalPiece)
			if p.name != "minecraft:ruined_portal/portal_9" || p.pos != (BlockPos{0, 60, 0}) || p.rot != 1 || p.mir != 0 || p.placement != rpOnOceanFloor || p.props.cold != tc.cold {
				t.Fatalf("piece=%+v", p)
			}
			chunks := map[[2]int32]*ProtoChunk{}
			bounds := plan.Bounds()
			for x := bounds.MinX >> 4; x <= bounds.MaxX>>4; x++ {
				for z := bounds.MinZ >> 4; z <= bounds.MaxZ>>4; z++ {
					c := NewProtoChunk(x, z, -64, 384)
					for y := int32(-64); y < 61; y++ {
						for i := int32(0); i < 16; i++ {
							for j := int32(0); j < 16; j++ {
								c.Set(i, y, j, gen(world.Pos{Y: int(y)}))
							}
						}
					}
					chunks[[2]int32{x, z}] = c
				}
			}
			writes, entities, bes := plan.Place(chunks, NewLegacy(12345))
			if len(writes) == 0 || len(entities) != 0 || len(bes) != 1 {
				t.Fatalf("writes=%d entities=%v blockEntities=%v", len(writes), entities, bes)
			}
			if bes[0].Pos != (BlockPos{9, 61, 3}) || bes[0].Data["LootTable"] != "minecraft:chests/ruined_portal" {
				t.Fatalf("chest=%+v", bes[0])
			}
			counts := map[string]int{}
			for _, c := range chunks {
				for _, b := range c.Blocks {
					n, _ := world.StateOwner(b)
					counts[n]++
				}
			}
			if counts["obsidian"] != 11 || counts["crying_obsidian"] != 1 || counts["chest"] != 1 || counts["lava"] != 0 {
				t.Fatalf("counts=%v", counts)
			}
			if tc.cold && counts["magma_block"] != 0 || !tc.cold && counts["magma_block"] == 0 {
				t.Fatalf("cold=%t magma=%d", tc.cold, counts["magma_block"])
			}
		})
	}
	if plan, err := PlanCommandStructure(Vanilla(), 1, "ruined_portal_ocean", 0, 0, -64, 384, -63, func(int32, int32, int) int32 { return 61 }); err != nil || plan != nil {
		t.Fatalf("unmeasured context plan=%v err=%v", plan, err)
	}
}
