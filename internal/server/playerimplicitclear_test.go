package server

import "testing"

func TestImplicitClearUsesExecutor(t *testing.T) {
	s, _ := randSim(t)
	other := &Session{server: s}
	other.setName("Other")
	frame := &commandSourceFrame{actor: "Caller"}
	check := func(want string) {
		t.Helper()
		r := s.runClear("Caller", nil)
		key := r.comp.translate
		if key == "" && len(r.comp.extra) == 1 {
			key = r.comp.extra[0].translate
		}
		if r.fail != nil || key != want {
			t.Fatalf("clear result %#v want%s", r, want)
		}
	}
	s.withCommandFrame(frame, func() reply { check("permissions.requires.player"); return reply{} })
	frame.executor = &dataEntity{mob: &Entity{}}
	s.withCommandFrame(frame, func() reply { check("permissions.requires.player"); return reply{} })
	frame.executor = &dataEntity{sess: other}
	s.withCommandFrame(frame, func() reply { check("clear.failed.single"); return reply{} })
}
