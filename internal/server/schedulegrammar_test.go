package server

import (
	"reflect"
	"testing"
)

func TestScheduleTimeGrammarBeforeLookupAndMutation(t *testing.T) {
	s := &Server{functions: map[string][]string{"pin:noop": {}}, functionScheduleSerial: 17, functionSchedule: []scheduledFunction{{name: "pin:noop", target: "pin:noop", age: 99}}}
	before := append([]scheduledFunction(nil), s.functionSchedule...)
	for _, tc := range []struct {
		args       []string
		key        string
		token, off int
	}{
		{[]string{"function", "pin:noop", "1t!"}, "command.expected.separator", 3, 2},
		{[]string{"function", "pin:noop", "1s/"}, "command.expected.separator", 3, 2},
		{[]string{"function", "pin:noop", "1t:x"}, "command.expected.separator", 3, 2},
		{[]string{"function", "missing:noop", "1t!"}, "command.expected.separator", 3, 2},
		{[]string{"function", "missing:noop", "1q"}, "argument.time.invalid_unit", 3, 2},
		{[]string{"function", "missing:noop", "-1t"}, "argument.time.tick_count_too_low", 3, 3},
		{[]string{"function", "missing:noop", "1t", "extra"}, "command.unknown.argument", 4, 0},
		{[]string{"function", "pin:noop", "1q", "extra"}, "argument.time.invalid_unit", 3, 2},
		{[]string{"function", "pin:noop", "1t", "append", "extra"}, "command.unknown.argument", 5, 0},
		{[]string{"function", "#missing:noop", "1t!"}, "command.expected.separator", 3, 2},
	} {
		r := s.runFunctionSchedule(tc.args)
		if r.fail == nil || r.fail.token != tc.token || r.fail.off != tc.off || len(r.comp.extra) != 1 || r.comp.extra[0].translate != tc.key {
			t.Fatalf("%v reply%#v", tc.args, r)
		}
		if !reflect.DeepEqual(s.functionSchedule, before) || s.functionScheduleSerial != 17 {
			t.Fatalf("rejected%v changed queue%#v", tc.args, s.functionSchedule)
		}
	}
	for _, tc := range []struct{ target, time, key string }{{"pin:noop", "-.5t", "commands.schedule.same_tick"}, {"missing:noop", "0t", "arguments.function.unknown"}, {"missing:noop", "1t", "arguments.function.unknown"}} {
		r := s.runFunctionSchedule([]string{"function", tc.target, tc.time})
		if r.fail != nil || len(r.comp.extra) != 1 || r.comp.extra[0].translate != tc.key {
			t.Fatalf("runtime%+v %#v", tc, r)
		}
		if !reflect.DeepEqual(s.functionSchedule, before) || s.functionScheduleSerial != 17 {
			t.Fatal("runtime rejection changed existing queue")
		}
	}
}
