//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func executePlanFeedbackKeysBB(lines []string) []string {
	var keys []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if key, ok := x["translate"].(string); ok {
				keys = append(keys, key)
			}
			for _, child := range x {
				walk(child)
			}
		case nbt.List:
			for _, child := range x.Items {
				walk(child)
			}
		case []any:
			for _, child := range x {
				walk(child)
			}
		}
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, fmt.Sprintf("0x%x:", server.IDPlaySystemChatCB)) {
			continue
		}
		i := strings.IndexByte(line, ':')
		root, err := nbt.ReadUnnamed(strings.NewReader(line[i+1:]))
		if err == nil {
			walk(root)
		}
	}
	return keys
}
func TestExecutePlanBudgetParity(t *testing.T) {
	const name = "ExecutePlan"
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(name)}
	for path, body := range map[string]string{"pack.mcmeta": `{"pack":{"description":"execute plan","min_format":107,"max_format":107}}`, "data/budget/function/child.mcfunction": "scoreboard players add value budget 10\nscoreboard players add value budget 100\n", "data/budget/function/flat.mcfunction": "scoreboard players add value budget 1\nscoreboard players add value budget 10\nscoreboard players add value budget 100\n", "data/budget/tags/function/group.json": `{"values":["budget:flat","budget:child"]}`} {
		relative := "datapacks/budget/" + path
		files["world/"+relative] = body
		local := filepath.Join(directory, relative)
		if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	targets := requireBlackboxFiles(t, nil, files, func(s *server.Server) { s.WorldDir = directory })
	defer targets.cleanup()
	controls := []struct {
		command        string
		values, stored []int
	}{
		{"execute as @e[tag=budgetactor] run function budget:child", []int{0, 0, 0, 10, 110, 110, 120, 220, 220}, []int{}},
		{"execute as @e[tag=budgetactor] run function #budget:group", []int{0, 0, 0, 1, 11, 111, 111, 121, 221}, []int{}},
		{"execute as @e[tag=budgetactor] run scoreboard players add value budget 1", []int{0, 0, 1, 2, 2, 2, 2, 2, 2}, []int{}},
		{"execute run execute as @e[tag=budgetactor] run function budget:child", []int{0, 0, 0, 10, 110, 110, 120, 220, 220}, []int{}},
		{"execute as @e[tag=budgetactor] at @s run scoreboard players add value budget 1", []int{0, 0, 0, 1, 2, 2, 2, 2, 2, 2}, []int{}},
		{"execute if score ready budget matches 1 as @e[tag=budgetactor] run scoreboard players add value budget 1", []int{0, 0, 0, 1, 2, 2, 2, 2, 2, 2}, []int{}},
		{"execute as @e[tag=budgetactor] if entity @s run scoreboard players add value budget 1", []int{0, 0, 0, 1, 2, 2, 2, 2, 2, 2}, []int{}},
		{"execute positioned 0 -58 0 rotated 0 0 run scoreboard players add value budget 1", []int{0, 0, 0, 1, 1, 1, 1, 1, 1, 1}, []int{}},
		{"execute as @e[tag=budgetactor] at @s positioned ~1 ~ ~ run function budget:child", []int{0, 0, 0, 0, 0, 10, 110, 110, 120, 220}, []int{}},
		{"execute run function budget:child", []int{0, 0, 10, 110, 110}, []int{}},
		{"execute run scoreboard players add value budget 1", []int{1, 1, 1, 1, 1}, []int{}},
		{"execute run execute run scoreboard players add value budget 1", []int{1, 1, 1, 1, 1}, []int{}},
		{"execute if score ready budget matches 1 run scoreboard players add value budget 1", []int{0, 0, 1, 1, 1}, []int{}},
		{"execute store result score stored budget run scoreboard players add value budget 1", []int{0, 0, 1, 1, 1}, []int{99, 99, 1, 1, 1}},
		{"execute store result score stored budget if score ready budget matches 1", []int{0, 0, 0, 0, 0}, []int{99, 99, 1, 1, 1}},
		{"execute store success score stored budget if score ready budget matches 0", []int{0, 0, 0, 0, 0}, []int{99, 99, 0, 0, 0}},
		{"execute store result score stored budget as @e[tag=budgetactor] run scoreboard players add value budget 1", []int{0, 0, 0, 1, 2}, []int{99, 99, 99, 1, 2}},
		{"execute as @e[tag=budgetactor] store result score stored budget run scoreboard players add value budget 1", []int{0, 0, 0, 1, 2}, []int{99, 99, 99, 1, 2}},
		{"execute store success score stored budget as @e[type=minecraft:armor_stand,tag=budgetactor] run clear @a minecraft:diamond 0", []int{0, 0, 0, 0, 0}, []int{99, 99, 99, 0, 0}},
	}
	for _, target := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, name)
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			command := func(text string) []string {
				serial++
				sendCommand(t, pw, text)
				marker := fmt.Sprintf("EXEC_PLAN_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 15*time.Second)
			}
			score := func(holder string, want int, label string) {
				t.Helper()
				marker := "EXEC_PLAN_EXPECT_" + label
				if !structureSaidBB(command(fmt.Sprintf("execute if score %s budget matches %d run say %s", holder, want, marker)), marker) {
					t.Errorf("%s %s want%d", label, holder, want)
				}
			}
			command("tick freeze")
			command("scoreboard objectives add budget dummy")
			command("scoreboard players set ready budget 1")
			for i := 0; i < 2; i++ {
				command(fmt.Sprintf("summon minecraft:armor_stand %d -58 8 {Tags:[\"budgetactor\"],NoGravity:1b,Marker:1b}", 8+i))
			}
			for index, control := range controls {
				for limit, want := range control.values {
					command(fmt.Sprintf("gamerule minecraft:max_command_sequence_length %d", limit))
					command("scoreboard players set value budget 0")
					command("scoreboard players set stored budget 99")
					command(control.command)
					command("gamerule minecraft:max_command_sequence_length 65536")
					score("value", want, fmt.Sprintf("case%d_%d_value", index, limit))
					if len(control.stored) > 0 {
						score("stored", control.stored[limit], fmt.Sprintf("case%d_%d_stored", index, limit))
					}
				}
			}
			for _, control := range []struct {
				name, command string
				values        []int
				error         bool
			}{
				{"if_missing_objective", "execute if score value missing matches 0 run scoreboard players add value budget 1", []int{0, 0, 0, 0, 0}, false},
				{"unless_missing_objective", "execute unless score value missing matches 0 run scoreboard players add value budget 1", []int{0, 0, 0, 0, 0}, false},
				{"unless_missing_score", "execute unless score absent budget matches 0 run scoreboard players add value budget 1", []int{0, 0, 1, 1, 1}, false},
				{"store_before_missing_objective", "execute store success score stored budget unless score value missing matches 0 run scoreboard players add value budget 1", []int{0, 0, 0, 0, 0}, false},
				{"terminal_missing_objective", "execute unless score value missing matches 0", []int{0, 0, 0, 0, 0}, true},
			} {
				for limit, want := range control.values {
					command(fmt.Sprintf("gamerule minecraft:max_command_sequence_length %d", limit))
					command("scoreboard players set value budget 0")
					command("scoreboard players set stored budget 99")
					found := false
					for _, key := range executePlanFeedbackKeysBB(command(control.command)) {
						if key == "arguments.objective.notFound" {
							found = true
						}
					}
					if found != control.error {
						t.Errorf("%s limit%d objective error%v want%v", control.name, limit, found, control.error)
					}
					command("gamerule minecraft:max_command_sequence_length 65536")
					score("value", want, fmt.Sprintf("%s_%d_value", control.name, limit))
					score("stored", 99, fmt.Sprintf("%s_%d_stored", control.name, limit))
				}
			}
			for _, control := range []struct {
				name, command, key string
				present            bool
			}{{"unknown", "execute if entity @s run missing_command", "command.unknown.argument", true}, {"incomplete", "execute if entity @s run scoreboard players set", "command.unknown.command", true}, {"bad_integer", "execute if entity @s run scoreboard players set value budget nope", "parsing.int.expected", true}, {"unknown_function_modified", "execute if entity @s run function budget:absent", "arguments.function.unknown", false}, {"unknown_function_pure", "execute run function budget:absent", "arguments.function.unknown", true}} {
				for _, limit := range []int{0, 1, 2, 4} {
					command(fmt.Sprintf("gamerule minecraft:max_command_sequence_length %d", limit))
					command("scoreboard players set value budget 0")
					found := false
					for _, key := range executePlanFeedbackKeysBB(command(control.command)) {
						if key == control.key {
							found = true
						}
					}
					if found != control.present {
						t.Errorf("%s limit%d key%s present%v want%v", control.name, limit, control.key, found, control.present)
					}
					command("gamerule minecraft:max_command_sequence_length 65536")
					score("value", 0, fmt.Sprintf("syntax_%s_%d", control.name, limit))
				}
			}
		})
	}
}
