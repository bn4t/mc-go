package server

import "testing"

func TestCreeperFuseConstructorDefault(t *testing.T) {
	for _, tc := range []struct{ input, want int32 }{{0, 30}, {1, 1}, {-1, -1}, {3, 3}} {
		e := NewManager().Spawn(&Entity{Type: mustEntityType("creeper"), Living: true, MaxSwell: tc.input})
		if e.MaxSwell != tc.want {
			t.Fatalf("constructor input%d fuse%d want%d", tc.input, e.MaxSwell, tc.want)
		}
	}
	e := NewManager().Spawn(&Entity{Type: mustEntityType("pig"), Living: true})
	if e.MaxSwell != 0 {
		t.Fatal("noncreeper received fuse default")
	}
}

func TestCreeperFuseSavedSignedValueAndCountdown(t *testing.T) {
	s := simulationTestServer(t)
	s.simulationAction(func() {
		e := s.entities.Spawn(&Entity{Type: mustEntityType("creeper"), Living: true, Health: 20})
		for _, fuse := range []int16{0, -1, 1, 3, 30} {
			saved := entitySaveMap(e, s.clock.count, s.entities).m
			saved["Fuse"] = fuse
			e.Swell = 2
			s.applyEntitySave(e, saved)
			if e.MaxSwell != int32(fuse) {
				t.Fatalf("loaded Fuse%d got%d", fuse, e.MaxSwell)
			}
			if e.Swell != 2 {
				t.Fatal("Fuse load reset countdown", e.Swell)
			}
			if got := entitySaveMap(e, s.clock.count, s.entities).m["Fuse"]; got != fuse {
				t.Fatalf("saved Fuse=%#v want short%d", got, fuse)
			}
		}
		for _, bad := range []any{"bad", []int32{1}} {
			saved := entitySaveMap(e, s.clock.count, s.entities).m
			saved["Fuse"] = bad
			s.applyEntitySave(e, saved)
			if e.MaxSwell != 30 {
				t.Fatalf("wrong-type Fuse%#v failed default30: %d", bad, e.MaxSwell)
			}
		}

		saved := entitySaveMap(e, s.clock.count, s.entities).m
		delete(saved, "Fuse")
		s.applyEntitySave(e, saved)
		if e.MaxSwell != 30 {
			t.Fatal("absent Fuse failed to restore default30", e.MaxSwell)
		}
	})
}
