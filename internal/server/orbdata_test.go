package server

import (
	"bytes"
	"testing"
)

func TestOrbSavedFieldsNativeCodec(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		save                      map[string]any
		health, age, value, count int32
	}{
		{"default", nil, 5, 0, 0, 1},
		{"explicit", map[string]any{"Health": int16(4), "Age": int16(17), "Value": int16(7), "Count": int32(3)}, 4, 17, 7, 3},
		{"narrow", map[string]any{"Health": int32(65535), "Age": int32(32768), "Value": int32(65536), "Count": int32(0)}, -1, -32768, 0, 1},
		{"negativecount", map[string]any{"Count": int32(-3)}, 5, 0, 0, 1},
		{"zerohealth", map[string]any{"Health": int16(0)}, 0, 0, 0, 1},
		{"floatcount", map[string]any{"Count": float64(3.9)}, 5, 0, 0, 3},
		{"largefloatcount", map[string]any{"Count": float64(1e40)}, 5, 0, 0, 2147483647},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &Entity{}
			loadOrbSavedFields(e, tc.save)
			if e.OrbHP != tc.health || e.ItemAge != tc.age || e.OrbValue != tc.value || e.OrbCount != tc.count {
				t.Fatalf("health%d age%d value%d count%d", e.OrbHP, e.ItemAge, e.OrbValue, e.OrbCount)
			}
			save := map[string]any{}
			orbSavedFields(e, save)
			if save["Health"] != int16(tc.health) || save["Age"] != int16(tc.age) || save["Value"] != int16(tc.value) || save["Count"] != tc.count {
				t.Fatalf("typed save%#v", save)
			}
		})
	}
}

func TestOrbDataMergePreservesOtherState(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	e := s.entities.Spawn(&Entity{Type: mustEntityType("experience_orb"), Dim: dimOverworld, X: .5, Y: 170, Z: .5, MotionX: .1, NoGravity: true, OrbCount: 3, OrbValue: 7})
	if e.OrbHP != 5 {
		t.Fatalf("constructor health%d", e.OrbHP)
	}
	save := entitySaveMap(e, 0, s.entities).m
	save["Age"] = int16(117)
	s.applyEntitySave(e, save)
	if e.ItemAge != 117 || e.OrbHP != 5 || e.OrbValue != 7 || e.OrbCount != 3 || !e.NoGravity || e.MotionX != .1 {
		t.Fatalf("partial merge changed orb: age%d health%d value%d count%d NoGravity%t MotionX%v", e.ItemAge, e.OrbHP, e.OrbValue, e.OrbCount, e.NoGravity, e.MotionX)
	}
	save = entitySaveMap(e, 0, s.entities).m
	save["Value"] = int16(11)
	s.applyEntitySave(e, save)
	if !bytes.Equal(e.dataSync, []byte{8, 1, 11, 255}) {
		t.Fatalf("value dirty pack%v", e.dataSync)
	}
	if _, ok := e.ExtraNBT["Value"]; ok {
		t.Fatal("live value retained as stale extra")
	}
}

func TestOrbLoadedZeroHealthSurvivesSpawn(t *testing.T) {
	s := New("unused")
	t.Cleanup(func() {
		for _, d := range s.dims {
			d.w.Stop()
		}
	})
	for _, health := range []any{int16(0), int32(65536)} {
		e := &Entity{Type: mustEntityType("experience_orb"), Dim: dimOverworld}
		loadOrbSavedFields(e, map[string]any{"Health": health})
		s.entities.Spawn(e)
		if e.OrbHP != 0 {
			t.Fatalf("loaded Health%v became%d at spawn", health, e.OrbHP)
		}
	}
}
