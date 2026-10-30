package world

import (
	"math"
	"reflect"
	"testing"

	"mc-go/internal/nbt"
)

func TestSavedTickDecodeNativePartialNumericAndChunkRules(t *testing.T) {
	row := func() map[string]any {
		return map[string]any{"i": "stone_button", "x": int32(-1), "y": int32(1000), "z": int32(16), "t": int32(1000), "p": int32(0)}
	}
	valid := row()
	valid["i"] = ":stone_button"
	duplicate := row()
	duplicate["t"], duplicate["p"] = int64(2147483648), float64(-99.75)
	missing := row()
	delete(missing, "t")
	unknown := row()
	unknown["i"] = "minecraft:not_a_block"
	foreign := row()
	foreign["x"] = int32(0)
	stringNumber := row()
	stringNumber["t"] = "1000"
	numeric := row()
	numeric["x"], numeric["t"], numeric["p"] = float64(-1.75), float64(1000.75), int16(99)
	root := map[string]any{"block_ticks": nbt.List{Elem: nbt.TagCompound, Items: []any{valid, missing, unknown, foreign, stringNumber, numeric, duplicate}}}
	got := DecodeSavedChunkTicks(root, -1, 1)
	want := []SavedTick{
		{Type: "minecraft:stone_button", Pos: Pos{-1, 1000, 16}, Delay: 1000},
		{Type: "minecraft:stone_button", Pos: Pos{-1, 1000, 16}, Delay: 1000, Priority: 3},
		{Type: "minecraft:stone_button", Pos: Pos{-1, 1000, 16}, Delay: math.MinInt32, Priority: -3},
	}
	if !reflect.DeepEqual(got.Blocks, want) || len(got.Fluids) != 0 {
		t.Fatalf("decoded %#v, want blocks %#v", got, want)
	}
	valid["t"] = int32(7)
	if got.Blocks[0].Delay != 1000 {
		t.Fatal("decoded record retained caller data")
	}
}

func TestSavedTickDecodeFluidRegistry(t *testing.T) {
	var items []any
	for _, name := range []string{"water", "flowing_water", "lava", "flowing_lava", "empty", "not_a_fluid", "other:water"} {
		items = append(items, map[string]any{"i": name, "x": int32(0), "y": int32(0), "z": int32(0), "t": int32(1), "p": int32(0)})
	}
	got := DecodeSavedChunkTicks(map[string]any{"fluid_ticks": nbt.List{Elem: nbt.TagCompound, Items: items}}, 0, 0)
	if len(got.Fluids) != 5 {
		t.Fatalf("fluid registry records %#v", got.Fluids)
	}
}

func TestSavedTickCodecNumberIntValue(t *testing.T) {
	for _, tc := range []struct {
		value any
		want  int32
	}{
		{int8(-1), -1}, {int16(1000), 1000}, {int32(123), 123},
		{int64(2147483648), math.MinInt32}, {int64(-2147483649), math.MaxInt32},
		{float32(-1.75), -1}, {float64(1000.75), 1000},
		{math.NaN(), 0}, {math.Inf(1), math.MaxInt32}, {math.Inf(-1), math.MinInt32},
	} {
		if got, ok := savedTickInt(tc.value); !ok || got != tc.want {
			t.Errorf("%v (%T): got %d,%v want %d", tc.value, tc.value, got, ok, tc.want)
		}
	}
	if _, ok := savedTickInt("1000"); ok {
		t.Fatal("numeric string accepted")
	}
}

func TestSavedTickLoadHandoffRetainsDuplicatesAndReplacesOldLoad(t *testing.T) {
	w := NewGen(nil, 1, func(Pos) Block { return Stone })
	t.Cleanup(w.Stop)
	record := SavedTick{Type: "minecraft:stone_button", Pos: Pos{1, 0, 1}, Delay: 100, Priority: -1}
	second := record
	second.Delay = 200
	root := t.TempDir()
	if err := w.CaptureDimensionSaveWithTicks(0, 16, nil, nil, map[[2]int32]SavedChunkTicks{{}: {Blocks: []SavedTick{record, second}}}).Write(root, "minecraft:overworld"); err != nil {
		t.Fatal(err)
	}
	if err := w.LoadDimension(root, "minecraft:overworld", 0, 16); err != nil {
		t.Fatal(err)
	}
	got := w.SavedChunkTicks()
	if !reflect.DeepEqual(got[[2]int32{}].Blocks, []SavedTick{record, second}) {
		t.Fatalf("load lost records %#v", got)
	}
	got[[2]int32{}].Blocks[0].Delay = 0
	delete(got, [2]int32{})
	if w.SavedChunkTicks()[[2]int32{}].Blocks[0].Delay != 100 {
		t.Fatal("handoff retained caller-owned map/slice")
	}
	if err := w.LoadRegion(t.TempDir(), 0, 16); err != nil {
		t.Fatal(err)
	}
	if len(w.SavedChunkTicks()) != 0 {
		t.Fatal("new load retained old queue handoff")
	}
}
