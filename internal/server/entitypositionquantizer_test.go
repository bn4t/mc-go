package server

import (
	"fmt"
	"math"
	"testing"
)

// Public VecDeltaCodec API corpus; source and reproducible bit-exact inputs
// are in tools/probeentityposition. Signed half ties differ from math.Round.
func TestMobPositionQuantizerFacts(t *testing.T) {
	cases := []struct {
		bits uint64
		want int64
	}{
		{0x0, 0},
		{0x3f20000000000000, 1},
		{0xbf20000000000000, 0},
		{0x3f1fffffffffffff, 0},
		{0x3f20000000000001, 1},
		{0xbf20000000000001, -1},
		{0xbf1fffffffffffff, 0},
		{0x4064400000000000, 663552},
		{0x4064199999800000, 658637},
		{0x4064266666600000, 660275},
		{0x406440000218def4, 663552},
		{0x7ff8000000000000, 0},
		{0x7ff0000000000000, 9223372036854775807},
		{0xfff0000000000000, -9223372036854775808},
		{0x4320000000000000, 9223372036854775807},
		{0xc320000000000000, -9223372036854775808},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%016x", tc.bits), func(t *testing.T) {
			if got := mobFixed(math.Float64frombits(tc.bits)); got != tc.want {
				t.Fatalf("got%d want%d", got, tc.want)
			}
		})
	}
	if got := mobFixed(160.79999995231628) - mobFixed(162); got != -4915 {
		t.Fatalf("ceiling delta%d want-4915", got)
	}
}
