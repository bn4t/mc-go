package worldgen

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

// vegHashSetCases are patch-shaped position sets: flat, sloped along
// x+z (heavy Vec3i-hash collisions, tree bins) and a ceiling-like slope.
func vegHashSetCases() [][]BlockPos {
	var out [][]BlockPos
	for _, c := range []struct {
		ox, oy, oz, r int32
		y             func(dx, dz int32) int32
	}{
		{300, -20, -370, 8, func(dx, dz int32) int32 { return 0 }},
		{300, -20, -370, 8, func(dx, dz int32) int32 { return dx + dz }},
		{-1650, 70, -1410, 7, func(dx, dz int32) int32 { return (dx*3 + dz) / 2 }},
		{5021, 118, 1581, 3, func(dx, dz int32) int32 { return dz }},
	} {
		var ps []BlockPos
		for dx := -c.r; dx <= c.r; dx++ {
			for dz := -c.r; dz <= c.r; dz++ {
				if (dx*7+dz*3)%5 == 0 {
					continue
				}
				ps = append(ps, BlockPos{c.ox + dx, c.oy + c.y(dx, dz), c.oz + dz})
			}
		}
		out = append(out, ps)
	}
	return out
}

// TestJavaHashSetOrder pins javaHashSetOrder against iteration orders
// recorded from a JDK 25 java.util.HashSet fed the same positions in
// the same order (SHA-256 of the "x y z\n" listing).
func TestJavaHashSetOrder(t *testing.T) {
	want := []string{
		"4080e57dc25d06afaf8595600317fdca5781dba7372edf6df21a4783deab20f6",
		"5f68fd655af810a231f4b0a5ba4f55b42ac50d87d71391769cf858e41f5b720e",
		"3482a353a35e0fd31aec533b2b1699e396a783f05d0c7d8c6f14974c755368eb",
		"998e126933c3e19aea0e99064a332f4a59e55e0f985a461b23008eb8d8a01895",
	}
	for i, ps := range vegHashSetCases() {
		h := sha256.New()
		for _, p := range javaHashSetOrder(ps) {
			fmt.Fprintf(h, "%d %d %d\n", p[0], p[1], p[2])
		}
		if got := fmt.Sprintf("%x", h.Sum(nil)); got != want[i] {
			t.Errorf("case %d (%d positions): order hash %s, want %s", i, len(ps), got, want[i])
		}
	}
}
