package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestCloneStrictPlacementParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("CloneStrict")}, func(s *server.Server) { s.GameMode = server.GameModeCreative })
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, "CloneStrict")
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				sendCommand(t, pw, cmd)
				serial++
				marker := fmt.Sprintf("CLONE_STRICT_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			assert := func(condition, label string) {
				marker := "CLONE_STRICT_RESULT_" + label
				if !structureSaidBB(command("execute "+condition+" run say "+marker), marker) {
					t.Errorf("missing %s: %s", label, condition)
				}
			}
			command("tick freeze")
			command("setblock 4 -60 4 redstone_block")
			command("setblock 12 -60 4 stone")
			assert("if block 4 -60 4 redstone_block", "power_source")
			assert("if block 12 -60 4 stone", "shape_source")
			for _, strict := range []bool{false, true} {
				mode := "replace"
				label := "normal"
				tnt := "air"
				entityCondition := "if"
				if strict {
					mode = "strict replace"
					label = "strict"
					tnt = "tnt"
					entityCondition = "unless"
				}
				command("setblock 4 -60 9 air")
				command("setblock 4 -60 8 air")
				command("kill @e[type=minecraft:tnt]")
				command("setblock 4 -60 9 tnt")
				command("clone 4 -60 4 4 -60 4 4 -60 8 " + mode)
				assert("if block 4 -60 8 redstone_block", label+"_power_placed")
				assert("if block 4 -60 9 "+tnt, label+"_neighbor")
				assert(entityCondition+" entity @e[type=minecraft:tnt]", label+"_entity")
				command("setblock 12 -60 4 stone")
				command("setblock 12 -60 8 air")
				command("setblock 13 -60 8 oak_fence")
				assert("if block 13 -60 8 oak_fence[west=false]", label+"_shape_precondition")
				command("clone 12 -60 4 12 -60 4 12 -60 8 " + mode)
				assert("if block 12 -60 8 stone", label+"_shape_placed")
				assert(fmt.Sprintf("if block 13 -60 8 oak_fence[west=%v]", !strict), label+"_shape_neighbor")
				command("setblock 13 -60 4 oak_fence")
				assert("if block 13 -60 4 oak_fence[west=true]", label+"_move_precondition")
				command("setblock 12 -60 12 air")
				command("clone 12 -60 4 12 -60 4 12 -60 12 " + mode + " move")
				assert("if block 12 -60 4 air", label+"_move_source")
				assert("if block 12 -60 12 stone", label+"_move_destination")
				assert(fmt.Sprintf("if block 13 -60 4 oak_fence[west=%v]", strict), label+"_move_neighbor")
			}
		})
	}
}
