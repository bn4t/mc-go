package server

import "testing"

func TestAttributeModifierPasses(t *testing.T) {
	cases := []struct {
		name string
		base float64
		mods map[string]attrMod
		want float64
	}{
		{"plain", 10, nil, 10},
		{"addition", 10, map[string]attrMod{"add": {value: 2}}, 12},
		{"post-add base", 10, map[string]attrMod{"add": {value: 2}, "base": {value: .5, op: 1}}, 18},
		{"negative addition", 10, map[string]attrMod{"add": {value: -2}, "base": {value: .5, op: 1}}, 12},
		{"total after base", 10, map[string]attrMod{"add": {value: 2}, "base": {value: .5, op: 1}, "total": {value: 1, op: 2}}, 36},
		{"shared base for multiple modifiers", 10, map[string]attrMod{"add": {value: 2}, "first": {value: .25, op: 1}, "second": {value: .25, op: 1}, "total": {value: .5, op: 2}, "total2": {value: 1, op: 2}}, 54},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := attrValue(tc.base, tc.mods); got != tc.want {
				t.Fatalf("value %v, want %v", got, tc.want)
			}
		})
	}
}
