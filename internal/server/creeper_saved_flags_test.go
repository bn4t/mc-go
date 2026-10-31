package server

import (
	"reflect"
	"testing"
)

func TestCreeperSavedFlagsStickyAndDefaults(t *testing.T) {
	s := simulationTestServer(t)
	s.simulationAction(func() {
		e := s.entities.Spawn(&Entity{Type: mustEntityType("creeper"), Living: true, Health: 20})
		view := entitySaveMap(e, s.clock.count, s.entities).m
		if view["powered"] != int8(0) || view["ignited"] != int8(0) {
			t.Fatalf("fresh false flags must be present bytes: %v/%v", view["powered"], view["ignited"])
		}
		opaque := map[string]any{"version": int32(19), "keep": "yes"}
		view["powered"], view["ignited"], view["OpaqueCreeper"] = int8(1), int8(1), opaque
		e.Swell = 2
		s.applyEntitySave(e, view)
		if !e.Powered || !e.Ignited || e.Swell != 2 {
			t.Fatal("true flags not loaded or live counter changed", e.Powered, e.Ignited, e.Swell)
		}
		saved := entitySaveMap(e, s.clock.count, s.entities).m
		saved["powered"], saved["ignited"] = int8(0), int8(0)
		s.applyEntitySave(e, saved)
		if e.Powered || !e.Ignited || e.Swell != 2 {
			t.Fatal("powered replace/ignition latch semantics differ")
		}
		saved = entitySaveMap(e, s.clock.count, s.entities).m
		if saved["powered"] != int8(0) || saved["ignited"] != int8(1) {
			t.Fatal("raw input shadowed canonical sticky flags", saved["powered"], saved["ignited"])
		}
		if !reflect.DeepEqual(saved["OpaqueCreeper"], opaque) {
			t.Fatal("opaque compound lost")
		}
		delete(saved, "powered")
		delete(saved, "ignited")
		s.applyEntitySave(e, saved)
		if e.Powered || !e.Ignited {
			t.Fatal("absent flags incorrectly reset ignition")
		}
		fresh := s.entities.Spawn(&Entity{Type: mustEntityType("creeper"), Living: true, Health: 20})
		s.applyEntitySave(fresh, saved)
		if fresh.Powered || fresh.Ignited || fresh.Swell != 0 {
			t.Fatal("fresh reader defaults differ")
		}
		saved["powered"], saved["ignited"] = int8(1), int8(1)
		s.applyEntitySave(fresh, saved)
		if !fresh.Powered || !fresh.Ignited || fresh.Swell != 0 {
			t.Fatal("cold flags did not restore with transient counter reset")
		}
		pig := s.entities.Spawn(&Entity{Type: mustEntityType("pig"), Living: true, Health: 10})
		other := entitySaveMap(pig, s.clock.count, s.entities).m
		other["powered"] = int32(7)
		other["ignited"] = "opaque"
		s.applyEntitySave(pig, other)
		other = entitySaveMap(pig, s.clock.count, s.entities).m
		if other["powered"] != int32(7) || other["ignited"] != "opaque" {
			t.Fatal("creeper mask consumed unrelated entity fields")
		}
	})
}
