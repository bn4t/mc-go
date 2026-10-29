package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestPistonSourceBaseItemTickCollisionPolicy(t *testing.T) {
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
	for _, escape := range []float64{.1, .2, .3} {
		e := s.entities.Spawn(&Entity{Type: mustEntityType("item"), Dim: d.name, X: 1.3, Y: 160.9, Z: .5, NoGravity: true})
		s.movingPistons = []movingPiston{mp}
		s.pistonSourceBaseBodyPush(d, mp, .5)
		if math.Abs(e.X-1.145) > 1e-9 {
			t.Fatalf("initialclip%v", e.X)
		}
		// The item tick may escape outside the original overlap before the block
		// entity acts; its retained movement policy must still bypass clipping.
		e.X -= escape
		e.MotionX = -escape * float64(float32(.98))
		e.itemNoPhysics = true
		current := mp
		current.ticks--
		s.movingPistons = []movingPiston{current}
		s.pistonSourceBaseBodyPush(d, current, .5)
		if math.Abs(e.X-1.125) > 1e-9 || e.MotionX != -escape*float64(float32(.98)) {
			t.Errorf("escape%v pos%v motion%v", escape, e.X, e.MotionX)
		}
		s.entities.Remove(e.ID)
	}
}
