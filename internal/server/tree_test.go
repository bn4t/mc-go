package server

import (
	"mc-go/internal/world"
	"testing"
)

// TestSpruceCanopyShape drives spruceCanopy against a void dimension
// and checks the invariant the measured zigzag guarantees for every
// seed: four below the attachment every row has radius >= 1, so the
// two non-corner band cells are always leaves.
func TestSpruceCanopyShape(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	base := world.Pos{X: 0, Y: -60, Z: 0}
	leaves := map[world.Pos]bool{}
	for i := 0; i < 200; i++ {
		th := 5 + s.rng.intn(3) + s.rng.intn(2) // 5..8
		placed := map[world.Pos]bool{}
		s.spruceCanopy(d, world.Pos{X: base.X, Y: base.Y + th, Z: base.Z}, th,
			func(p world.Pos) { placed[p] = true })
		for p := range placed {
			leaves[p] = true
		}
		// The guaranteed band: posY + th - 3 → attachment - 4.
		y := base.Y + th - 3
		if !placed[world.Pos{X: base.X + 1, Y: y, Z: base.Z}] ||
			!placed[world.Pos{X: base.X - 1, Y: y, Z: base.Z}] {
			t.Fatalf("th=%d: guaranteed band cell missing a leaf", th)
		}
	}
	if len(leaves) == 0 {
		t.Fatal("spruceCanopy placed nothing")
	}
}
