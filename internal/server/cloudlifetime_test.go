package server

import (
	"math"
	"testing"
)

func TestCloudPerVictimDuration(t *testing.T) {
	for _, tc := range []struct {
		name            string
		duration, onUse int32
		radiusDelta     float32
		want            int32
		alive           bool
	}{
		{"discard", 100, -1000, 0, -900, false},
		{"shorten", 100, -20, 0, 80, true},
		{"extend", 100, 50, 0, 150, true},
		{"infinite", -1, -1000, 0, -1, true},
		{"radius first", 100, -1000, -1, 100, false},
		{"signed overflow", math.MaxInt32, 1, 0, math.MinInt32, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Entity{CloudRadius: 1, CloudRadiusOnUse: tc.radiusDelta, CloudDuration: tc.duration, CloudDurationOnUse: tc.onUse}
			if alive := cloudAfterDose(&e); alive != tc.alive || e.CloudDuration != tc.want {
				t.Fatalf("alive%v duration%d", alive, e.CloudDuration)
			}
		})
	}
}

func TestCloudDurationOnUseNativeField(t *testing.T) {
	s := simulationTestServer(t)
	s.simulationAction(func() {
		e := s.entities.Spawn(&Entity{Type: mustEntityType("area_effect_cloud"), Dim: dimOverworld, CloudDuration: -1, CloudDurationOnUse: -1000})
		saved := entitySaveMap(e, s.clock.count, s.entities).m
		if saved["DurationOnUse"] != int32(-1000) {
			t.Fatal("missing typed live value", saved["DurationOnUse"])
		}
		e.CloudDurationOnUse = 1
		s.applyEntitySave(e, saved)
		if e.CloudDurationOnUse != -1000 {
			t.Fatal("load did not restore value")
		}
		delete(saved, "DurationOnUse")
		s.applyEntitySave(e, saved)
		if e.CloudDurationOnUse != 0 {
			t.Fatal("omission did not restore native zero default")
		}
	})
}

func TestCloudDurationOnUseSummon(t *testing.T) {
	s := nativeSaveTestServer(t)
	s.SetFrozen(true)
	enforceSession(t, s, "CloudSummon")
	s.runSummon("CloudSummon", []string{"area_effect_cloud", "0", "0", "0", `{Radius:1f,Duration:100,WaitTime:0,DurationOnUse:-1000,potion_contents:{custom_effects:[{id:"minecraft:saturation",duration:0}]}}`})
	found := false
	for _, e := range s.entities.All() {
		if name, _ := EntityTypeName(e.Type); name == "area_effect_cloud" {
			found = true
			if e.CloudDurationOnUse != -1000 {
				t.Fatalf("summoned durationOnUse%d ExtraNBT%v", e.CloudDurationOnUse, e.ExtraNBT)
			}
		}
	}
	if !found {
		t.Fatal("cloud not summoned")
	}
}
