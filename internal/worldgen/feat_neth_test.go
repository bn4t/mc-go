package worldgen

import (
	"testing"
)

// TestNethManhattanOrder checks the withinManhattan walk visits every
// cell of the clipped octahedron once, shell by shell.
func TestNethManhattanOrder(t *testing.T) {
	o := BlockPos{10, 20, 30}
	for _, r := range [][3]int32{{3, 0, 5}, {2, 3, 1}, {7, 7, 7}, {0, 0, 0}} {
		seen := map[BlockPos]bool{}
		last := int32(-1)
		nethManhattan(o, r[0], r[1], r[2], func(p BlockPos) bool {
			d := manhattan(p, o)
			if d < last {
				t.Fatalf("radii %v: depth %d after %d", r, d, last)
			}
			last = d
			if seen[p] {
				t.Fatalf("radii %v: %v twice", r, p)
			}
			seen[p] = true
			return true
		})
		want := 0
		for x := -r[0]; x <= r[0]; x++ {
			for y := -r[1]; y <= r[1]; y++ {
				for z := -r[2]; z <= r[2]; z++ {
					want++
				}
			}
		}
		if len(seen) != want {
			t.Fatalf("radii %v: %d cells, want %d", r, len(seen), want)
		}
	}
}

// TestNethEndFeaturesCompile compiles every configured feature of this
// group's types.
func TestNethEndFeaturesCompile(t *testing.T) {
	types := map[string]bool{"basalt_columns": true, "basalt_pillar": true, "delta_feature": true, "glowstone_blob": true,
		"huge_fungus": true, "nether_forest_vegetation": true, "netherrack_replace_blobs": true, "twisting_vines": true,
		"weeping_vines": true, "chorus_plant": true, "end_gateway": true, "end_island": true, "end_spike": true,
		"end_platform": true, "spike": true, "block_pile": true, "bonus_chest": true, "void_start_platform": true}
	g, err := NewGenerator(Vanilla(), "nether", "nether", 1)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, k := range VanillaKeys("configured_feature/") {
		raw, _ := Vanilla().Get(k)
		var o obj
		jsonUnmarshal(raw, &o)
		if !types[trimNS(o.str("type"))] {
			continue
		}
		g.compileConfigured(raw)
		n++
	}
	for ty := range types {
		if UnsupportedFeatures[ty] {
			t.Errorf("%s unsupported", ty)
		}
	}
	if n < 25 {
		t.Errorf("only %d configured features compiled", n)
	}
}

// TestEndfSpikeRing checks the spike ring's shape: ten spikes on the
// radius-42 circle with each size index used once.
func TestEndfSpikeRing(t *testing.T) {
	ss := endfSpikesFor(12345)
	sizes := map[int32]bool{}
	guarded := 0
	for _, s := range ss {
		d := s.x*s.x + s.z*s.z
		if d < 40*40 || d > 44*44 {
			t.Errorf("spike at %d,%d off the ring", s.x, s.z)
		}
		sizes[s.height] = true
		if s.guarded {
			guarded++
		}
	}
	if len(sizes) != 10 || guarded != 2 {
		t.Errorf("sizes %v, %d guarded", sizes, guarded)
	}
}
