package worldgen

import "testing"

func TestCommandOceanPortalNormalSource(t *testing.T) {
	for _, tc := range []struct {
		seed        int64
		origin      BlockPos
		rot, mirror int
		chest       BlockPos
	}{
		{0, BlockPos{0, 60, 0}, 0, 2, BlockPos{-4, 61, 0}},
		{1, BlockPos{0, 39, 0}, 1, 0, BlockPos{9, 40, 3}},
	} {
		actual, err := NewGenerator(Vanilla(), "overworld", "overworld", tc.seed)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := PlanCommandStructure(Vanilla(), actual.RS.Seed, "ruined_portal_ocean", 0, 0, -64, 384, 63, actual.BaseHeight, CommandStructureSource{NormalOverworld: true})
		if err != nil || plan == nil {
			t.Fatalf("seed%d plan=%v err=%v", tc.seed, plan, err)
		}
		piece := plan.start.Pieces[0].(*ruinedPortalPiece)
		if piece.name != "minecraft:ruined_portal/portal_9" || piece.pos != tc.origin || piece.rot != tc.rot || piece.mir != tc.mirror || piece.props.cold || piece.placement != rpOnOceanFloor {
			t.Fatalf("seed%d piece=%+v", tc.seed, piece)
		}
		chunks := map[[2]int32]*ProtoChunk{}
		b := plan.Bounds()
		for x := b.MinX >> 4; x <= b.MaxX>>4; x++ {
			for z := b.MinZ >> 4; z <= b.MaxZ>>4; z++ {
				chunks[[2]int32{x, z}] = actual.Terrain(x, z)
			}
		}
		writes, entities, bes := plan.Place(chunks, NewLegacy(12345))
		if len(writes) == 0 || len(entities) != 0 || len(bes) != 1 {
			t.Fatalf("seed%d writes%d entities%v blockEntities%v", tc.seed, len(writes), entities, bes)
		}
		if bes[0].Pos != tc.chest || bes[0].Data["LootTable"] != "minecraft:chests/ruined_portal" {
			t.Fatalf("seed%d chest=%+v", tc.seed, bes[0])
		}
		if len(plan.generator.structs.starts) != 0 || len(actual.structs.starts) != 0 {
			t.Fatal("installed natural start")
		}
	}
}
