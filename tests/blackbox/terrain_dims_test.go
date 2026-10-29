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

// presetTerrainParity is dimTerrainParity for an overworld world preset
// (level-type) with its own noise settings.
func presetTerrainParity(t *testing.T, levelType, settings string, areas []terrainArea) {
	t.Helper()
	defer func(v string) { terrainLevelType = v }(terrainLevelType)
	terrainLevelType = levelType
	ov := worldgen.StageOverrides(worldgen.Vanilla(), func(string) bool { return false }, true, true)
	region := vanillaDimTerrain(t, 12345, stageDatapack(ov), "overworld", areas)
	g, err := worldgen.NewGenerator(worldgen.Overlay(worldgen.Vanilla(), ov), settings, "overworld", 12345)
	if err != nil {
		t.Fatal(err)
	}
	compareTerrain(t, region, g, areas)
	for _, a := range areas {
		for cx := a.x0; cx <= a.x1; cx++ {
			for cz := a.z0; cz <= a.z1; cz++ {
				want := savedBiomes(readSavedChunk(t, region, cx, cz, -64, 384).raw, -64, 384)
				got := g.ChunkBiomes(cx, cz, nil)
				for i := range want {
					if want[i] != got[i] {
						t.Errorf("chunk %d,%d biome cell %d: vanilla %s mc-go %s", cx, cz, i, want[i], got[i])
						break
					}
				}
			}
		}
	}
}

// TestTerrainAmplifiedParity pins the amplified preset (noise settings
// minecraft:amplified) block for block, features stripped.
func TestTerrainAmplifiedParity(t *testing.T) {
	presetTerrainParity(t, "minecraft:amplified", "amplified", []terrainArea{{0, 0, 2, 2}, {-40, 30, -38, 32}})
}

// TestTerrainLargeBiomesParity pins the large_biomes preset (its own
// noise router over the overworld_large_biomes density functions and
// the large climate noises).
func TestTerrainLargeBiomesParity(t *testing.T) {
	presetTerrainParity(t, "minecraft:large_biomes", "large_biomes", []terrainArea{{0, 0, 2, 2}, {60, -70, 62, -68}})
}
