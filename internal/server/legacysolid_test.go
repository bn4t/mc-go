package server

import "testing"

func TestLegacySolidProperties(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"stone", true}, {"glass", true}, {"end_rod", false}, {"scaffolding", false},
		{"powder_snow", false}, {"cobweb", true}, {"bamboo_sapling", true},
		{"bamboo", true}, {"pointed_dripstone", true}, {"shulker_box", true},
	} {
		st := mkBlockState(t, tc.name, nil)
		if got := legacySolid(st); got != tc.want {
			t.Errorf("%s solid%v want%v", tc.name, got, tc.want)
		}
	}
}
