package server

import (
	"fmt"
	"strconv"
	"testing"
)

func TestExecutePlanNativeBudgetStages(t *testing.T) {
	s, _ := randSim(t)
	source := &commandSourceFrame{actor: neutralCommandActor, dim: s.dimOf(neutralCommandActor), pos: [3]float64{8.5, -58, 8.5}, level: 4, display: literal("Server"), results: &commandExecutionResults{}}
	invoke := func(text string) reply {
		t.Helper()
		fields := splitCommandArgs(text)
		var known bool
		r := s.withCommandFrame(source, func() reply {
			out, ok := s.runCommandAt(neutralCommandActor, 4, fields[0], fields[1:])
			known = ok
			return out
		})
		if !known {
			t.Fatalf("unknown %s", text)
		}
		if s.functionExecution != nil || s.activeCommandSource != nil {
			t.Fatal("command execution context leaked")
		}
		return r
	}
	get := func(holder string) int {
		t.Helper()
		r := invoke("scoreboard players get " + holder + " budget")
		if !r.hasResult {
			t.Fatalf("missing %s", holder)
		}
		return r.result
	}
	invoke("scoreboard objectives add budget dummy")
	invoke("scoreboard players set ready budget 1")
	for i := 0; i < 2; i++ {
		r := invoke(fmt.Sprintf("summon minecraft:armor_stand %d -58 8 {Tags:[\"budgetactor\"],NoGravity:1b,Marker:1b}", 8+i))
		if r.fail != nil || r.outcome == commandFailed {
			t.Fatalf("summon %#v", r)
		}
	}
	s.functions = map[string][]string{"budget:child": {"scoreboard players add value budget 10", "scoreboard players add value budget 100"}, "budget:flat": {"scoreboard players add value budget 1", "scoreboard players add value budget 10", "scoreboard players add value budget 100"}}
	s.functionTags = map[string][]string{"budget:group": {"budget:flat", "budget:child"}}
	for _, control := range []struct {
		command string
		values  []int
	}{
		{"execute as @e[tag=budgetactor] run function budget:child", []int{0, 0, 0, 10, 110, 110, 120, 220, 220}},
		{"execute as @e[tag=budgetactor] run function #budget:group", []int{0, 0, 0, 1, 11, 111, 111, 121, 221}},
		{"execute as @e[tag=budgetactor] run scoreboard players add value budget 1", []int{0, 0, 1, 2, 2, 2, 2, 2, 2}},
		{"execute run execute as @e[tag=budgetactor] run function budget:child", []int{0, 0, 0, 10, 110, 110, 120, 220, 220}},
		{"execute as @e[tag=budgetactor] at @s run scoreboard players add value budget 1", []int{0, 0, 0, 1, 2, 2, 2, 2, 2, 2}},
		{"execute if score ready budget matches 1 as @e[tag=budgetactor] run scoreboard players add value budget 1", []int{0, 0, 0, 1, 2, 2, 2, 2, 2, 2}},
		{"execute as @e[tag=budgetactor] if entity @s run scoreboard players add value budget 1", []int{0, 0, 0, 1, 2, 2, 2, 2, 2, 2}},
		{"execute positioned 0 -58 0 rotated 0 0 run scoreboard players add value budget 1", []int{0, 0, 0, 1, 1, 1, 1, 1, 1, 1}},
		{"execute as @e[tag=budgetactor] at @s positioned ~1 ~ ~ run function budget:child", []int{0, 0, 0, 0, 0, 10, 110, 110, 120, 220}},
		{"execute run function budget:child", []int{0, 0, 10, 110, 110}},
		{"execute run scoreboard players add value budget 1", []int{1, 1, 1, 1, 1}},
		{"execute run execute run scoreboard players add value budget 1", []int{1, 1, 1, 1, 1}},
		{"execute if score ready budget matches 1 run scoreboard players add value budget 1", []int{0, 0, 1, 1, 1}},
	} {
		for limit, want := range control.values {
			invoke("gamerule minecraft:max_command_sequence_length " + strconv.Itoa(limit))
			invoke("scoreboard players set value budget 0")
			r := invoke(control.command)
			if r.fail != nil {
				t.Fatalf("parse %s: %#v", control.command, r)
			}
			if got := get("value"); got != want {
				t.Errorf("%s limit%d value%d want%d", control.command, limit, got, want)
			}
		}
	}
	for _, control := range []struct {
		command        string
		values, stored []int
	}{
		{"execute store result score stored budget run scoreboard players add value budget 1", []int{0, 0, 1, 1, 1}, []int{99, 99, 1, 1, 1}},
		{"execute store result score stored budget if score ready budget matches 1", []int{0, 0, 0, 0, 0}, []int{99, 99, 1, 1, 1}},
		{"execute store success score stored budget if score ready budget matches 0", []int{0, 0, 0, 0, 0}, []int{99, 99, 0, 0, 0}},
		{"execute store result score stored budget as @e[tag=budgetactor] run scoreboard players add value budget 1", []int{0, 0, 0, 1, 2}, []int{99, 99, 99, 1, 2}},
		{"execute as @e[tag=budgetactor] store result score stored budget run scoreboard players add value budget 1", []int{0, 0, 0, 1, 2}, []int{99, 99, 99, 1, 2}},
	} {
		for limit, want := range control.values {
			invoke("gamerule minecraft:max_command_sequence_length " + strconv.Itoa(limit))
			invoke("scoreboard players set value budget 0")
			invoke("scoreboard players set stored budget 99")
			invoke(control.command)
			if got := get("value"); got != want {
				t.Errorf("%s limit%d value%d want%d", control.command, limit, got, want)
			}
			if got := get("stored"); got != control.stored[limit] {
				t.Errorf("%s limit%d stored%d want%d", control.command, limit, got, control.stored[limit])
			}
		}
	}
	invoke("gamerule minecraft:max_command_sequence_length 65536")
	invoke("scoreboard players set value budget 0")
	invoke("execute as @e[tag=budgetactor] if score value budget matches 0 run scoreboard players add value budget 1")
	if got := get("value"); got != 2 {
		t.Fatalf("modifier batching observed leaf mutations: %d", got)
	}
	for _, control := range []struct {
		command string
		want    int
	}{
		{"execute store result score value budget store success score value budget run scoreboard players set value budget 9", 1},
		{"execute store success score value budget store result score value budget run scoreboard players set value budget 9", 9},
		{"execute as @e[tag=budgetactor] store result score value budget store success score value budget run scoreboard players add value budget 1", 1},
	} {
		invoke("scoreboard players set value budget 0")
		invoke(control.command)
		if got := get("value"); got != control.want {
			t.Errorf("callback order %s=%d want%d", control.command, got, control.want)
		}
	}
	for _, limit := range []int{0, 1, 2, 3, 4, 5, 6, 8} {
		invoke("gamerule minecraft:max_command_forks " + strconv.Itoa(limit))
		for _, control := range []struct {
			command string
			forks   int
		}{{"execute as @e[tag=budgetactor] run scoreboard players add value budget 1", 2}, {"execute as @e[tag=budgetactor] as @e[tag=budgetactor] run scoreboard players add value budget 1", 4}, {"execute positioned 0 -58 0 run scoreboard players add value budget 1", 1}} {
			invoke("scoreboard players set value budget 0")
			invoke(control.command)
			want := 0
			if limit > control.forks {
				want = control.forks
			}
			if got := get("value"); got != want {
				t.Errorf("forklimit%d %s=%d want%d", limit, control.command, got, want)
			}
		}
	}
	invoke("gamerule minecraft:max_command_forks 65536")
	for _, command := range []string{
		"execute if score value missing matches 0 run scoreboard players add value budget 1",
		"execute unless score value missing matches 0 run scoreboard players add value budget 1",
		"execute store success score stored budget unless score value missing matches 0 run scoreboard players add value budget 1",
	} {
		invoke("scoreboard players set value budget 0")
		invoke("scoreboard players set stored budget 99")
		invoke(command)
		if get("value") != 0 || get("stored") != 99 {
			t.Fatalf("unknown objective became condition success: %s", command)
		}
	}
	invoke("execute unless score absent budget matches 0 run scoreboard players add value budget 1")
	if get("value") != 1 {
		t.Fatal("missing score did not behave as false")
	}
	r := invoke("execute unless score value missing matches 0")
	if r.outcome != commandFailed {
		t.Fatal("terminal unknown objective did not fail")
	}
	invoke("gamerule minecraft:max_command_sequence_length 0")
	for _, command := range []string{"execute if score ready budget matches 1 run missing_command", "execute if score ready budget matches 1 run scoreboard players set", "execute if score ready budget matches 1 run scoreboard players set value budget nope"} {
		invoke("scoreboard players set value budget 0")
		r := invoke(command)
		if r.fail == nil {
			t.Errorf("exhausted budget hid syntax failure: %s", command)
		}
		if get("value") != 0 {
			t.Fatal("syntax failure mutated score")
		}
	}
}
