package server

import (
	"mc-go/internal/world"
	"strings"
	"testing"
)

func TestAnvilNameValidation(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		valid             bool
	}{
		{"filtered before limit", strings.Repeat("§\x00\x1f\x7f", 51) + "Keep", "Keep", true},
		{"BMP boundary", strings.Repeat("é", 50), strings.Repeat("é", 50), true},
		{"BMP overflow", strings.Repeat("é", 51), strings.Repeat("é", 51), false},
		{"supplementary boundary", strings.Repeat("😀", 25), strings.Repeat("😀", 25), true},
		{"supplementary overflow", strings.Repeat("😀", 25) + "x", strings.Repeat("😀", 25) + "x", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, valid := anvilValidatedName(tc.input)
			if got != tc.want || valid != tc.valid {
				t.Fatalf("got (%q,%v), want (%q,%v)", got, valid, tc.want, tc.valid)
			}
		})
	}
}

func TestAnvilBlankNameAndPriorPenalty(t *testing.T) {
	id, ok := world.ItemID("iron_pickaxe")
	if !ok {
		t.Fatal("missing pickaxe")
	}
	named := ItemStack{ID: id, Count: 1, Name: "Named", RepairCost: 100}
	for _, name := range []string{"", " \u00a0\u2003\u202f\u3000"} {
		out, cost, _, _ := anvilCompute(named, ItemStack{}, name, false)
		if out.Empty() || out.Name != "" || cost != 39 || out.RepairCost != 100 {
			t.Fatalf("blank %q: out=%+v cost=%d", name, out, cost)
		}
	}
	out, cost, _, _ := anvilCompute(named, ItemStack{}, "\u0085", false)
	if out.Empty() || out.Name != "\u0085" || cost != 39 {
		t.Fatalf("NEL must remain a name: out=%+v cost=%d", out, cost)
	}
}
