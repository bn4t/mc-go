package server

import "testing"

func TestPlayerSelfSelectorUsesExecutor(t *testing.T) {
	s, _ := randSim(t)
	caller := &Session{server: s}
	caller.setName("Caller")
	other := &Session{server: s}
	other.setName("Other")
	check := func(sender *Session, want *Session) {
		t.Helper()
		got, r := s.selectPlayers(sender, "@s", 1)
		if want == nil {
			if r == nil || r.fail != nil || r.outcome != commandFailed || len(got) != 0 {
				t.Fatalf("nonplayer selector got%v reply%#v", got, r)
			}
			return
		}
		if r != nil || len(got) != 1 || got[0] != want {
			t.Fatalf("selector got%v reply%#v want%p", got, r, want)
		}
	}
	check(caller, caller)
	check(nil, nil)
	caller.execEnt = &dataEntity{sess: other}
	check(caller, other)
	caller.execEnt = &dataEntity{mob: &Entity{}}
	check(caller, nil)
	frame := &commandSourceFrame{actor: "Caller", executor: &dataEntity{sess: other}}
	s.withCommandFrame(frame, func() reply { check(caller, other); return reply{} })
	frame.executor = &dataEntity{mob: &Entity{}}
	caller.execEnt = &dataEntity{sess: other}
	s.withCommandFrame(frame, func() reply { check(caller, nil); return reply{} })
	frame.executor = nil
	s.withCommandFrame(frame, func() reply { check(caller, nil); return reply{} })
	frame.actor = neutralCommandActor
	frame.executor = &dataEntity{sess: other}
	s.withCommandFrame(frame, func() reply { check(nil, other); return reply{} })
	caller.execEnt = nil
	s.withCommandFrame(frame, func() reply { check(caller, caller); return reply{} })
}
