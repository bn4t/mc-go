package server

import (
	"testing"

	"mc-go/internal/world"
)

func TestDripstonePositionedShapes(t *testing.T) {
	for _, tc := range []struct {
		thickness, direction string
		want                 cellBox
	}{
		{"tip", "up", cellBox{.1875, 0, .1875, .5625, .6875, .5625}},
		{"tip", "down", cellBox{.1875, .3125, .1875, .5625, 1, .5625}},
		{"tip_merge", "up", cellBox{.1875, 0, .1875, .5625, 1, .5625}},
		{"frustum", "up", cellBox{.125, 0, .125, .625, 1, .625}},
		{"middle", "up", cellBox{.0625, 0, .0625, .6875, 1, .6875}},
		{"base", "up", cellBox{0, 0, 0, .75, 1, .75}},
	} {
		block := mkBlockState(t, "pointed_dripstone", map[string]string{"thickness": tc.thickness, "vertical_direction": tc.direction})
		got := cellShapeAt(block, world.Pos{})
		if len(got) != 1 || got[0] != tc.want {
			t.Errorf("%s %s got%v want%v", tc.thickness, tc.direction, got, tc.want)
		}
	}
	for _, tc := range []struct {
		x, z   int
		dx, dz float64
	}{
		{0, 0, -.125, -.125}, {1, 0, .11666667461395264, -.01666666567325592},
		{-1, 0, -.125, -.125}, {17, -9, .11666667461395264, .016666680574417114},
	} {
		dx, dz := dripstoneOffset(tc.x, tc.z)
		if dx != tc.dx || dz != tc.dz {
			t.Errorf("offset%d,%d got%v,%v want%v,%v", tc.x, tc.z, dx, dz, tc.dx, tc.dz)
		}
	}
}
