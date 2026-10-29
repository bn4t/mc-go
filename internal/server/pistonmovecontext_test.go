package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

// Expected clips were queried through the real public Shapes.collide API
// on the extended piston base, including positions inside its outer box.
func TestPistonBaseOccupiedGridContacts(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	pos := world.Pos{Y: 160}
	mp := movingPiston{dim: d.name, pos: pos, state: mkBlockState(t, "piston", map[string]string{"facing": "east"}), dir: rsWest, source: true, ticks: 3}
	d.w.SetBlock(pos, mkBlockState(t, "moving_piston", map[string]string{"facing": "east"}))
	s.movingPistons = []movingPiston{mp}
	for _, tc := range []struct {
		width float64
		want  [5]float64
	}{{.5, [5]float64{-.51, 0, -.05, -.4, -.51}}, {float64(float32(.9)), [5]float64{-.51, -.51, -.100000011920929, -.20000001192092887, -.51}}} {
		for i, x := range []float64{.2, .5, .8, 1.4, 1.8} {
			half := tc.width / 2
			body := cellBox{x - half, 160, .5 - half, x + half, 161.975, .5 + half}
			got := s.clipPistonBodyMove(d, body, 0, -.51, rsWest)
			if math.Abs(got-tc.want[i]) > 1e-12 {
				t.Errorf("width%v x%v clip%v want%v", tc.width, x, got, tc.want[i])
			}
		}
	}
	// A distinct stationary rear wall still limits a piston-induced move.
	d.w.SetBlock(world.Pos{X: -1, Y: 160}, world.Stone)
	body := cellBox{.05, 160, .05, .95, 161.4, .95}
	if got := s.clipPistonBodyMove(d, body, 0, -.51, rsWest); math.Abs(got+.05) > 1e-12 {
		t.Errorf("rear wall clip%v want-.05", got)
	}
	// A different motion direction must retain the carried shape.
	same := s.pistonMoveShapeAt(d, d.w.GetBlock(pos), pos, rsWest)
	other := s.pistonMoveShapeAt(d, d.w.GetBlock(pos), pos, rsEast)
	if len(same) == 0 || len(other) == 0 {
		t.Fatal("missing base or carried collision")
	}
	maxX := 0.0
	for _, b := range other {
		maxX = math.Max(maxX, b[3])
	}
	if maxX <= 1 {
		t.Fatal("opposite move lost carried head")
	}
}

func TestPistonSourceBaseClippedBodies(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	pos := world.Pos{Y: 160}
	mp := movingPiston{dim: d.name, pos: pos, state: mkBlockState(t, "piston", map[string]string{"facing": "east"}), dir: rsWest, source: true, ticks: 3}
	d.w.SetBlock(pos, mkBlockState(t, "moving_piston", map[string]string{"facing": "east"}))
	for _, tc := range []struct {
		kind string
		want [5][2]float64
	}{
		{"armor_stand", [5][2]float64{{-.01, -.26}, {1.01, 1.27}, {1.26, 1.27}, {1.27, 1.27}, {1.29, 1.27}}},
		{"cow", [5][2]float64{{-.209999988, -.459999988}, {-.01, -.459999988}, {1.209999988, 1.469999988}, {1.469999988, 1.469999988}, {1.469999988, 1.469999988}}},
	} {
		for i, x := range []float64{.2, .5, .8, 1.4, 1.8} {
			e := s.entities.Spawn(&Entity{Type: mustEntityType(tc.kind), Dim: d.name, X: x, Y: 160, Z: .5, Living: true, Health: 20})
			for phase := 0; phase < 2; phase++ {
				current := mp
				current.ticks -= phase
				s.movingPistons = []movingPiston{current}
				s.pistonSourceBaseBodyPush(d, current, .5)
				if math.Abs(e.X-tc.want[i][phase]) > 1e-7 || e.Y != 160 || e.Z != .5 || e.MotionX != 0 || e.MotionY != 0 || e.MotionZ != 0 {
					t.Errorf("%s x%v phase%d pos%v,%v,%v", tc.kind, x, phase, e.X, e.Y, e.Z)
				}
			}
			s.entities.Remove(e.ID)
		}
	}
}
