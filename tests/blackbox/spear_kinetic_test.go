//go:build blackbox

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

func TestSpearKineticParity(t *testing.T) {
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
		handReceipts := 0
		handActive := false
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
					if e == nil && e2 == nil && e3 == nil && id == actorID && index == 8 && kind == 0 {
						flags, e := reader.Byte()
						if e == nil {
							handActive = flags&1 != 0
							handReceipts++
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
		targetID := int32(-1)
		armed := false
		released := false
		var impulses [][3]float64
		baselineMotion := [3]float64{}
		release := func() {
			var action protocol.Buffer
			action.VarInt(5)
			action.Position(0, 0, 0)
			action.Byte(0)
			action.VarInt(0)
			if err := w.WritePacket(server.IDPlayPlayerActionSB, action.Bytes()); err != nil {
				t.Fatal(err)
			}
		}
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
				if prefix == fmt.Sprintf("%#x", server.IDPlayEntityMotion) {
					id, e := reader.VarInt()
					x, y, z, e2 := reader.ReadLpVec3()
					if e == nil && e2 == nil && id == targetID && armed && (math.Abs(x-baselineMotion[0])+math.Abs(y-baselineMotion[1])+math.Abs(z-baselineMotion[2]) > 1e-3) {
						impulses = append(impulses, [3]float64{x, y, z})
						if !released {
							release()
							released = true
						}
					}
					continue
				}
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
		effective := func(selector, attr string) float64 {
			sendCommand(t, w, "attribute "+selector+" "+attr+" get")
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
		gameTime := func() int64 {
			sendCommand(t, w, "time query gametime")
			for _, line := range barrier() {
				key, args, ok := componentArgs(line)
				if !ok || key != "commands.time.query.gametime" || len(args) == 0 {
					continue
				}
				switch value := args[0].(type) {
				case int64:
					return value
				case int32:
					return int64(value)
				}
			}
			t.Fatal("game time reply absent")
			return 0
		}

		for index, tc := range []struct {
			name, base        string
			delta, attackBase float64
			motion, want      [3]float64
			health            float64
		}{
			{"zero", "0", .3, 1, [3]float64{}, [3]float64{0, 0, .4000000059604645}, 95},
			{"half", "0.5", .3, 1, [3]float64{}, [3]float64{0, 0, .20000000298023224}, 95},
			{"clamp_high", "2", .3, 1, [3]float64{}, [3]float64{}, 95},
			{"clamp_low", "-4", .3, 1, [3]float64{}, [3]float64{0, 0, 1.2000000178813934}, 95},
			{"slow", "0", .2, 1, [3]float64{}, [3]float64{}, 100},
			{"damage_only", "0", .24, 1, [3]float64{}, [3]float64{}, 96},
			{"rounding", "0", .5, 1, [3]float64{}, [3]float64{0, 0, .4000000059604645}, 93},
			{"attack_base", "0", .3, 10, [3]float64{}, [3]float64{0, 0, .4000000059604645}, 86},
			{"seed", "0", .3, 1, [3]float64{.1, .2, 0}, [3]float64{.05, .2, .4000000059604645}, 95},
			{"stored_velocity", "0", .3, 1, [3]float64{0, 0, .3}, [3]float64{0, 0, .5500000059604645}, 95},
			{"grounded", "0", .3, 1, [3]float64{}, [3]float64{0, .4, .4000000059604645}, 95},
			{"grounded_seed", "0", .3, 1, [3]float64{.1, .2, 0}, [3]float64{.05, .4, .4000000059604645}, 95},
			{"base_mod", "0", .3, 10, [3]float64{}, [3]float64{0, 0, .4000000059604645}, 86},
		} {
			release()
			armed = false
			sendCommand(t, w, "ride @s dismount")
			sendCommand(t, w, "tp @e[tag=SPEAR_SCENE] 40 -60 40")
			sendCommand(t, w, "tp @s 0.5 -60 0.5 0 0")
			sendCommand(t, w, "fill -2 -57 -2 2 -57 6 stone")
			boatTag, targetTag := fmt.Sprintf("spear_boat_%d", index), fmt.Sprintf("spear_target_%d", index)
			boat, target := "@e[tag="+boatTag+",limit=1]", "@e[tag="+targetTag+",limit=1]"
			sendCommand(t, w, fmt.Sprintf("summon oak_boat 0.5 -60.0 0.5 {Tags:[\"SPEAR_SCENE\",\"%s\"],NoGravity:1b}", boatTag))
			sendCommand(t, w, "ride @s mount "+boat)
			sendCommand(t, w, fmt.Sprintf("summon villager 0.5 -59.5 4.5 {Tags:[\"SPEAR_SCENE\",\"%s\"],NoAI:1b,NoGravity:1b,UUID:[I;1,2,3,%d],attributes:[{id:\"minecraft:max_health\",base:100d}],Health:100f,Motion:[%.17gd,%.17gd,%.17gd]}", targetTag, index+201, tc.motion[0], tc.motion[1], tc.motion[2]))
			sendCommand(t, w, "attribute "+target+" max_health base set 100")
			sendCommand(t, w, "data merge entity "+target+" {Health:100f}")
			if strings.HasPrefix(tc.name, "grounded") {
				sendCommand(t, w, "data merge entity "+target+" {OnGround:1b}")
			}
			t.Logf("%s %s OnGround=%s", leg.name, tc.name, readData(target, "OnGround"))
			sendCommand(t, w, "attribute "+target+" knockback_resistance base set "+tc.base)
			sendCommand(t, w, fmt.Sprintf("attribute @s attack_damage base set %.17g", tc.attackBase))
			for _, name := range []string{"add", "base", "total"} {
				sendCommand(t, w, "attribute @s attack_damage modifier remove test:spear_"+name)
			}
			if tc.name == "base_mod" {
				sendCommand(t, w, "attribute @s attack_damage modifier add test:spear_add 2 add_value")
				sendCommand(t, w, "attribute @s attack_damage modifier add test:spear_base 0.5 add_multiplied_base")
				sendCommand(t, w, "attribute @s attack_damage modifier add test:spear_total 0.5 add_multiplied_total")
			}
			sendCommand(t, w, "item replace entity @s weapon.mainhand with wooden_spear")
			waitWitness("execute if items entity @s weapon.mainhand wooden_spear", "SPEAR_READY")
			if readNumber(target, "Health") != 100 {
				t.Fatal("target health preparation", leg.name, tc.name)
			}
			assertVector("prepared velocity", readVector(target, "Motion"), tc.motion)
			settle(40)
			if tc.name == "base_mod" && effective("@s", "attack_damage") != 27 {
				t.Fatal("modifier preparation", leg.name)
			}
			var uuid [16]byte
			binary.BigEndian.PutUint32(uuid[0:4], 1)
			binary.BigEndian.PutUint32(uuid[4:8], 2)
			binary.BigEndian.PutUint32(uuid[8:12], 3)
			binary.BigEndian.PutUint32(uuid[12:16], uint32(index+201))
			deadline := time.Now().Add(15 * time.Second)
			for spawns[uuid] == 0 && time.Now().Before(deadline) {
				barrier()
			}
			if spawns[uuid] == 0 {
				t.Fatal("target tracking missing")
			}
			targetID = spawns[uuid]
			var move protocol.Buffer
			move.Double(.5)
			move.Double(-60)
			move.Double(.5 + tc.delta)
			move.Float(0)
			move.Float(0)
			move.Bool(true)
			if err := w.WritePacket(server.IDPlayMoveVehicleSB, move.Bytes()); err != nil {
				t.Fatal(err)
			}
			if err := w.WritePacket(server.IDPlayClientTickEndSB, nil); err != nil {
				t.Fatal(err)
			}
			waitWitness(fmt.Sprintf("execute if data entity %s {Pos:[0.5d,-60d,%.17gd]}", boat, .5+tc.delta), "SPEAR_MOVE_READY")
			t.Logf("%s %s actorPos=%v targetPos=%v effective=%g", leg.name, tc.name, readVector("@s", "Pos"), readVector(target, "Pos"), effective(target, "knockback_resistance"))
			baselineMotion = tc.motion
			impulses = nil
			armed = true
			released = false
			beforeHand := handReceipts
			var use protocol.Buffer
			use.VarInt(0)
			use.VarInt(int32(index + 1))
			use.Float(0)
			use.Float(0)
			if err := w.WritePacket(server.IDPlayUseItemSB, use.Bytes()); err != nil {
				t.Fatal(err)
			}
			barrier()
			frozenAt := gameTime()
			// Use age follows admitted player passes, independently of simulation.
			settle(20)
			if now := gameTime(); now != frozenAt {
				t.Fatal("use wait advanced simulation", frozenAt, now)
			}
			release()
			barrier()
			armed = false
			assertVector(tc.name, readVector(target, "Motion"), tc.want)
			if health := readNumber(target, "Health"); health != tc.health {
				t.Fatalf("%s %s health=%g want=%g", leg.name, tc.name, health, tc.health)
			}
			t.Logf("%s %s rawMotion=%v packets=%v health=%g handReceipts=%d active=%v releasedEarly=%v", leg.name, tc.name, readVector(target, "Motion"), impulses, readNumber(target, "Health"), handReceipts-beforeHand, handActive, released)
		}
	}
}
