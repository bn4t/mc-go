//go:build blackbox

package blackbox

import "testing"

// Legacy (non-jigsaw) ocean structures, pinned with structureParity:
// starts piece for piece (saved compounds after decoration, which
// moves template boxes and shipwreck heights, and their load round
// trip), references, blocks and block entity loot tables and seeds.

// TestStructureBuriedTreasureParity pins two buried treasures in
// neighbouring beach chunks: the legacy_type_2 frequency reduction on a
// spacing-1 placement, the ocean-floor biome check, the downward search
// for a stone-family floor, the sand / floor-block shell and the chest
// with its loot seed, and the piece box collapsing onto the chest.
func TestStructureBuriedTreasureParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{115, -32, 119, -28}}, "buried_treasures")
}

// TestStructureShipwreckParity pins an ocean shipwreck: the template
// and rotation draws, the ocean-floor height averaged over the
// unrotated footprint on first placement, the template placed with
// structure blocks and air ignored, waterlogging and shape updates,
// and the data-marker chests' loot tables.
func TestStructureShipwreckParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{10, 8, 13, 13}}, "shipwrecks")
}

// TestStructureShipwreckBeachedParity pins a beached shipwreck: the
// surface heightmap's lowest footprint cell, sunk by half the template
// height and a 0..2 draw from the placement random. The wreck spans
// two chunks, so mc-go's forward and reverse decoration orders cover
// both orders the data markers can re-seed its chests in.
func TestStructureShipwreckBeachedParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{113, -134, 115, -131}, {-145, 90, -142, 93}}, "shipwrecks")
}

// TestStructureOceanRuinColdParity pins a large cold ocean ruin with
// its cluster: the large / cluster draws, the shuffled cluster
// candidates and their overlap test, the stacked brick / cracked /
// mossy templates at their integrities, the per-placement floor height
// search, the capped suspicious gravel rule and the chest markers.
func TestStructureOceanRuinColdParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{1, 24, 5, 28}}, "ocean_ruins")
}

// TestStructureOceanRuinWarmParity pins a small warm ocean ruin (one
// sandstone template, suspicious sand).
func TestStructureOceanRuinWarmParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{-120, 106, -116, 111}}, "ocean_ruins")
}

// TestStructureOceanMonumentParity pins an ocean monument: the
// surrounding-biome check, the single saved building piece (its rooms
// regenerate from the chunk seed on load), the room graph with its
// random core and closed openings, the room fitters and every room's
// layout, the wings and penthouse, the water fill and the pillars.
func TestStructureOceanMonumentParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{35, -118, 40, -113}}, "ocean_monuments")
}
