package blackbox

import (
	"fmt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

func TestPlayerAttackAttributeParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
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
		var playTicks int32
		havePlayTicks := false
		statsPackets := 0
		cow, _ := server.EntityTypeID("cow")
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("ATTACK_ATTR_BARRIER%d", serial)
			sendCommand(t, pw, "say "+marker)
			lines := waitForExactSayBB(t, sink, marker, 15*time.Second)
			for _, line := range lines {
				prefix, body, ok := strings.Cut(line, ":")
				if ok && prefix == "0x3" {
					statsPackets++
					r := protocol.NewPacketReader([]byte(body))
					count, _ := r.VarInt()
					for n := int32(0); n < count; n++ {
						cat, _ := r.VarInt()
						id, _ := r.VarInt()
						value, err := r.VarInt()
						if err == nil && cat == 8 && id == server.CustomStatIDs["play_time"] {
							playTicks = value
							havePlayTicks = true
						}
					}
				}
				if !ok || prefix != fmt.Sprintf("%#x", server.IDPlaySpawnEntity) {
					continue
				}
				r := protocol.NewPacketReader([]byte(body))
				id, _ := r.VarInt()
				r.Read(16)
				typ, _ := r.VarInt()
				if typ == cow {
					eid = id
				}
			}
			return lines
		}
		playerTime := func() int32 {
			before := statsPackets
			requestStats := func() {
				var request protocol.Buffer
				request.VarInt(1)
				if err := pw.WritePacket(server.IDPlayClientStatusSB, request.Bytes()); err != nil {
					t.Fatal(err)
				}
			}
			requestStats()
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				barrier()
				if statsPackets > before {
					if havePlayTicks {
						return playTicks
					}
					before = statsPackets
					requestStats()
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatalf("%s player time absent after actual statistics response wait", leg.name)
			return 0
		}
		charge := func() {
			before := playerTime()
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				if playerTime()-before >= 32 {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("player cooldown did not advance")
		}
		sendCommand(t, pw, "tick unfreeze")
		sendCommand(t, pw, "tp @s 8.5 -60 6.5")
		sendCommand(t, pw, "scoreboard objectives add attack_result dummy")
		barrier()
		cases := []struct {
			base                   int
			weapon                 string
			add, mulBase, mulTotal bool
		}{{10, "air", false, false, false}, {100, "air", false, false, false}, {10, "stone_sword", false, false, false}, {10, "air", true, false, false}, {10, "air", true, true, false}, {10, "air", true, true, true}, {10, "stone_sword", true, true, true}, {10, "copper_sword", false, false, false}, {10, "copper_axe", false, false, false}, {10, "netherite_hoe", false, false, false}}
		for index, tc := range cases {
			sendCommand(t, pw, "kill @e[tag=attack_attr]")
			sendCommand(t, pw, "kill @e[type=item]")
			for _, id := range []string{"add", "base", "total"} {
				sendCommand(t, pw, "attribute @s minecraft:attack_damage modifier remove test:"+id)
			}
			sendCommand(t, pw, fmt.Sprintf("attribute @s minecraft:attack_damage base set %d", tc.base))
			sendCommand(t, pw, "item replace entity @s weapon.mainhand with "+tc.weapon)
			if tc.add {
				sendCommand(t, pw, "attribute @s minecraft:attack_damage modifier add test:add 2 add_value")
			}
			if tc.mulBase {
				sendCommand(t, pw, "attribute @s minecraft:attack_damage modifier add test:base 0.5 add_multiplied_base")
			}
			if tc.mulTotal {
				sendCommand(t, pw, "attribute @s minecraft:attack_damage modifier add test:total 1 add_multiplied_total")
			}
			eid = 0
			sendCommand(t, pw, `summon cow 8.5 -60 8.5 {Tags:["attack_attr"],NoAI:1b,NoGravity:1b,attributes:[{id:"minecraft:max_health",base:200.0}],Health:200f}`)
			barrier()
			trackingDeadline := time.Now().Add(15 * time.Second)
			for eid == 0 && time.Now().Before(trackingDeadline) {
				barrier()
				time.Sleep(20 * time.Millisecond)
			}
			if eid == 0 {
				t.Fatalf("%s cow not tracked", leg.name)
			}
			sendCommand(t, pw, `execute if data entity @e[tag=attack_attr,limit=1] {Health:200f} run say ATTACK_HEALTH_READY`)
			if !structureSaidBB(barrier(), "ATTACK_HEALTH_READY") {
				t.Fatalf("%s target health not200", leg.name)
			}
			charge()
			sendAttackEntity(pw, eid)
			barrier()
			damaged := false
			for attempts := 0; !damaged && attempts < 20; attempts++ {
				sendCommand(t, pw, `execute if entity @e[tag=attack_attr] unless data entity @e[tag=attack_attr,limit=1] {Health:200f} run say ATTACK_DAMAGED`)
				damaged = structureSaidBB(barrier(), "ATTACK_DAMAGED")
			}
			if !damaged {
				t.Fatalf("%s attack never changed target health", leg.name)
			}
			sendCommand(t, pw, "execute store result score hp attack_result run data get entity @e[tag=attack_attr,limit=1] Health 1000")
			sendCommand(t, pw, "scoreboard players get hp attack_result")
			found := false
			for _, line := range barrier() {
				key, args, ok := componentArgs(line)
				if ok && key == "commands.scoreboard.players.get.success" && len(args) > 1 {
					value := args[1]
					if wrapped, ok := value.(map[string]any); ok {
						value = wrapped[""]
					}
					actual := int64(0)
					valid := true
					switch number := value.(type) {
					case int64:
						actual = number
					case int32:
						actual = int64(number)
					case int:
						actual = int64(number)
					case float64:
						actual = int64(number)
					default:
						valid = false
					}
					expected := []int64{190000, 100000, 186000, 188000, 182000, 164000, 152000, 186000, 182000, 190000}[index]
					if !valid || actual != expected {
						t.Errorf("%s case%d health %v, want %d", leg.name, index, args[1], expected)
					}
					t.Logf("%s case%d base%d weapon%s add%v mulBase%v mulTotal%v healthMilli=%v", leg.name, index, tc.base, tc.weapon, tc.add, tc.mulBase, tc.mulTotal, args[1])
					found = true
				}
			}
			if !found {
				t.Fatalf("%s health result absent", leg.name)
			}
		}
		c.Close()
	}
}
