package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestPistonSourceBaseNoPhysicsStandPush(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	mp := movingPiston{dim: d.name, pos: world.Pos{Y: 160}, state: mkBlockState(t, "piston", map[string]string{"facing": "east"}), dir: rsWest, source: true, ticks: 3}
	cases := []struct {
		x      float64
		marker bool
		want   [2]float64
	}{{.2, false, [2]float64{-.01, -.26}}, {.5, false, [2]float64{-.01, -.26}}, {.8, false, [2]float64{.8, 1}}, {1.4, false, [2]float64{1.27, 1.27}}, {1.8, false, [2]float64{1.29, 1.27}}, {.8, true, [2]float64{.8, .8}}}
	stands := make([]*Entity, len(cases))
	for i, tc := range cases {
		stands[i] = s.entities.Spawn(&Entity{Type: mustEntityType("armor_stand"), Dim: d.name, X: tc.x, Y: 160.375, Z: .5, NoGravity: true, ArmorStandMarker: tc.marker, Living: true, Health: 20})
	}
	for phase := 0; phase < 2; phase++ {
		s.pistonSourceBaseStandPush(d, mp, .5)
		for i, e := range stands {
			if math.Abs(e.X-cases[i].want[phase]) > 1e-12 || e.Y != 160.375 || e.Z != .5 || e.MotionX != 0 || e.MotionY != 0 || e.MotionZ != 0 {
				t.Errorf("case%d phase%d pos%v,%v,%v wantX%v", i, phase, e.X, e.Y, e.Z, cases[i].want[phase])
			}
		}
		mp.ticks--
	}
}

func TestPistonSourceBasePhysicalRodState(t *testing.T) {
	mp := movingPiston{state: mkBlockState(t, "piston", map[string]string{"facing": "east"}), dir: rsWest, source: true, ticks: 3}
	for _, tc := range []struct {
		progress float32
		rodMin   float64
	}{{0, .75}, {.5, .5}} {
		boxes := pistonSourceBaseCarriedBoxes(mp, tc.progress)
		rodFound := false
		for _, b := range boxes {
			if b[1] == .375 && b[4] == .625 {
				rodFound = true
				if b[0] != tc.rodMin {
					t.Errorf("progress%v physicalrod%v wantmin%v", tc.progress, b, tc.rodMin)
				}
			}
		}
		if !rodFound {
			t.Fatal("missing carried source rod")
		}
	}
}
