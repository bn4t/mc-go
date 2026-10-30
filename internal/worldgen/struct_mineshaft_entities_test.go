package worldgen

import (
	"mc-go/internal/world"
	"testing"
)

func TestMineshaftChestDescriptorAndRandomDraws(t *testing.T) {
	shapes := map[string]bool{}
	for _, seed := range []int64{0, 4096} {
		g, err := NewGenerator(Vanilla(), "overworld", "overworld", 12345)
		if err != nil {
			t.Fatal(err)
		}
		chunk := &ProtoChunk{CX: 0, CZ: 0, MinY: -64, Height: 384, Blocks: make([]world.Block, 16*16*384)}
		chunk.Set(1, 39, 1, blockNamed("stone"))
		region := g.NewRegion(chunk, map[[2]int32]*ProtoChunk{{0, 0}: chunk})
		context := &PieceContext{G: g, Region: region, ChunkBox: BoundingBox{0, -64, 0, 15, 319, 15}}
		piece := &msCorridor{}
		piece.init("mscorridor", 0, 0, BoundingBox{0, 40, 0, 2, 42, 4})
		piece.setOrientation(dirSouth)
		random, reference := NewLegacy(seed), NewLegacy(seed)
		wantShape := "east_west"
		if reference.NextBool() {
			wantShape = "north_south"
		}
		wantSeed := reference.NextLong()
		if !piece.createChest(context, context.ChunkBox, random, 1, 0, 1) {
			t.Fatal("valid chest cell rejected")
		}
		if len(chunk.Entities) != 1 {
			t.Fatal("descriptor count", len(chunk.Entities))
		}
		descriptor := chunk.Entities[0]
		if descriptor.Structure != "mineshaft" || descriptor.Type != "chest_minecart" || descriptor.X != 1.5 || descriptor.Y != 40.5 || descriptor.Z != 1.5 || descriptor.Yaw != 0 || descriptor.LootTable != "minecraft:chests/abandoned_mineshaft" || descriptor.LootSeed != wantSeed {
			t.Fatal("native cart descriptor", descriptor, wantSeed)
		}
		shape, _ := world.StateProperty(region.at(BlockPos{1, 40, 1}), "shape")
		if shape != wantShape {
			t.Fatal("rail random shape", shape, wantShape)
		}
		shapes[shape] = true
		if piece.createChest(context, context.ChunkBox, random, 1, 0, 1) || len(chunk.Entities) != 1 {
			t.Fatal("repeat placement duplicated cart")
		}
		if random.NextLong() != reference.NextLong() {
			t.Fatal("descriptor changed existing decoration random draws")
		}
		// An unsupported cell consumes neither the rail nor the loot draw.
		random, reference = NewLegacy(seed), NewLegacy(seed)
		if piece.createChest(context, context.ChunkBox, random, 2, 0, 2) {
			t.Fatal("unsupported chest cell accepted")
		}
		if random.NextLong() != reference.NextLong() {
			t.Fatal("rejected cell consumed random")
		}
	}
	if len(shapes) != 2 {
		t.Fatal("test seeds did not exercise both native rail choices", shapes)
	}
}
