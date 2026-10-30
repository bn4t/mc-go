package worldgen

import (
	"mc-go/internal/world"
	"testing"
)

func TestCommandStructureDetachedPlacement(t *testing.T) {
	plan, err := PlanCommandStructure(Vanilla(), 1, "swamp_hut", 0, 0, -64, 384, -63, func(int32, int32, int) int32 { return -60 })
	if err != nil || plan == nil {
		t.Fatalf("plan %v %v", plan, err)
	}
	bounds := plan.Bounds()
	chunks := map[[2]int32]*ProtoChunk{}
	bedrock, _ := world.BlockStateOf("bedrock", nil)
	dirt, _ := world.BlockStateOf("dirt", nil)
	grass, _ := world.BlockStateOf("grass_block", nil)
	for cx := bounds.MinX >> 4; cx <= bounds.MaxX>>4; cx++ {
		for cz := bounds.MinZ >> 4; cz <= bounds.MaxZ>>4; cz++ {
			c := &ProtoChunk{CX: cx, CZ: cz, MinY: -64, Height: 384, Blocks: make([]world.Block, 384*256)}
			for x := int32(0); x < 16; x++ {
				for z := int32(0); z < 16; z++ {
					c.Set(x, -64, z, bedrock)
					c.Set(x, -63, z, dirt)
					c.Set(x, -62, z, dirt)
					c.Set(x, -61, z, grass)
				}
			}
			chunks[[2]int32{cx, cz}] = c
		}
	}
	writes, entities, _ := plan.Place(chunks, NewLegacy(1))
	if len(writes) == 0 || len(entities) != 2 {
		t.Fatalf("writes%d residents%v", len(writes), entities)
	}
	kinds := map[string]bool{}
	for _, e := range entities {
		if e.Structure != "swamp_hut" || e.X != 2.5 || e.Y != -58 || e.Z != 5.5 {
			t.Fatalf("resident %+v", e)
		}
		kinds[e.Type] = true
	}
	if !kinds["cat"] || !kinds["witch"] {
		t.Fatal(kinds)
	}
	counts := map[string]int{}
	for _, c := range chunks {
		for _, b := range c.Blocks {
			name, _ := world.StateOwner(b)
			counts[name]++
		}
	}
	for name, want := range map[string]int{"spruce_planks": 84, "oak_log": 16, "spruce_stairs": 26, "oak_fence": 4, "crafting_table": 1, "cauldron": 1, "potted_red_mushroom": 1} {
		if counts[name] != want {
			t.Errorf("%s=%d want%d", name, counts[name], want)
		}
	}
	// Explicit placement must not populate the natural-generation start cache.
	if len(plan.generator.structs.starts) != 0 {
		t.Fatal("installed natural start")
	}
}

func TestCommandDesertPyramidPlacement(t *testing.T) {
	const top = 61 // first free y above a grass layer at 60
	plan, err := PlanCommandStructure(Vanilla(), 1, "desert_pyramid", 0, 0, -64, 384, -63, func(int32, int32, int) int32 { return top })
	if err != nil || plan == nil {
		t.Fatalf("plan %v %v", plan, err)
	}
	bounds := plan.Bounds()
	chunks := map[[2]int32]*ProtoChunk{}
	bedrock, _ := world.BlockStateOf("bedrock", nil)
	stone, _ := world.BlockStateOf("stone", nil)
	grass, _ := world.BlockStateOf("grass_block", nil)
	for cx := bounds.MinX >> 4; cx <= bounds.MaxX>>4; cx++ {
		for cz := bounds.MinZ >> 4; cz <= bounds.MaxZ>>4; cz++ {
			c := &ProtoChunk{CX: cx, CZ: cz, MinY: -64, Height: 384, Blocks: make([]world.Block, 384*256)}
			for x := int32(0); x < 16; x++ {
				for z := int32(0); z < 16; z++ {
					c.Set(x, -64, z, bedrock)
					for y := int32(-63); y < 60; y++ {
						c.Set(x, y, z, stone)
					}
					c.Set(x, 60, z, grass)
				}
			}
			chunks[[2]int32{cx, cz}] = c
		}
	}
	writes, entities, blockEntities := plan.Place(chunks, NewLegacy(12345))
	if len(writes) == 0 || len(entities) != 0 {
		t.Fatalf("writes%d entities%v", len(writes), entities)
	}
	counts := map[string]int{}
	for _, c := range chunks {
		for _, b := range c.Blocks {
			name, _ := world.StateOwner(b)
			counts[name]++
		}
	}
	// Fixed blueprint and trap-room counts. Sand and sandstone depend on the
	// level random (cellar walls, collapsed roof, suspicious-sand selection).
	for name, want := range map[string]int{
		"cut_sandstone": 238, "chiseled_sandstone": 62, "orange_terracotta": 66,
		"blue_terracotta": 2, "sandstone_slab": 2, "sandstone_stairs": 17,
		"tnt": 9, "stone_pressure_plate": 1, "chest": 4,
	} {
		if counts[name] != want {
			t.Errorf("%s=%d want%d", name, counts[name], want)
		}
	}
	// The cellar runs from local -14 to the roof at local 10; this seed's
	// 0..-2 lowering puts the base at 60.
	minY, maxY := int32(1<<30), int32(-1<<30)
	for _, w := range writes {
		minY, maxY = min(minY, w.Position[1]), max(maxY, w.Position[1])
	}
	if minY != 46 || maxY != 70 {
		t.Errorf("write y %d..%d want46..70", minY, maxY)
	}
	if n := counts["suspicious_sand"]; n < 6 || n > 8 {
		t.Errorf("suspicious sand %d outside 6..8", n)
	}
	chests, archaeology := 0, 0
	for _, be := range blockEntities {
		table, _ := be.Data["LootTable"].(string)
		switch table {
		case "minecraft:chests/desert_pyramid":
			chests++
			if _, ok := be.Data["LootTableSeed"].(int64); !ok {
				t.Errorf("chest %v has no loot seed", be.Pos)
			}
		case "minecraft:archaeology/desert_pyramid":
			archaeology++
		}
	}
	if chests != 4 || archaeology != counts["suspicious_sand"] {
		t.Errorf("chests=%d archaeology=%d suspicious=%d", chests, archaeology, counts["suspicious_sand"])
	}
}

func TestCommandJungleTemplePlacement(t *testing.T) {
	plan, err := PlanCommandStructure(Vanilla(), 1, "jungle_pyramid", 0, 0, -64, 384, -63, func(int32, int32, int) int32 { return -60 })
	if err != nil || plan == nil {
		t.Fatalf("plan %v %v", plan, err)
	}
	bounds := plan.Bounds()
	chunks := map[[2]int32]*ProtoChunk{}
	bedrock, _ := world.BlockStateOf("bedrock", nil)
	dirt, _ := world.BlockStateOf("dirt", nil)
	grass, _ := world.BlockStateOf("grass_block", nil)
	for cx := bounds.MinX >> 4; cx <= bounds.MaxX>>4; cx++ {
		for cz := bounds.MinZ >> 4; cz <= bounds.MaxZ>>4; cz++ {
			c := &ProtoChunk{CX: cx, CZ: cz, MinY: -64, Height: 384, Blocks: make([]world.Block, 384*256)}
			for x := int32(0); x < 16; x++ {
				for z := int32(0); z < 16; z++ {
					c.Set(x, -64, z, bedrock)
					c.Set(x, -63, z, dirt)
					c.Set(x, -62, z, dirt)
					c.Set(x, -61, z, grass)
				}
			}
			chunks[[2]int32{cx, cz}] = c
		}
	}
	writes, entities, blockEntities := plan.Place(chunks, NewLegacy(12345))
	if len(writes) == 0 || len(entities) != 0 {
		t.Fatalf("writes%d entities%v", len(writes), entities)
	}
	counts := map[string]int{}
	for _, c := range chunks {
		for _, b := range c.Blocks {
			name, _ := world.StateOwner(b)
			counts[name]++
		}
	}
	for name, want := range map[string]int{
		"chiseled_stone_bricks": 3, "cobblestone_stairs": 24, "tripwire": 5,
		"tripwire_hook": 4, "redstone_wire": 15, "lever": 3,
		"sticky_piston": 3, "repeater": 1, "chest": 2, "dispenser": 2,
	} {
		if counts[name] != want {
			t.Errorf("%s=%d want%d", name, counts[name], want)
		}
	}
	// The moss selector picks one state per selected cell; only the total is
	// fixed (the split follows the level random in the command path).
	if counts["cobblestone"]+counts["mossy_cobblestone"] != 1157 {
		t.Errorf("cobblestone+mossy=%d want1157", counts["cobblestone"]+counts["mossy_cobblestone"])
	}
	if counts["cobblestone"] == 0 || counts["mossy_cobblestone"] == 0 {
		t.Errorf("selector split cobble=%d mossy=%d", counts["cobblestone"], counts["mossy_cobblestone"])
	}
	minY, maxY := int32(1<<30), int32(-1<<30)
	for _, w := range writes {
		minY, maxY = min(minY, w.Position[1]), max(maxY, w.Position[1])
	}
	if minY != -64 || maxY != -51 {
		t.Errorf("write y %d..%d want-64..-51", minY, maxY)
	}
	loot := map[string]int{}
	for _, be := range blockEntities {
		table, _ := be.Data["LootTable"].(string)
		loot[table]++
		if _, ok := be.Data["LootTableSeed"].(int64); !ok {
			t.Errorf("%v has no loot seed", be.Pos)
		}
	}
	if loot["minecraft:chests/jungle_temple"] != 2 || loot["minecraft:chests/jungle_temple_dispenser"] != 2 {
		t.Errorf("loot tables %v", loot)
	}
}

func TestCommandIglooPlacement(t *testing.T) {
	plan, err := PlanCommandStructure(Vanilla(), 1, "igloo", 0, 0, -64, 384, -63, func(int32, int32, int) int32 { return 61 })
	if err != nil || plan == nil {
		t.Fatalf("plan %v %v", plan, err)
	}
	// Seed 1 draws the laboratory: the top, the bottom and six middle
	// ladder sections.
	if len(plan.start.Pieces) != 8 {
		t.Fatalf("pieces=%d want8", len(plan.start.Pieces))
	}
	bounds := plan.Bounds()
	chunks := map[[2]int32]*ProtoChunk{}
	bedrock, _ := world.BlockStateOf("bedrock", nil)
	stone, _ := world.BlockStateOf("stone", nil)
	grass, _ := world.BlockStateOf("grass_block", nil)
	for cx := bounds.MinX >> 4; cx <= bounds.MaxX>>4; cx++ {
		for cz := bounds.MinZ >> 4; cz <= bounds.MaxZ>>4; cz++ {
			c := &ProtoChunk{CX: cx, CZ: cz, MinY: -64, Height: 384, Blocks: make([]world.Block, 384*256)}
			for x := int32(0); x < 16; x++ {
				for z := int32(0); z < 16; z++ {
					c.Set(x, -64, z, bedrock)
					for y := int32(-63); y < 60; y++ {
						c.Set(x, y, z, stone)
					}
					c.Set(x, 60, z, grass)
				}
			}
			chunks[[2]int32{cx, cz}] = c
		}
	}
	writes, entities, blockEntities := plan.Place(chunks, NewLegacy(12345))
	if len(writes) == 0 || len(entities) != 0 {
		t.Fatalf("writes%d entities%v", len(writes), entities)
	}
	counts := map[string]int{}
	for _, c := range chunks {
		for _, b := range c.Blocks {
			name, _ := world.StateOwner(b)
			counts[name]++
		}
	}
	for name, want := range map[string]int{
		"snow_block": 94, "ice": 2, "chest": 1, "ladder": 22,
		"cobweb": 1, "brewing_stand": 1, "cauldron": 0,
	} {
		if counts[name] != want {
			t.Errorf("%s=%d want%d", name, counts[name], want)
		}
	}
	minY, maxY := int32(1<<30), int32(-1<<30)
	for _, w := range writes {
		minY, maxY = min(minY, w.Position[1]), max(maxY, w.Position[1])
	}
	if minY != 36 || maxY != 64 {
		t.Errorf("write y %d..%d want36..64", minY, maxY)
	}
	blockAt := func(p BlockPos) string {
		for _, c := range chunks {
			if p[0]>>4 == c.CX && p[2]>>4 == c.CZ {
				name, _ := world.StateOwner(c.Get(p[0]&15, p[1], p[2]&15))
				return name
			}
		}
		return ""
	}
	for pos, want := range map[BlockPos]string{
		{1, 38, 6}: "brewing_stand", {3, 38, 9}: "oak_wall_sign",
		{5, 37, 6}: "chest", {5, 61, 7}: "furnace",
	} {
		if got := blockAt(pos); got != want {
			t.Errorf("block %v=%s want%s", pos, got, want)
		}
	}
	chests := 0
	for _, be := range blockEntities {
		if table, _ := be.Data["LootTable"].(string); table == "minecraft:chests/igloo_chest" {
			chests++
			if be.Pos != (BlockPos{5, 37, 6}) {
				t.Errorf("chest at %v", be.Pos)
			}
			if _, ok := be.Data["LootTableSeed"].(int64); !ok {
				t.Error("chest has no loot seed")
			}
		}
	}
	if chests != 1 {
		t.Errorf("igloo chests=%d want1", chests)
	}
}
