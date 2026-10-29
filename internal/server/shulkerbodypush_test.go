package server

import (
	"math"
	"mc-go/internal/world"
	"testing"
)

func TestShulkerBodyPushNativeObservations(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	d := s.dims[dimOverworld]
	pos := world.Pos{Y: 162}
	type direction struct {
		name string
		axis int
		sign float64
		wall bool
	}
	dirs := []direction{{"east", 0, 1, false}, {"west", 0, -1, false}, {"up", 1, 1, false}, {"down", 1, -1, false}, {"south", 2, 1, false}, {"north", 2, -1, false}, {"east", 0, 1, true}}
	type experiment struct {
		kind   string
		marker bool
		dir    direction
	}
	var cases []experiment
	for _, kind := range []string{"armor_stand", "cow"} {
		for _, dir := range dirs {
			cases = append(cases, experiment{kind: kind, dir: dir})
		}
	}
	cases = append(cases, experiment{kind: "armor_stand", marker: true, dir: dirs[0]})
	goldens := [15][10]float64{
		{1.360000001, 1.470000003, 1.580000012, 1.690000006, 1.800000000, 1.910000024, 2.020000048, 2.130000072, 2.240000095, 2.350000000},
		{-0.360000001, -0.470000003, -0.580000012, -0.690000006, -0.800000000, -0.910000024, -1.020000048, -1.130000072, -1.240000095, -1.350000000},
		{163.110000001, 163.220000003, 163.330000012, 163.440000006, 163.550000000, 163.660000024, 163.770000048, 163.880000072, 163.990000095, 164.100000000},
		{159.914999975, 159.804999973, 159.694999964, 159.584999970, 159.474999976, 159.364999952, 159.254999928, 159.144999904, 159.034999881, 158.924999976},
		{1.360000001, 1.470000003, 1.580000012, 1.690000006, 1.800000000, 1.910000024, 2.020000048, 2.130000072, 2.240000095, 2.350000000},
		{-0.360000001, -0.470000003, -0.580000012, -0.690000006, -0.800000000, -0.910000024, -1.020000048, -1.130000072, -1.240000095, -1.350000000},
		{1.360000001, 1.470000003, 1.580000012, 1.690000006, 1.800000000, 1.910000024, 2.020000048, 2.130000072, 2.240000095, 2.350000000},
		{1.360000001, 1.470000003, 1.580000012, 1.690000006, 1.800000000, 1.910000024, 2.020000048, 2.130000072, 2.240000095, 2.350000000},
		{-0.360000001, -0.470000003, -0.580000012, -0.690000006, -0.800000000, -0.910000024, -1.020000048, -1.130000072, -1.240000095, -1.350000000},
		{163.110000001, 163.220000003, 163.330000012, 163.440000006, 163.550000000, 163.660000024, 163.770000048, 163.880000072, 163.990000095, 164.100000000},
		{160.490000023, 160.380000021, 160.270000012, 160.160000018, 160.050000024, 159.940000000, 159.829999976, 159.719999952, 159.609999929, 159.500000024},
		{1.360000001, 1.470000003, 1.580000012, 1.690000006, 1.800000000, 1.910000024, 2.020000048, 2.130000072, 2.240000095, 2.350000000},
		{-0.360000001, -0.470000003, -0.580000012, -0.690000006, -0.800000000, -0.910000024, -1.020000048, -1.130000072, -1.240000095, -1.350000000},
		{1.360000001, 1.470000003, 1.550000012, 1.550000012, 1.550000012, 1.550000012, 1.550000012, 1.550000012, 1.550000012, 1.550000012},
		{1.250000000, 1.250000000, 1.250000000, 1.250000000, 1.250000000, 1.250000000, 1.250000000, 1.250000000, 1.250000000, 1.250000000},
	}
	for ci, tc := range cases {
		state := mkBlockState(t, "shulker_box", map[string]string{"facing": tc.dir.name})
		d.w.SetBlock(pos, state)
		c := s.ensureContainer(d, pos, state)
		d.w.SetBlock(world.Pos{X: 2, Y: 162}, world.Air)
		if tc.dir.wall {
			d.w.SetBlock(world.Pos{X: 2, Y: 162}, world.Stone)
		}
		initial := [3]float64{.5, 162.5, .5}
		if tc.dir.axis == 1 {
			height := float64(float32(1.975))
			if tc.kind == "cow" {
				height = float64(float32(1.4))
			}
			initial[1] = 163
			if tc.dir.sign < 0 {
				initial[1] = 162 - height
			}
		} else {
			initial[tc.dir.axis] = .5 + tc.dir.sign*.75
		}
		e := s.entities.Spawn(&Entity{Type: mustEntityType(tc.kind), Dim: d.name, X: initial[0], Y: initial[1], Z: initial[2], NoGravity: true, NoAI: true, ArmorStandMarker: tc.marker, Living: true, Health: 20})
		// The capability guard also applies to supported render types.
		neutral := s.entities.Spawn(&Entity{Type: mustEntityType("cow"), Dim: d.name, X: initial[0], Y: initial[1], Z: initial[2], NoGravity: true, presenceOnly: true})
		progress := float32(0)
		for phase := 0; phase < 10; phase++ {
			next := progress + float32(.1)
			if next > 1 {
				next = 1
			}
			d.containers.mu.Lock()
			c.shulkerProgress = next
			d.containers.mu.Unlock()
			s.shulkerPushEntities(d, pos, progress, next)
			if [3]float64{neutral.X, neutral.Y, neutral.Z} != initial {
				t.Errorf("case%d phase%d moved neutral capability record", ci, phase)
			}
			progress = next
			position := [3]float64{e.X, e.Y, e.Z}
			want := initial
			want[tc.dir.axis] = goldens[ci][phase]
			for axis := range position {
				if math.Abs(position[axis]-want[axis]) > 1e-7 {
					t.Errorf("case%d phase%d pos%v want%v", ci, phase, position, want)
				}
			}
			if e.MotionX != 0 || e.MotionY != 0 || e.MotionZ != 0 {
				t.Errorf("case%d changed velocity", ci)
			}
		}
		before := [3]float64{e.X, e.Y, e.Z}
		s.shulkerPushEntities(d, pos, 1, 0)
		if [3]float64{e.X, e.Y, e.Z} != before {
			t.Errorf("case%d closing dragged body", ci)
		}
		s.entities.Remove(e.ID)
		s.entities.Remove(neutral.ID)
	}
}
