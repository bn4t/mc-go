package server

import "testing"

func TestCreeperRadiusConstructorDefault(t *testing.T) {
	for _, tc := range []struct{ input, want int32 }{{0, 3}, {1, 1}, {-1, -1}, {-128, -128}} {
		e := NewManager().Spawn(&Entity{Type: mustEntityType("creeper"), Living: true, BlastPower: tc.input})
		if e.BlastPower != tc.want {
			t.Fatalf("constructor radius input%d got%d want%d", tc.input, e.BlastPower, tc.want)
		}
	}
	e := NewManager().Spawn(&Entity{Type: mustEntityType("tnt")})
	if e.BlastPower != 0 {
		t.Fatal("creeper default applied to TNT")
	}
}

func TestCreeperRadiusSavedByteCodec(t *testing.T) {
	s := simulationTestServer(t)
	s.simulationAction(func() {
		e := s.entities.Spawn(&Entity{Type: mustEntityType("creeper"), Living: true, Health: 20, MaxSwell: 7})
		for _, tc := range []struct {
			input any
			want  int8
		}{{int8(-128), -128}, {int8(-1), -1}, {int8(0), 0}, {int8(1), 1}, {int8(3), 3}, {int8(127), 127}, {int32(255), -1}, {int32(256), 0}, {int32(257), 1}, {"bad", 3}, {[]int32{1}, 3}} {
			saved := entitySaveMap(e, s.clock.count, s.entities).m
			saved["ExplosionRadius"] = tc.input
			e.Swell = 2
			s.applyEntitySave(e, saved)
			if e.BlastPower != int32(tc.want) {
				t.Fatalf("radius load%#v got%d want%d", tc.input, e.BlastPower, tc.want)
			}
			if got := entitySaveMap(e, s.clock.count, s.entities).m["ExplosionRadius"]; got != tc.want {
				t.Fatalf("saved radius%#v want byte%d", got, tc.want)
			}
			if e.Swell != 2 || e.MaxSwell != 7 {
				t.Fatal("radius load changed fuse", e.Swell, e.MaxSwell)
			}
		}
		saved := entitySaveMap(e, s.clock.count, s.entities).m
		delete(saved, "ExplosionRadius")
		s.applyEntitySave(e, saved)
		if e.BlastPower != 3 {
			t.Fatal("absent radius failed default3", e.BlastPower)
		}
	})
}
