//go:build blackbox

package blackbox

import (
	"fmt"
	"testing"
	"time"
)

func TestExecutePlanForksAndCallbacksParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("PlanForks")}, nil)
	defer targets.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, "PlanForks")
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			command := func(text string) []string {
				serial++
				sendCommand(t, pw, text)
				marker := fmt.Sprintf("PLAN_FORK_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			score := func(want int, label string) {
				t.Helper()
				marker := "PLAN_FORK_EXPECT_" + label
				if !structureSaidBB(command(fmt.Sprintf("execute if score value cap matches %d run say %s", want, marker)), marker) {
					t.Errorf("%s value want%d", label, want)
				}
			}
			command("tick freeze")
			command("scoreboard objectives add cap dummy")
			for i := 0; i < 2; i++ {
				command(fmt.Sprintf("summon minecraft:armor_stand %d -58 8 {Tags:[\"capactor\"],NoGravity:1b,Marker:1b}", 8+i))
			}
			for _, limit := range []int{0, 1, 2, 3, 4, 5, 6, 8} {
				for _, control := range []struct {
					name, command string
					contexts      int
					forked        bool
				}{
					{"single", "execute as @e[tag=capactor] run scoreboard players add value cap 1", 2, true},
					{"double", "execute as @e[tag=capactor] as @e[tag=capactor] run scoreboard players add value cap 1", 4, true},
					{"filtered", "execute as @e[tag=capactor] if entity @s run scoreboard players add value cap 1", 2, true},
					{"at", "execute as @e[tag=capactor] at @s run scoreboard players add value cap 1", 2, true},
					{"self", "execute as @s run scoreboard players add value cap 1", 1, true},
					{"condition", "execute if entity @s run scoreboard players add value cap 1", 1, true},
					{"positioned", "execute positioned 0 -58 0 run scoreboard players add value cap 1", 1, false},
				} {
					command(fmt.Sprintf("gamerule minecraft:max_command_forks %d", limit))
					command("scoreboard players set value cap 0")
					found := false
					for _, key := range executePlanFeedbackKeysBB(command(control.command)) {
						if key == "command.forkLimit" {
							found = true
						}
					}
					want := 0
					if limit > control.contexts {
						want = control.contexts
					}
					wantError := want == 0 && !control.forked
					if found != wantError {
						t.Errorf("%s cap%d limit error%v want%v", control.name, limit, found, wantError)
					}
					command("gamerule minecraft:max_command_forks 65536")
					score(want, fmt.Sprintf("%s_%d", control.name, limit))
				}
			}
			for _, control := range []struct {
				name, command string
				want          int
				messages      int
			}{
				{"result_success", "execute store result score value cap store success score value cap run scoreboard players set value cap 9", 1, 1},
				{"success_result", "execute store success score value cap store result score value cap run scoreboard players set value cap 9", 9, 1},
				{"per_fork", "execute as @e[tag=capactor] store result score value cap store success score value cap run scoreboard players add value cap 1", 1, 2},
			} {
				command("scoreboard players set value cap 0")
				messages := 0
				for _, key := range executePlanFeedbackKeysBB(command(control.command)) {
					if key == "commands.scoreboard.players.set.success.single" || key == "commands.scoreboard.players.add.success.single" {
						messages++
					}
				}
				if messages != control.messages {
					t.Errorf("%s completed leaves%d want%d", control.name, messages, control.messages)
				}
				score(control.want, control.name)
			}
		})
	}
}
