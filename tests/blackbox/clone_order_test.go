package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestCloneHydrationOrderParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("CloneOrder")}, func(s *server.Server) { s.GameMode = server.GameModeCreative })
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, "CloneOrder")
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				sendCommand(t, pw, cmd)
				serial++
				marker := fmt.Sprintf("CLONE_ORDER_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			assert := func(condition, label string) {
				marker := "CLONE_ORDER_RESULT_" + label
				if !structureSaidBB(command("execute "+condition+" run say "+marker), marker) {
					t.Errorf("missing %s: %s", label, condition)
				}
			}
			command("tick freeze")
			for i := 0; i < 2; i++ {
				start := 4 + i*4
				controller, power := start+1, start
				if i == 1 {
					controller, power = start, start+1
				}
				name := fmt.Sprintf("mcgo:clone_order_%d", i)
				command(fmt.Sprintf("setblock %d -60 4 redstone_block", power))
				command(fmt.Sprintf("setblock %d -60 4 structure_block[mode=save]", controller))
				command(fmt.Sprintf(`data merge block %d -60 4 {name:"%s",mode:"SAVE",powered:0b,posX:0,posY:0,posZ:2,sizeX:1,sizeY:1,sizeZ:1,ignoreEntities:1b}`, controller, name))
				assert(fmt.Sprintf(`if block %d -60 4 structure_block{powered:0b,name:"%s"}`, controller, name), fmt.Sprintf("source%d", i))
				command(fmt.Sprintf("setblock %d -60 10 gold_block", controller))
				command(fmt.Sprintf("clone %d -60 4 %d -60 4 %d -60 8", start, start+1, start))
				assert(fmt.Sprintf(`if block %d -60 8 structure_block{powered:1b,name:"%s"}`, controller, name), fmt.Sprintf("latched%d", i))
				assert(fmt.Sprintf(`if block %d -60 4 structure_block{powered:0b,name:"%s"}`, controller, name), fmt.Sprintf("source_retained%d", i))
				command(fmt.Sprintf("setblock %d -60 10 snow_block", controller))
				command(fmt.Sprintf(`data merge block %d -60 8 {mode:"LOAD",powered:0b}`, controller))
				command(fmt.Sprintf("setblock %d -60 8 air", power))
				command(fmt.Sprintf("setblock %d -60 8 redstone_block", power))
				assert(fmt.Sprintf("if block %d -60 10 gold_block", controller), fmt.Sprintf("saved_then_loaded%d", i))
			}
		})
	}
}
