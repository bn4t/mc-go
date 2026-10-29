package blackbox

import (
	"bytes"
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"reflect"
	"strings"
	"testing"
	"time"
)

func registeredTestDescriptionBB() any {
	row := func(key string, v any) map[string]any {
		return map[string]any{"translate": "test_instance.description." + key, "with": nbt.List{Elem: nbt.TagCompound, Items: []any{v}}, "extra": nbt.List{Elem: nbt.TagString, Items: []any{"\n"}}}
	}
	info := row("structure", map[string]any{"color": "blue", "text": "minecraft:empty"})
	info["extra"] = nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"": "\n"}, row("batch", map[string]any{"color": "blue", "text": "minecraft:default"})}}
	out := row("type", map[string]any{"color": "blue", "translate": "test_instance.type.function"})
	out["extra"] = nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"": "\n"}, row("function", map[string]any{"color": "blue", "text": "minecraft:always_pass"}), info}}
	return out
}

// The built-in definition exercises registered lookup without a custom pack.
func TestTestInstanceRegisteredParity(t *testing.T) {
	directory := t.TempDir()
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("TestInstRun")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.SetOpLevel("TestInstRun", 4)
		s.WorldDir = directory
	})
	defer tg.cleanup()
	for _, tc := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "TestInstRun")
			defer c.Close()
			drainToJoinBlackbox(t, tc.name, pr)
			awaitPlayReadyBlackbox(t, tc.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				serial++
				mark := fmt.Sprintf("TIR_END_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+mark)
				return waitForExactSayBB(t, sink, mark, 10*time.Second)
			}
			check := func(condition string) {
				serial++
				mark := fmt.Sprintf("TIR_PASS_%d", serial)
				lines := command("execute " + condition + " run say " + mark)
				if !structureSaidBB(lines, mark) {
					t.Errorf("failed %s", condition)
				}
			}
			pos := [3]int32{4, 70, 4}
			command("tick freeze")
			command("setblock 0 69 0 stone")
			command("tp @s 0.5 70 0.5")
			created := command("setblock 4 70 4 minecraft:test_instance_block")
			ready := false
			deadline := time.Now().Add(3 * time.Second)
			for !ready {
				for _, u := range structureBEUpdatesBB(t, created) {
					if u.pos == pos {
						data, _ := u.tag["data"].(map[string]any)
						ready = data["status"] == "cleared"
					}
				}
				if ready {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("constructor receipt missing")
				}
				time.Sleep(10 * time.Millisecond)
				created = sink.take()
			}
			request := testInstanceRequestBB{test: "minecraft:always_pass", size: [3]int32{2, 1, 3}, ignore: true}
			action := func(id int32, wantSize *[3]int32) []string {
				sink.take()
				writeTestInstanceActionBB(t, pw, pos, id, request)
				lines := command("say TIR_APPLIED")
				received := func() bool {
					if id >= 2 {
						for _, u := range structureBEUpdatesBB(t, lines) {
							data, _ := u.tag["data"].(map[string]any)
							ignore := request.ignore
							if id == 6 {
								ignore = false
							}
							status := []string{"cleared", "running", "finished"}[request.status]
							if id == 3 || id == 6 {
								status = "cleared"
							}
							rotation := []string{"none", "clockwise_90", "180", "counterclockwise_90"}[request.rotation]
							if u.pos == pos && data["test"] == request.test && data["rotation"] == rotation && data["status"] == status && data["ignore_entities"] == int8(boolByteBB(ignore)) && reflect.DeepEqual(data["size"], []int32{request.size[0], request.size[1], request.size[2]}) {
								return true
							}
						}
						return false
					}
					for _, l := range lines {
						if strings.HasPrefix(l, "0x7e:") {
							return true
						}
					}
					return false
				}
				deadline := time.Now().Add(3 * time.Second)
				for !received() && time.Now().Before(deadline) {
					time.Sleep(10 * time.Millisecond)
					lines = append(lines, sink.take()...)
				}
				if !received() {
					t.Fatalf("action%d applied receipt missing", id)
				}
				time.Sleep(100 * time.Millisecond)
				lines = append(lines, sink.take()...)
				if id <= 1 {
					count := 0
					for _, l := range lines {
						p, body, _ := strings.Cut(l, ":")
						if p != "0x7e" {
							continue
						}
						count++
						r := bytes.NewReader([]byte(body))
						v, err := nbt.ReadUnnamed(r)
						if err != nil || !reflect.DeepEqual(v, registeredTestDescriptionBB()) {
							t.Fatalf("description %#v err%v", v, err)
						}
						flag, err := r.ReadByte()
						if err != nil || flag != boolByteBB(wantSize != nil) {
							t.Fatalf("size flag%d err%v", flag, err)
						}
						if wantSize != nil {
							for _, want := range wantSize {
								got, err := protocol.ReadVarInt(r)
								if err != nil || got != want {
									t.Fatalf("size%d want%d err%v", got, want, err)
								}
							}
						}
						if r.Len() != 0 {
							t.Fatal("status trailing bytes")
						}
					}
					if count != 1 {
						t.Fatalf("status count%d", count)
					}
				}
				return lines
			}
			action(0, nil)
			one := [3]int32{1, 1, 1}
			action(1, &one)
			check(`if data block 4 70 4 {data:{size:[I;0,0,0]}}`)
			command("fill 4 71 5 5 71 7 air")
			command("setblock 4 71 5 gold_block")
			command("setblock 5 71 7 stone")
			action(4, nil)
			size := [3]int32{2, 1, 3}
			action(1, &size)
			corners := [][3]string{{"4 71 5", "5 71 7", "4 71 6"}, {"6 71 5", "4 71 6", "5 71 5"}, {"5 71 7", "4 71 5", "5 71 6"}, {"4 71 6", "6 71 5", "5 71 6"}}
			for rotation, points := range corners {
				command("fill 4 71 5 6 71 7 diamond_block")
				request.rotation = int32(rotation)
				lines := action(3, nil)
				success := false
				for _, l := range lines {
					key, args, ok := componentArgs(l)
					if ok && key == "test_instance_block.reset_success" && len(args) == 1 && args[0] == "minecraft:always_pass" {
						success = true
					}
				}
				if !success {
					t.Error("registered RESET success missing")
				}
				check("if block " + points[0] + " gold_block")
				check("if block " + points[1] + " stone")
				check("if block " + points[2] + " air")
				check("if block 6 71 7 diamond_block")
				check(`if data block 4 70 4 {data:{status:"cleared",ignore_entities:1b}}`)
			}
			request.rotation = 0
			action(6, nil)
			check(`if data block 4 70 4 {data:{status:"cleared",ignore_entities:0b,size:[I;2,1,3]}}`)
			check("if block 3 71 5 barrier")
			check("if block 4 72 5 barrier")
			time.Sleep(300 * time.Millisecond)
			check(`if data block 4 70 4 {data:{status:"cleared"}}`)
			// Exact status expectations follow the measured setup-tick timeline.
			for i := 1; i <= 4; i++ {
				before := gameTimeOf(t, pw, sink)
				command("tick step 1")
				deadline := time.Now().Add(10 * time.Second)
				for {
					after := gameTimeOf(t, pw, sink)
					if after >= before+1 {
						if after != before+1 {
							t.Fatalf("step advanced%d", after-before)
						}
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("step timeout")
					}
				}
				status := "cleared"
				if i >= 3 {
					status = "finished"
				}
				check(`if data block 4 70 4 {data:{status:"` + status + `"}}`)
			}
			check("if block 3 71 5 air")
			check("if block 4 72 5 air")
			check("if block 4 70 4 test_instance_block")
			request.status = 2
			action(3, nil)
			check("if block 3 71 5 air")
			check("if block 4 72 5 air")
			check(`if data block 4 70 4 {data:{status:"cleared"}}`)

			// Lifecycle probes retain exact native witnesses before choosing retirement policy.
			advance := func(n int) {
				before := gameTimeOf(t, pw, sink)
				command(fmt.Sprintf("tick step %d", n))
				deadline := time.Now().Add(10 * time.Second)
				for gameTimeOf(t, pw, sink) < before+int64(n) {
					if time.Now().After(deadline) {
						t.Fatal("lifecycle step timeout")
					}
				}
			}
			probe := func(label string, at [3]int32, want string) {
				matched := 0
				for _, status := range []string{"cleared", "running", "finished"} {
					mark := "TIR_PROBE_" + label + "_" + status
					lines := command(fmt.Sprintf(`execute if data block %d %d %d {data:{status:"%s"}} run say %s`, at[0], at[1], at[2], status, mark))
					if structureSaidBB(lines, mark) {
						t.Logf("lifecycle %s %s", label, status)
						matched++
						if status != want {
							t.Errorf("%s status%s want%s", label, status, want)
						}
					}
				}
				if matched != 1 {
					t.Fatalf("%s matched%d statuses", label, matched)
				}
			}
			request.status = 0
			request.test = "minecraft:always_pass"
			action(6, nil)
			request.test = "test:missing"
			request.error = "edited error"
			action(2, nil)
			check(`if data block 4 70 4 {data:{error_message:"edited error"}}`)
			advance(3)
			probe("set_missing", pos, "finished")
			check(`if data block 4 70 4 {data:{test:"test:missing"}}`)
			check("unless data block 4 70 4 data.error_message")
			request.error = nil
			request.test = "minecraft:always_pass"
			action(6, nil)
			action(3, nil)
			advance(3)
			probe("reset_active", pos, "finished")
			action(6, nil)
			command("setblock 4 70 4 air")
			command("setblock 4 70 4 minecraft:test_instance_block")
			advance(3)
			probe("replacement", pos, "finished")
			check("unless data block 4 70 4 data.test")
			check(`if data block 4 70 4 {data:{size:[I;0,0,0]}}`)
			action(6, nil)
			other := [3]int32{12, 70, 4}
			created = command("setblock 12 70 4 minecraft:test_instance_block")
			deadline = time.Now().Add(3 * time.Second)
			ready = false
			for !ready {
				for _, u := range structureBEUpdatesBB(t, created) {
					if u.pos == other {
						ready = true
					}
				}
				if ready {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("second controller receipt missing")
				}
				time.Sleep(10 * time.Millisecond)
				created = sink.take()
			}
			writeTestInstanceActionBB(t, pw, other, 6, request)
			lines := command("say TIR_SECOND_RUN")
			deadline = time.Now().Add(3 * time.Second)
			ready = false
			for !ready {
				for _, u := range structureBEUpdatesBB(t, lines) {
					if u.pos == other {
						ready = true
					}
				}
				if ready {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("second RUN receipt missing")
				}
				time.Sleep(10 * time.Millisecond)
				lines = sink.take()
			}
			advance(3)
			probe("replaced_runner_first", pos, "cleared")
			probe("replaced_runner_second", other, "finished")
			command("scoreboard objectives add tir_count dummy")
			command("kill @e[type=armor_stand]")
			command(`summon armor_stand 4.5 71 5.5 {Tags:["TIR_CARGO"],NoGravity:1b}`)
			request.ignore = false
			action(4, nil)
			action(6, nil)
			command("execute store result score #count tir_count if entity @e[type=armor_stand,tag=TIR_CARGO]")
			check("if score #count tir_count matches 1")
			advance(3)
			command("execute store result score #count tir_count if entity @e[type=armor_stand,tag=TIR_CARGO]")
			check("if score #count tir_count matches 0")

		})
	}
}
func boolByteBB(v bool) byte {
	if v {
		return 1
	}
	return 0
}
