package server

import (
	"mc-go/internal/world"
	"mc-go/internal/worldgen"
	"testing"
)

func TestStructurePlacementOptionsIndependentPolicies(t *testing.T) {
	s := simulationTestServer(t)
	d := s.dims[dimOverworld]
	p := world.Pos{X: 4, Y: -59, Z: 4}
	options := func() structureTemplatePlacement {
		return structureTemplatePlacement{origin: p, integrity: 1, knownShape: true, keepLiquids: true, settingsRandom: worldgen.NewLegacy(42), placementRandom: worldgen.NewLegacy(99)}
	}
	s.simulationAction(func() {
		redstone, _ := world.BlockState("redstone_block")
		s.setSilentBlock(d, world.Pos{X: 5, Y: -59, Z: 4}, redstone)
		tnt, _ := world.BlockState("tnt")
		s.structureTemplatePlaceWithOptions(d, oneBlockStructureForTest(tnt, nil), options())
		if d.w.GetBlock(p) != world.Air {
			t.Fatal("known shape incorrectly suppressed TNT onPlace")
		}
		opts := options()
		opts.skipSideEffects = true
		s.structureTemplatePlaceWithOptions(d, oneBlockStructureForTest(tnt, nil), opts)
		if d.w.GetBlock(p) != tnt {
			t.Fatal("strict options did not suppress TNT onPlace")
		}
		water, _ := world.BlockStateOf("water", map[string]string{"level": "0"})
		fence, _ := world.BlockStateOf("oak_fence", map[string]string{"waterlogged": "false"})
		for _, keep := range []bool{false, true} {
			s.setSilentBlockWithPolicy(d, p, water, true, true)
			opts := options()
			opts.skipSideEffects = true
			opts.keepLiquids = keep
			s.structureTemplatePlaceWithOptions(d, oneBlockStructureForTest(fence, nil), opts)
			want := "false"
			if keep {
				want = "true"
			}
			if got := world.BlockProperties(d.w.GetBlock(p))["waterlogged"]; got != want {
				t.Fatalf("keepLiquids%v waterlogged%s want%s", keep, got, want)
			}
		}
	})
}

func TestStructurePlacementOptionsSharedRandomStream(t *testing.T) {
	s := simulationTestServer(t)
	d := s.dims[dimOverworld]
	s.simulationAction(func() {
		chest, _ := world.BlockStateOf("chest", map[string]string{"facing": "north", "type": "single", "waterlogged": "false"})
		native := oneBlockStructureForTest(chest, map[string]any{"id": "minecraft:chest", "LootTable": "minecraft:chests/simple_dungeon"})
		placement := worldgen.NewLegacy(17)
		// Measured java.util.Random(17).nextLong() twice; palette draws must
		// neither consume nor reset this stream between runtime pieces.
		for i, want := range []int64{-4937981208836185383, -5582529378488325032} {
			p := world.Pos{X: 4 + i*3, Y: -59, Z: 4}
			s.structureTemplatePlaceWithOptions(d, native, structureTemplatePlacement{origin: p, rotation: 1, integrity: 1, knownShape: true, keepLiquids: true, settingsRandom: worldgen.NewLegacy(int64(i + 1)), placementRandom: placement})
			tag, ok := s.blockEntitySaveMap(d, p)
			if !ok || tag["LootTableSeed"] != want {
				t.Fatalf("piece%d loot%v want%d", i, tag, want)
			}
			if got := world.BlockProperties(d.w.GetBlock(p))["facing"]; got != "east" {
				t.Fatalf("piece%d facing%s want east", i, got)
			}
		}
	})
}
