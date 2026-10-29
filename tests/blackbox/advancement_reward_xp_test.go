//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
)

func TestAdvancementRewardExperienceParity(t *testing.T) {
	names := []string{"AdvRewardV", "AdvRewardM"}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	run := func(addr, name string) []bool {
		d := newMenuLifecycleObserver(t, addr, name)
		command := func(c string) { sendCommand(t, d.pw, c); settleUse(t, d.sink, func([]usePkt) {}, "reward-command") }
		command("scoreboard objectives add reward_xp dummy")
		command("experience set @s 0 levels")
		command("experience set @s 0 points")
		var out []bool
		cases := []struct {
			command string
			total   int
		}{
			{"advancement grant @s only minecraft:adventure/bullseye", 50},
			{"advancement grant @s only minecraft:adventure/bullseye", 50},
			{"advancement revoke @s only minecraft:adventure/bullseye", 50},
			{"advancement grant @s only minecraft:adventure/bullseye bullseye", 100},
			{"advancement grant @s only minecraft:adventure/bullseye bullseye", 100},
			{"advancement grant @s only minecraft:nether/netherite_armor", 200},
		}
		for i, c := range cases {
			command(c.command)
			command("execute store result score @s reward_xp run data get entity @s XpTotal")
			yes := fmt.Sprintf("ADV_REWARD_Y_%d", i)
			sendCommand(t, d.pw, fmt.Sprintf("execute if score @s reward_xp matches %d run say %s", c.total, yes))
			sendCommand(t, d.pw, fmt.Sprintf("execute unless score @s reward_xp matches %d run say ADV_REWARD_NO", c.total))
			got := false
			settleUse(t, d.sink, func(ps []usePkt) {
				for _, p := range ps {
					if bytes.Contains(p.data, []byte(yes)) {
						got = true
					}
				}
			}, "reward-probe")
			out = append(out, got)
			if !got {
				t.Errorf("%s %s wanted XpTotal%d", name, c.command, c.total)
			}
		}
		return out
	}
	v, g := run(tg.vanilla, names[0]), run(tg.mcgo, names[1])
	if !reflect.DeepEqual(v, g) {
		t.Errorf("reward XP differs: vanilla%v mcgo%v", v, g)
	}
}
