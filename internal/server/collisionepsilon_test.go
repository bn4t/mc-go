package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestBoxAxisSubepsilonFaceContacts(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	w := s.dims[dimOverworld].w
	w.SetBlock(world.Pos{Y: 162}, world.Stone)
	for axis := 0; axis < 3; axis++ {
		for _, sign := range []float64{-1, 1} {
			for _, overlap := range []float64{-2e-7, -5e-8, 0, 5e-8, 2e-7} {
				boundary := 0.
				if sign < 0 {
					boundary = 1
				}
				if axis == 1 {
					boundary += 162
				}
				face := boundary + sign*overlap
				span := [2]float64{face - .25, face}
				if sign < 0 {
					span = [2]float64{face, face + .25}
				}
				o1min, o1max, o2min, o2max := 162.3, 162.55, .375, .625
				if axis == 1 {
					o1min, o1max = .375, .625
				} else if axis == 2 {
					o1min, o1max, o2min, o2max = .375, .625, 162.3, 162.55
				}
				desired := sign * .2
				want := -sign * overlap
				if overlap > 1e-7 {
					want = desired
				}
				got := s.clampBoxAxis(w, span, desired, axis, o1min, o1max, o2min, o2max)
				if math.Abs(got-want) > 1e-12 {
					t.Errorf("axis%d sign%v overlap%v got%v want%v", axis, sign, overlap, got, want)
				}
			}
		}
	}
}

func TestEntityMoveTinyResolvedMovement(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	w := s.dims[dimOverworld].w
	w.SetBlock(world.Pos{Y: 162}, world.Stone)
	for _, parallel := range []float64{0, .05} {
		e := &Entity{X: -.125 - 5e-8, Y: 162.3, Z: .5, MotionX: .2, MotionZ: parallel}
		want := e.X
		if parallel != 0 {
			want = -.125
		}
		s.entityMove(e, w, .125, .25, .2, 0, parallel, .04, float64(float32(.98)), false)
		if math.Abs(e.X-want) > 1e-12 || e.Z != .5+parallel || e.MotionX != 0 || e.MotionZ != parallel {
			t.Errorf("parallel%v pos%v,%v motion%v,%v", parallel, e.X, e.Z, e.MotionX, e.MotionZ)
		}
	}
	e := &Entity{X: 2.5, Y: 162.3, Z: .5, MotionX: .0002}
	s.entityMove(e, w, .125, .25, .0002, 0, 0, .04, float64(float32(.98)), false)
	if math.Abs(e.X-2.5002) > 1e-12 {
		t.Fatalf("small free movement%v", e.X)
	}
}

func TestEntityMoveRetainedGapBounceSupport(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	d.w.SetBlock(world.Pos{Y: 162}, mkBlockState(t, "slime_block", nil))
	e := &Entity{Dim: dimOverworld, X: .5, Y: 163.0002, Z: .5, MotionY: -.2}
	before := e.Y
	drag := float64(float32(.98))
	ratio := (before - 163) / .2
	s.entityMove(e, d.w, .125, .25, 0, -.2, 0, .04, drag, false)
	want := (ratio*.04 + .2) * (1 + ratio*(drag-1)) * float64(float32(.8))
	if e.Y != before || math.Abs(e.MotionY-want) > 1e-11 {
		t.Fatalf("retained gap Y%v want%v MotionY%v want%v", e.Y, before, e.MotionY, want)
	}
}
