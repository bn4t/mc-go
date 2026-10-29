package server

import (
	"mc-go/internal/nbt"
	"reflect"
	"testing"
)

func TestCrystalSummonTagsSet(t *testing.T) {
	e := newEndCrystal(dimOverworld, .5, 165, .5)
	crystalSummonTags(e, map[string]any{"Tags": nbt.List{Elem: nbt.TagString, Items: []any{"first", "second", "first"}}})
	if got := mobTagList(e); !reflect.DeepEqual(got, []string{"first", "second"}) {
		t.Fatalf("tags%v", got)
	}
	if !removeEntityTag(&dataEntity{mob: e}, "first") {
		t.Fatal("remove tag failed")
	}
	if got := mobTagList(e); !reflect.DeepEqual(got, []string{"second"}) {
		t.Fatalf("remaining%v", got)
	}
}
