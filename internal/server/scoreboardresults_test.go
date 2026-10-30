package server

import "testing"

func TestScoreboardNumericResultsMeasured(t *testing.T) {
	s, _ := randSim(t)
	check := func(want int, success bool, args ...string) {
		t.Helper()
		r := s.runScoreboard("RCON", args)
		if r.fail != nil || !r.hasResult || r.result != want || (r.outcome == commandSucceeded) != success {
			t.Fatalf("%v: result=%d present=%v outcome=%v fail=%v", args, r.result, r.hasResult, r.outcome, r.fail)
		}
	}
	check(1, true, "objectives", "add", "score", "dummy")
	check(2, true, "objectives", "add", "other", "dummy")
	check(7, true, "players", "set", "A", "score", "7")
	check(7, true, "players", "set", "B", "score", "7")
	check(20, true, "players", "add", "*", "score", "3")
	check(16, true, "players", "remove", "*", "score", "2")
	check(8, true, "players", "get", "A", "score")
	check(0, false, "players", "get", "Missing", "score")
	check(2, true, "objectives", "list")
	check(2, true, "players", "list")
	check(1, true, "players", "list", "A")
	check(0, true, "objectives", "modify", "score", "displayname", `"Named"`)
	check(0, true, "objectives", "setdisplay", "sidebar", "score")
	check(0, false, "objectives", "setdisplay", "sidebar", "score")
	check(2, true, "players", "set", "src", "other", "2")
	check(22, true, "players", "operation", "*", "score", "+=", "src", "other")
	check(10, true, "players", "get", "A", "score")
	check(10, true, "players", "get", "B", "score")
	check(2, true, "players", "get", "src", "score")
	check(3, true, "players", "reset", "*", "score")
	check(1, true, "players", "reset", "A", "score")
	check(3, true, "objectives", "add", "trig", "trigger")
	check(1, true, "players", "enable", "A", "trig")
	check(0, false, "players", "enable", "A", "trig")
	check(1, true, "players", "display", "name", "A", "score", `"Label"`)
	check(2, true, "objectives", "remove", "trig")
	check(1, true, "objectives", "remove", "other")
	// Only A survives with a score; reintroduce B for the measured signed sum.
	check(0, true, "players", "set", "B", "score", "0")
	check(-2, true, "players", "set", "*", "score", "2147483647")
	check(0, true, "players", "add", "*", "score", "1")
	check(-2, true, "players", "remove", "*", "score", "1")
	r := s.runScoreboard("RCON", []string{"players", "set", "A", "score", "nope"})
	if r.fail == nil || r.hasResult {
		t.Fatalf("syntax failure delivered result: %#v", r)
	}
}
