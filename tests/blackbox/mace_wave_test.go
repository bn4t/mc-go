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

func TestMaceWaveParity(t *testing.T) {
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "survival", "allow-flight": "true"}, map[string]string{"ops.json": opsJSONFor("P")}, func(s *server.Server) {
		s.GameMode = server.GameModeSurvival
		s.OpsRequired = true
		s.SetOpLevel("P", 4)
	})
	defer tg.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		c, r, w := joinPlayBlackbox(t, leg.addr, "P")
		defer c.Close()
		sendClientInformation(t, w)
		sink := newChatSinkResolvedTP(r, w)
		qc, qr, qw := joinPlayBlackbox(t, leg.addr, "Q")
		defer qc.Close()
		sendClientInformation(t, qw)
		observer := newChatSinkResolvedTP(qr, qw)
		actorID := int32(-1)
		sprintReceipts := 0
		sprinting := false
		observe := func(lines []string) {
			for _, line := range lines {
				prefix, body, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				reader := protocol.NewPacketReader([]byte(body))
				if prefix == fmt.Sprintf("%#x", server.IDPlaySpawnEntity) {
					id, e := reader.VarInt()
					uuid, e2 := reader.UUID()
					if e == nil && e2 == nil && uuid == offlineUUIDBlackbox("P") {
						actorID = id
					}
				}
				if prefix == fmt.Sprintf("%#x", server.IDPlayEntityMetadata) {
					id, e := reader.VarInt()
					index, e2 := reader.Byte()
					kind, e3 := reader.VarInt()
					if e == nil && e2 == nil && e3 == nil && id == actorID && index == 0 && kind == 0 {
						flags, e := reader.Byte()
						if e == nil {
							sprinting = flags&8 != 0
							sprintReceipts++
						}
					}
				}
			}
		}
		play := int32(0)
		statsPackets := 0
		havePlay := false
		serial := 0
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

			}
			observe(observer.take())
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
		sendCommand(t, w, "tp Q 30.5 -60 6.5")
		barrier()
		trackingDeadline := time.Now().Add(15 * time.Second)
		for actorID < 0 && time.Now().Before(trackingDeadline) {
			barrier()
		}
		if actorID < 0 {
			t.Fatal("observer did not track actor", leg.name)
		}
		readData := func(selector, path string) string {
			t.Helper()
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
		readNumber := func(selector, path string) float64 {
			text := readData(selector, path)
			value, err := strconv.ParseFloat(strings.TrimRight(text, "fd"), 64)
			if err != nil {
				t.Fatal("numeric data", path, text, err)
			}
			return value
		}
		readVector := func(selector, path string) [3]float64 {
			text := readData(selector, path)
			parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(text, "["), "]"), ",")
			if !strings.HasPrefix(text, "[") || !strings.HasSuffix(text, "]") || len(parts) != 3 {
				t.Fatal("invalid vector", path, text)
			}
			var vector [3]float64
			for i, part := range parts {
				value, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(part), "d"), 64)
				if err != nil {
					t.Fatal("vector component", text, err)
				}
				vector[i] = value
			}
			return vector
		}
		assertVector := func(name string, got, want [3]float64) {
			t.Helper()
			for i := range want {
				if math.IsNaN(got[i]) || math.IsInf(got[i], 0) || math.Abs(got[i]-want[i]) > 1e-12 {
					t.Fatalf("%s %s=%v want=%v", leg.name, name, got, want)
				}
			}
		}
		effective := func(selector string) float64 {
			sendCommand(t, w, "attribute "+selector+" knockback_resistance get")
			for _, line := range barrier() {
				key, args, ok := componentArgs(line)
				if !ok || key != "commands.attribute.value.get.success" || len(args) != 3 {
					continue
				}
				value, ok := args[2].(map[string]any)
				if !ok {
					t.Fatal("attribute component", args[2])
				}
				number, err := strconv.ParseFloat(fmt.Sprint(value[""]), 64)
				if err != nil {
					t.Fatal(err)
				}
				return number
			}
			t.Fatal("missing effective query")
			return 0
		}
		waitWitness := func(command, marker string) {
			t.Helper()
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				sendCommand(t, w, command+" run say "+marker)
				if structureSaidBB(barrier(), marker) {
					return
				}
			}
			t.Fatal("applied-state witness missing", leg.name, marker)
		}
		for index, tc := range []struct {
			name, resistance, modifier string
			x, y                       float64
			fall                       int
			motion, want               [3]float64
			effective, primaryHealth   float64
		}{
			{"default", "0", "", 10.5, -60, 4, [3]float64{}, [3]float64{1.0499999821186066, .699999988079071, 0}, 0, 80},
			{"half", "0.5", "", 10.5, -60, 4, [3]float64{}, [3]float64{.5249999910593033, .699999988079071, 0}, .5, 80},
			{"upper", "2", "", 10.5, -60, 4, [3]float64{}, [3]float64{}, 1, 80},
			{"lower", "-4", "", 10.5, -60, 4, [3]float64{}, [3]float64{3.1499999463558197, .699999988079071, 0}, -2, 80},
			{"modifier", "0", "-0.5", 10.5, -60, 4, [3]float64{}, [3]float64{1.5749999731779099, .699999988079071, 0}, -.5, 80},
			{"fall5", "0.5", "", 10.5, -60, 5, [3]float64{}, [3]float64{.5249999910593033, .699999988079071, 0}, .5, 78},
			{"fall6", "0.5", "", 10.5, -60, 6, [3]float64{}, [3]float64{1.0499999821186066, .699999988079071, 0}, .5, 76},
			{"vertical", "0.5", "", 10.5, -59, 4, [3]float64{}, [3]float64{.3956733022366206, .699999988079071, 0}, .5, 80},
			{"coincident", "0.5", "", 8.5, -60, 4, [3]float64{}, [3]float64{0, .699999988079071, 0}, .5, 80},
			{"radius", "0.5", "", 12, -60, 4, [3]float64{}, [3]float64{}, .5, 80},
			{"epsilon_below", "0.5", "", 8.500001, -60, 4, [3]float64{}, [3]float64{0, .699999988079071, 0}, .5, 80},
			{"epsilon_low", "0.5", "", 8.50001, -60, 4, [3]float64{}, [3]float64{1.224996479138434, .699999988079071, 0}, .5, 80},
			{"epsilon_high", "0.5", "", 8.501, -60, 4, [3]float64{}, [3]float64{1.224649979144335, .699999988079071, 0}, .5, 80},
			{"existing_velocity", "0.5", "", 10.5, -60, 4, [3]float64{.1, .2, .3}, [3]float64{.6249999910593033, .899999988079071, .3}, .5, 80},
		} {
			primaryTag, childTag := "mace_primary_"+tc.name, "mace_child_"+tc.name
			primary, child := "@e[tag="+primaryTag+",limit=1]", "@e[tag="+childTag+",limit=1]"
			sendCommand(t, w, "tp @e[tag=MACE_PRIMARY] 40 -60 40")
			sendCommand(t, w, "tp @e[tag=MACE_CHILD] 40 -60 40")
			sendCommand(t, w, fmt.Sprintf("tp @s 8.5 %d 6.5 180 0", -58+tc.fall))
			sendCommand(t, w, fmt.Sprintf("summon iron_golem 8.5 -60.0 6.5 {Tags:[\"MACE_PRIMARY\",\"%s\"],NoAI:1b,NoGravity:1b,UUID:[I;1,2,3,%d]}", primaryTag, index+100))
			sendCommand(t, w, fmt.Sprintf("summon pig %.9f %.9f 6.5 {Tags:[\"MACE_CHILD\",\"%s\"],NoAI:1b,NoGravity:1b,UUID:[I;1,2,3,%d],Motion:[%.17gd,%.17gd,%.17gd]}", tc.x, tc.y, childTag, index+201, tc.motion[0], tc.motion[1], tc.motion[2]))
			sendCommand(t, w, "attribute "+primary+" knockback_resistance base set 1")
			sendCommand(t, w, "attribute "+child+" knockback_resistance base set "+tc.resistance)
			if tc.modifier != "" {
				sendCommand(t, w, "attribute "+child+" knockback_resistance modifier add test:mace "+tc.modifier+" add_value")
			}
			sendCommand(t, w, "item replace entity @s weapon.mainhand with mace")
			waitWitness("execute if items entity @s weapon.mainhand mace", "MACE_READY")
			if readNumber(primary, "Health") != 100 || readNumber(child, "Health") != 10 {
				t.Fatal("fresh target health", leg.name, tc.name)
			}
			assertVector("prepared position", readVector(child, "Pos"), [3]float64{tc.x, tc.y, 6.5})
			assertVector("prepared velocity", readVector(child, "Motion"), tc.motion)
			settle(40)
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
				t.Fatal("primary not tracked", leg.name, tc.name)
			}
			var move protocol.Buffer
			move.Double(8.5)
			move.Double(-58)
			move.Double(6.5)
			move.Byte(0)
			if err := w.WritePacket(server.IDPlayMovePosSB, move.Bytes()); err != nil {
				t.Fatal(err)
			}
			waitWitness("execute positioned 8.5 -58 6.5 if entity @s[distance=..0.1]", "MACE_ACTOR_READY")
			if fall := readNumber("@s", "fall_distance"); fall != float64(tc.fall) {
				t.Fatal("applied fall distance", leg.name, tc.name, fall, tc.fall)
			}
			// Positive observer receipt establishes the noncritical input, rather
			// than inferring sprint from a command barrier or movement alone.
			beforeSprint := sprintReceipts
			for _, action := range []int32{2, 1} {
				var state protocol.Buffer
				state.VarInt(actorID)
				state.VarInt(action)
				state.VarInt(0)
				if err := w.WritePacket(server.IDPlayPlayerCommandSB, state.Bytes()); err != nil {
					t.Fatal(err)
				}
			}
			sprintDeadline := time.Now().Add(15 * time.Second)
			for (sprintReceipts <= beforeSprint || !sprinting) && time.Now().Before(sprintDeadline) {
				barrier()
			}
			if sprintReceipts <= beforeSprint || !sprinting {
				t.Fatal("actual sprint receipt missing", leg.name, tc.name)
			}
			var attack protocol.Buffer
			attack.VarInt(spawns[uuid])
			if err := w.WritePacket(server.IDPlayAttackSB, attack.Bytes()); err != nil {
				t.Fatal(err)
			}
			waitWitness("execute unless data entity "+primary+" {Health:100f}", "MACE_HIT")
			if health := readNumber(primary, "Health"); health != tc.primaryHealth {
				t.Fatal("smash damage", leg.name, tc.name, health, tc.primaryHealth)
			}
			if health := readNumber(child, "Health"); health != 10 {
				t.Fatal("wave damaged secondary", leg.name, tc.name, health)
			}
			if resistance := effective(child); resistance != tc.effective {
				t.Fatal("effective resistance", leg.name, tc.name, resistance, tc.effective)
			}
			assertVector(tc.name+" wave", readVector(child, "Motion"), tc.want)
			if fall := readNumber("@s", "fall_distance"); fall != 0 {
				t.Fatal("fall distance not reset", leg.name, tc.name, fall)
			}
			t.Logf("%s %s wave=%v primaryHealth=%g", leg.name, tc.name, tc.want, tc.primaryHealth)
		}
	}
}
