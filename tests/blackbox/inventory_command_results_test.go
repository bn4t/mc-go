//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"testing"
)

func TestInventoryCommandResultsParity(t *testing.T) {
	names := []string{"InvResultV", "InvResultM"}
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(names...)}, nil)
	defer tg.cleanup()
	run := func(addr, name string) {
		d := newMenuLifecycleObserver(t, addr, name)
		command := func(c string) {
			sendCommand(t, d.pw, c)
			settleUse(t, d.sink, func([]usePkt) {}, "inventory-result-command")
		}
		command("scoreboard objectives add native_result dummy")
		command("scoreboard objectives add native_success dummy")
		command("give @s minecraft:stone 8")
		command("recipe take @s minecraft:oak_boat")
		cases := []struct {
			command         string
			result, success int
		}{
			{"clear @s minecraft:stone 0", 8, 1},
			{"clear @s minecraft:stone 3", 3, 1},
			{"clear @s minecraft:stone 0", 5, 1},
			{"clear @s minecraft:diamond 0", 0, 0},
			{"clear @s minecraft:stone", 5, 1},
			{"clear @s minecraft:stone", 0, 0},
			{"recipe give @s minecraft:oak_boat", 1, 1},
			{"recipe give @s minecraft:oak_boat", 0, 0},
			{"recipe take @s minecraft:oak_boat", 1, 1},
			{"recipe take @s minecraft:oak_boat", 0, 0},
		}
		for i, c := range cases {
			command("scoreboard players set @s native_result -1")
			command("scoreboard players set @s native_success -1")
			command("execute store result score @s native_result store success score @s native_success run " + c.command)
			for _, probe := range []struct {
				objective string
				want      int
			}{{"native_result", c.result}, {"native_success", c.success}} {
				yes := fmt.Sprintf("INV_RESULT_Y_%d_%s", i, probe.objective)
				sendCommand(t, d.pw, fmt.Sprintf("execute if score @s %s matches %d run say %s", probe.objective, probe.want, yes))
				sendCommand(t, d.pw, fmt.Sprintf("execute unless score @s %s matches %d run say INV_RESULT_NO", probe.objective, probe.want))
				got := false
				settleUse(t, d.sink, func(ps []usePkt) {
					for _, p := range ps {
						if bytes.Contains(p.data, []byte(yes)) {
							got = true
						}
					}
				}, "inventory-result-probe")
				if !got {
					t.Errorf("%s %s: %s wanted%d", name, c.command, probe.objective, probe.want)
				}
			}
		}
	}
	run(tg.vanilla, names[0])
	run(tg.mcgo, names[1])
}
