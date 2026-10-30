package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestXPOrbLiveDataParity(t *testing.T) {
	const actor = "OrbDataReader"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(actor)}, nil)
	defer targets.cleanup()
	run := func(addr string) {
		c, pr, pw := joinPlayBlackbox(t, addr, actor)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		command := func(cmd string) []string {
			serial++
			receipt := fmt.Sprintf("ORB_DATA_RECEIPT_%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+receipt)
			return waitForExactSayBB(t, sink, receipt, 30*time.Second)
		}
		query := func(sel, path string) (string, float64) {
			state, value := "nothing_found", float64(-1)
			for _, line := range command("data get entity " + sel + " " + path) {
				if i := strings.Index(line, "data.entity.query"); i >= 0 {
					state, value = "query", lastNumberSigned(line[:i])
				}
			}
			return state, value
		}
		command("gamemode creative @s")
		command("tp @s 12.0 175.0 12.0")
		command("tick freeze")
		for _, tc := range []struct {
			name, nbt                 string
			age, health, value, count float64
		}{
			{"default", "", 0, 5, 0, 1},
			{"configured", ",Age:17s,Health:4s,Value:7s,Count:3", 17, 4, 7, 3},
			{"narrow", ",Age:32768,Health:65535,Value:65536,Count:0", -32768, -1, 0, 1},
			{"zerohealth", ",Health:0s", 0, 0, 0, 1},
			{"wrappedzero", ",Health:65536,Count:1e40d", 0, 0, 0, 2147483647},
			{"floatcount", ",Count:3.9d", 0, 5, 0, 3},
			{"negativecount", ",Count:-1", 0, 5, 0, 1},
		} {
			command("kill @e[type=experience_orb]")
			command(fmt.Sprintf(`summon experience_orb 0.5 170.0 0.5 {Tags:["OrbData"],NoGravity:1b%s}`, tc.nbt))
			sel := "@e[type=experience_orb,tag=OrbData,limit=1]"
			for _, field := range []struct {
				name string
				want float64
			}{{"Age", tc.age}, {"Health", tc.health}, {"Value", tc.value}, {"Count", tc.count}} {
				state, value := query(sel, field.name)
				t.Logf("%s %s %s state%s value%v", addr, tc.name, field.name, state, value)
				if state != "query" || value != field.want {
					t.Errorf("%s %s %s state%s value%v want%v", addr, tc.name, field.name, state, value, field.want)
				}
			}
			command("data merge entity " + sel + " {Age:117s,Health:2s,Value:11s,Count:4}")
			for _, field := range []struct {
				name string
				want float64
			}{{"Age", 117}, {"Health", 2}, {"Value", 11}, {"Count", 4}} {
				state, value := query(sel, field.name)
				if state != "query" || value != field.want {
					t.Errorf("%s %s reload %s state%s value%v want%v", addr, tc.name, field.name, state, value, field.want)
				}
			}
			age := float64(117)
			for tick := 0; tick < 3 && age == 117; tick++ {
				shulkerStepTicks(t, pw, sink, 1)
				state, value := query(sel, "Age")
				if state != "query" {
					t.Errorf("%s %s missing live Age", addr, tc.name)
					break
				}
				age = value
			}
			if age != 118 {
				t.Errorf("%s %s live age%v want118", addr, tc.name, age)
			}
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
