//go:build blackbox

package blackbox

import (
	"fmt"
	"testing"
	"time"
)

func TestExecutePlanPartialForksParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("PlanPartial")}, nil)
	defer targets.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, "PlanPartial")
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			command := func(text string) []string {
				serial++
				sendCommand(t, pw, text)
				marker := fmt.Sprintf("PLAN_PART_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			score := func(holder, objective string, want int, label string) {
				t.Helper()
				marker := "PLAN_PART_EXPECT_" + label
				if !structureSaidBB(command(fmt.Sprintf("execute if score %s %s matches %d run say %s", holder, objective, want, marker)), marker) {
					t.Errorf("%s %s/%s want%d", label, holder, objective, want)
				}
			}
			command("tick freeze")
			command("tp @s 8 -58 8")
			command("tag @s add partial")
			command("summon minecraft:armor_stand 10 -58 8 {Tags:[\"partial\"],NoGravity:1b,Marker:1b}")
			for _, objective := range []string{"hits", "out", "before", "near"} {
				command("scoreboard objectives add " + objective + " dummy")
			}
			command("give @s minecraft:diamond 1")
			command("execute as @e[tag=partial,sort=furthest] store success score @s out run clear @s minecraft:diamond 0")
			score("PlanPartial", "out", 1, "player_after_failed_first_fork")
			score("@e[tag=partial,type=minecraft:armor_stand,limit=1]", "out", 0, "stand_failure_callback")
			command("scoreboard players set value hits 0")
			command("execute as @e[tag=partial] if score value hits matches 0 run scoreboard players add value hits 1")
			score("value", "hits", 2, "all_conditions_before_leaf_changes")
			command("scoreboard players set value hits 0")
			command("scoreboard players set PlanPartial near 99")
			command("execute as @e[tag=partial,sort=furthest] at @s store success score @s before store result score @p[distance=..1] near run scoreboard players add value hits 1")
			score("value", "hits", 1, "surviving_store_context")
			score("PlanPartial", "before", 1, "player_callback")
			score("PlanPartial", "near", 1, "player_store_target")
			found := false
			for _, key := range executePlanFeedbackKeysBB(command("scoreboard players get @e[tag=partial,type=minecraft:armor_stand,limit=1] before")) {
				if key == "commands.scoreboard.players.get.null" {
					found = true
				}
			}
			if !found {
				t.Error("empty store target invoked an earlier callback")
			}
		})
	}
}
