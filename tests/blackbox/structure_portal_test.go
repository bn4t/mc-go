//go:build blackbox

package blackbox

import "testing"

// TestStructureRuinedPortalParity pins overworld ruined portals of
// several setups in one boot: standard on the land surface and
// underground (setup weights, the air pocket draw, template / rotation
// / mirror draws, findSuitableY over the four corner noise columns),
// jungle (overgrown leaves and vines), ocean floor (lava to magma, no
// air pocket) and mountain. Each portal places only from the chunk
// holding its center, then spreads netherrack and magma and hangs drip
// columns.
func TestStructureRuinedPortalParity(t *testing.T) {
	structureParity(t, 12345, []terrainArea{
		{-2, -20, 2, -16},  // standard, on land
		{-1, -2, 3, 2},     // standard, underground
		{-76, 15, -72, 19}, // jungle
		{59, 47, 63, 51},   // ocean
		{81, 87, 85, 91},   // mountain
	}, "ruined_portals")
}
