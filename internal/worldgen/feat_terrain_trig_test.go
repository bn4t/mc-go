package worldgen

import (
	"math"
	"testing"
)

// TestJavaTrig pins javaSin/javaCos against java.lang.Math.sin/cos on
// JDK 25 (raw long bits printed by a scratch Java program), at
// arguments where Go's math.Sin or math.Cos round differently.
func TestJavaTrig(t *testing.T) {
	cases := [][3]int64{
		{0x400c1594e150db7b, -0x4028eb6879448305, -0x40122741061ff4a7},
		{0x401121eea7b62144, -0x4012e7334770fc1a, -0x40255d2144f33413},
		{0x400ead331a3de76b, -0x401b8eb2dff9d79b, -0x40176181b6fa2677},
		{0x4019f8567ae80fe2, 0x3fca99683bbfe28f, 0x3fef4d2b0613dd75},
		{0x3fc0b94d87cc34a0, 0x3fc0ad21e6499dd9, 0x3fefba2d7e7c9da5},
		{0x4015406ba70c1dc0, -0x40159943be92e650, 0x3fe21513e2bc977a},
		{0x4015a8c8821574fa, -0x4017934cc5a86637, 0x3fe4acb18e8dd6ea},
		{0x401532ae7b8fe06c, -0x40155bc44ac2e543, 0x3fe1b9fb72c81c21},
		{0x4005279a5cca7aeb, 0x3fde8769eda149b3, -0x4013e017461c20d8},
		{0x3fffd67abb30f965, 0x3fed3b23771e2ad8, -0x4025f537572cbcb0},
		{0x40186768b70bb526, -0x4038cea6ad1c5bcd, 0x3fef7866d0427cc0},
		{0x3fda37e34705f4f2, 0x3fd97db90aa7c9a3, 0x3fed5a28f109e2fe},
	}
	for _, c := range cases {
		x := math.Float64frombits(uint64(c[0]))
		if s := math.Float64bits(javaSin(x)); s != uint64(c[1]) {
			t.Errorf("sin(%v) = %v, java %v", x, javaSin(x), math.Float64frombits(uint64(c[1])))
		}
		if s := math.Float64bits(javaCos(x)); s != uint64(c[2]) {
			t.Errorf("cos(%v) = %v, java %v", x, javaCos(x), math.Float64frombits(uint64(c[2])))
		}
	}
}
