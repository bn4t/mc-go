package server

import "testing"

func TestFunctionScheduleQueueIdentityAndReplace(t *testing.T) {
	s := &Server{functions: map[string][]string{"pin:a": {}}}
	for _, args := range [][]string{{"function", "pin:a", "5t", "append"}, {"function", "pin:a", "5t", "append"}, {"function", "pin:a", "6t", "append"}} {
		r := s.runFunctionSchedule(args)
		if r.result < 5 || !r.hasResult || r.outcome != commandSucceeded {
			t.Fatalf("schedule %#v", r)
		}
	}
	if len(s.functionSchedule) != 2 {
		t.Fatalf("duplicate entries %v", s.functionSchedule)
	}
	if r := s.runFunctionSchedule([]string{"clear", "pin:a"}); r.result != 2 || !r.hasResult {
		t.Fatalf("clear %#v", r)
	}
	if len(s.functionSchedule) != 0 {
		t.Fatal("clear retained events")
	}
	s.runFunctionSchedule([]string{"function", "pin:a", "3t", "append"})
	s.runFunctionSchedule([]string{"function", "pin:a", "4t", "replace"})
	if len(s.functionSchedule) != 1 || s.functionSchedule[0].age != 4 {
		t.Fatalf("replace %v", s.functionSchedule)
	}
}

func TestFunctionScheduleTimeConversions(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want int32
	}{{"5t", 5}, {"1s", 20}, {"1d", 24000}, {"0.075s", 2}, {"0.024s", 0}, {"-0.075s", -1}} {
		got, problem, _ := functionScheduleTime(c.raw)
		if problem != "" || got != c.want {
			t.Errorf("%s=%d,%s want%d", c.raw, got, problem, c.want)
		}
	}
}

func TestFunctionScheduleTimeRejectsNativeTokenForms(t *testing.T) {
	for raw, want := range map[string]string{"1e2t": "argument.time.invalid_unit", "+1t": "parsing.float.expected", "5x": "argument.time.invalid_unit", "1..0t": "parsing.float.invalid", "1tgarbage": "argument.time.invalid_unit"} {
		_, problem, _ := functionScheduleTime(raw)
		if problem != want {
			t.Errorf("%q: %s want%s", raw, problem, want)
		}
	}
}
