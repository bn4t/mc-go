//go:build blackbox

package blackbox

import (
	"fmt"
	"testing"
	"time"
)

func TestCrystalSummonTagsParity(t *testing.T) {
	const actor = "CrystalTagRead"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(actor)}, nil)
	defer targets.cleanup()
	run := func(addr string) []string {
		c, pr, pw := joinPlayBlackbox(t, addr, actor)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		command := func(cmd string) []string {
			serial++
			mark := fmt.Sprintf("CRYSTAL_TAG_RECEIPT_%d", serial)
			sendCommand(t, pw, cmd)
			sendCommand(t, pw, "say "+mark)
			return waitForExactSayBB(t, sink, mark, 30*time.Second)
		}
		command("gamemode creative @s")
		command("tp @s 4.0 165.0 4.0")
		command("tick freeze")
		command(`summon end_crystal 0.5 165.0 0.5 {Tags:["CrystalFirst","CrystalSecond","CrystalFirst"],Invulnerable:1b}`)
		var out []string
		check := func(name, condition string) {
			mark := "CRYSTAL_TAG_" + name
			lines := command("execute " + condition + " run say " + mark)
			valid := structureSaidBB(lines, mark)
			out = append(out, fmt.Sprintf("%s:%t", name, valid))
			if !valid {
				t.Errorf("%s %s missing tag witness", addr, name)
			}
		}
		check("SUMMON_FIRST", "if entity @e[type=end_crystal,tag=CrystalFirst]")
		check("SUMMON_SECOND", "if entity @e[type=end_crystal,tag=CrystalSecond]")
		check("SUMMON_NBT", `if entity @e[type=end_crystal,nbt={Tags:["CrystalFirst","CrystalSecond"]}]`)
		command("tag @e[type=end_crystal,limit=1] remove CrystalFirst")
		check("REMOVE_ALL_DUPLICATES", "unless entity @e[type=end_crystal,tag=CrystalFirst]")
		check("RETAIN_SECOND", "if entity @e[type=end_crystal,tag=CrystalSecond]")
		command(`data merge entity @e[type=end_crystal,limit=1] {Tags:["CrystalReload"]}`)
		check("RELOAD_NEW", "if entity @e[type=end_crystal,tag=CrystalReload]")
		check("RELOAD_REPLACE", "unless entity @e[type=end_crystal,tag=CrystalSecond]")
		command("data remove entity @e[type=end_crystal,limit=1] Tags")
		check("REMOVE_LIST", "unless entity @e[type=end_crystal,tag=CrystalReload]")
		return out
	}
	v, g := run(targets.vanilla), run(targets.mcgo)
	if fmt.Sprint(v) != fmt.Sprint(g) {
		t.Errorf("crystal tags vanilla%v mcgo%v", v, g)
	}
}
