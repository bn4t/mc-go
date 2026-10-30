package server

import (
	"math"
	"testing"
)

func TestTimeArgumentUnitsAndRounding(t *testing.T) {
	s := New("unused")
	for _, tc := range []struct {
		raw  string
		want int64
	}{
		{"1d", 24000}, {"1s", 20}, {"1t", 1}, {"2.5s", 50}, {".5t", 1}, {".025s", 1}, {"-.5t", 0}, {"16777217", 16777216}, {"0.49999999t", 1}, {"99999999999999999999999999999999999999999", math.MaxInt32},
	} {
		s.clock.day = 50000
		r := s.runTime("", []string{"set", tc.raw})
		if r.outcome != commandSucceeded || s.clock.day != tc.want || !r.hasResult || r.result != int(tc.want) {
			t.Fatalf("set %s: clock%d reply%#v", tc.raw, s.clock.day, r)
		}
	}
	s.clock.day = 50000
	r := s.runTime("", []string{"add", "-1.5t"})
	if s.clock.day != 49999 || r.result != 49999 {
		t.Fatalf("negative half-round %d %#v", s.clock.day, r)
	}
	for _, tc := range []struct{ raw, key string }{{"1q", "argument.time.invalid_unit"}, {"1e2", "argument.time.invalid_unit"}, {"+1", "parsing.float.expected"}, {"1..0t", "parsing.float.invalid"}} {
		before := s.clock.day
		r := s.runTime("", []string{"add", tc.raw})
		if r.fail == nil || len(r.comp.extra) != 1 || r.comp.extra[0].translate != tc.key || s.clock.day != before {
			t.Fatalf("bad argument %s %#v", tc.raw, r)
		}
	}
}

func TestPeriodicBuiltinMarkers(t *testing.T) {
	s := New("unused")
	for _, tc := range []struct {
		name        string
		start, want int64
	}{
		{"day", 50000, 73000}, {"noon", 50000, 54000}, {"night", 50000, 61000}, {"midnight", 50000, 66000}, {"roll_village_siege", 50000, 66000}, {"wake_up_from_sleep", 50000, 72000}, {"noon", 6000, 30000}, {":noon", 50000, 54000}, {":wake_up_from_sleep", 50000, 72000}, {"wake_up_from_sleep", 0, 24000},
	} {
		s.clock.day = tc.start
		s.clock.dayState.partial = .75
		r := s.runTime("", []string{"set", tc.name})
		if r.outcome != commandSucceeded || s.clock.day != tc.want || s.clock.dayState.partial != 0 || r.result != int(tc.want) {
			t.Fatalf("marker%s from%d: got%d %#v", tc.name, tc.start, s.clock.day, r)
		}
	}
	if got := nextBuiltinTimeMarker(math.MaxInt64, 6000); got != -9223372036854753616 {
		t.Fatalf("native long wrap%d", got)
	}
	if got := nextBuiltinTimeMarker(-1, 6000); got != 6000 {
		t.Fatalf("signed remainder%d", got)
	}
}
