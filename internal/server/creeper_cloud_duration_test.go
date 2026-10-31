package server

import "testing"

func TestCreeperCloudCopiedDuration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		expiry int64
		want   int32
	}{{"finite", 101, 25}, {"infinite", -1, -1}} {
		t.Run(tc.name, func(t *testing.T) {
			s := simulationTestServer(t)
			s.simulationAction(func() {
				s.clock.count = 2
				id, _ := MobEffectID("slowness")
				source := s.entities.Spawn(&Entity{Type: mustEntityType("creeper"), Dim: dimOverworld, Effects: map[int32]mobEffectInst{id: {expiry: tc.expiry, born: 1, amplifier: 2, flags: 9, flagsSet: true, hide: true}}})
				s.spawnLingeringCloud(source)
				var cloud *Entity
				for _, e := range s.entities.All() {
					if e.Type == mustEntityType("area_effect_cloud") {
						cloud = &e
					}
				}
				if cloud == nil {
					t.Fatal("no copied cloud")
				}
				for _, now := range []int64{2, 12, 152, 301} {
					doses := potionCloudDoses(cloud, now)
					if len(doses) != 1 || doses[0].ticks != tc.want || doses[0].amplifier != 2 || doses[0].flags != 9 || !doses[0].hide {
						t.Fatalf("at%d doses%+v", now, doses)
					}
				}
				if source.Effects[id].expiry != tc.expiry {
					t.Fatal("source effect mutated")
				}
				if cloud.CloudOwnerID != source.ID || cloud.CloudDuration != 300 || cloud.CloudWait != 10 || cloud.CloudRadius != 2.5 {
					t.Fatal("cloud geometry/ownership changed")
				}
			})
		})
	}
}

func TestCreeperCloudNoEffectControl(t *testing.T) {
	s := simulationTestServer(t)
	s.simulationAction(func() {
		source := s.entities.Spawn(&Entity{Type: mustEntityType("creeper")})
		s.spawnLingeringCloud(source)
		if len(s.entities.All()) != 1 {
			t.Fatal("clean creeper spawned cloud")
		}
	})
}
