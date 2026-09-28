package server

import (
	"mc-go/internal/world"
	"testing"
)

// A stage-1 oak sapling eventually grows: trunk 4-6 logs at the
// origin with a leaf canopy around the top. Fancy oak (10% roll) has
// no placer yet and fails silently, so attempts loop until one lands.
func TestSaplingGrowsOak(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	origin := world.Pos{X: 0, Y: -60, Z: 0}
	sapling, ok := world.BlockStateOf("oak_sapling", map[string]string{"stage": "1"})
	if !ok {
		t.Fatal("oak_sapling state missing")
	}
	s.set(d, origin, sapling)
	var grew bool
	for i := 0; i < 100 && !grew; i++ {
		s.growTree(d, origin, d.w.GetBlock(origin))
		grew = ownerName(d, origin) == "oak_log"
	}
	if !grew {
		t.Fatal("100 growTree attempts produced no tree")
	}
	// Trunk: a contiguous oak_log column 4-6 tall from the origin.
	height := 0
	for y := 0; y < 8; y++ {
		if ownerName(d, world.Pos{X: origin.X, Y: origin.Y + y, Z: origin.Z}) == "oak_log" {
			height++
		} else {
			break
		}
	}
	if height < 4 || height > 6 {
		t.Fatalf("trunk is %d logs, want 4-6", height)
	}
	// Canopy: leaves exist next to the top of the trunk.
	top := world.Pos{X: origin.X, Y: origin.Y + height, Z: origin.Z}
	var nearLeaf bool
	for _, nb := range posNeighbors(top) {
		if ownerName(d, nb) == "oak_leaves" {
			nearLeaf = true
		}
	}
	if !nearLeaf {
		t.Fatal("no oak_leaves around the trunk top")
	}
}

// A stage-0 sapling under light cycles its stage property first and
// only grows after the second advance.
func TestSaplingStageAdvancesBeforeGrowth(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	origin := world.Pos{X: 0, Y: -60, Z: 0}
	sapling, _ := world.BlockStateOf("oak_sapling", map[string]string{"stage": "0"})
	s.set(d, origin, sapling)
	s.advanceTree(d, origin, d.w.GetBlock(origin))
	got := d.w.GetBlock(origin)
	if name := func() string { n, _ := world.StateOwner(got); return n }(); name != "oak_sapling" {
		t.Fatalf("the sapling is %s after one advance, want still a sapling", name)
	}
	if st, _ := world.StateProperty(got, "stage"); st != "1" {
		t.Fatalf("stage is %s after one advance, want 1", st)
	}
}

// The space check blocks growth: a ceiling inside the top two layers
// of the tallest tree keeps the sapling planted.
func TestSaplingBlockedByCeiling(t *testing.T) {
	s := fluidSim(t)
	d := s.dims[dimOverworld]
	origin := world.Pos{X: 0, Y: -60, Z: 0}
	sapling, _ := world.BlockStateOf("oak_sapling", map[string]string{"stage": "1"})
	s.set(d, origin, sapling)
	stone, _ := world.BlockStateOf("stone", nil)
	// A slab of stone at y+4 sits inside the top-two-layer check of a
	// 4-log tree and inside the top check of a 6-log one.
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			s.set(d, world.Pos{X: origin.X + dx, Y: origin.Y + 4, Z: origin.Z + dz}, stone)
		}
	}
	s.growTree(d, origin, d.w.GetBlock(origin))
	if n := ownerName(d, origin); n != "oak_sapling" {
		t.Fatalf("ceilinged sapling became %s, want oak_sapling", n)
	}
}

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
