package worldgen

import "testing"

// Placement values measured on the 26.2 jar (bytecode-measured
// ChunkGeneratorStructureState driven through a probe:
// StructurePlacement.isStructureChunk over chunks [-200, 200]^2 and
// generateRingPositions for the stronghold rings), seed 12345.
// Pillager outposts exercise legacy_type_1 frequency reduction and the
// village exclusion zone; ring 9 needs the lastResult tie-break of the
// biome search.
func TestStructurePlacementVanilla(t *testing.T) {
	g, err := NewGenerator(Vanilla(), "overworld", "overworld", 12345)
	if err != nil {
		t.Fatal(err)
	}
	st := g.structureState()
	outposts := [][2]int32{{-170, -138}, {-169, -50}, {-157, -174}, {-119, 68}, {-108, 162}, {-96, 167}, {-79, 132},
		{-73, -124}, {-59, 100}, {-57, 141}, {-10, 181}, {5, 2}, {9, -186}, {17, -60}, {53, 134}, {67, -41}, {74, -108},
		{108, 112}, {180, 116}}
	want := map[[2]int32]bool{}
	for _, c := range outposts {
		want[c] = true
	}
	p := st.setByName["pillager_outposts"].placement
	for x := int32(-200); x <= 200; x++ {
		for z := int32(-200); z <= 200; z++ {
			if got := g.isStructureChunk(p, x, z); got != want[[2]int32{x, z}] {
				t.Errorf("pillager_outposts %d,%d: %v", x, z, got)
			}
		}
	}
	counts := map[string]int{"villages": 140, "mineshafts": 638, "ruined_portals": 102, "trail_ruins": 138,
		"trial_chambers": 140, "ancient_cities": 271, "woodland_mansions": 25, "nether_fossils": 40401}
	for name, n := range counts {
		p := st.setByName[name].placement
		got := 0
		for x := int32(-200); x <= 200; x++ {
			for z := int32(-200); z <= 200; z++ {
				if g.isStructureChunk(p, x, z) {
					got++
				}
			}
		}
		if got != n {
			t.Errorf("%s: %d structure chunks, want %d", name, got, n)
		}
	}
	rings := [][2]int32{{-105, 124}, {-35, -103}, {110, 23}, {293, -3}, {182, 310}, {-165, 297}, {-307, 0}, {-179, -308},
		{138, -244}, {444, 332}, {151, 469}, {-155, 457}, {-442, 310}, {-486, -12}, {-383, -285}, {-147, -501}}
	got := g.RingPositions("strongholds")
	if len(got) != 128 {
		t.Fatalf("%d ring positions", len(got))
	}
	for i, c := range rings {
		if got[i] != c {
			t.Errorf("ring %d: %v, want %v", i, got[i], c)
		}
	}
}
