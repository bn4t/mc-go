//go:build blackbox

package blackbox

import (
	"encoding/binary"
	"fmt"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTestInstanceEnvironmentParity(t *testing.T) {
	const name = "TestInstEnv"
	files := map[string]string{"ops.json": opsJSONFor(name),
		"world/datapacks/env/pack.mcmeta":                               `{"pack":{"description":"environment controls","min_format":107,"max_format":107}}`,
		"world/datapacks/env/data/testpin/test_environment/storm.json":  `{"type":"minecraft:all_of","definitions":[{"type":"minecraft:difficulty","difficulty":"easy"},{"type":"minecraft:weather","weather":"thunder"},{"type":"minecraft:difficulty","difficulty":"hard"}]}`,
		"world/datapacks/env/data/testpin/test_instance/pass.json":      `{"type":"minecraft:function","environment":"testpin:storm","structure":"minecraft:empty","function":"minecraft:always_pass","max_ticks":1,"setup_ticks":110}`,
		"world/datapacks/env/data/testpin/test_environment/normal.json": `{"type":"minecraft:difficulty","difficulty":"normal"}`,
		"world/datapacks/env/data/testpin/test_instance/normal.json":    `{"type":"minecraft:function","environment":"testpin:normal","structure":"minecraft:empty","function":"minecraft:always_pass","max_ticks":1,"setup_ticks":0}`,
		"world/datapacks/env/data/testpin/test_instance/fail.json":      `{"type":"minecraft:function","environment":"testpin:storm","structure":"minecraft:empty","function":"testpin:missing","max_ticks":1,"setup_ticks":0}`,
	}
	directory := t.TempDir()
	for path, body := range files {
		if strings.HasPrefix(path, "world/") {
			local := filepath.Join(directory, strings.TrimPrefix(path, "world/"))
			if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(local, []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, files, func(s *server.Server) {
		s.GameMode = server.GameModeCreative
		s.WorldDir = directory
		s.SetOpLevel(name, 4)
	})
	defer tg.cleanup()
	for _, tc := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, tc.addr, name)
			defer c.Close()
			drainToJoinBlackbox(t, tc.name, pr)
			awaitPlayReadyBlackbox(t, tc.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			phase := "startup"
			type weatherEvent struct {
				kind byte
				bits uint32
			}
			events := map[string][]weatherEvent{}
			record := func(lines []string) {
				for _, line := range lines {
					id, body, _ := strings.Cut(line, ":")
					if id == "0x26" && len(body) == 5 {
						raw := []byte(body)
						kind := raw[0]
						if kind == 1 || kind == 2 || kind == 7 || kind == 8 {
							bits := binary.BigEndian.Uint32(raw[1:])
							events[phase] = append(events[phase], weatherEvent{kind, bits})
						}
					}
				}
			}
			command := func(cmd string) []string {
				serial++
				marker := fmt.Sprintf("TIE_END_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				lines := waitForExactSayBB(t, sink, marker, 10*time.Second)
				record(lines)
				return lines
			}
			clock := func() int64 {
				sendCommand(t, pw, "time query gametime")
				deadline := time.Now().Add(10 * time.Second)
				for time.Now().Before(deadline) {
					lines := sink.take()
					record(lines)
					for _, line := range lines {
						key, args, ok := componentArgs(line)
						if ok && key == "commands.time.query.gametime" && len(args) > 0 {
							if v, ok := args[0].(int64); ok {
								return v
							}
						}
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("clock receipt missing")
				return 0
			}
			advance := func(n int) {
				before := clock()
				command(fmt.Sprintf("tick step %d", n))
				deadline := time.Now().Add(10 * time.Second)
				for {
					after := clock()
					if after >= before+int64(n) {
						if after != before+int64(n) {
							t.Fatalf("step advanced%d want%d", after-before, n)
						}
						return
					}
					if time.Now().After(deadline) {
						t.Fatal("step receipt missing")
					}
				}
			}
			difficulties := map[string]string{"before": "peaceful", "immediate": "hard", "frozen_wait": "hard", "fail_immediate": "hard", "fail1": "hard", "fail2": "hard", "fail3": "easy", "fail4": "easy", "replacement_before_run": "hard", "replacement_immediate": "hard", "replacement_tick1": "hard", "replacement_finished": "peaceful", "cross_before_run": "hard", "cross_immediate": "normal", "cross_tick1": "normal", "cross_finished": "peaceful"}
			for _, i := range []int{1, 20, 30, 101, 111} {
				difficulties[fmt.Sprintf("pass%d", i)] = "hard"
			}
			for _, i := range []int{112, 113, 192, 193, 213, 215} {
				difficulties[fmt.Sprintf("pass%d", i)] = "peaceful"
			}
			observe := func(label string) {
				phase = label
				count := 0
				for _, line := range command("difficulty") {
					key, args, ok := componentArgs(line)
					if !ok || key != "commands.difficulty.query" {
						continue
					}
					count++
					want := []any{map[string]any{"translate": "options.difficulty." + difficulties[label]}}
					if !reflect.DeepEqual(args, want) {
						t.Fatalf("%s difficulty %#v want%#v", label, args, want)
					}
				}
				if count != 1 {
					t.Fatalf("%s difficulty receipts %d", label, count)
				}
				status := "cleared"
				if strings.HasPrefix(label, "pass") {
					var tick int
					fmt.Sscanf(label, "pass%d", &tick)
					if tick >= 112 {
						status = "finished"
					}
				}
				if label == "fail2" {
					status = "running"
				}
				if label == "fail3" || label == "fail4" || label == "replacement_finished" || label == "cross_finished" {
					status = "finished"
				}
				marker := "TIE_STATUS_" + label
				if !structureSaidBB(command(`execute if data block 4 70 4 {data:{status:"`+status+`"}} run say `+marker), marker) {
					t.Fatalf("%s status want%s", label, status)
				}
			}
			command("tick freeze")
			command("setblock 0 69 0 stone")
			command("tp @s 0.5 70 0.5")
			command("difficulty peaceful")
			command("weather clear")
			pos := [3]int32{4, 70, 4}
			lines := command("setblock 4 70 4 test_instance_block")
			deadline := time.Now().Add(3 * time.Second)
			constructed := func() bool {
				for _, u := range structureBEUpdatesBB(t, lines) {
					if u.pos == pos {
						return true
					}
				}
				return false
			}
			for !constructed() {
				if time.Now().After(deadline) {
					t.Fatal("constructor receipt missing")
				}
				time.Sleep(10 * time.Millisecond)
				lines = append(lines, sink.take()...)
			}
			request := testInstanceRequestBB{test: "testpin:pass", size: [3]int32{1, 1, 1}}
			run := func() {
				sink.take()
				writeTestInstanceActionBB(t, pw, pos, 6, request)
				lines := command("say TIE_RUN")
				deadline := time.Now().Add(5 * time.Second)
				for {
					started, updated := false, false
					for _, line := range lines {
						key, args, ok := componentArgs(line)
						if ok && key == "test_instance_block.starting" && len(args) == 1 && args[0] == request.test {
							started = true
						}
					}
					for _, u := range structureBEUpdatesBB(t, lines) {
						data, _ := u.tag["data"].(map[string]any)
						if u.pos == pos && data["test"] == request.test && data["status"] == "cleared" && data["ignore_entities"] == int8(0) {
							updated = true
						}
					}
					if started && updated {
						return
					}
					if time.Now().After(deadline) {
						t.Fatalf("RUN applied receipt missing started%v updated%v", started, updated)
					}
					time.Sleep(10 * time.Millisecond)
					more := sink.take()
					record(more)
					lines = append(lines, more...)
				}
			}
			lateJoin := func(index int) {
				oc, opr, opw := joinPlayBlackboxView(t, tc.addr, fmt.Sprintf("EnvObserver%d", index), 1)
				defer oc.Close()
				drainToJoinBlackbox(t, tc.name, opr)
				other := newChatSinkTP(opr, opw)
				marker := fmt.Sprintf("TIE_OBSERVER_%d", index)
				command("say " + marker)
				record(waitForExactSayBB(t, other, marker, 10*time.Second))
			}
			phase = "late_join0"
			lateJoin(0)
			observe("before")
			run()
			observe("immediate")
			time.Sleep(300 * time.Millisecond)
			observe("frozen_wait")
			for i := 1; i <= 215; i++ {
				phase = fmt.Sprintf("pass_step%d", i)
				advance(1)
				if i == 30 || i == 101 {
					phase = fmt.Sprintf("late_join%d", i)
					lateJoin(i)
				}
				switch i {
				case 1, 20, 30, 101, 111, 112, 113, 192, 193, 213, 215:
					observe(fmt.Sprintf("pass%d", i))
				}
			}
			command("difficulty easy")
			command("weather rain")
			request.test = "testpin:fail"
			run()
			observe("fail_immediate")
			for i := 1; i <= 4; i++ {
				phase = fmt.Sprintf("fail_step%d", i)
				advance(1)
				observe(fmt.Sprintf("fail%d", i))
			}

			command("difficulty peaceful")
			command("weather clear")
			request.test = "testpin:pass"
			run()
			phase = "replacement_first_tick"
			advance(1)
			observe("replacement_before_run")
			command("difficulty easy")
			run()
			observe("replacement_immediate")
			advance(1)
			observe("replacement_tick1")
			advance(111)
			observe("replacement_finished")
			command("difficulty peaceful")
			request.test = "testpin:pass"
			run()
			advance(1)
			observe("cross_before_run")
			command("difficulty easy")
			request.test = "testpin:normal"
			run()
			observe("cross_immediate")
			advance(1)
			observe("cross_tick1")
			advance(1)
			observe("cross_finished")

			pair := func(bits uint32) []weatherEvent { return []weatherEvent{{7, bits}, {8, bits}} }
			golden := map[string][]weatherEvent{
				"immediate": {}, "frozen_wait": {}, "late_join0": {}, "late_join30": {{1, 0}, {7, 0x3e999999}, {8, 0x3db851ea}}, "late_join101": {{1, 0}, {7, 0x3f800000}, {8, 0x3f800000}},
				"pass_step1": pair(0x3c23d70a), "pass_step5": pair(0x3d4ccccc), "pass_step20": {{7, 0x3e4cccce}, {8, 0x3e4cccce}, {1, 0}, {7, 0x3e4cccce}, {8, 0x3e4cccce}},
				"pass_step30": pair(0x3e999999), "pass_step100": pair(0x3f7ffff5), "pass_step101": pair(0x3f800000), "pass_step102": {}, "pass_step111": {}, "pass_step112": {},
				"pass_step113": pair(0x3f7d70a4), "pass_step192": pair(0x3e4cccfb), "pass_step193": {{7, 0x3e428f8a}, {8, 0x3e428f8a}, {2, 0}, {7, 0x3e428f8a}, {8, 0x3e428f8a}}, "pass_step213": pair(0), "pass_step214": {}, "pass_step215": {},
				"fail_step1": pair(0x3c23d70a), "fail_step2": pair(0x3ca3d70a), "fail_step3": pair(0x3cf5c28f), "fail_step4": pair(0x3ca3d70a),
			}
			for label, want := range golden {
				got := events[label]
				if len(got) != len(want) {
					t.Errorf("%s weather %#v want%#v", label, got, want)
					continue
				}
				for i := range want {
					if got[i] != want[i] {
						t.Errorf("%s weather[%d] %#v want%#v", label, i, got[i], want[i])
					}
				}
			}

		})
	}
}
