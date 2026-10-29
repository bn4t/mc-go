package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestBarrelNativeDataParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("BarrelNative")}, func(s *server.Server) { s.GameMode = server.GameModeCreative })
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, "BarrelNative")
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				sendCommand(t, pw, cmd)
				serial++
				marker := fmt.Sprintf("BARREL_NATIVE_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			assert := func(condition, label string) {
				marker := "BARREL_NATIVE_RESULT_" + label
				if !structureSaidBB(command("execute "+condition+" run say "+marker), marker) {
					t.Errorf("missing %s", label)
				}
			}
			command("tick freeze")
			command("setblock 4 -60 4 barrel")
			assert(`if block 4 -60 4 barrel{Items:[]}`, "empty")
			command(`data merge block 4 -60 4 {Items:[{Slot:0b,id:"minecraft:diamond",count:3},{Slot:1b,id:"minecraft:potion",count:1,components:{"minecraft:potion_contents":{potion:"minecraft:swiftness",custom_color:-1}}}]}`)
			pattern := `{Items:[{Slot:0b,id:"minecraft:diamond",count:3},{Slot:1b,id:"minecraft:potion",count:1,components:{"minecraft:potion_contents":{potion:"minecraft:swiftness",custom_color:-1}}}]}`
			assert("if block 4 -60 4 barrel"+pattern, "stored")
			command("clone 4 -60 4 4 -60 4 4 -60 8")
			assert("if block 4 -60 8 barrel"+pattern, "copied")
			command("data modify block 4 -60 4 Items[0].count set value 5")
			assert(`if block 4 -60 4 barrel{Items:[{Slot:0b,count:5}]}`, "modified")
			assert("if block 4 -60 8 barrel"+pattern, "copy_independent")
			command("data remove block 4 -60 4 Items[0]")
			assert(`unless block 4 -60 4 barrel{Items:[{Slot:0b}]}`, "removed")
			assert(`if block 4 -60 4 barrel{Items:[{Slot:1b,id:"minecraft:potion"}]}`, "retained")
		})
	}
}
