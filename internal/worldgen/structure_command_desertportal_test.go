package worldgen

import (
	"mc-go/internal/world"
	"testing"
)

func TestCommandDesertPortalPlacement(t *testing.T) {
	gen, _ := world.FlatGen(&world.FlatPreset{Layers: []world.FlatLayer{{Block: "bedrock", Height: 1}, {Block: "stone", Height: 190}, {Block: "water", Height: 13}, {Block: "grass_block", Height: 1}}}, -64)
	plan, err := PlanCommandStructure(Vanilla(), 1, "ruined_portal_desert", 0, 0, -64, 384, -63, func(int32, int32, int) int32 { return 141 }, CommandStructureSource{BaseBlock: func(x, y, z int32) world.Block { return gen(world.Pos{X: int(x), Y: int(y), Z: int(z)}) }})
	if err != nil || plan == nil {
		t.Fatal(err)
	}
	p := plan.start.Pieces[0].(*ruinedPortalPiece)
	if p.name != "minecraft:ruined_portal/portal_9" || p.pos != (BlockPos{0, 140, 0}) || p.rot != 1 || p.mir != 0 || p.placement != rpPartlyBuried {
		t.Fatalf("piece %+v", p)
	}
	chunks := map[[2]int32]*ProtoChunk{}
	b := plan.Bounds()
	for x := b.MinX >> 4; x <= b.MaxX>>4; x++ {
		for z := b.MinZ >> 4; z <= b.MaxZ>>4; z++ {
			c := NewProtoChunk(x, z, -64, 384)
			for y := int32(-64); y < 141; y++ {
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
		t.Fatalf("writes=%d entities=%v block entities=%v", len(writes), entities, bes)
	}
	if bes[0].Pos != (BlockPos{9, 141, 3}) || bes[0].Data["LootTable"] != "minecraft:chests/ruined_portal" || bes[0].Data["LootTableSeed"] != int64(6674089274190705457) {
		t.Fatalf("chest %+v", bes[0])
	}
	counts := map[string]int{}
	for _, c := range chunks {
		for _, b := range c.Blocks {
			n, _ := world.StateOwner(b)
			counts[n]++
		}
	}
	for _, cell := range []struct {
		pos  BlockPos
		name string
	}{{BlockPos{6, 141, 3}, "obsidian"}, {BlockPos{3, 144, 3}, "crying_obsidian"}, {BlockPos{5, 145, 3}, "obsidian"}} {
		c := chunks[[2]int32{cell.pos[0] >> 4, cell.pos[2] >> 4}]
		n, _ := world.StateOwner(c.Get(cell.pos[0]&15, cell.pos[1], cell.pos[2]&15))
		if n != cell.name {
			t.Errorf("cell %v=%s want%s", cell.pos, n, cell.name)
		}
	}

	if counts["obsidian"] != 11 || counts["chest"] != 1 {
		t.Fatalf("counts %v", counts)
	}
}

func TestCommandPortalBaseColumnPredicates(t *testing.T) {
	g, err := NewGenerator(Vanilla(), "overworld", "overworld", 1)
	if err != nil {
		t.Fatal(err)
	}
	g.RS.SetLevelBounds(-64, 384)
	stone := blockNamed("stone")
	water := blockNamed("water")
	calls := 0
	g.runtimeBaseBlock = func(x, y, z int32) world.Block {
		calls++
		if y == 55 {
			return water
		}
		if y <= 54 {
			return stone
		}
		return world.Air
	}
	b := BoundingBox{0, 0, 0, 8, 7, 8}
	if got := ruinedPortalY(g, NewLegacy(1), rpOnLandSurface, false, 60, 8, b); got != 55 {
		t.Fatalf("non-air predicate got%d want55", got)
	}
	if got := ruinedPortalY(g, NewLegacy(1), rpOnOceanFloor, false, 60, 8, b); got != 54 {
		t.Fatalf("motion predicate got%d want54", got)
	}
	if calls != 8*384 {
		t.Fatalf("sample count%d", calls)
	}
}
