package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestEntityMoveNoGravityRestitution(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	d.w.SetBlock(world.Pos{Y: 162}, mkBlockState(t, "slime_block", nil))
	for _, noGravity := range []bool{false, true} {
		for _, gravity := range []float64{.03, .04} {
			for _, gap := range []float64{0, 5e-8, .0002, .0004} {
				e := &Entity{Dim: dimOverworld, X: .5, Y: 163 + gap, Z: .5, MotionY: -.2, NoGravity: noGravity}
				ratio := (e.Y - 163) / .2
				effective := gravity
				if noGravity {
					effective = 0
				}
				drag := float64(float32(.98))
				want := (ratio*effective + .2) * (1 + ratio*(drag-1)) * float64(float32(.8))
				wantY := e.Y
				if gap*gap > 1e-7 {
					wantY = 163
				}
				s.entityMove(e, d.w, .125, .25, 0, -.2, 0, gravity, drag, false)
				if e.Y != wantY || math.Abs(e.MotionY-want) > 1e-11 {
					t.Fatalf("NoGravity%t gravity%v gap%v Y%v want%v MotionY%v want%v", noGravity, gravity, gap, e.Y, wantY, e.MotionY, want)
				}
			}
		}
	}
}
