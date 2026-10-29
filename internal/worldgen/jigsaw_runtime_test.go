package worldgen

import (
	"encoding/json"
	"mc-go/internal/nbt"
	"mc-go/internal/world"
	"testing"
)

func TestRuntimeJigsawNativeIsolation(t *testing.T) {
	list := func(v ...any) nbt.List { return nbt.List{Elem: nbt.TagInt, Items: v} }
	native := func(final string) map[string]any {
		return map[string]any{"size": list(int32(1), int32(1), int32(1)), "palette": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"Name": "minecraft:jigsaw", "Properties": map[string]any{"orientation": "up_north"}}}}, "blocks": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"pos": list(int32(0), int32(0), int32(0)), "state": int32(0), "nbt": map[string]any{"name": "test:anchor", "pool": "minecraft:empty", "target": "minecraft:empty", "joint": "rollable", "final_state": final}}}}, "entities": nbt.List{Elem: nbt.TagEnd}}
	}
	pool := json.RawMessage(`{"fallback":"minecraft:empty","elements":[{"weight":1,"element":{"element_type":"minecraft:single_pool_element","location":"test:root","projection":"rigid","processors":"minecraft:empty"}}]}`)
	data := Overlay(Vanilla(), map[string]json.RawMessage{"template_pool/test:root": pool})
	for _, final := range []string{"minecraft:gold_block", "minecraft:diamond_block"} {
		source := native(final)
		lookup := func(string) map[string]any { return source }
		for _, keep := range []bool{false, true} {
			pieces, err := PlanRuntimeJigsaw(data, 1, BlockPos{1, 70, 0}, "test:root", "test:anchor", 1, -64, 384, lookup, nil)
			if err != nil || len(pieces) != 1 {
				t.Fatalf("pieces=%v err=%v", pieces, err)
			}
			if pieces[0].Position != (BlockPos{1, 69, 0}) {
				t.Fatal("root position", pieces[0].Position)
			}
			calls := 0
			VisitRuntimeJigsawTemplates(pieces[0], BlockPos{1, 70, 0}, keep, lookup, func(int32, int32, int32) world.Block { return world.Air }, func(int, int32, int32) int32 { return -60 }, func(p RuntimeJigsawTemplate) {
				calls++
				palette := p.Native["palette"].(nbt.List)
				got := palette.Items[0].(map[string]any)["Name"]
				want := final
				if keep {
					want = "minecraft:jigsaw"
				}
				if got != want {
					t.Fatalf("keep%v got%v want%s", keep, got, want)
				}
			})
			if calls != 1 {
				t.Fatal("template callbacks", calls)
			}
		}
		tag := source["blocks"].(nbt.List).Items[0].(map[string]any)["nbt"].(map[string]any)
		if tag["final_state"] != final {
			t.Fatal("mutated repository template")
		}
	}
	if pieces, err := PlanRuntimeJigsaw(data, 1, BlockPos{1, 70, 0}, "test:root", "test:anchor", 0, -64, 384, nil, nil); err != nil || len(pieces) != 0 {
		t.Fatal("zero-depth generation", pieces, err)
	}
}
