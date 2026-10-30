package server

import (
	"runtime"
	"strconv"
	"testing"
)

func TestFunctionSequenceBudgetsMeasured(t *testing.T) {
	s, _ := randSim(t)
	s.functions = map[string][]string{
		"budget:exec_run":       {"execute run scoreboard players add value budget 1"},
		"budget:exec_if":        {"execute if score ready budget matches 1 run scoreboard players add value budget 1"},
		"budget:exec_condition": {"execute if score ready budget matches 1", "scoreboard players add value budget 10"},
		"budget:exec_recur":     {"scoreboard players add value budget 1", "execute run function budget:exec_recur", "scoreboard players add tail budget 1"},
		"budget:flat":           {"scoreboard players add value budget 1", "scoreboard players add value budget 10", "scoreboard players add value budget 100"},
		"budget:child":          {"scoreboard players add value budget 10", "scoreboard players add value budget 100"},
		"budget:nested":         {"scoreboard players add value budget 1", "function budget:child", "scoreboard players add value budget 1000"},
		"budget:recur":          {"scoreboard players add value budget 1", "function budget:recur", "scoreboard players add tail budget 1"},
		"budget:failure":        {"clear @s minecraft:diamond 0", "scoreboard players add value budget 10"},
		"budget:nested_group":   {"scoreboard players add value budget 1000", "function #budget:group", "scoreboard players add value budget 10000"},
	}
	s.functionTags = map[string][]string{"budget:group": {"budget:flat", "budget:child"}}
	command := func(name string, args ...string) reply {
		r, ok := s.runCommandAt("RCON", 4, name, args)
		if !ok || r.fail != nil {
			t.Fatalf("command %s %v: %#v", name, args, r)
		}
		return r
	}
	command("scoreboard", "objectives", "add", "budget", "dummy")
	set := func(holder string, n int) {
		s.runScoreboard("RCON", []string{"players", "set", holder, "budget", strconv.Itoa(n)})
	}
	get := func(holder string) int {
		r := command("scoreboard", "players", "get", holder, "budget")
		if !r.hasResult {
			t.Fatalf("missing score %s: %#v", holder, r)
		}
		return r.result
	}
	set("ready", 1)
	limits := []int{0, 1, 2, 3, 4, 5, 6, 8}
	for _, test := range []struct {
		name   string
		values []int
	}{
		{"exec_run", []int{0, 0, 1, 1, 1, 1, 1, 1}},
		{"exec_if", []int{0, 0, 0, 1, 1, 1, 1, 1}},
		{"exec_condition", []int{0, 0, 0, 10, 10, 10, 10, 10}},
		{"exec_recur", []int{0, 0, 1, 1, 2, 2, 3, 4}},
		{"flat", []int{0, 0, 1, 11, 111, 111, 111, 111}},
		{"nested", []int{0, 0, 1, 1, 11, 111, 1111, 1111}},
		{"recur", []int{0, 0, 1, 1, 2, 2, 3, 4}},
		{"failure", []int{0, 0, 0, 10, 10, 10, 10, 10}},
		{"#group", []int{0, 0, 1, 11, 111, 111, 121, 221}},
		{"nested_group", []int{0, 0, 1000, 1000, 1001, 1011, 1111, 1121}},
	} {
		for i, limit := range limits {
			command("gamerule", "minecraft:max_command_sequence_length", strconv.Itoa(limit))
			set("value", 0)
			set("tail", 0)
			name := "budget:" + test.name
			if test.name == "#group" {
				name = "#budget:group"
			}
			s.runFunction("RCON", []string{name})
			if got := get("value"); got != test.values[i] {
				t.Errorf("%s limit%d=%d want%d", test.name, limit, got, test.values[i])
			}
			if got := get("tail"); got != 0 {
				t.Errorf("%s limit%d tail=%d", test.name, limit, got)
			}
			if s.functionExecution != nil {
				t.Fatal("function execution leaked after drain")
			}
		}
	}
	command("gamerule", "minecraft:max_command_sequence_length", "65536")
	set("value", 0)
	set("tail", 0)
	s.runFunction("RCON", []string{"budget:recur"})
	if got := get("value"); got != 32768 {
		t.Fatalf("default recursion budget=%d", got)
	}
	if got := get("tail"); got != 0 {
		t.Fatalf("recursive pending tails=%d", got)
	}
	// Thousands of unreachable commands must remain a single continuation
	// per recursive call, rather than an eagerly expanded action array.
	lines := []string{"scoreboard players add value budget 1", "function budget:large_tail"}
	for i := 0; i < 4096; i++ {
		lines = append(lines, "scoreboard players add tail budget 1")
	}
	s.functions["budget:large_tail"] = lines
	command("gamerule", "minecraft:max_command_sequence_length", "256")
	set("value", 0)
	set("tail", 0)
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	s.runFunction("RCON", []string{"budget:large_tail"})
	runtime.ReadMemStats(&after)
	if got := get("value"); got != 128 {
		t.Fatalf("large-tail recursion=%d", got)
	}
	if got := get("tail"); got != 0 {
		t.Fatalf("unreachable tail executed=%d", got)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 16<<20 {
		t.Fatalf("large unreachable tails allocated %d bytes", allocated)
	} else {
		t.Logf("large-tail recursion allocated %d bytes", allocated)
	}
}

func TestFunctionQueueDropsUnreachableForkWork(t *testing.T) {
	q := &functionExecution{remaining: 32}
	actions := make([]functionAction, 4096)
	for i := range actions {
		actions[i] = functionAction{cost: 1, index: i}
	}
	q.push(actions)
	if q.pendingCost != 32 || len(q.stack)-q.head != 32 {
		t.Fatalf("unreachable fork retained: cost%d pending%d", q.pendingCost, len(q.stack)-q.head)
	}
	for i := 0; i < 32; i++ {
		if got := q.stack[len(q.stack)-1-i].index; got != i {
			t.Fatalf("fork order%d=%d", i, got)
		}
	}
	q.remaining = 1
	q.push([]functionAction{{cost: 1, index: 9999}})
	if q.pendingCost != 1 || len(q.stack)-q.head != 1 || q.stack[len(q.stack)-1].index != 9999 {
		t.Fatal("recursive child did not displace unreachable parent/fork work")
	}
}
