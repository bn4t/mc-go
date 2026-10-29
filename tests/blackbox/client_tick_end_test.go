package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestClientTickEndMovementParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{"ops.json": opsJSONFor("TickEnd")}, func(s *server.Server) { s.GameMode = server.GameModeSurvival })
	defer tg.cleanup()
	for _, tc := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "TickEnd")
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
				marker := fmt.Sprintf("CTE_DONE_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForMarker(t, sink, marker, 10*time.Second)
			}
			command := func(cmd string) { sendCommand(t, pw, cmd); barrier() }
			check := func(condition string) {
				serial++
				marker := fmt.Sprintf("CTE_PASS_%d", serial)
				sendCommand(t, pw, "execute "+condition+" run say "+marker)
				if !markerSeen(barrier(), marker) {
					t.Errorf("failed %s", condition)
				}
			}
			move := func(x float64) {
				var b protocol.Buffer
				b.Double(x)
				b.Double(-60)
				b.Double(.5)
				b.Byte(1)
				if err := pw.WritePacket(server.IDPlayMovePosSB, b.Bytes()); err != nil {
					t.Fatal(err)
				}
			}
			command("tick freeze")
			command("item replace entity @s weapon.mainhand with minecraft:stone_sword")
			command("attribute @s minecraft:attack_speed base set 1024")
			for phase := 0; phase < 5; phase++ {
				command("kill @e[tag=tick_end_pin]")
				command("tp @s 0.5 -60 0.5 -90 0")
				time.Sleep(120 * time.Millisecond)
				barrier()
				sendCommand(t, pw, `summon minecraft:pig 2.4 -60 0.5 {Tags:["tick_end_pin","tick_end_main"],NoAI:1b,NoGravity:1b}`)
				lines := barrier()
				var target int32
				for _, line := range lines {
					prefix, raw, ok := strings.Cut(line, ":")
					if !ok {
						continue
					}
					id, err := strconv.ParseInt(prefix, 0, 32)
					if err != nil || int32(id) != server.IDPlaySpawnEntity {
						continue
					}
					r := protocol.NewPacketReader([]byte(raw))
					eid, err := r.VarInt()
					if err == nil {
						target = eid
					}
				}
				if target == 0 {
					t.Fatal("primary pig spawn missing")
				}
				command(`summon minecraft:pig 2.4 -60 1.5 {Tags:["tick_end_pin","tick_end_neighbor"],NoAI:1b,NoGravity:1b}`)
				move(.5)
				move(.8)
				ends := phase
				if ends > 2 {
					ends = 1
				}
				for i := 0; i < ends; i++ {
					if err := pw.WritePacket(server.IDPlayClientTickEndSB, nil); err != nil {
						t.Fatal(err)
					}
				}
				if phase == 3 {
					var b protocol.Buffer
					b.Float(-90)
					b.Float(0)
					b.Byte(1)
					if err := pw.WritePacket(server.IDPlayMoveRotSB, b.Bytes()); err != nil {
						t.Fatal(err)
					}
				}
				if phase == 4 {
					if err := pw.WritePacket(server.IDPlayMoveStatusSB, []byte{1}); err != nil {
						t.Fatal(err)
					}
				}
				barrier()

				var attack protocol.Buffer
				attack.VarInt(target)
				if err := pw.WritePacket(server.IDPlayAttackSB, attack.Bytes()); err != nil {
					t.Fatal(err)
				}
				barrier()
				time.Sleep(100 * time.Millisecond)
				check(`if data entity @e[tag=tick_end_main,limit=1] {Health:5.0f}`)
				health := "10.0f"
				if phase >= 2 {
					health = "9.0f"
				}
				check(`if data entity @e[tag=tick_end_neighbor,limit=1] {Health:` + health + `}`)
				t.Logf("phase%d endPackets%d neighborHealth%s", phase, ends, health)
			}
		})
	}
}
