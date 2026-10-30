package world

import (
	"mc-go/internal/nbt"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSavedTicksCaptureIncludesGeneratedOnlyChunkAndDetaches(t *testing.T) {
	w := NewGen(nil, 1, func(Pos) Block { return Stone })
	if len(w.Snapshot()) != 0 {
		t.Fatal("fixture has block overrides")
	}
	key := [2]int32{-2, 1}
	block := SavedTick{Type: "minecraft:stone_button", Pos: Pos{-17, 0, 16}, Delay: -7, Priority: -3}
	fluid := SavedTick{Type: "minecraft:flowing_lava", Pos: Pos{-18, 0, 16}, Delay: 27, Priority: 2}
	records := map[[2]int32]SavedChunkTicks{key: {Blocks: []SavedTick{block}, Fluids: []SavedTick{fluid}}}
	snapshot := w.CaptureDimensionSaveWithTicks(0, 16, nil, nil, records)
	// Both map and underlying slices are caller-owned mutable input.
	blocks := records[key].Blocks
	fluids := records[key].Fluids
	blocks[0].Type = "mutated"
	blocks[0].Delay = 99
	fluids[0].Pos = Pos{}
	delete(records, key)
	w.SetBlock(block.Pos, Air)
	w.Stop()
	dir := t.TempDir()
	if err := snapshot.Write(dir, "test:dimension"); err != nil {
		t.Fatal(err)
	}
	chunks, err := ReadRegionFile(filepath.Join(DimensionDir(dir, "test:dimension"), "region", "r.-1.0.mca"))
	if err != nil {
		t.Fatal(err)
	}
	chunk := chunks[[2]int32{30, 1}]
	if chunk == nil {
		t.Fatal("tick-only generated chunk omitted")
	}
	for domain, record := range map[string]SavedTick{"block_ticks": block, "fluid_ticks": fluid} {
		got, ok := chunk[domain].(nbt.List)
		if !ok || got.Elem != nbt.TagCompound || len(got.Items) != 1 {
			t.Fatalf("%s list %#v", domain, chunk[domain])
		}
		want := map[string]any{"i": record.Type, "x": int32(record.Pos.X), "y": int32(record.Pos.Y), "z": int32(record.Pos.Z), "t": record.Delay, "p": record.Priority}
		if !reflect.DeepEqual(got.Items[0], want) {
			t.Errorf("%s got%#v want%#v", domain, got.Items[0], want)
		}
	}
}
