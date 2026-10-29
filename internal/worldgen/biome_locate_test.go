package worldgen

import (
	"fmt"
	"testing"
)

// TestOutFromOrigin pins Mth.outFromOrigin's order: the clamped origin,
// then alternately above and below while either side fits.
func TestOutFromOrigin(t *testing.T) {
	for _, c := range []struct {
		origin, lo, hi, step int32
		want                 string
	}{
		{70, -63, 321, 64, "[70 134 6 198 -58 262]"},
		{-200, -63, 321, 64, "[-63 1 65 129 193 257 321]"},
		{300, -63, 321, 64, "[300 236 172 108 44 -20]"},
	} {
		if got := fmt.Sprint(outFromOrigin(c.origin, c.lo, c.hi, c.step)); got != c.want {
			t.Errorf("outFromOrigin(%d, %d, %d, %d) = %s, want %s", c.origin, c.lo, c.hi, c.step, got, c.want)
		}
	}
}
