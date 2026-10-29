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
