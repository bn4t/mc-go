package server

import "testing"

func TestPlayerFallDamageAmount(t *testing.T) {
	for _, tc := range []struct{ distance, want float64 }{
		{3, 0}, {3.5, 0}, {9.5, 6}, {4, 1},
		{4 - 0.5e-6, 1}, {4 - 2e-6, 0},
	} {
		if got := playerFallDamageAmount(tc.distance); got != tc.want {
			t.Errorf("fall distance %.9f: got %v want %v", tc.distance, got, tc.want)
		}
	}
}
