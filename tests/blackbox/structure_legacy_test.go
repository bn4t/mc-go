//go:build blackbox

package blackbox

import "testing"

// Legacy (non-jigsaw) overworld land and underground structures, pinned
// with structureParity: starts piece for piece (type, box, orientation,
// gen depth, the saved compound and its load round trip), references,
// interior chunks block for block and the block entities' loot tables
// and seeds.

// TestStructureMineshaftParity pins a normal mineshaft: the room with
// its entrances and half-sphere roof, corridors (supports with fences
// and torches, cobwebs, rails, chest-minecart rails, cave spider
// spawners, log pillars and chains), crossings and stairs, the move
// below sea level, mineshaft_blocking / liquid invalid-location checks
// and the fences' post-processed connections.
func TestStructureMineshaftParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{-7, -2, -1, 7}}, "mineshafts")
}

// TestStructureMineshaftMesaParity pins a badlands mineshaft: dark oak
// wood, planks and fences, and the vertical placement between sea
// level and the surface (WORLD_SURFACE_WG at the pieces' center).
func TestStructureMineshaftMesaParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{{-171, 107, -165, 113}}, "mineshafts")
}
