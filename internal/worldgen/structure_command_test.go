package worldgen

import (
	"mc-go/internal/world"
	"testing"
)

func TestCommandStructureDetachedPlacement(t *testing.T) {
	plan, err := PlanCommandStructure(Vanilla(), 1, "swamp_hut", 0, 0, -64, 384, func(int32, int32, int) int32 { return -60 })
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
	writes, entities := plan.Place(chunks)
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
