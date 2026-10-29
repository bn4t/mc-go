package blackbox

import (
	"bytes"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
)

type testInstanceRequestBB struct {
	test             string
	size             [3]int32
	rotation, status int32
	ignore           bool
	error            any
}

func writeTestInstanceActionBB(t *testing.T, pw *protocol.Writer, pos [3]int32, action int32, value testInstanceRequestBB) {
	t.Helper()
	var b protocol.Buffer
	b.Position(pos[0], pos[1], pos[2])
	b.VarInt(action)
	b.Bool(value.test != "")
	if value.test != "" {
		b.String(value.test)
	}
	for _, n := range value.size {
		b.VarInt(n)
	}
	b.VarInt(value.rotation)
	b.Bool(value.ignore)
	b.VarInt(value.status)
	b.Bool(value.error != nil)
	if value.error != nil {
		var raw bytes.Buffer
		if err := nbt.WriteUnnamed(&raw, value.error); err != nil {
			t.Fatal(err)
		}
		b.Write(raw.Bytes())
	}
	if err := pw.WritePacket(server.IDPlayTestInstanceBlockActionSB, b.Bytes()); err != nil {
		t.Fatal(err)
	}
}
func TestTestInstanceEditorParity(t *testing.T) {
	directory := t.TempDir()
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor("TestInstEdit")}, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.OpsRequired = true
		s.SetOpLevel("TestInstEdit", 4)
		s.WorldDir = directory
	})
	defer tg.cleanup()
	for _, tc := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, "TestInstEdit")
			defer c.Close()
			drainToJoinBlackbox(t, tc.name, pr)
			awaitPlayReadyBlackbox(t, tc.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			pos := [3]int32{4, 70, 4}
			markerSeen := structureSaidBB
			barrier := func() []string {
				serial++
				marker := fmt.Sprintf("TI_DONE_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			command := func(cmd string) []string { sendCommand(t, pw, cmd); return barrier() }
			check := func(condition string) {
				serial++
				marker := fmt.Sprintf("TI_PASS_%d", serial)
				sendCommand(t, pw, "execute "+condition+" run say "+marker)
				if !markerSeen(barrier(), marker) {
					t.Errorf("failed %s", condition)
				}
			}
			type response struct {
				tag                                      map[string]any
				status                                   any
				hasSize                                  bool
				beCount, statusCount, noTest, unableSave int
				exportDisabled                           bool
			}
			action := func(at [3]int32, id int32, value testInstanceRequestBB, wantBE, wantStatus bool) response {
				time.Sleep(120 * time.Millisecond)
				sink.take()
				out := response{}
				collect := func(lines []string) {
					for _, u := range structureBEUpdatesBB(t, lines) {
						if u.pos == at {
							out.tag = u.tag
							out.beCount++
						}
					}
					for _, line := range lines {
						prefix, raw, ok := strings.Cut(line, ":")
						if !ok {
							continue
						}
						packet, err := strconv.ParseInt(prefix, 0, 32)
						if err != nil {
							continue
						}
						if packet == 0x7e { // protocol776 report: test_instance_block_status.
							reader := bytes.NewReader([]byte(raw))
							value, err := nbt.ReadUnnamed(reader)
							if err != nil {
								t.Fatal(err)
							}
							flag, err := reader.ReadByte()
							if err != nil {
								t.Fatal(err)
							}
							out.status = value
							out.hasSize = flag != 0
							out.statusCount++
						}
						if packet == 0x79 {
							if strings.Contains(raw, "test_instance_block.error.no_test") {
								out.noTest++
							}
							if strings.Contains(raw, "test_instance_block.error.unable_to_save") {
								out.unableSave++
							}
							if strings.Contains(raw, "Test structure exporting is disabled") {
								out.exportDisabled = true
							}
						}
					}
				}
				writeTestInstanceActionBB(t, pw, at, id, value)
				collect(barrier())
				deadline := time.Now().Add(2 * time.Second)
				for time.Now().Before(deadline) && ((wantBE && out.tag == nil) || (wantStatus && out.status == nil)) {
					time.Sleep(10 * time.Millisecond)
					collect(sink.take())
				}
				time.Sleep(120 * time.Millisecond)
				collect(sink.take())
				be, status := 0, 0
				if wantBE {
					be = 1
				}
				if wantStatus {
					status = 1
				}
				if out.beCount != be || out.statusCount != status {
					t.Errorf("action%d BE%d/%d status%d/%d tag%#v", id, out.beCount, be, out.statusCount, status, out.tag)
				}
				return out
			}
			command("tick freeze")
			command("setblock 0 69 0 stone")
			command("tp @s 0.5 70 0.5")
			created := command("setblock 4 70 4 minecraft:test_instance_block")
			constructed := false
			inspectConstructor := func(lines []string) {
				for _, update := range structureBEUpdatesBB(t, lines) {
					if update.pos == pos {
						data, _ := update.tag["data"].(map[string]any)
						if data["status"] == "cleared" && reflect.DeepEqual(data["size"], []int32{0, 0, 0}) {
							constructed = true
						}
					}
				}
			}
			inspectConstructor(created)
			constructorDeadline := time.Now().Add(3 * time.Second)
			for !constructed && time.Now().Before(constructorDeadline) {
				time.Sleep(10 * time.Millisecond)
				inspectConstructor(sink.take())
			}
			if !constructed {
				t.Fatal("controller construction update missing")
			}
			check(`if data block 4 70 4 {data:{size:[I;0,0,0],rotation:"none",ignore_entities:0b,status:"cleared"}}`)
			check("unless data block 4 70 4 errors")
			request := testInstanceRequestBB{test: "test:missing", size: [3]int32{3, 2, 4}, rotation: 1, status: 2, ignore: true, error: " λ error "}
			for _, id := range []int32{0, 1, -1} {
				out := action(pos, id, request, false, true)
				expected := map[string]any{"translate": "test_instance.description.no_test", "color": "red"}
				if !reflect.DeepEqual(out.status, expected) || out.hasSize {
					t.Errorf("action%d status%#v size%v", id, out.status, out.hasSize)
				}
			}
			check(`if data block 4 70 4 {data:{size:[I;0,0,0],rotation:"none",ignore_entities:0b,status:"cleared"}}`)
			first := action(pos, 2, request, true, false)
			data, _ := first.tag["data"].(map[string]any)
			for key, want := range map[string]any{"test": "test:missing", "size": []int32{3, 2, 4}, "rotation": "clockwise_90", "ignore_entities": int8(1), "status": "finished", "error_message": " λ error "} {
				if !reflect.DeepEqual(data[key], want) {
					t.Errorf("typed field%s got%T:%v want%T:%v", key, data[key], data[key], want, want)
				}
			}
			action(pos, 2, request, true, false)
			command(`data merge block 4 70 4 {data:{rotation:"BAD"},unknown:1}`)
			check(`if data block 4 70 4 {data:{size:[I;3,2,4],rotation:"clockwise_90",ignore_entities:1b,status:"finished",error_message:" λ error "}}`)
			check("unless data block 4 70 4 unknown")
			command(`data merge block 4 70 4 {data:{size:[1.9f,-0.9f,3.9f],ignore_entities:256}}`)
			check(`if data block 4 70 4 {data:{size:[I;1,0,3],ignore_entities:1b}}`)
			command(`data merge block 4 70 4 {errors:[{pos:[I;7,70,8],text:"marker λ",unknown:1}]}`)
			check(`if data block 4 70 4 {errors:[{pos:[I;7,70,8],text:"marker λ"}]}`)
			check("unless data block 4 70 4 errors[0].unknown")
			request.test = ""
			request.size = [3]int32{2, 1, 3}

			probes := []struct {
				pos                 [3]int32
				clearEven, clearOdd bool
			}{
				{[3]int32{3, 71, 5}, true, true},
				{[3]int32{6, 71, 5}, true, false},
				{[3]int32{7, 71, 5}, false, true},
				{[3]int32{4, 71, 8}, true, false},
				{[3]int32{4, 71, 7}, false, true},
				{[3]int32{4, 71, 5}, false, false},
				{[3]int32{2, 71, 5}, false, false},
				{[3]int32{4, 72, 5}, true, true},
				{[3]int32{4, 70, 5}, true, true},
			}
			for rotation := int32(0); rotation < 4; rotation++ {
				for _, probe := range probes {
					p := probe.pos
					command(fmt.Sprintf("setblock %d %d %d barrier", p[0], p[1], p[2]))
				}
				value := request
				value.rotation = rotation
				out := action(pos, 3, value, true, false)
				if out.noTest != 0 || out.unableSave != 0 {
					t.Error("reset unexpectedly emitted missing-test feedback")
				}
				names := []string{"none", "clockwise_90", "180", "counterclockwise_90"}
				check(`if data block 4 70 4 {data:{status:"cleared",rotation:"` + names[rotation] + `"}}`)
				check("unless data block 4 70 4 data.error_message")
				check("unless data block 4 70 4 errors")
				check("unless data block 4 70 4 data.test")
				for _, probe := range probes {
					cleared := probe.clearEven
					if rotation%2 != 0 {
						cleared = probe.clearOdd
					}
					block := "barrier"
					if cleared {
						block = "air"
					}
					p := probe.pos
					check(fmt.Sprintf("if block %d %d %d %s", p[0], p[1], p[2], block))
				}
				t.Logf("non-square reset rotation%d verified%d shell/interior probes", rotation, len(probes))
			}
			for _, id := range []int32{4, 5, 6} {
				out := action(pos, id, request, true, false)
				if id == 6 {
					if out.noTest != 1 || out.unableSave != 0 {
						t.Errorf("run feedback no-test%d unable-save%d", out.noTest, out.unableSave)
					}
				} else {
					if out.unableSave != 1 || out.noTest != 0 {
						t.Errorf("action%d feedback unable-save%d no-test%d", id, out.unableSave, out.noTest)
					}
				}
				check(`if data block 4 70 4 {data:{status:"finished",error_message:" λ error "}}`)
				check("unless data block 4 70 4 data.test")
			}

			command("fill 4 71 5 5 71 6 air")
			command("setblock 4 71 5 gold_block")
			command("setblock 5 71 6 stone")
			saved := testInstanceRequestBB{test: "test:packet_save", size: [3]int32{2, 1, 2}, ignore: true}
			out := action(pos, 4, saved, true, false)
			if out.noTest != 0 {
				t.Error("unregistered selected key could not save")
			}
			command("setblock 20 70 20 structure_block[mode=load]")
			command("fill 20 71 20 21 71 21 diamond_block")
			time.Sleep(120 * time.Millisecond)
			sink.take()
			writeStructureEditBB(t, pw, [3]int32{20, 70, 20}, 2, 1, "test:packet_save", [3]byte{0, 1, 0}, [3]byte{2, 1, 2}, 0, 0, "", 1, 17, 1)
			deadline := time.Now().Add(5 * time.Second)
			loaded := false
			for !loaded && time.Now().Before(deadline) {
				for _, u := range structureBEUpdatesBB(t, sink.take()) {
					if u.pos == [3]int32{20, 70, 20} && u.tag["name"] == "test:packet_save" {
						loaded = true
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !loaded {
				t.Error("saved-template load response missing")
			}
			check("if block 20 71 20 gold_block")
			check("if block 21 71 21 stone")
			check("if block 20 71 21 diamond_block")
			check("if block 21 71 20 diamond_block")
			out = action(pos, 5, saved, true, false)
			if !out.exportDisabled {
				t.Error("default export-disabled feedback missing")
			}
			command("gamemode survival")
			action(pos, 2, request, false, false)
			check(`if data block 4 70 4 {data:{test:"test:packet_save",size:[I;2,1,2],status:"cleared"}}`)
			command("gamemode creative")
			command("setblock 6 70 4 stone")
			action([3]int32{6, 70, 4}, 2, request, false, false)
			check("if block 6 70 4 stone")
		})
	}
}
