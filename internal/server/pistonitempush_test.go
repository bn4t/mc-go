package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestPistonItemForwardFacePush(t *testing.T) {
	for _, tc := range []struct {
		name     string
		node     int
		x, first float64
		source   bool
	}{{"head", 1, 1.5, 1.635, true}, {"stone", 2, 2.375, 2.635, false}} {
		t.Run(tc.name, func(t *testing.T) {
			s := New("unused")
			t.Cleanup(func() {
				for _, d := range s.dims {
					d.w.Stop()
				}
			})
			d := s.dims[dimOverworld]
			pos := world.Pos{X: tc.node, Y: 160}
			st := mkBlockState(t, "stone", nil)
			if tc.source {
				st = mkBlockState(t, "piston_head", map[string]string{"facing": "east", "short": "false"})
			}
			mp := movingPiston{dim: d.name, pos: pos, state: st, dir: rsEast, extend: true, source: tc.source, ticks: 3}
			d.w.SetBlock(pos, mkBlockState(t, "moving_piston", map[string]string{"facing": "east"}))
			s.movingPistons = []movingPiston{mp}
			e := s.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: d.name, X: tc.x, Y: 160.5, Z: .5, NoGravity: true})
			for phase := 0; phase < 2; phase++ {
				s.pistonPushTick(d, mp, .5)
				want := tc.first + float64(phase)*.5
				if math.Abs(e.X-want) > 1e-12 || e.Y != 160.5 || e.Z != .5 || e.MotionX != 0 || e.MotionY != 0 || e.MotionZ != 0 {
					t.Fatalf("phase%d entity%+v wantX%v", phase, e, want)
				}
				mp.ticks--
				s.movingPistons[0] = mp
				if s.itemInsideWorldCollision(d, e.X, e.Y, e.Z, .125, .25) {
					t.Fatal("item remained inside live moving shape")
				}
			}
		})
	}
}

func TestItemInsidePositionedWorldShapes(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	pos := world.Pos{X: 2, Y: 160}
	d.w.SetBlock(pos, mkBlockState(t, "moving_piston", nil))
	s.movingPistons = []movingPiston{{dim: d.name, pos: pos, state: mkBlockState(t, "stone", nil), dir: rsEast, extend: true, ticks: 2}}
	for _, tc := range []struct {
		x      float64
		inside bool
	}{{2.7, false}, {2.3, true}, {1.4, true}, {2.625, false}} {
		if got := s.itemInsideWorldCollision(d, tc.x, 160.5, .5, .125, .25); got != tc.inside {
			t.Errorf("x%v inside%t want%t", tc.x, got, tc.inside)
		}
	}
	s.movingPistons = nil
	if s.itemInsideWorldCollision(d, 2.3, 160.5, .5, .125, .25) {
		t.Fatal("orphan moving block is shapeless")
	}
}
