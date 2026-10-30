package server

import "testing"

func TestTimeGrammarRejectsBeforeClockMutation(t *testing.T) {
	s := New("unused")
	s.clock.day = 50000
	for _, tc := range []struct {
		args       []string
		key        string
		token, off int
	}{
		{[]string{"add", "1t!"}, "command.expected.separator", 2, 2},
		{[]string{"add", "1s/"}, "command.expected.separator", 2, 2},
		{[]string{"add", "1t:x"}, "command.expected.separator", 2, 2},
		{[]string{"set", "1t!"}, "command.unknown.argument", 2, 0},
		{[]string{"set", "day", "extra"}, "command.unknown.argument", 3, 0},
		{[]string{"add", "1d", "extra"}, "command.unknown.argument", 3, 0},
		{[]string{"add", "1q", "extra"}, "argument.time.invalid_unit", 2, 2},
		{[]string{"set", "1q", "extra"}, "command.unknown.argument", 3, 0},
		{[]string{"set", "a:b:c"}, "command.unknown.argument", 2, 0},
		{[]string{"set", "..:noon"}, "command.unknown.argument", 2, 0},
		{[]string{"of", "overworld", "add", "1t!"}, "command.expected.separator", 4, 2},
	} {
		r := s.runTime("", tc.args)
		if r.fail == nil || r.fail.token != tc.token || r.fail.off != tc.off || len(r.comp.extra) != 1 || r.comp.extra[0].translate != tc.key || s.clock.day != 50000 {
			t.Fatalf("%v reply%#v clock%d", tc.args, r, s.clock.day)
		}
	}
	for _, raw := range []string{"+1", "NOON"} {
		var r reply
		s.withCommandFrame(&commandSourceFrame{dim: s.dims[dimNether]}, func() reply { r = s.runTime(neutralCommandActor, []string{"set", raw}); return r })
		if r.fail == nil || r.fail.token != 2 || r.comp.extra[0].translate != "command.unknown.argument" {
			t.Fatalf("Nether%s precedence%#v", raw, r)
		}
	}
}

func TestTimeArgumentTokenBoundary(t *testing.T) {
	for raw, want := range map[string]int{"1t!": 2, "1t:x": 2, "1s/": 2, "1q!": 2, "1.2foo": 6, "1.00000006d": 11, "-.5t": 4} {
		if got := timeArgumentTokenEnd(raw); got != want {
			t.Fatalf("%s boundary%d want%d", raw, got, want)
		}
	}
}

func TestTimeGrammarExecuteCursorRebase(t *testing.T) {
	original := &failMark{token: 2, off: 1}
	leaf := reply{fail: original}
	parent := rebaseExecuteLeaf(leaf, 4)
	if parent.fail == original || original.token != 2 || parent.fail.token != 6 || parent.fail.off != 1 {
		t.Fatalf("rebase leaked cursor mutation%#v %#v", leaf, parent)
	}
	absolute := reply{fail: &failMark{hasAbs: true, abs: 12}}
	if rebaseExecuteLeaf(absolute, 4).fail != absolute.fail {
		t.Fatal("absolute marks unexpectedly rewritten")
	}
}

func TestTimeGrammarNeutralExecuteCursor(t *testing.T) {
	s := New("unused")
	for _, tc := range []struct {
		args  []string
		token int
	}{
		{[]string{"in", "the_nether", "run", "time", "set", "+1"}, 6},
		{[]string{"in", "overworld", "run", "execute", "in", "the_nether", "run", "time", "set", "+1"}, 10},
		{[]string{"in", "the_nether", "run", "notacommand"}, 4},
	} {
		r := s.withCommandFrame(&commandSourceFrame{dim: s.dims[dimOverworld], level: 4}, func() reply { return s.runExecute(neutralCommandActor, tc.args) })
		if r.fail == nil || r.fail.token != tc.token {
			t.Fatalf("neutral execute%v cursor%#v want%d", tc.args, r.fail, tc.token)
		}
	}
}
