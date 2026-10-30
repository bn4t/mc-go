package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestSlimeStepUsesPostRestitutionVelocity(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	d.w.SetBlock(world.Pos{Y: 162}, mkBlockState(t, "slime_block", nil))
	for _, tc := range []struct {
		name         string
		rawY         float64
		noGravity    bool
		wantX, wantY float64
	}{
		{"suppressed", -.03, false, .04, 0},
		{"no-gravity", -.03, true, .04048000000715256, .02400000035762787},
		{"requested-over-threshold", -.11, false, .04176000002622605, .0880000013113022},
		{"post-bounce-below-threshold", -.12, false, .04192000002861023, .09600000143051148},
		{"post-bounce-over-threshold", -.13, false, .1, .10400000154972076},
		{"large-bounce", -.2, false, .1, .1600000023841858},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &Entity{Dim: dimOverworld, X: .5, Y: 163, Z: .5, MotionX: .1, MotionY: tc.rawY, NoGravity: tc.noGravity}
			s.entityMove(e, d.w, .125, .25, .1, tc.rawY, 0, .04, float64(float32(.98)), false)
			if math.Abs(e.X-.6) > 1e-11 || e.Y != 163 || math.Abs(e.MotionX-tc.wantX) > 1e-11 || math.Abs(e.MotionY-tc.wantY) > 1e-11 {
				t.Fatalf("position%v,%v Motion%v,%v want.6,163,%v,%v", e.X, e.Y, e.MotionX, e.MotionY, tc.wantX, tc.wantY)
			}
		})
	}
}
