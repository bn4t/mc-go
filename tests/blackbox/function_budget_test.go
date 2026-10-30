//go:build blackbox

package blackbox

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestFunctionBudgetParity(t *testing.T) {
	const name = "FunctionBudget"
	files := map[string]string{"ops.json": opsJSONFor(name)}
	directory := t.TempDir()
	bodies := map[string]string{
		"pack.mcmeta": `{"pack":{"description":"function budget","min_format":107,"max_format":107}}`,
		"data/budget/function/exec_run.mcfunction":       "execute run scoreboard players add value budget 1\n",
		"data/budget/function/exec_if.mcfunction":        "execute if score ready budget matches 1 run scoreboard players add value budget 1\n",
		"data/budget/function/exec_condition.mcfunction": "execute if score ready budget matches 1\nscoreboard players add value budget 10\n",
		"data/budget/function/exec_recur.mcfunction":     "scoreboard players add value budget 1\nexecute run function budget:exec_recur\nscoreboard players add tail budget 1\n",
		"data/budget/function/fork_fn.mcfunction":        "execute as @e[type=minecraft:armor_stand,tag=budgetactor] run function budget:child\n",
		"data/budget/function/flat.mcfunction":           "scoreboard players add value budget 1\nscoreboard players add value budget 10\nscoreboard players add value budget 100\n",
		"data/budget/function/child.mcfunction":          "scoreboard players add value budget 10\nscoreboard players add value budget 100\n",
		"data/budget/function/nested.mcfunction":         "scoreboard players add value budget 1\nfunction budget:child\nscoreboard players add value budget 1000\n",
		"data/budget/function/recur.mcfunction":          "scoreboard players add value budget 1\nfunction budget:recur\nscoreboard players add tail budget 1\n",
		"data/budget/function/failure.mcfunction":        "clear @s minecraft:diamond 0\nscoreboard players add value budget 10\n",
		"data/budget/function/fork.mcfunction":           "execute as @e[type=minecraft:armor_stand,tag=budgetactor] run scoreboard players add value budget 1\n",
		"data/budget/function/nested_group.mcfunction":   "scoreboard players add value budget 1000\nfunction #budget:group\nscoreboard players add value budget 10000\n",
		"data/budget/function/source.mcfunction":         "setblock ~ ~-1 ~ minecraft:stone\ntp @s 50 -58 50\nsetblock ~1 ~-1 ~ minecraft:diamond_block\ntp @s ~2 ~ ~\n",
		"data/budget/function/actor.mcfunction":          "tag @s add budgetseen\nexecute if entity @s[type=minecraft:armor_stand] run scoreboard players add source_count budget 1\n",
		"data/budget/function/actor_fork.mcfunction":     "execute as @e[type=minecraft:armor_stand,tag=budgetactor] at @s positioned ~1 ~ ~ run function budget:actor\n",
		"data/budget/tags/function/group.json":           `{"values":["budget:flat","budget:child"]}`,
	}
	for path, body := range bodies {
		rel := "datapacks/budget/" + path
		files["world/"+rel] = body
		local := filepath.Join(directory, rel)
		if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tg := requireBlackboxFiles(t, nil, files, func(s *server.Server) { s.WorldDir = directory })
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, name)
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				sendCommand(t, pw, cmd)
				serial++
				marker := fmt.Sprintf("BUDGET_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 30*time.Second)
			}
			expect := func(condition, label string) {
				marker := "BUDGET_RESULT_" + label
				if !structureSaidBB(command("execute "+condition+" run say "+marker), marker) {
					t.Errorf("%s: %s", label, condition)
				}
			}
			command("tick freeze")
			command("scoreboard objectives add budget dummy")
			command("tp @s 8 -58 8")
			command("function budget:source")
			expect("if block 8 -59 8 minecraft:stone", "source_first")
			expect("if block 9 -59 8 minecraft:diamond_block", "source_retained_after_teleport")
			expect("unless block 51 -59 50 minecraft:diamond_block", "source_not_rebound_to_executor")
			expect("if entity @s[x=10.5,y=-58,z=8.5,distance=..0.01]", "relative_teleport_retains_source")
			command("summon minecraft:armor_stand 8 -58 8 {Tags:[\"budgetactor\"],NoGravity:1b,Marker:1b}")
			command("summon minecraft:armor_stand 8 -58 9 {Tags:[\"budgetactor\"],NoGravity:1b,Marker:1b}")
			command("scoreboard players set source_count budget 0")
			command("function budget:actor_fork")
			expect("if score source_count budget matches 2", "queued_fork_executors")
			expect("unless entity @s[tag=budgetseen]", "fork_does_not_rebind_player")
			expect("unless entity @e[type=minecraft:armor_stand,tag=budgetactor,tag=!budgetseen]", "all_fork_sources_retained")
			command("scoreboard players set ready budget 1")
			limits := []int{0, 1, 2, 3, 4, 5, 6, 8}
			for _, control := range []struct {
				name   string
				values []int
			}{
				{"exec_run", []int{0, 0, 1, 1, 1, 1, 1, 1}},
				{"exec_if", []int{0, 0, 0, 1, 1, 1, 1, 1}},
				{"exec_condition", []int{0, 0, 0, 10, 10, 10, 10, 10}},
				{"exec_recur", []int{0, 0, 1, 1, 2, 2, 3, 4}},
				{"fork_fn", []int{0, 0, 0, 0, 10, 110, 110, 220}},
				{"flat", []int{0, 0, 1, 11, 111, 111, 111, 111}},
				{"nested", []int{0, 0, 1, 1, 11, 111, 1111, 1111}},
				{"recur", []int{0, 0, 1, 1, 2, 2, 3, 4}},
				{"failure", []int{0, 0, 0, 10, 10, 10, 10, 10}},
				{"#group", []int{0, 0, 1, 11, 111, 111, 121, 221}},
				{"nested_group", []int{0, 0, 1000, 1000, 1001, 1011, 1111, 1121}},
				{"fork", []int{0, 0, 0, 1, 2, 2, 2, 2}},
			} {
				for i, limit := range limits {
					command(fmt.Sprintf("gamerule minecraft:max_command_sequence_length %d", limit))
					command("scoreboard players set value budget 0")
					command("scoreboard players set tail budget 0")
					id := "budget:" + control.name
					if control.name == "#group" {
						id = "#budget:group"
					}
					command("function " + id)
					// Assertion execute chains need their own unrestricted context.
					command("gamerule minecraft:max_command_sequence_length 65536")
					expect(fmt.Sprintf("if score value budget matches %d", control.values[i]), fmt.Sprintf("value_%d", serial))
					expect("if score tail budget matches 0", fmt.Sprintf("tail_%d", serial))
				}
			}
			command("gamerule minecraft:max_command_sequence_length 65536")
			command("scoreboard players set value budget 0")
			command("scoreboard players set tail budget 0")
			command("function budget:recur")
			expect("if score value budget matches 32768", "default_budget_recursion")
			expect("if score tail budget matches 0", "default_budget_discards_tails")
		})
	}
}
