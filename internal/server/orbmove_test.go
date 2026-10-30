package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestOrbMoveIncomingFloorResponse(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	for _, block := range []string{"stone", "slime_block"} {
		d.w.SetBlock(world.Pos{Y: 162}, mkBlockState(t, block, nil))
		for _, noGravity := range []bool{false, true} {
			for _, gap := range []float64{0, 5e-8, .0002, .0004} {
				e := &Entity{Dim: dimOverworld, X: .5, Y: 163 + gap, Z: .5, MotionY: -.2, NoGravity: noGravity}
				wantY := e.Y
				if gap*gap > 1e-7 {
					wantY = 163
				}
				s.moveExperienceOrb(e, d.w)
				if e.Y != wantY || math.Abs(e.MotionY-.08) > 1e-11 {
					t.Fatalf("%s NoGravity%t gap%v Y%v want%v MotionY%v want.08", block, noGravity, gap, e.Y, wantY, e.MotionY)
				}
			}
		}
	}
}

func TestOrbMoveThresholdAndFloatFriction(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	for _, tc := range []struct {
		block                    string
		noGravity                bool
		rawX, rawY, wantX, wantY float64
	}{
		{"stone", false, .1, -.03, .05880000591278076, 0},
		{"slime_block", false, .1, -.03, .031736321588401804, .01881600121879579},
		{"stone", true, .1, -.03, .05880000591278076, .012},
		{"slime_block", true, .1, -.03, .031736321588401804, .012},
		{"slime_block", false, 0, -.04, 0, .016},
		{"stone", false, 0, -.02, 0, 0},
		{"slime_block", true, 0, -.01, 0, .004},
	} {
		d.w.SetBlock(world.Pos{Y: 162}, mkBlockState(t, tc.block, nil))
		e := &Entity{Dim: dimOverworld, X: .5, Y: 163, Z: .5, MotionX: tc.rawX, MotionY: tc.rawY, NoGravity: tc.noGravity}
		s.moveExperienceOrb(e, d.w)
		if math.Abs(e.MotionX-tc.wantX) > 1e-11 || math.Abs(e.MotionY-tc.wantY) > 1e-11 {
			t.Fatalf("%s NoGravity%t raw%v,%v Motion%v,%v want%v,%v", tc.block, tc.noGravity, tc.rawX, tc.rawY, e.MotionX, e.MotionY, tc.wantX, tc.wantY)
		}
	}
}

func TestOrbMoveAirFloatDrag(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	e := &Entity{Dim: dimOverworld, X: .5, Y: 164, Z: .5, MotionX: .1, MotionY: -.2}
	s.moveExperienceOrb(e, d.w)
	if math.Abs(e.X-.6) > 1e-11 || math.Abs(e.Y-163.8) > 1e-11 || e.Grounded || math.Abs(e.MotionX-.09800000190734864) > 1e-11 || math.Abs(e.MotionY+.1960000038146973) > 1e-11 {
		t.Fatalf("air position%v,%v grounded%t Motion%v,%v", e.X, e.Y, e.Grounded, e.MotionX, e.MotionY)
	}
}
