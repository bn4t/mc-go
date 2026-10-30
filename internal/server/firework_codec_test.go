package server

import (
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"testing"
)

func TestNativeFireworkRocketDefaultsRestore(t *testing.T) {
	cases := []struct {
		name      string
		component map[string]any
		want      string
	}{
		{"absent", nil, ""}, {"empty", map[string]any{}, "0"}, {"flight_only", map[string]any{"flight_duration": int8(2)}, "2"}, {"explosions_only", map[string]any{"explosions": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"shape": "small_ball"}}}}, "0;small_ball|||0|0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := map[string]any{"id": "minecraft:stone", "count": int32(1)}
			if tc.component != nil {
				record["components"] = map[string]any{"minecraft:fireworks": tc.component}
			}
			got := itemFromNBT(record)
			if got.ID != world.ItemIDs["stone"] || got.Count != 1 || got.Fireworks != tc.want {
				t.Fatalf("restored%#v want fireworks%q", got, tc.want)
			}
		})
	}
}
