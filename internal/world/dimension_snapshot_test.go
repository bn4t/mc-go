package world

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"mc-go/internal/nbt"
)

func TestDimensionSaveSnapshotDetachedAfterStop(t *testing.T) {
	w := NewGen(nil, 2, func(Pos) Block { return Stone })
	w.SetBiome("minecraft:desert")
	pos := Pos{-17, 0, 16}
	w.SetBlock(pos, Air)
	bytes := []byte{1, 2}
	ints := []int32{3, 4}
	longs := []int64{5, 6}
	nested := map[string]any{"bytes": bytes, "ints": ints, "longs": longs}
	list := nbt.List{Elem: nbt.TagCompound, Items: []any{nested}}
	entity := map[string]any{"id": "minecraft:chest", "x": int32(-17), "y": int32(0), "z": int32(16), "nested": list}
	light := &ChunkLight{Sky: map[int][]byte{0: make([]byte, 2048)}, Block: map[int][]byte{0: make([]byte, 2048)}}
	light.Sky[0][0] = 42
	calls := 0
	snapshot := w.CaptureDimensionSave(0, 16, func(cx, cz int) []map[string]any {
		calls++
		if cx != -2 || cz != 1 {
			t.Fatalf("chunk %d,%d", cx, cz)
		}
		return []map[string]any{entity}
	}, func(int, int) *ChunkLight { return light })
	// A baseline before mutations also checks repeat writes use captured data.
	firstDir := t.TempDir()
	if err := snapshot.Write(firstDir, "test:dimension"); err != nil {
		t.Fatal(err)
	}
	before, err := ReadRegionFile(filepath.Join(DimensionDir(firstDir, "test:dimension"), "region", "r.-1.0.mca"))
	if err != nil {
		t.Fatal(err)
	}
	bytes[0] = 99
	ints[0] = 99
	longs[0] = 99
	nested["new"] = "mutated"
	list.Items[0] = "mutated"
	entity["id"] = "mutated"
	light.Sky[0][0] = 99
	light.Block[0] = []byte{99}
	delete(light.Sky, 0)
	w.SetBlock(pos, Dirt)
	w.SetBiome("minecraft:plains")
	w.Restore(map[Pos]Block{{X: 100}: Air})
	w.Stop()
	secondDir := t.TempDir()
	if err := snapshot.Write(secondDir, "test:dimension"); err != nil {
		t.Fatal(err)
	}
	after, err := ReadRegionFile(filepath.Join(DimensionDir(secondDir, "test:dimension"), "region", "r.-1.0.mca"))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || !reflect.DeepEqual(before, after) {
		t.Fatalf("capture retained mutable state or callbacks: calls=%d", calls)
	}
	chunk := after[[2]int32{30, 1}]
	gotEntity := chunk["block_entities"].(nbt.List).Items[0].(map[string]any)
	if gotEntity["id"] != "minecraft:chest" {
		t.Fatal("block entity changed")
	}
	section := chunk["sections"].(nbt.List).Items[0].(map[string]any)
	if section["SkyLight"].([]byte)[0] != 42 {
		t.Fatal("light changed")
	}
}

func TestDimensionSaveSnapshotInvalidNBTDefersErrorWithoutWriting(t *testing.T) {
	w := NewGen(nil, 1, func(Pos) Block { return Air })
	w.SetBlock(Pos{}, Stone)
	s := w.CaptureDimensionSave(0, 16, func(int, int) []map[string]any { return []map[string]any{{"invalid": new(int)}} }, nil)
	w.Stop()
	dir := t.TempDir()
	path := filepath.Join(DimensionDir(dir, "minecraft:overworld"), "region", "r.0.0.mca")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	prior := []byte("prior complete region")
	if err := os.WriteFile(path, prior, 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(dir, "minecraft:overworld"); err == nil {
		t.Fatal("unsupported NBT accepted")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(prior) {
		t.Fatalf("previous region changed: %q, %v", got, err)
	}
}
