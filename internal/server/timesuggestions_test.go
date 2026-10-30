package server

import (
	"reflect"
	"testing"
)

func TestTimeSuggestionsBuiltinRanges(t *testing.T) {
	s := New("unused")
	s.OpsRequired = true
	s.SetOpLevel("TimeSuggest", 4)
	sess := &Session{server: s}
	sess.setName("TimeSuggest")
	for _, tc := range []struct {
		text          string
		start, length int
		values        []string
	}{
		{"/time ", 6, 0, []string{"add", "of", "pause", "query", "rate", "resume", "set"}},
		{"/time set n", 10, 1, []string{"minecraft:night", "minecraft:noon"}},
		{"/time set 1", 11, 0, []string{"d", "s", "t"}},
		{"/time set 1s", 0, 0, nil},
		{"/time set 1S", 11, 1, []string{"s"}},
		{"/time query schedule", 12, 8, []string{"minecraft:villager_schedule"}},
		{"/time of end", 9, 3, []string{"minecraft:the_end"}},
		{"/time of overworld ", 19, 0, []string{"add", "pause", "query", "rate", "resume", "set"}},
		{"/time of the_end set ", 0, 0, nil},
		{"/time of the_end query ", 23, 0, []string{"time"}},
		{"/time of the_end query day ", 27, 0, []string{"repetition"}},
		{"/time set minecraft:roll", 0, 0, nil},
	} {
		start, length, values := s.suggestCompletions(sess, tc.text)
		if start != tc.start || length != tc.length || !reflect.DeepEqual(values, tc.values) {
			t.Fatalf("%q (%d,%d)%q want(%d,%d)%q", tc.text, start, length, values, tc.start, tc.length, tc.values)
		}
	}
	sess.setName("NoOp")
	if start, length, values := s.suggestCompletions(sess, "/time set n"); start != 0 || length != 0 || len(values) != 0 {
		t.Fatalf("nonop completion(%d,%d)%q", start, length, values)
	}
}

func TestTimeSuggestionsRootRegistries(t *testing.T) {
	s := New("unused")
	sess := &Session{server: s}
	s.commands.Register("plugin_probe", func(string, []string) string { return "ok" })
	// A plugin registration sharing a builtin name must not duplicate it.
	s.commands.Register("give", func(string, []string) string { return "plugin" })
	for _, tc := range []struct {
		text          string
		start, length int
		values        []string
	}{
		{"/gi", 1, 2, []string{"give"}}, {"/GI", 1, 2, []string{"give"}},
		{"/plugin_p", 1, 8, []string{"plugin_probe"}}, {"/time", 0, 0, nil}, {"/zz_no_command", 0, 0, nil},
	} {
		start, length, values := s.suggestCompletions(sess, tc.text)
		if start != tc.start || length != tc.length || !reflect.DeepEqual(values, tc.values) {
			t.Fatalf("%q (%d,%d)%q want(%d,%d)%q", tc.text, start, length, values, tc.start, tc.length, tc.values)
		}
	}
}
