package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

func TestClientTickEndVehicleSpearParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{"ops.json": opsJSONFor("TickEndSpear")}, func(s *server.Server) { s.GameMode = server.GameModeSurvival })
	defer tg.cleanup()
	for _, tc := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "TickEndSpear")
			defer c.Close()
			drainToJoinBlackbox(t, tc.name, pr)
			awaitPlayReadyBlackbox(t, tc.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			markerSeen := func(lines []string, marker string) bool {
				for _, line := range lines {
					if strings.HasPrefix(line, "0x21:") && strings.Contains(line, marker) {
						return true
					}
				}
				return false
			}
			barrier := func() []string {
				serial++
				marker := fmt.Sprintf("CTEV_DONE_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForMarker(t, sink, marker, 10*time.Second)
			}
			command := func(cmd string) { sendCommand(t, pw, cmd); barrier() }
			check := func(condition string) {
				serial++
				marker := fmt.Sprintf("CTEV_PASS_%d", serial)
				sendCommand(t, pw, "execute "+condition+" run say "+marker)
				if !markerSeen(barrier(), marker) {
					t.Errorf("failed %s", condition)
				}
			}
			release := func() {
				var b protocol.Buffer
				b.VarInt(5)
				b.Position(0, 0, 0)
				b.Byte(0)
				b.VarInt(0)
				if err := pw.WritePacket(server.IDPlayPlayerActionSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
			}
			command("tick freeze")
			command("attribute @s minecraft:attack_speed base set 1024")
			command("fill -2 -57 -2 6 -57 2 minecraft:stone")
			for ends := 1; ends <= 2; ends++ {
				release()
				command("kill @e[tag=tick_end_vehicle_pin]")
				command("tp @s 0.5 -60 0.5 -90 0")
				time.Sleep(120 * time.Millisecond)
				barrier()
				command(`summon minecraft:oak_boat 0.5 -60 0.5 {Tags:["tick_end_vehicle_pin","tick_end_vehicle_boat"],NoGravity:1b}`)
				command("ride @s mount @e[tag=tick_end_vehicle_boat,limit=1]")
				time.Sleep(150 * time.Millisecond)
				barrier()
				command(`summon minecraft:zombie 4.5 -60 0.5 {Tags:["tick_end_vehicle_pin","tick_end_vehicle_target"],NoAI:1b,NoGravity:1b,attributes:[{id:"minecraft:max_health",base:100.0d}],Health:100.0f}`)
				check(`if data entity @e[tag=tick_end_vehicle_target,limit=1] {Health:100.0f}`)
				command("item replace entity @s weapon.mainhand with minecraft:wooden_spear")
				time.Sleep(150 * time.Millisecond)
				barrier()
				var move protocol.Buffer
				move.Double(1.3)
				move.Double(-60)
				move.Double(.5)
				move.Float(-90)
				move.Float(0)
				move.Bool(true)
				if err := pw.WritePacket(server.IDPlayMoveVehicleSB, move.Bytes()); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < ends; i++ {
					if err := pw.WritePacket(server.IDPlayClientTickEndSB, nil); err != nil {
						t.Fatal(err)
					}
				}
				barrier()
				check(`if data entity @e[tag=tick_end_vehicle_boat,limit=1] {Pos:[1.3d,-60.0d,0.5d]}`)
				var use protocol.Buffer
				use.VarInt(0)
				use.VarInt(int32(ends))
				use.Float(-90)
				use.Float(0)
				if err := pw.WritePacket(server.IDPlayUseItemSB, use.Bytes()); err != nil {
					t.Fatal(err)
				}
				barrier()
				shulkerStepTicks(t, pw, sink, 20)
				time.Sleep(150 * time.Millisecond)
				barrier()
				condition := `unless data entity @e[tag=tick_end_vehicle_target,limit=1] {Health:100.0f}`
				if ends == 2 {
					condition = `if data entity @e[tag=tick_end_vehicle_target,limit=1] {Health:100.0f}`
				}
				check(condition)
				sendCommand(t, pw, "data get entity @e[tag=tick_end_vehicle_target,limit=1] Health")
				for _, line := range barrier() {
					if strings.Contains(line, "data.entity.query") {
						t.Logf("ends%d targetHealth%q", ends, line)
					}
				}
			}
			release()
		})
	}
}
