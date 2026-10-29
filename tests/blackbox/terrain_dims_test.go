//go:build blackbox

package blackbox

import (
	"testing"

	"mc-go/internal/worldgen"
)

// dimTerrainParity has the real server generate areas of one dimension
// with every feature stripped (noise, biomes, surface and carvers on)
// and compares them with mc-go block for block and biome cell for cell.
func dimTerrainParity(t *testing.T, seed int64, dim, settings, biomes string, areas []terrainArea) {
	t.Helper()
	ov := worldgen.StageOverrides(worldgen.Vanilla(), func(string) bool { return false }, true, true)
	region := vanillaDimTerrain(t, seed, stageDatapack(ov), dim, areas)
	g, err := worldgen.NewGenerator(worldgen.Overlay(worldgen.Vanilla(), ov), settings, biomes, seed)
	if err != nil {
		t.Fatal(err)
	}
	// Both dimension types span y 0..255; the noise fills the lower 128.
	g.RS.SetLevelBounds(0, 256)
	compareTerrain(t, region, g, areas)
	cells := 0
	for _, a := range areas {
		for cx := a.x0; cx <= a.x1; cx++ {
			for cz := a.z0; cz <= a.z1; cz++ {
				want := savedBiomes(readSavedChunk(t, region, cx, cz, 0, 256).raw, 0, 256)
				got := g.ChunkBiomes(cx, cz, nil)
				for i := range want {
					if want[i] != got[i] {
						t.Errorf("chunk %d,%d biome cell %d: vanilla %s mc-go %s", cx, cz, i, want[i], got[i])
						break
					}
					cells++
				}
			}
		}
	}
	t.Logf("%d biome cells compared", cells)
}

// TestTerrainNetherParity pins nether terrain: the nether noise router
// (no aquifers or veins, lava sea at 32), the nether multi-noise biome
// list, the nether surface rule (bedrock roof and floor, soul sand
// valleys, basalt deltas, crimson/warped nylium) and the nether cave
// carver, over areas spread across the biome map.
func TestTerrainNetherParity(t *testing.T) {
	dimTerrainParity(t, 12345, "the_nether", "nether", "nether",
		[]terrainArea{{-2, -2, 1, 1}, {40, -30, 42, -28}, {-60, 50, -58, 52}, {90, 90, 92, 92}})
}

// TestTerrainEndParity pins end terrain: the end-islands density, the
// end biome source (the_end inside 64 chunks, then highlands /
// midlands / barrens / small islands from the erosion at each
// section's centre) and the end's surface. Areas avoid the origin,
// where the dragon fight places the exit portal.
func TestTerrainEndParity(t *testing.T) {
	dimTerrainParity(t, 12345, "the_end", "end", "the_end",
		[]terrainArea{{4, 4, 6, 6}, {70, 70, 73, 73}, {-90, 40, -87, 43}, {120, -150, 123, -147}})
}
