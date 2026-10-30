//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMobInvulnerableDamageParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
	})
	defer tg.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		c, r, w := joinPlayBlackbox(t, leg.addr, "P")
		defer c.Close()
		sendClientInformation(t, w)
		sink := newChatSinkResolvedTP(r, w)
		serial := 0
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("INVULN_END_%d", serial)
			sendCommand(t, w, "say "+marker)
			return waitForExactSayBB(t, sink, marker, 15*time.Second)
		}
		command := func(cmd string) []string { sendCommand(t, w, cmd); return barrier() }
		read := func(target, path string) string {
			for _, line := range command("data get entity " + target + " " + path) {
				key, args, ok := componentArgs(line)
				if ok && key == "commands.data.entity.query" && len(args) == 2 {
					return mobKBComponentTextBB(args[1])
				}
			}
			t.Fatal("missing data", target, path)
			return ""
		}
		command("tick freeze")
		command("scoreboard objectives add ivs dummy")
		command("scoreboard objectives add ivr dummy")
		for i, tc := range []struct {
			name, mode, kind, source string
			flag                     bool
			want                     float64
		}{
			{"ordinary", "survival", "generic", "", false, 95},
			{"invulnerable", "survival", "generic", "", true, 100},
			{"kill_bypass", "survival", "generic_kill", "", true, 95},
			{"void_bypass", "survival", "out_of_world", "", true, 95},
			{"survival_source", "survival", "player_attack", " by @s", true, 100},
			{"creative_source", "creative", "player_attack", " by @s", true, 95},
			{"creative_generic", "creative", "generic", " by @s", true, 95},
			{"spectator_source", "spectator", "player_attack", " by @s", true, 100},
		} {
			command("gamemode " + tc.mode + " @s")
			marker := fmt.Sprintf("INVULN_MODE_%d", i)
			commandLines := command("execute if entity @s[gamemode=" + tc.mode + "] run say " + marker)
			if !structureSaidBB(commandLines, marker) {
				t.Fatal("mode preparation", leg.name, tc.name)
			}
			tag := fmt.Sprintf("invuln_%d", i)
			target := "@e[tag=" + tag + ",limit=1]"
			command(fmt.Sprintf("summon villager 0.5 -60 3.5 {Tags:[\"%s\"],NoAI:1b,NoGravity:1b}", tag))
			command("attribute " + target + " max_health base set 100")
			flag := "0b"
			if tc.flag {
				flag = "1b"
			}
			command("data merge entity " + target + " {Health:100f,Invulnerable:" + flag + "}")
			if read(target, "Health") != "100.0f" && read(target, "Health") != "100f" {
				t.Fatal("health preparation", leg.name, tc.name)
			}
			if got := read(target, "Invulnerable"); got != flag {
				t.Fatal("flag preparation", got, flag)
			}
			command("scoreboard players set P ivs -1")
			command("scoreboard players set P ivr -1")
			replies := command("execute store success score P ivs store result score P ivr run damage " + target + " 5 " + tc.kind + tc.source)
			value := read(target, "Health")
			health, err := strconv.ParseFloat(strings.TrimSuffix(value, "f"), 64)
			if err != nil {
				t.Fatal(value, err)
			}
			keys := []string{}
			for _, line := range replies {
				key, _, ok := componentArgs(line)
				if ok {
					keys = append(keys, key)
				}
			}
			t.Logf("%s %s health=%g replies=%v", leg.name, tc.name, health, keys)
			expectedKey := "commands.damage.success"
			result := 1
			if tc.want == 100 {
				expectedKey = "commands.damage.invulnerable"
				result = 0
			}
			found := false
			for _, key := range keys {
				if key == expectedKey {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s %s missing reply %s: %v", leg.name, tc.name, expectedKey, keys)
			}
			resultMarker := fmt.Sprintf("INVULN_RESULT_%d", i)
			resultLines := command(fmt.Sprintf("execute if score P ivs matches %d if score P ivr matches %d run say %s", result, result, resultMarker))
			if !structureSaidBB(resultLines, resultMarker) {
				t.Fatal("stored result", leg.name, tc.name, result)
			}
			if health != tc.want {
				t.Fatalf("native %s health%g want%g", tc.name, health, tc.want)
			}
		}
	}
}
