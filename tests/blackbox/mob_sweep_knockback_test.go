package blackbox

import (
	"encoding/binary"
	"fmt"
	"math"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMobSweepKnockbackParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
	})
	defer tg.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		c, r, w := joinPlayBlackbox(t, leg.addr, "P")
		defer c.Close()
		sendClientInformation(t, w)
		sink := newChatSinkResolvedTP(r, w)
		play := int32(0)
		statsPackets := 0
		havePlay := false
		serial := 0
		owner := int32(-1)
		spawns := map[[16]byte]int32{}
		barrier := func() []string {
			serial++
			marker := fmt.Sprintf("MOB_KB_%d", serial)
			sendCommand(t, w, "say "+marker)
			lines := waitForExactSayBB(t, sink, marker, 15*time.Second)
			for _, line := range lines {
				prefix, body, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				reader := protocol.NewPacketReader([]byte(body))
				if prefix == "0x3" {
					statsPackets++
					n, _ := reader.VarInt()
					for i := int32(0); i < n; i++ {
						cat, _ := reader.VarInt()
						id, _ := reader.VarInt()
						value, e := reader.VarInt()
						if e == nil && cat == 8 && id == server.CustomStatIDs["play_time"] {
							play = value
							havePlay = true
						}
					}
					continue
				}
				if prefix == fmt.Sprintf("%#x", server.IDPlaySpawnEntity) {
					id, e := reader.VarInt()
					uuid, e2 := reader.UUID()
					if e == nil && e2 == nil {
						spawns[uuid] = id
					}
				}
				if prefix == fmt.Sprintf("%#x", server.IDPlayJoinGame) {
					owner, _ = reader.Int()
				}
			}
			return lines
		}
		playerTime := func() int32 {
			before := statsPackets
			var request protocol.Buffer
			request.VarInt(1)
			w.WritePacket(server.IDPlayClientStatusSB, request.Bytes())
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				barrier()
				if statsPackets > before {
					if havePlay {
						return play
					}
					before = statsPackets
					w.WritePacket(server.IDPlayClientStatusSB, request.Bytes())
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("no actual player ticks")
			return 0
		}
		settle := func(n int32) {
			before := playerTime()
			deadline := time.Now().Add(15 * time.Second)
			for time.Now().Before(deadline) {
				if playerTime()-before >= n {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Fatal("player cooldown clock stalled")
		}
		sendCommand(t, w, "tick freeze")
		barrier()
		for index, tc := range []struct {
			name, kind, base, mod string
			attack, sprint        bool
		}{
			{"sweep_half", "pig", "0", "", true, false},
		} {
			target := "@e[tag=KB_" + tc.name + ",limit=1]"
			sendCommand(t, w, "tp @e[tag=KB] 40 -60 40")
			sendCommand(t, w, "tp @s 8 -60 8 180 0")
			sendCommand(t, w, fmt.Sprintf("summon minecraft:%s 8 -60 6 {Tags:[\"KB\",\"KB_%s\"],NoAI:1b,NoGravity:1b,UUID:[I;1,2,3,%d]}", tc.kind, tc.name, index+100))
			sendCommand(t, w, "summon minecraft:pig 9.3 -60 6.5 {Tags:[\"KBS\"],NoAI:1b,NoGravity:1b,UUID:[I;1,2,3,201]}")
			sendCommand(t, w, "attribute @e[tag=KBS,limit=1] knockback_resistance base set 0.5")
			sendCommand(t, w, "item replace entity @s weapon.mainhand with iron_sword")
			sendCommand(t, w, "execute if data entity @e[tag=KBS,limit=1] {Health:10f} run say SECONDARY_READY")
			sendCommand(t, w, "execute if items entity @s weapon.mainhand iron_sword run say SWORD_READY")
			prep := barrier()
			if !structureSaidBB(prep, "SECONDARY_READY") || !structureSaidBB(prep, "SWORD_READY") {
				t.Fatal("secondary/sword preparation", leg.name)
			}
			if tc.base != "" {
				sendCommand(t, w, "attribute "+target+" knockback_resistance base set "+tc.base)
			}
			if tc.mod != "" {
				sendCommand(t, w, "attribute "+target+" knockback_resistance modifier add test:kb "+tc.mod+" add_value")
			}
			sendCommand(t, w, "execute if entity "+target+" run say MOB_READY")
			if !structureSaidBB(barrier(), "MOB_READY") {
				t.Fatal("mob preparation failed", leg.name, tc.name)
			}
			sendCommand(t, w, "attribute "+target+" knockback_resistance get")
			before := barrier()
			if tc.attack {
				settle(14)
				var uuid [16]byte
				binary.BigEndian.PutUint32(uuid[0:4], 1)
				binary.BigEndian.PutUint32(uuid[4:8], 2)
				binary.BigEndian.PutUint32(uuid[8:12], 3)
				binary.BigEndian.PutUint32(uuid[12:16], uint32(index+100))
				deadline := time.Now().Add(15 * time.Second)
				for spawns[uuid] == 0 && time.Now().Before(deadline) {
					barrier()
				}
				if spawns[uuid] == 0 {
					t.Fatal("target was not tracked", leg.name, tc.name)
				}
				var move protocol.Buffer
				move.Double(8.5)
				move.Double(-60)
				move.Double(8.5)
				move.Byte(1)
				if err := w.WritePacket(server.IDPlayMovePosSB, move.Bytes()); err != nil {
					t.Fatal(err)
				}
				sendCommand(t, w, "data get entity @s Pos")
				sendCommand(t, w, "execute positioned 8 -60 8 if entity @s[distance=..1] run say ACTOR_READY")
				actor := barrier()

				if !structureSaidBB(actor, "ACTOR_READY") {
					t.Fatal("actor position outside attack arena", leg.name, tc.name)
				}
				sendCommand(t, w, "execute if data entity "+target+" {Health:10f} run say HEALTH_READY")
				if !structureSaidBB(barrier(), "HEALTH_READY") {
					t.Fatal("attack target health baseline", leg.name, tc.name)
				}
				if tc.sprint {
					if owner < 0 {
						t.Fatal("no owner id")
					}
					var sprint protocol.Buffer
					sprint.VarInt(owner)
					sprint.VarInt(1)
					sprint.VarInt(0)
					w.WritePacket(server.IDPlayPlayerCommandSB, sprint.Bytes())
				}
				var attack protocol.Buffer
				attack.VarInt(spawns[uuid])
				if err := w.WritePacket(server.IDPlayAttackSB, attack.Bytes()); err != nil {
					t.Fatal(err)
				}
			} else {
				sendCommand(t, w, "damage "+target+" 1 minecraft:mob_attack by P")
			}
			if tc.attack {
				applied := false
				deadline := time.Now().Add(5 * time.Second)
				for !applied && time.Now().Before(deadline) {
					sendCommand(t, w, "execute unless data entity "+target+" {Health:10f} run say MOB_HIT")
					applied = structureSaidBB(barrier(), "MOB_HIT")
				}
				if !applied {
					t.Fatal("actual attack did not damage target", leg.name, tc.name)
				}
			}
			sendCommand(t, w, "execute unless data entity @e[tag=KBS,limit=1] {Health:10f} run say SECONDARY_HIT")
			secondary := barrier()
			if !structureSaidBB(secondary, "SECONDARY_HIT") {
				t.Fatal("sweep did not damage secondary", leg.name)
			}
			assertAttribute := func(lines []string, want float64) {
				t.Helper()
				found := false
				for _, line := range lines {
					key, args, ok := componentArgs(line)
					if !ok || key != "commands.attribute.value.get.success" || len(args) != 3 {
						continue
					}
					value, ok := args[2].(map[string]any)
					if !ok {
						t.Fatal("attribute value", args[2])
					}
					v, err := strconv.ParseFloat(fmt.Sprint(value[""]), 64)
					if err != nil || v != want {
						t.Fatal("effective resistance", leg.name, v, want, err)
					}
					found = true
				}
				if !found {
					t.Fatal("attribute query missing", leg.name)
				}
			}
			assertAttribute(before, 0)
			sendCommand(t, w, "attribute @e[tag=KBS,limit=1] knockback_resistance get")
			assertAttribute(barrier(), .5)
			readData := func(selector, path string) string {
				sendCommand(t, w, "data get entity "+selector+" "+path)
				found := false
				var text string
				for _, line := range barrier() {
					key, args, ok := componentArgs(line)
					if !ok || key != "commands.data.entity.query" || len(args) != 2 {
						continue
					}
					if found {
						t.Fatal("duplicate data reply", path)
					}
					text = mobKBComponentTextBB(args[1])
					found = true
				}
				if !found {
					t.Fatal("missing data reply", selector, path)
				}
				return text
			}
			readMotion := func(selector string) [3]float64 {
				text := readData(selector, "Motion")
				parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(text, "["), "]"), ",")
				if !strings.HasPrefix(text, "[") || !strings.HasSuffix(text, "]") || len(parts) != 3 {
					t.Fatal("invalid Motion", text)
				}
				var motion [3]float64
				for i, part := range parts {
					value, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(part), "d"), 64)
					if err != nil {
						t.Fatal(err)
					}
					motion[i] = value
				}
				return motion
			}
			for _, victim := range []struct {
				selector string
				motion   [3]float64
				health   float64
			}{
				{"@e[tag=KBS,limit=1]", [3]float64{.037139068188825605, 0, -.29284767345229623}, 9},
				{target, [3]float64{0, 0, -.4000000059604645}, 4},
			} {
				motion := readMotion(victim.selector)
				// The off-axis secondary distinguishes hurt-then-facing composition
				// from the reversed order, which has the same horizontal magnitude.
				for axis, want := range victim.motion {
					if math.Abs(motion[axis]-want) > 1e-12 {
						t.Fatalf("%s %s Motion=%v want=%v", leg.name, victim.selector, motion, victim.motion)
					}
				}
				health, err := strconv.ParseFloat(strings.TrimSuffix(readData(victim.selector, "Health"), "f"), 64)
				if err != nil || health != victim.health {
					t.Fatal("sweep health", leg.name, victim.selector, health, victim.health, err)
				}
				t.Logf("%s %s Motion=%v Health=%g", leg.name, victim.selector, motion, health)
			}
		}
	}
}
