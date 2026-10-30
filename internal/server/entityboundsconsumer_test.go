package server

import "testing"

func TestMeasuredStaticMountBoundsAndBoatOverride(t *testing.T) {
	boats := 0
	for _, name := range vehicleBoundNames() {
		if !isBoatEntityName(name) {
			continue
		}
		id := mustEntityType(name)
		if _, ok := mobBoundsTable[id]; !ok {
			t.Errorf("boat %s would use the new fallback", name)
		}
		boats++
	}
	if boats == 0 {
		t.Fatal("boat legacy-bound audit found no types")
	}
	t.Logf("audited %d boat types: all retain legacy dimensions", boats)

	for _, tc := range []struct {
		name          string
		width, height float64
	}{{"horse", 1.396484375, 1.600000023841858}, {"donkey", 1.396484375, 1.5}, {"mule", 1.396484375, 1.600000023841858}} {
		got := mobBounds(mustEntityType(tc.name))
		if got != [2]float64{tc.width, tc.height} {
			t.Errorf("%s bounds%v want[%g %g]", tc.name, got, tc.width, tc.height)
		}
	}
	// The type record's default point is not AbstractBoat's runtime seat.
	if got := passengerAttachAdult("oak_boat", mustEntityType("oak_boat")); got != [3]float64{0, .1875, 0} {
		t.Fatalf("boat class seat override lost: %v", got)
	}
	if got := mobBounds(-1); got != [2]float64{.6, 1.8} {
		t.Fatalf("unknown type fallback changed: %v", got)
	}
}
