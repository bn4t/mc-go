package server

import (
	"math"
	"testing"

	"mc-go/internal/world"
)

func TestCollisionFactsCoverRegistry(t *testing.T) {
	if len(vanillaStateShapes) != world.StateCount() {
		t.Fatalf("shape rows%d registry states%d", len(vanillaStateShapes), world.StateCount())
	}
	for id := 0; id < world.StateCount(); id++ {
		row := stateShapeRow(world.Block(id))
		for _, index := range []uint16{row.outline, row.interact, row.collision} {
			if int(index) >= len(vanillaShapes) {
				t.Fatalf("state%d invalid shape index%d", id, index)
			}
		}
		if row.flags & ^uint8(15) != 0 {
			t.Fatalf("state%d changed existing flag namespace: %d", id, row.flags)
		}
		for _, box := range vanillaShapes[row.collision] {
			for axis := 0; axis < 3; axis++ {
				if math.IsNaN(box[axis]) || math.IsNaN(box[axis+3]) || math.IsInf(box[axis], 0) || math.IsInf(box[axis+3], 0) || box[axis] >= box[axis+3] {
					t.Fatalf("state%d invalid collision box%v", id, box)
				}
			}
		}
	}
}

func TestMeasuredCollisionContextAndGeometry(t *testing.T) {
	inside := func(boxes []cellBox, p [3]float64) bool {
		for _, b := range boxes {
			if p[0] > b[0] && p[0] < b[3] && p[1] > b[1] && p[1] < b[4] && p[2] > b[2] && p[2] < b[5] {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		name  string
		props map[string]string
		point [3]float64
		want  bool
	}{
		{"air", nil, [3]float64{.5, .5, .5}, false},
		{"stone", nil, [3]float64{.5, .5, .5}, true},
		{"water", nil, [3]float64{.5, .5, .5}, false},
		{"oak_trapdoor", map[string]string{"open": "true", "facing": "south"}, [3]float64{.5, .95, .05}, true},
		{"oak_trapdoor", map[string]string{"open": "true", "facing": "south"}, [3]float64{.5, .95, .95}, false},
		{"decorated_pot", nil, [3]float64{.5, .95, .5}, true},
		{"decorated_pot", nil, [3]float64{.01, .5, .5}, false},
		{"cactus", nil, [3]float64{.5, .96, .5}, false},
		{"cactus", nil, [3]float64{.5, .93, .5}, true},
		{"copper_chest", nil, [3]float64{.01, .5, .5}, false},
		{"copper_chest", nil, [3]float64{.5, .5, .5}, true},
		{"iron_chain", map[string]string{"axis": "y"}, [3]float64{.1, .5, .5}, false},
		{"iron_chain", map[string]string{"axis": "y"}, [3]float64{.5, .5, .5}, true},
		{"scaffolding", map[string]string{"bottom": "true"}, [3]float64{.5, .05, .5}, false},
		{"scaffolding", map[string]string{"bottom": "true"}, [3]float64{.5, .95, .5}, true},
		{"scaffolding", map[string]string{"bottom": "true"}, [3]float64{.05, .05, .05}, true},
	} {
		state := mkBlockState(t, tc.name, tc.props)
		if got := inside(measuredCollisionShapeAt(state, world.Pos{}), tc.point); got != tc.want {
			t.Errorf("%s %v at%v collision=%t want%t", tc.name, tc.props, tc.point, got, tc.want)
		}
	}
	for _, tc := range []struct {
		name   string
		motion bool
	}{{"stone", true}, {"water", false}, {"short_dry_grass", false}} {
		if got := stateShapeRow(mkBlockState(t, tc.name, nil)).blocksMotion; got != tc.motion {
			t.Errorf("%s blocksMotion=%t want%t", tc.name, got, tc.motion)
		}
	}
}

func TestMeasuredCollisionRestoresPositionOffset(t *testing.T) {
	state := mkBlockState(t, "pointed_dripstone", map[string]string{"thickness": "tip", "vertical_direction": "up"})
	for _, tc := range []struct {
		x, z   int
		dx, dz float64
	}{
		{0, 0, -.125, -.125},
		{1, 0, .11666667461395264, -.01666666567325592},
		{17, -9, .11666667461395264, .016666680574417114},
	} {
		boxes := measuredCollisionShapeAt(state, world.Pos{X: tc.x, Y: 150, Z: tc.z})
		want := cellBox{.3125 + tc.dx, 0, .3125 + tc.dz, .6875 + tc.dx, .6875, .6875 + tc.dz}
		if len(boxes) != 1 || boxes[0] != want {
			t.Errorf("position%d,%d got%v want%v", tc.x, tc.z, boxes, want)
		}
	}
}
