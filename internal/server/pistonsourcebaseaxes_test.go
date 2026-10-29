package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

// Recorded native NoGravity+NoAI cow observations retain ordinary block
// collision in all axes, unlike the NoGravity armor-stand apparatus.
func TestPistonSourceBaseCowAxesNativeObservations(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	pos := world.Pos{Y: 162}
	goldens := map[string][6][2]float64{
		"east":  {{-0.209999988, -0.459999988}, {-0.010000000, -0.459999988}, {1.209999988, 1.469999988}, {1.469999988, 1.469999988}, {1.469999988, 1.469999988}, {2.800000000, 2.800000000}},
		"west":  {{1.209999988, 1.459999988}, {1.010000000, 1.459999988}, {-0.209999988, -0.469999988}, {-0.469999988, -0.469999988}, {-0.469999988, -0.469999988}, {-1.800000000, -1.800000000}},
		"up":    {{160.990000012, 160.590000024}, {161.290000012, 160.780000012}, {161.590000012, 161.080000012}, {163.010000000, 163.020000000}, {163.020000000, 163.020000000}, {164.100000012, 164.100000012}},
		"down":  {{162.610000012, 163.010000000}, {162.310000012, 162.820000012}, {162.010000012, 162.520000012}, {160.590000024, 160.580000024}, {160.580000024, 160.580000024}, {159.500000012, 159.500000012}},
		"south": {{-0.209999988, -0.459999988}, {-0.010000000, -0.459999988}, {1.209999988, 1.469999988}, {1.469999988, 1.469999988}, {1.469999988, 1.469999988}, {2.800000000, 2.800000000}},
		"north": {{1.209999988, 1.459999988}, {1.010000000, 1.459999988}, {-0.209999988, -0.469999988}, {-0.469999988, -0.469999988}, {-0.469999988, -0.469999988}, {-1.800000000, -1.800000000}},
	}
	for _, dir := range []struct {
		facing rsDir
		axis   int
		sign   float64
	}{{rsEast, 0, 1}, {rsWest, 0, -1}, {rsUp, 1, 1}, {rsDown, 1, -1}, {rsSouth, 2, 1}, {rsNorth, 2, -1}} {
		name := rsDirName(dir.facing)
		mp := movingPiston{dim: d.name, pos: pos, state: mkBlockState(t, "piston", map[string]string{"facing": name}), dir: rsOpposite(dir.facing), source: true, ticks: 3}
		d.w.SetBlock(pos, mkBlockState(t, "moving_piston", map[string]string{"facing": name}))
		for i, start := range []float64{.2, .5, .8, 1.4, 1.8, 2.8} {
			initial := [3]float64{.5, 162.375, .5}
			local := start
			if dir.sign < 0 {
				local = 1 - local
			}
			initial[dir.axis] = local
			if dir.axis == 1 {
				initial[1] += 162 - float64(float32(1.4))/2
			}
			e := s.entities.Spawn(&Entity{Type: mustEntityType("cow"), Dim: d.name, X: initial[0], Y: initial[1], Z: initial[2], NoGravity: true, NoAI: true, Living: true, Health: 20})
			for phase := 0; phase < 2; phase++ {
				current := mp
				current.ticks -= phase
				s.movingPistons = []movingPiston{current}
				s.pistonSourceBaseBodyPush(d, current, .5)
				position := [3]float64{e.X, e.Y, e.Z}
				want := initial
				want[dir.axis] = goldens[name][i][phase]
				for axis := range position {
					if math.Abs(position[axis]-want[axis]) > 1e-7 {
						t.Errorf("%s depth%v phase%d pos%v want%v", name, start, phase, position, want)
					}
				}
				if e.MotionX != 0 || e.MotionY != 0 || e.MotionZ != 0 {
					t.Errorf("%s depth%v changed Motion", name, start)
				}
			}
			s.entities.Remove(e.ID)
		}
	}
}
