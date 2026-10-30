package server

import (
	"math"
	"testing"
)

// This validates registry getter coverage, not instance pose/size/scale behavior.
func TestEntityTypeDimensionFactsCoverage(t *testing.T) {
	if len(entityTypeNames) != 158 || len(entityTypeBaseDims) != 158 || len(entityTypePassengerAttachments) != 158 || len(entityTypeVehicleAttachments) != 158 {
		t.Fatalf("registry facts incomplete: types%d dims%d passenger%d vehicle%d", len(entityTypeNames), len(entityTypeBaseDims), len(entityTypePassengerAttachments), len(entityTypeVehicleAttachments))
	}
	for _, name := range entityTypeNames {
		dims, ok := entityTypeBaseDims[name]
		if !ok {
			t.Errorf("missing dimensions for%s", name)
			continue
		}
		for _, v := range dims {
			if v < 0 || math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				t.Errorf("invalid dimensions for%s: %v", name, dims)
			}
		}
		for _, table := range []map[string][3]float64{entityTypePassengerAttachments, entityTypeVehicleAttachments} {
			v, ok := table[name]
			if !ok {
				t.Errorf("missing attachment for%s", name)
				continue
			}
			for _, coord := range v {
				if math.IsNaN(coord) || math.IsInf(coord, 0) {
					t.Errorf("invalid attachment for%s: %v", name, v)
				}
			}
		}
	}
}
