package server

import (
	"reflect"
	"testing"
)

func TestCreeperLiveFuseCanonicalSaveView(t *testing.T) {
	s := simulationTestServer(t)
	s.simulationAction(func() {
		e := s.entities.Spawn(&Entity{Type: mustEntityType("creeper"), Living: true, Health: 20, MaxSwell: 5})
		sentinel := map[string]any{"version": int32(19), "value": "keep"}
		for _, tc := range []struct {
			raw     any
			want    int16
			missing bool
		}{{"bad", 30, false}, {[]int32{1}, 30, false}, {int32(65539), 3, false}, {int32(65535), -1, false}, {int16(3), 3, false}, {nil, 30, true}} {
			saved := entitySaveMap(e, s.clock.count, s.entities).m
			if tc.missing {
				delete(saved, "Fuse")
			} else {
				saved["Fuse"] = tc.raw
			}
			saved["UnmodeledSentinel"] = sentinel
			e.Swell = 2
			s.applyEntitySave(e, saved)
			view := entitySaveMap(e, s.clock.count, s.entities).m
			if e.MaxSwell != int32(tc.want) || view["Fuse"] != tc.want {
				t.Fatalf("raw%#v loaded%d saved%#v want short%d", tc.raw, e.MaxSwell, view["Fuse"], tc.want)
			}
			if e.Swell != 2 {
				t.Fatal("canonicalization reset elapsed countdown", e.Swell)
			}
			if !reflect.DeepEqual(view["UnmodeledSentinel"], sentinel) {
				t.Fatal("unrelated carried-through tag changed", view["UnmodeledSentinel"])
			}
		}
	})
}
