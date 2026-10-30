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
			c := NewProtoChunk(cx, cz, -64, 384)
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
			c := NewProtoChunk(cx, cz, -64, 384)
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
			c := NewProtoChunk(cx, cz, -64, 384)
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
			c := NewProtoChunk(cx, cz, -64, 384)
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

func TestCommandShipwreckPlacement(t *testing.T) {
	for _, tc := range []struct {
		name     string
		template string
		rot      int
		planks   map[string]int
		chests   map[BlockPos]string
		minY     int32
		maxY     int32
	}{
		{
			name: "shipwreck", template: "minecraft:shipwreck/rightsideup_fronthalf", rot: 2,
			planks: map[string]int{"spruce_planks": 128, "dark_oak_planks": 58},
			chests: map[BlockPos]string{{4, -58, 22}: "minecraft:chests/shipwreck_supply"},
			minY:   -60, maxY: -52,
		},
		{
			name: "shipwreck_beached", template: "minecraft:shipwreck/rightsideup_backhalf", rot: 2,
			planks: map[string]int{"oak_planks": 164, "spruce_planks": 58},
			chests: map[BlockPos]string{
				{2, -61, 18}: "minecraft:chests/shipwreck_treasure",
				{3, -63, 24}: "minecraft:chests/shipwreck_map",
			},
			minY: -64, maxY: -57,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := PlanCommandStructure(Vanilla(), 1, tc.name, 0, 0, -64, 384, -63, func(int32, int32, int) int32 { return -60 })
			if err != nil || plan == nil {
				t.Fatalf("plan %v %v", plan, err)
			}
			p := plan.start.Pieces[0].(*shipwreckPiece)
			if p.name != tc.template || p.rot != tc.rot || p.tooBig() {
				t.Fatalf("template=%s rot=%d tooBig=%v", p.name, p.rot, p.tooBig())
			}
			bounds := plan.Bounds()
			chunks := map[[2]int32]*ProtoChunk{}
			bedrock, _ := world.BlockStateOf("bedrock", nil)
			dirt, _ := world.BlockStateOf("dirt", nil)
			grass, _ := world.BlockStateOf("grass_block", nil)
			for cx := bounds.MinX >> 4; cx <= bounds.MaxX>>4; cx++ {
				for cz := bounds.MinZ >> 4; cz <= bounds.MaxZ>>4; cz++ {
					c := NewProtoChunk(cx, cz, -64, 384)
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
					n, _ := world.StateOwner(b)
					counts[n]++
				}
			}
			for name, want := range tc.planks {
				if counts[name] != want {
					t.Errorf("%s=%d want%d", name, counts[name], want)
				}
			}
			if counts["chest"] != len(tc.chests) {
				t.Errorf("chest=%d want%d", counts["chest"], len(tc.chests))
			}
			minY, maxY := int32(1<<30), int32(-1<<30)
			for _, w := range writes {
				minY, maxY = min(minY, w.Position[1]), max(maxY, w.Position[1])
			}
			if minY != tc.minY || maxY != tc.maxY {
				t.Errorf("write y %d..%d want%d..%d", minY, maxY, tc.minY, tc.maxY)
			}
			seen := map[BlockPos]bool{}
			for _, be := range blockEntities {
				table, _ := be.Data["LootTable"].(string)
				if want, ok := tc.chests[be.Pos]; ok && table == want {
					seen[be.Pos] = true
					if _, ok := be.Data["LootTableSeed"].(int64); !ok {
						t.Errorf("chest %v has no loot seed", be.Pos)
					}
				}
			}
			if len(seen) != len(tc.chests) {
				t.Errorf("chests %v want %v", seen, tc.chests)
			}
		})
	}
}

func TestCommandOceanRuinPlacement(t *testing.T) {
	cases := []struct {
		name    string
		pieces  int
		bounds  BoundingBox
		chests  map[BlockPos]string
		writesY [2]int32
	}{
		{
			name: "ocean_ruin_cold", pieces: 18,
			bounds: BoundingBox{MinX: -34, MinY: 90, MinZ: -27, MaxX: 7, MaxY: 105, MaxZ: 8},
			chests: map[BlockPos]string{
				{-27, -60, -10}: "minecraft:chests/underwater_ruin_small",
				{-31, -60, 6}:   "minecraft:chests/underwater_ruin_small",
				{-13, -60, -23}: "minecraft:chests/underwater_ruin_small",
				{-9, -59, -10}:  "minecraft:chests/underwater_ruin_big",
				{1, -60, -21}:   "minecraft:chests/underwater_ruin_small",
				{6, -60, -10}:   "minecraft:chests/underwater_ruin_small",
			},
			writesY: [2]int32{-60, -52},
		},
		{
			name: "ocean_ruin_warm", pieces: 6,
			bounds: BoundingBox{MinX: -34, MinY: 90, MinZ: -27, MaxX: 7, MaxY: 105, MaxZ: 8},
			chests: map[BlockPos]string{
				{-33, -59, 4}:   "minecraft:chests/underwater_ruin_small",
				{-25, -60, -10}: "minecraft:chests/underwater_ruin_small",
				{-13, -60, -24}: "minecraft:chests/underwater_ruin_small",
				{-2, -59, -20}:  "minecraft:chests/underwater_ruin_small",
				{-7, -60, -7}:   "minecraft:chests/underwater_ruin_big",
				{5, -60, -10}:   "minecraft:chests/underwater_ruin_small",
			},
			writesY: [2]int32{-60, -54},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := PlanCommandStructure(Vanilla(), 1, tc.name, 0, 0, -64, 384, -63, func(int32, int32, int) int32 { return -60 })
			if err != nil || plan == nil {
				t.Fatalf("plan %v %v", plan, err)
			}
			if len(plan.start.Pieces) != tc.pieces {
				t.Fatalf("pieces=%d want%d", len(plan.start.Pieces), tc.pieces)
			}
			if b := plan.Bounds(); b != tc.bounds {
				t.Fatalf("bounds=%+v want%+v", b, tc.bounds)
			}
			bounds := plan.Bounds()
			chunks := map[[2]int32]*ProtoChunk{}
			bedrock, _ := world.BlockStateOf("bedrock", nil)
			dirt, _ := world.BlockStateOf("dirt", nil)
			grass, _ := world.BlockStateOf("grass_block", nil)
			for cx := bounds.MinX >> 4; cx <= bounds.MaxX>>4; cx++ {
				for cz := bounds.MinZ >> 4; cz <= bounds.MaxZ>>4; cz++ {
					c := NewProtoChunk(cx, cz, -64, 384)
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
					n, _ := world.StateOwner(b)
					counts[n]++
				}
			}
			if counts["chest"] != len(tc.chests) {
				t.Errorf("chest=%d want%d", counts["chest"], len(tc.chests))
			}
			seen := map[BlockPos]bool{}
			for _, be := range blockEntities {
				table, _ := be.Data["LootTable"].(string)
				if want, ok := tc.chests[be.Pos]; ok && table == want {
					seen[be.Pos] = true
					if _, ok := be.Data["LootTableSeed"].(int64); !ok {
						t.Errorf("chest %v has no loot seed", be.Pos)
					}
				}
			}
			if len(seen) != len(tc.chests) {
				t.Errorf("chests %v want %v", seen, tc.chests)
			}
			minY, maxY := int32(1<<30), int32(-1<<30)
			for _, w := range writes {
				minY, maxY = min(minY, w.Position[1]), max(maxY, w.Position[1])
			}
			if minY != tc.writesY[0] || maxY != tc.writesY[1] {
				t.Errorf("write y %d..%d want%d..%d", minY, maxY, tc.writesY[0], tc.writesY[1])
			}
		})
	}
}

func TestCommandRuinedPortalPlacement(t *testing.T) {
	cases := []struct {
		name      string
		template  string
		rot, mir  int
		placement int
		chest     BlockPos
		obsidian  int
		minY      int32
		maxY      int32
	}{
		{
			name: "ruined_portal_jungle", template: "minecraft:ruined_portal/portal_8", rot: 1, mir: 0, placement: 0,
			chest: BlockPos{9, -57, 1}, obsidian: 15, minY: -64, maxY: -53,
		},
		{
			name: "ruined_portal_swamp", template: "minecraft:ruined_portal/portal_9", rot: 1, mir: 0, placement: 2,
			chest: BlockPos{9, -60, 3}, obsidian: 11, minY: -64, maxY: -54,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := PlanCommandStructure(Vanilla(), 1, tc.name, 0, 0, -64, 384, -63, func(int32, int32, int) int32 { return -60 })
			if err != nil || plan == nil {
				t.Fatalf("plan %v %v", plan, err)
			}
			p := plan.start.Pieces[0].(*ruinedPortalPiece)
			if p.name != tc.template || p.rot != tc.rot || p.mir != tc.mir || p.placement != tc.placement || p.props.airPocket {
				t.Fatalf("template=%s rot=%d mir=%d placement=%d props=%+v", p.name, p.rot, p.mir, p.placement, p.props)
			}
			bounds := plan.Bounds()
			chunks := map[[2]int32]*ProtoChunk{}
			bedrock, _ := world.BlockStateOf("bedrock", nil)
			dirt, _ := world.BlockStateOf("dirt", nil)
			grass, _ := world.BlockStateOf("grass_block", nil)
			for cx := bounds.MinX >> 4; cx <= bounds.MaxX>>4; cx++ {
				for cz := bounds.MinZ >> 4; cz <= bounds.MaxZ>>4; cz++ {
					c := NewProtoChunk(cx, cz, -64, 384)
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
					n, _ := world.StateOwner(b)
					counts[n]++
				}
			}
			if counts["obsidian"] != tc.obsidian || counts["chest"] != 1 {
				t.Errorf("obsidian=%d chest=%d want%d/1", counts["obsidian"], counts["chest"], tc.obsidian)
			}
			chest := false
			for _, be := range blockEntities {
				if be.Pos == tc.chest && be.Data["LootTable"] == "minecraft:chests/ruined_portal" {
					chest = true
					if _, ok := be.Data["LootTableSeed"].(int64); !ok {
						t.Error("portal chest has no loot seed")
					}
				}
			}
			if !chest {
				t.Errorf("portal chest absent at %v", tc.chest)
			}
			minY, maxY := int32(1<<30), int32(-1<<30)
			for _, w := range writes {
				minY, maxY = min(minY, w.Position[1]), max(maxY, w.Position[1])
			}
			if minY != tc.minY || maxY != tc.maxY {
				t.Errorf("write y %d..%d want%d..%d", minY, maxY, tc.minY, tc.maxY)
			}
		})
	}
}
