package server

import (
	"math"
	"testing"
)

// Runtime26.2 bounds of a north-connected shape. One connected arm
// overlaps its center node, so their union is the measured rectangle.
func TestConnectedCollisionWidths(t *testing.T) {
	for _, tc := range []struct {
		name  string
		props map[string]string
		want  cellBox
	}{
		{"oak_fence", map[string]string{"north": "true"}, cellBox{.375, 0, 0, .625, 1.5, .625}},
		{"nether_brick_fence", map[string]string{"north": "true"}, cellBox{.375, 0, 0, .625, 1.5, .625}},
		{"cobblestone_wall", map[string]string{"north": "low", "up": "false"}, cellBox{.3125, 0, 0, .6875, 1.5, .6875}},
		{"glass_pane", map[string]string{"north": "true"}, cellBox{.4375, 0, 0, .5625, 1, .5625}},
		{"iron_bars", map[string]string{"north": "true"}, cellBox{.4375, 0, 0, .5625, 1, .5625}},
	} {
		boxes, _ := collisionBoxesOf(mkBlockState(t, tc.name, tc.props))
		bounds := cellBox{1, 1, 1, 0, 0, 0}
		for _, box := range boxes {
			for i := 0; i < 3; i++ {
				bounds[i] = math.Min(bounds[i], box[i])
				bounds[i+3] = math.Max(bounds[i+3], box[i+3])
			}
		}
		if len(boxes) == 0 || bounds != tc.want {
			t.Errorf("%s bounds%v want%v", tc.name, bounds, tc.want)
		}
	}
	for _, name := range []string{"copper_bars", "exposed_copper_bars", "weathered_copper_bars", "oxidized_copper_bars", "waxed_copper_bars", "waxed_exposed_copper_bars", "waxed_weathered_copper_bars", "waxed_oxidized_copper_bars"} {
		boxes, ok := collisionBoxesOf(mkBlockState(t, name, map[string]string{"north": "true"}))
		if !ok || len(boxes) != 2 || boxes[1] != (cellBox{.4375, 0, 0, .5625, 1, .5}) {
			t.Errorf("%s missing thin bar arm: %v", name, boxes)
		}
	}
}
