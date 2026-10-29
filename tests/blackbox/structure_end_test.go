//go:build blackbox

package blackbox

import "testing"

// TestStructureEndCityParity pins end cities on the outer islands: the
// 5x5 lowest-corner start height, the house tower and the recursive
// tower, bridge, fat tower and house sections with their collision
// rule and random generation depths, the ship, the templates' chests
// (treasure loot from the data markers) and the overwrite flag.
func TestStructureEndCityParity(t *testing.T) {
	structureParityDim(t, 12345, "the_end", "end", "the_end", structureOverrides(nil, "end_cities"),
		[]terrainArea{{40, -82, 47, -75}, {61, 40, 70, 47}})
}
