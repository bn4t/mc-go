package server

import (
	"math"
	"reflect"
	"testing"
)

func TestClockControlsKeepFractionAndSelectFromNether(t *testing.T) {
	s := New("unused")
	s.clock.day = 500
	s.clock.dayState = worldClockState{partial: .25, rate: .5}
	s.clock.end = 42
	s.clock.endState = worldClockState{partial: .75, rate: 2, paused: true}
	for _, args := range [][]string{{"of", "overworld", "pause"}, {"of", "overworld", "rate", "1.25"}} {
		r := s.runTime("", args)
		if !r.hasResult || r.result != 1 || r.outcome != commandSucceeded {
			t.Fatalf("control reply%#v", r)
		}
	}
	s.GameTick()
	if s.clock.day != 500 || s.clock.dayState.partial != .25 || s.clock.dayState.baseRate() != 1.25 || !s.clock.dayState.paused {
		t.Fatal("paused rate setter changed total/fraction")
	}
	s.runTime("", []string{"resume"})
	s.withCommandFrame(&commandSourceFrame{dim: s.dims[dimNether]}, func() reply { return s.runTime(neutralCommandActor, []string{"of", "the_end", "rate", "2.5"}) })
	s.withCommandFrame(&commandSourceFrame{dim: s.dims[dimNether]}, func() reply { return s.runTime(neutralCommandActor, []string{"of", "the_end", "resume"}) })
	s.GameTick()
	s.GameTick()
	if s.clock.day != 502 || s.clock.dayState.partial != .75 || s.clock.end != 47 || s.clock.endState.partial != .75 {
		t.Fatal("explicit controls or float progression wrong")
	}
	before := s.captureWorldClocks()
	for _, value := range []string{"0", ".000001", "1001", "abc", "NaN"} {
		r := s.runTime("", []string{"rate", value})
		if r.fail == nil {
			t.Fatal("invalid rate accepted", value)
		}
	}
	if !reflect.DeepEqual(before, s.captureWorldClocks()) {
		t.Fatal("invalid rate mutated saved state")
	}
	if r := s.runTime("", []string{"of", "overworld", "rate", "0"}); r.fail == nil || r.fail.token != 4 {
		t.Fatal("explicit parse cursor offset")
	}
}
func TestClockBuiltinTimelineAndNumericResults(t *testing.T) {
	s := New("unused")
	s.clock.day = 50000
	s.clock.end = 42
	for _, tc := range []struct {
		args []string
		want int
	}{{[]string{"of", "overworld", "query", "time"}, 50000}, {[]string{"query", "day"}, 2000}, {[]string{"query", "day", "repetition"}, 2}, {[]string{"query", "moon"}, 50000}, {[]string{"query", "moon", "repetition"}, 0}, {[]string{"query", "early_game"}, 50000}, {[]string{"query", "early_game", "repetition"}, 0}, {[]string{"query", "villager_schedule"}, 2000}} {
		r := s.runTime("", tc.args)
		if r.outcome != commandSucceeded || !r.hasResult || r.result != tc.want {
			t.Fatalf("query%v result%#v", tc.args, r)
		}
	}
	if r := s.runTime("", []string{"of", "the_end", "query", "day"}); r.outcome != commandFailed || r.fail != nil {
		t.Fatal("wrong-clock timeline accepted")
	}
	if r := s.runTime("", []string{"of", "overworld", "query", "gametime"}); r.fail == nil {
		t.Fatal("explicit gametime accepted as root literal")
	}
	s.clock.day = -1
	if r := s.runTime("", []string{"query", "day"}); r.result != -1 {
		t.Fatal("timeline remainder lost native sign")
	}
	for _, tc := range []struct {
		total  int64
		result int
	}{{math.MaxInt32, 0}, {math.MaxInt32 + 1, 1}, {math.MaxInt64, 1}, {math.MinInt64, -2}} {
		s.clock.day = tc.total
		r := s.runTime("", []string{"query", "time"})
		if r.result != tc.result {
			t.Fatalf("wrapped result %d=%d want%d", tc.total, r.result, tc.result)
		}
	}
	if r := s.runTime("", []string{"of", "the_end", "set", "2147483647"}); r.result != math.MaxInt32 {
		t.Fatal("set result unexpectedly wrapped")
	}
	if r := s.runTime("", []string{"of", "the_end", "add", "1"}); r.result != 1 {
		t.Fatal("add result not wrapped")
	}
}
