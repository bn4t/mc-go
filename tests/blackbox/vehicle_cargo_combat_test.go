package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

func TestVehicleCargoCombatParity(t *testing.T) {
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
		var eid int32
		var typeid int32
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("CARGO_COMBAT_BARRIER%d", serial)
			sendCommand(t, pw, "say "+marker)
			lines := waitForExactSayBB(t, sink, marker, 15*time.Second)
			for _, line := range lines {
				prefix, body, ok := strings.Cut(line, ":")
				if !ok || prefix != fmt.Sprintf("%#x", server.IDPlaySpawnEntity) {
					continue
				}
				r := protocol.NewPacketReader([]byte(body))
				id, _ := r.VarInt()
				r.Read(16)
				typ, _ := r.VarInt()
				if typ == typeid {
					eid = id
				}
			}
			return lines
		}
		sendCommand(t, pw, "tp @s 8.5 -60 6.5")
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "item replace entity @s weapon.mainhand with netherite_sword")
		barrier()
		for _, name := range []string{"chest_minecart", "hopper_minecart", "oak_chest_boat"} {
			for _, mode := range []string{"creative", "survival"} {
				for _, drops := range []bool{true, false} {
					sendCommand(t, pw, "kill @e[tag=cargo_combat]")
					sendCommand(t, pw, "kill @e[type=item]")
					sendCommand(t, pw, "gamemode "+mode+" @s")
					sendCommand(t, pw, fmt.Sprintf("gamerule minecraft:entity_drops %v", drops))
					barrier()
					typeid, _ = server.EntityTypeID(name)
					eid = 0
					sendCommand(t, pw, fmt.Sprintf(`summon %s 8.5 -60 8.5 {Tags:["cargo_combat"],NoGravity:1b,Items:[{Slot:0b,id:"minecraft:diamond",count:3}]}`, name))
					barrier()
					for attempts := 0; eid == 0 && attempts < 40; attempts++ {
						sendCommand(t, pw, "tick step 1")
						barrier()
					}
					if eid == 0 {
						t.Fatalf("%s %s target not tracked", leg.name, name)
					}
					sendCommand(t, pw, `execute if data entity @e[tag=cargo_combat,limit=1] {Items:[{Slot:0b,id:"minecraft:diamond",count:3}]} run say COMBAT_SOURCE_READY`)
					if !structureSaidBB(barrier(), "COMBAT_SOURCE_READY") {
						t.Fatalf("%s %s missing initial cargo", leg.name, name)
					}
					for hit := 0; hit < 5; hit++ {
						sendAttackEntity(pw, eid)
					}
					barrier()
					removed := false
					for attempts := 0; !removed && attempts < 20; attempts++ {
						sendCommand(t, pw, "execute unless entity @e[tag=cargo_combat] run say COMBAT_REMOVED")
						removed = structureSaidBB(barrier(), "COMBAT_REMOVED")
						if !removed {
							sendCommand(t, pw, "tick step 1")
							barrier()
						}
					}
					sendCommand(t, pw, `execute if entity @e[type=item,nbt={Item:{id:"minecraft:diamond",count:3}}] run say COMBAT_DIAMOND`)
					sendCommand(t, pw, fmt.Sprintf(`execute if entity @e[type=item,nbt={Item:{id:"minecraft:%s",count:1}}] run say COMBAT_VEHICLE`, name))
					lines := barrier()
					if !removed || !structureSaidBB(lines, "COMBAT_DIAMOND") || structureSaidBB(lines, "COMBAT_VEHICLE") != (mode == "survival" && drops) {
						t.Errorf("%s %s %s drops%v unexpected removal/cargo/vehicle", leg.name, name, mode, drops)
					}
					t.Logf("%s attack %s %s drops%v removed%v diamond%v vehicle%v", leg.name, name, mode, drops, removed, structureSaidBB(lines, "COMBAT_DIAMOND"), structureSaidBB(lines, "COMBAT_VEHICLE"))
				}
			}
		}
		c.Close()
	}
}
