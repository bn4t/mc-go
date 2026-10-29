package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

// Native one-tick chamber observations constrain movement independently
// of the random impulse sample: gravity is damped before motion, and the
// final stored velocity is the displacement times float-valued air drag.
func TestEmbeddedItemMovementNativeInvariants(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	w := s.dims[dimOverworld].w
	dirs := []struct {
		axis       int
		sign       float64
		dx, dy, dz int
	}{{2, -1, 0, 0, -1}, {2, 1, 0, 0, 1}, {0, -1, -1, 0, 0}, {0, 1, 1, 0, 0}, {1, 1, 0, 1, 0}}
	for _, noGravity := range []bool{true, false} {
		for _, dir := range dirs {
			for x := -1; x <= 1; x++ {
				for y := 161; y <= 163; y++ {
					for z := -1; z <= 1; z++ {
						w.SetBlock(world.Pos{X: x, Y: y, Z: z}, world.Stone)
					}
				}
			}
			w.SetBlock(world.Pos{X: dir.dx, Y: 162 + dir.dy, Z: dir.dz}, world.Air)
			e := s.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: dimOverworld, X: .25, Y: 162.3, Z: .6, Item: ItemStack{ID: world.ItemIDs["stone"], Count: 1}, NoGravity: noGravity, PickupDelay: 32767})
			s.itemTickStep()
			delta := [3]float64{e.X - .25, e.Y - 162.3, e.Z - .6}
			motion := [3]float64{e.MotionX, e.MotionY, e.MotionZ}
			raw := delta
			if !noGravity && dir.axis != 1 {
				raw[1] += .03
			}
			speed := dir.sign * raw[dir.axis]
			if speed < .1-1e-7 || speed > .3+1e-7 {
				t.Errorf("axis%d noGravity%t impulse%v", dir.axis, noGravity, speed)
			}
			for axis := range raw {
				if axis != dir.axis && math.Abs(raw[axis]) > 1e-9 {
					t.Errorf("axis%d noGravity%t transverse%v", dir.axis, noGravity, delta)
				}
				if math.Abs(motion[axis]-delta[axis]*float64(float32(.98))) > 1e-10 {
					t.Errorf("axis%d noGravity%t drag delta%v Motion%v", dir.axis, noGravity, delta, motion)
				}
			}
			s.entities.Remove(e.ID)
		}
	}
}
