//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCustomFunctionScheduleParity(t *testing.T) {
	const name = "FunctionSchedule"
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(name)}
	bodies := map[string]string{
		"pack.mcmeta":                                  `{"pack":{"description":"schedule pin","min_format":107,"max_format":107}}`,
		"data/schedulepin/function/a.mcfunction":       "scoreboard players add calls sched 1\nexecute if entity @s run scoreboard players add executor sched 1\nexecute store result score fired sched run time query gametime\nsummon minecraft:armor_stand ~ ~ ~ {Tags:[\"schedule_origin\"],Marker:1b}\n",
		"data/schedulepin/function/self.mcfunction":    "scoreboard players add self sched 1\nexecute if score self sched matches ..2 run schedule function schedulepin:self 1t append\n",
		"data/schedulepin/function/failure.mcfunction": "scoreboard players add before sched 1\nkill @e[type=minecraft:pig,tag=missing_schedule_target]\nscoreboard players add after sched 1\n",
	}
	for path, body := range bodies {
		rel := "datapacks/schedulepin/" + path
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
			sink := newChatSinkTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				sendCommand(t, pw, cmd)
				serial++
				marker := fmt.Sprintf("FUNCTION_SCHEDULE_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			expect := func(condition, label string) {
				marker := "FUNCTION_SCHEDULE_RESULT_" + label
				if !structureSaidBB(command("execute "+condition+" run say "+marker), marker) {
					t.Errorf("%s: %s", label, condition)
				}
			}
			step := func(n int) {
				before := gameTimeOf(t, pw, sink)
				command(fmt.Sprintf("tick step %d", n))
				deadline := time.Now().Add(20 * time.Second)
				for time.Now().Before(deadline) {
					if gameTimeOf(t, pw, sink) == before+int64(n) {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatalf("step%d did not finish", n)
			}
			command("tick freeze")
			command("scoreboard objectives add sched dummy")
			command("setworldspawn 32 -50 48")
			for _, holder := range []string{"calls", "executor", "before", "after"} {
				command("scoreboard players set " + holder + " sched 0")
			}
			command("execute store result score base sched run time query gametime")
			command("execute store result score queued sched run schedule function schedulepin:a 5t")
			command("scoreboard players operation queued sched -= base sched")
			expect("if score queued sched matches 5", "deadline_result")
			expect("if score calls sched matches 0", "frozen")
			step(4)
			expect("if score calls sched matches 0", "before_deadline")
			step(1)
			expect("if score calls sched matches 1", "at_deadline")
			expect("if score executor sched matches 0", "server_source")
			expect("if entity @e[type=minecraft:armor_stand,tag=schedule_origin,x=32,y=-50,z=48,distance=..0.01]", "server_spawn_position")
			command("scoreboard players operation fired sched -= base sched")
			expect("if score fired sched matches 5", "execution_age")
			command("schedule function schedulepin:a 3t append")
			command("schedule function schedulepin:a 3t append")
			command("schedule function schedulepin:a 4t append")
			command("execute store result score cleared sched run schedule clear schedulepin:a")
			expect("if score cleared sched matches 2", "deduplicated_clear_count")
			step(4)
			expect("if score calls sched matches 1", "clear_removed")
			command("schedule function schedulepin:a 1t append")
			command("schedule function schedulepin:a 2t append")
			command("schedule function schedulepin:a 3t replace")
			step(2)
			expect("if score calls sched matches 1", "replace_removed_earlier")
			step(1)
			expect("if score calls sched matches 2", "replace_fired")
			command("schedule function schedulepin:failure 1t")
			step(1)
			expect("if score before sched matches 1", "failure_before")
			expect("if score after sched matches 1", "failure_continues")
			command("execute store success score zero sched run schedule function schedulepin:a 0t")
			expect("if score zero sched matches 0", "zero_rejected")
			step(1)
			expect("if score calls sched matches 2", "zero_did_not_queue")
			command("schedule function schedulepin:a 0.075s")
			step(1)
			expect("if score calls sched matches 2", "fraction_not_early")
			step(1)
			expect("if score calls sched matches 3", "fraction_rounded")

			for _, probe := range []struct{ raw, key string }{{"0.0249s", "commands.schedule.same_tick"}, {"-0.025s", "commands.schedule.same_tick"}, {"-0.075s", "argument.time.tick_count_too_low"}, {"1e2t", "argument.time.invalid_unit"}, {"+1t", "parsing.float.expected"}, {"5x", "argument.time.invalid_unit"}, {"1..0t", "parsing.float.invalid"}} {
				found := false
				for _, line := range command("schedule function schedulepin:a " + probe.raw) {
					key, _, ok := componentArgs(line)
					if ok && key == probe.key {
						found = true
					}
				}
				if !found {
					t.Errorf("time %q missing error %s", probe.raw, probe.key)
				}
			}
			command("schedule function schedulepin:a 0.025s")
			step(1)
			expect("if score calls sched matches 4", "half_tick_rounds_up")
			command("scoreboard players set self sched 0")
			command("schedule function schedulepin:self 1t")
			step(3)
			expect("if score self sched matches 3", "self_reschedule")
		})
	}
}
