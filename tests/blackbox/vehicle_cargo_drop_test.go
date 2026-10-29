package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestVehicleCargoKilledDropParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
	})
	defer tg.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		c, pr, pw := joinPlayBlackbox(t, leg.addr, "P")
		_ = c.SetDeadline(time.Time{})
		sendClientInformation(t, pw)
		sink := newChatSinkTP(pr, pw)
		serial := 0
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("DROP_BARRIER%d", serial)
			sendCommand(t, pw, "say "+marker)
			lines := waitForExactSayBB(t, sink, marker, 15*time.Second)
			return lines
		}
		sendCommand(t, pw, "tp @s 0 -60 0")
		sendCommand(t, pw, "tick freeze")
		barrier()
		for _, name := range []string{"chest_minecart", "hopper_minecart", "oak_chest_boat"} {
			for _, drops := range []bool{true, false} {
				sendCommand(t, pw, "kill @e[type=item]")
				sendCommand(t, pw, "kill @e[tag=cargo_drop]")
				sendCommand(t, pw, fmt.Sprintf("gamerule minecraft:entity_drops %v", drops))
				barrier()
				sendCommand(t, pw, fmt.Sprintf(`summon %s 8 -60 8 {Tags:["cargo_drop"],NoGravity:1b,Items:[{Slot:0b,id:"minecraft:diamond",count:3}]}`, name))
				barrier()
				sendCommand(t, pw, `execute if data entity @e[tag=cargo_drop,limit=1] {Items:[{Slot:0b,id:"minecraft:diamond",count:3}]} run say DROP_INPUT_READY`)
				if !structureSaidBB(barrier(), "DROP_INPUT_READY") {
					t.Fatalf("%s %s missing source cargo", leg.name, name)
				}
				sendCommand(t, pw, "kill @e[tag=cargo_drop]")
				barrier()
				sendCommand(t, pw, `execute if entity @e[type=item,nbt={Item:{id:"minecraft:diamond",count:3}}] run say DROP_DIAMOND`)
				sendCommand(t, pw, fmt.Sprintf(`execute if entity @e[type=item,nbt={Item:{id:"minecraft:%s",count:1}}] run say DROP_VEHICLE`, name))
				lines := barrier()
				if !structureSaidBB(lines, "DROP_DIAMOND") || structureSaidBB(lines, "DROP_VEHICLE") {
					t.Errorf("%s %s entity_drops%v lost cargo or dropped vehicle", leg.name, name, drops)
				}
				t.Logf("%s kill %s drops%v diamond%v vehicle%v", leg.name, name, drops, structureSaidBB(lines, "DROP_DIAMOND"), structureSaidBB(lines, "DROP_VEHICLE"))
			}
		}
		c.Close()
	}
}
