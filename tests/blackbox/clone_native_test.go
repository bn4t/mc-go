package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestCloneNativeBlockEntitiesParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("CloneNative")}, func(s *server.Server) { s.GameMode = server.GameModeCreative })
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, "CloneNative")
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				sendCommand(t, pw, cmd)
				serial++
				marker := fmt.Sprintf("CLONE_NATIVE_BARRIER_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			assert := func(condition, label string) {
				marker := "CLONE_NATIVE_RESULT_" + label
				if !structureSaidBB(command("execute "+condition+" run say "+marker), marker) {
					t.Errorf("missing %s: %s", label, condition)
				}
			}
			command("tick freeze")
			for i, tc := range []struct{ block, tag, pattern string }{
				{"chest[facing=north]", `{CustomName:"Cargo",Items:[{Slot:0b,id:"minecraft:diamond",count:3},{Slot:1b,id:"minecraft:potion",count:1,components:{"minecraft:potion_contents":{potion:"minecraft:swiftness",custom_color:-1}}}]}`, `{CustomName:"Cargo",Items:[{Slot:0b,id:"minecraft:diamond",count:3},{Slot:1b,id:"minecraft:potion",count:1,components:{"minecraft:potion_contents":{potion:"minecraft:swiftness",custom_color:-1}}}]}`},
				{"oak_sign", `{is_waxed:1b,front_text:{messages:["Copied sign","","",""]}}`, `{is_waxed:1b,front_text:{messages:["Copied sign","","",""]}}`},
				{"structure_block[mode=save]", `{name:"minecraft:clone_probe",mode:"SAVE",posX:3,sizeX:2,author:"CloneAuthor"}`, `{name:"minecraft:clone_probe",mode:"SAVE",posX:3,sizeX:2,author:"CloneAuthor"}`},
				{"jigsaw", `{name:"minecraft:clone_probe",target:"minecraft:target",pool:"minecraft:empty",final_state:"minecraft:gold_block",joint:"aligned"}`, `{name:"minecraft:clone_probe",target:"minecraft:target",pool:"minecraft:empty",final_state:"minecraft:gold_block",joint:"aligned"}`},
				{"command_block", `{Command:"say CLONED_COMMAND",TrackOutput:1b,SuccessCount:7}`, `{Command:"say CLONED_COMMAND",TrackOutput:1b,SuccessCount:7}`},
			} {
				x := 4 + i*3
				dest := x
				command(fmt.Sprintf("setblock %d -60 4 %s", x, tc.block))
				command(fmt.Sprintf("data merge block %d -60 4 %s", x, tc.tag))
				assert(fmt.Sprintf("if block %d -60 4 %s%s", x, tc.block, tc.pattern), fmt.Sprintf("source%d", i))
				command(fmt.Sprintf("clone %d -60 4 %d -60 4 %d -60 8", x, x, dest))
				assert(fmt.Sprintf("if block %d -60 8 %s%s", dest, tc.block, tc.pattern), fmt.Sprintf("copied%d", i))
				assert(fmt.Sprintf("if block %d -60 4 %s%s", x, tc.block, tc.pattern), fmt.Sprintf("retained%d", i))
			}
			command("kill @e[type=minecraft:item]")
			command("clone 4 -60 4 4 -60 4 4 -60 12 filtered chest move")
			assert("if block 4 -60 4 air", "move_source_removed")
			assert(`if block 4 -60 12 chest{Items:[{Slot:0b,id:"minecraft:diamond",count:3},{Slot:1b,id:"minecraft:potion",components:{"minecraft:potion_contents":{potion:"minecraft:swiftness",custom_color:-1}}}]}`, "move_native_data")
			assert("unless entity @e[type=minecraft:item]", "move_no_spill")
			for x, item := range map[int]string{4: "diamond", 5: "apple"} {
				command(fmt.Sprintf("setblock %d -60 14 furnace", x))
				command(fmt.Sprintf(`data merge block %d -60 14 {Items:[{Slot:0b,id:"minecraft:%s",count:2}]}`, x, item))
				assert(fmt.Sprintf(`if block %d -60 14 furnace{Items:[{id:"minecraft:%s",count:2}]}`, x, item), fmt.Sprintf("overlap_source%d", x))
			}
			command("clone 4 -60 14 5 -60 14 5 -60 14 replace force")
			assert(`if block 4 -60 14 furnace{Items:[{id:"minecraft:diamond",count:2}]}`, "overlap_original")
			assert(`if block 5 -60 14 furnace{Items:[{id:"minecraft:diamond",count:2}]}`, "overlap_first")
			assert(`if block 6 -60 14 furnace{Items:[{id:"minecraft:apple",count:2}]}`, "overlap_snapshot")
			assert("unless entity @e[type=minecraft:item]", "overlap_no_spill")
		})
	}
}
