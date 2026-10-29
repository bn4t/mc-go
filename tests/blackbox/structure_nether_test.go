//go:build blackbox

package blackbox

import "testing"

// Legacy (non-jigsaw) nether structures, pinned with structureParityDim
// in the_nether.

// TestStructureNetherFortressParity pins a nether fortress with the
// full nether_complexes set (fortress weight 2 against the bastion's
// 3): the start crossing, the bridge and castle piece weights with
// their place limits and no-repeat rule, end fillers, the 48..70
// height move, every piece type's layout, blaze spawners, corridor
// chests, nether wart, and the fences' neighbour shapes after
// post-processing.
func TestStructureNetherFortressParity(t *testing.T) {
	structureParityDim(t, 12345, "the_nether", "nether", "nether", structureOverrides(nil, "nether_complexes"),
		[]terrainArea{{3, -35, 10, -28}, {-14, -40, -8, -34}})
}

// TestStructureNetherFossilPortalParity pins nether fossils (the base
// column height search from the height provider, template and
// rotation, every touched chunk placing the whole fossil, the dried
// ghast from a seed-positioned random, beard_thin terrain around
// them) in a soul sand valley, and a nether ruined portal (in_nether
// placement heights, blackstone replacement, lava kept).
func TestStructureNetherFossilPortalParity(t *testing.T) {
	structureParityDim(t, 12345, "the_nether", "nether", "nether", structureOverrides(nil, "nether_fossils", "ruined_portals"),
		[]terrainArea{{-61, 27, -55, 35}, {-1, -2, 3, 2}})
}
