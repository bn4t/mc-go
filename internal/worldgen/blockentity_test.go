package worldgen

import (
	"testing"

	"mc-go/internal/world"
)

// The WorldGenRegion block entity rules: a block-entity write leaves a
// pending default, reading it configures it, a later default write over
// a configured one keeps the configuration, a write of a plain block
// drops both, and a type change falls back to the fresh default.
func TestProtoBlockEntities(t *testing.T) {
	p := newProto(0, 0, 0, 16)
	rg := &Region{center: p, chunks: map[[2]int32]*ProtoChunk{{0, 0}: p}}
	set := func(pos BlockPos, b world.Block) {
		old := p.Get(pos[0], pos[1], pos[2])
		p.Set(pos[0], pos[1], pos[2], b)
		p.noteBlockWrite(pos, old, b)
	}
	chest, spawner, stone := blockNamed("chest"), blockNamed("spawner"), blockNamed("stone")
	a, b, c, d := BlockPos{1, 1, 1}, BlockPos{2, 1, 1}, BlockPos{3, 1, 1}, BlockPos{4, 1, 1}
	set(a, chest) // stays default
	set(b, chest)
	setLootTable(rg.blockEntity(b), "minecraft:chests/simple_dungeon", 7)
	set(b, chest) // DUMMY over a configured chest
	set(c, chest)
	rg.blockEntity(c)["x"] = 1
	set(c, stone) // dropped
	set(d, chest)
	rg.blockEntity(d)["LootTable"] = "t"
	set(d, spawner) // another type: fresh default
	if rg.blockEntity(BlockPos{5, 1, 1}) != nil {
		t.Error("block entity on air")
	}
	got := map[BlockPos]map[string]any{}
	for _, e := range p.BlockEntities() {
		got[e.Pos] = e.Data
	}
	if len(got) != 3 {
		t.Fatalf("got %v", got)
	}
	if v, ok := got[a]; !ok || v != nil {
		t.Errorf("default chest: %v %v", v, ok)
	}
	if got[b]["LootTable"] != "minecraft:chests/simple_dungeon" || got[b]["LootTableSeed"] != int64(7) {
		t.Errorf("configured chest: %v", got[b])
	}
	if v, ok := got[d]; !ok || v != nil {
		t.Errorf("retyped position: %v %v", v, ok)
	}
	if blockPosAsLong(BlockPos{-3682, 88, -4473}) != -1011825593782184 {
		t.Errorf("asLong %d", blockPosAsLong(BlockPos{-3682, 88, -4473}))
	}
}
