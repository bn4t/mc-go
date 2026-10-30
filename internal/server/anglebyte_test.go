package server

import (
	"math"
	"testing"
)

// Golden bits and bytes come from the native public packDegrees API, including
// adjacent float32 inputs and saturation after intermediate overflow.
func TestAngleByteNativeGolden(t *testing.T) {
	for _, row := range []struct {
		bits uint32
		want byte
	}{
		{0xc4340000, 0}, {0xc3b40000, 0}, {0xc3340000, 128},
		{0xbfb40000, 255}, {0xbfb40001, 254}, {0xbfb3ffff, 255},
		{0xbdcccccd, 255}, {0x80000000, 0}, {0x00000000, 0},
		{0x3dcccccd, 0}, {0x3fb3ffff, 0}, {0x3fb40000, 1}, {0x3fb40001, 1},
		{0x43340000, 128}, {0x43b40000, 0}, {0x44340000, 0},
		{0x7f7fffff, 255}, {0xff7fffff, 0}, {0x7fc00000, 0},
		{0x7f800000, 255}, {0xff800000, 0},
	} {
		if got := angleByte(math.Float32frombits(row.bits)); got != row.want {
			t.Errorf("bits%08x got%d want%d", row.bits, got, row.want)
		}
	}
}
