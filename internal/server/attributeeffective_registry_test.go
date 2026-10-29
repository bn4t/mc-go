package server

import (
	"math"
	"testing"
)

func TestRegistryAttributeEffectiveRanges(t *testing.T) {
	for _, tc := range []struct {
		name      string
		low, high float64
	}{
		{"armor", 0, 30}, {"gravity", -1, 1}, {"jump_strength", 0, 32},
		{"knockback_resistance", -2, 1}, {"max_health", 1, 1024}, {"waypoint_receive_range", 0, 6e7},
	} {
		for _, c := range []struct{ value, want float64 }{{-4096, tc.low}, {1e9, tc.high}, {math.Inf(-1), tc.low}, {math.Inf(1), tc.high}, {math.NaN(), tc.low}} {
			if got := sanitizeAttributeValue(tc.name, c.value); got != c.want {
				t.Errorf("%s sanitize %v=%v want%v", tc.name, c.value, got, c.want)
			}
		}
	}
}
