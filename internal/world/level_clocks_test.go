package world

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestLevelClockStatesOptIn(t *testing.T) {
	explicit := map[string]any{"minecraft:overworld": map[string]any{"total_ticks": int64(12), "partial_tick": float32(.25), "rate": float32(.5)}, "minecraft:the_end": map[string]any{"total_ticks": int64(42), "paused": int8(1)}}
	for _, clocks := range []map[string]any{nil, {}, explicit} {
		d := LevelData{Dir: t.TempDir(), GameTime: 88, DayTime: 99, ClockStates: clocks}
		if err := WriteLevel(d); err != nil {
			t.Fatal(err)
		}
		root, err := readGzipNBT(filepath.Join(d.Dir, "data", "minecraft", "world_clocks.dat"))
		if err != nil {
			t.Fatal(err)
		}
		got, ok := savedDataPayload(root)
		if !ok {
			t.Fatal("missing payload")
		}
		want := clocks
		if clocks == nil {
			want = map[string]any{"minecraft:overworld": map[string]any{"total_ticks": int64(99)}, "minecraft:the_end": map[string]any{"total_ticks": int64(88)}}
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("clock projection %#v want%#v", got, want)
		}
	}
}
