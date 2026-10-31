package worldgen

import "testing"

func TestCommandStandardPortalSurfaceSource(t *testing.T) {
	for _, tc := range []struct {
		seed        int64
		template    string
		rot, mirror int
		air         bool
		chest       BlockPos
	}{
		{0, "portal_8", 2, 0, false, BlockPos{10, 66, 6}},
		{1, "portal_4", 0, 0, true, BlockPos{3, 65, 2}},
	} {
		actual, err := NewGenerator(Vanilla(), "overworld", "overworld", tc.seed)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := PlanCommandStructure(Vanilla(), actual.RS.Seed, "ruined_portal", 0, 0, -64, 384, 63, actual.BaseHeight, CommandStructureSource{NormalOverworld: true})
		if err != nil || plan == nil {
			t.Fatalf("seed%d plan%v err%v", tc.seed, plan, err)
		}
		p := plan.start.Pieces[0].(*ruinedPortalPiece)
		if !plan.lazyWG || p.name != "minecraft:ruined_portal/"+tc.template || p.pos != (BlockPos{0, 62, 0}) || p.rot != tc.rot || p.mir != tc.mirror || p.props.airPocket != tc.air || p.props.cold || p.placement != rpOnLandSurface {
			t.Fatalf("seed%d piece%+v", tc.seed, p)
		}
		chunks := map[[2]int32]*ProtoChunk{}
		b := plan.Bounds()
		for x := b.MinX >> 4; x <= b.MaxX>>4; x++ {
			for z := b.MinZ >> 4; z <= b.MaxZ>>4; z++ {
				chunks[[2]int32{x, z}] = actual.Terrain(x, z)
			}
		}
		retained := CommandWGHeightmaps{}
		writes, entities, bes := plan.Place(chunks, NewLegacy(12345), retained)
		if len(writes) == 0 || len(entities) != 0 || len(bes) != 1 || bes[0].Pos != tc.chest || bes[0].Data["LootTable"] != "minecraft:chests/ruined_portal" {
			t.Fatalf("seed%d writes%d entities%v bes%v", tc.seed, len(writes), entities, bes)
		}
		if len(retained) == 0 {
			t.Fatal("no retained WG sample")
		}
		for key := range retained {
			if key[2] != HMWorldSurfaceWG {
				t.Fatalf("unexpected retained type%v", key)
			}
		}
		if len(plan.generator.structs.starts) != 0 || len(actual.structs.starts) != 0 {
			t.Fatal("installed natural start")
		}
	}
}

func TestCommandStandardPortalUnsupportedSetup(t *testing.T) {
	// Independently inspect the selected existing builder to ensure that the
	// command rejects underground rather than drawing a replacement surface.
	g, err := NewGenerator(Vanilla(), "overworld", "overworld", 4096)
	if err != nil {
		t.Fatal(err)
	}
	g.structureState()
	def := g.structure("ruined_portal")
	r := NewLegacy(0)
	setLargeFeatureSeed(r, 4096, 0, 0)
	stub, ok := def.find(&GenContext{G: g, Random: r, ChunkX: 0, ChunkZ: 0, Seed: 4096, Def: def})
	if !ok {
		t.Fatal("no selected setup")
	}
	stub.Generate(func(piece StructurePiece) {
		p := piece.(*ruinedPortalPiece)
		if p.placement != rpUnderground || p.pos != (BlockPos{0, 17, 0}) {
			t.Fatalf("selected setup%+v", p)
		}
	})
	for _, src := range []CommandStructureSource{{NormalOverworld: true}, {}, {FlatBiome: "plains"}} {
		plan, err := PlanCommandStructure(Vanilla(), 4096, "ruined_portal", 0, 0, -64, 384, 63, g.BaseHeight, src)
		if err != nil || plan != nil {
			t.Fatalf("unsupported plan%v err%v", plan, err)
		}
	}
	for _, src := range []CommandStructureSource{{}, {FlatBiome: "plains"}} {
		plan, err := PlanCommandStructure(Vanilla(), 0, "ruined_portal", 0, 0, -64, 384, 63, g.BaseHeight, src)
		if err != nil || plan != nil {
			t.Fatalf("unsupported source plan%v err%v", plan, err)
		}
	}
}
