//go:build blackbox

package blackbox

import (
	"bytes"
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The startup catalog is observed during configuration, before querying or
// running either custom definition. No registry mutation is injected directly.
func TestTestInstancePackParity(t *testing.T) {
	const name = "TestInstPack"
	bodies := map[string]string{
		"pack.mcmeta": `{"pack":{"description":"test registry controls","min_format":107,"max_format":107}}`,
		"data/testpin/test_environment/empty.json":         `{"type":"minecraft:all_of","definitions":[]}`,
		"data/testpin/test_instance/function.json":         `{"type":"minecraft:function","environment":"testpin:empty","structure":"testpin:shared","function":"minecraft:always_pass","max_ticks":1,"setup_ticks":0,"required":false}`,
		"data/testpin/test_instance/block.json":            `{"type":"minecraft:block_based","environment":"testpin:empty","structure":"testpin:shared","max_ticks":5,"setup_ticks":0,"required":true}`,
		"data/testpin/test_instance/unknown_function.json": `{"type":"minecraft:function","environment":"testpin:empty","structure":"testpin:shared","function":"testpin:missing","max_ticks":1,"setup_ticks":0}`,
	}
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(name)}
	for path, body := range bodies {
		rel := "datapacks/testpin/" + path
		files["world/"+rel] = body
		local := filepath.Join(directory, rel)
		if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte(body), 0644); err != nil {
			t.Fatal(err)
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
			c, pr, pw := loginStart(t, tc.addr, name)
			defer c.Close()
			readLoginSuccess(t, pr, pw, tc.name)
			if err := pw.WritePacket(server.IDLoginAcknowledged, nil); err != nil {
				t.Fatal(err)
			}
			sendKnownPacks(t, pw, "99.9-test-registry")
			catalog := map[string]map[string]map[string]any{}
			for i := 0; i < 100; i++ {
				p, err := pr.ReadPacket()
				if err != nil {
					t.Fatal(err)
				}
				if p.ID == server.IDConfigFinish {
					break
				}
				if p.ID != server.IDConfigRegistryData {
					continue
				}
				r := bytes.NewReader(p.Data)
				key, err := protocol.ReadString(r)
				if err != nil {
					t.Fatal(err)
				}
				n, err := protocol.ReadVarInt(r)
				if err != nil {
					t.Fatal(err)
				}
				rows := map[string]map[string]any{}
				for id := int32(0); id < n; id++ {
					key, err := protocol.ReadString(r)
					if err != nil {
						t.Fatal(err)
					}
					has, err := protocol.ReadBool(r)
					if err != nil {
						t.Fatal(err)
					}
					if has {
						v, err := nbt.ReadUnnamed(r)
						if err != nil {
							t.Fatal(err)
						}
						m, _ := v.(map[string]any)
						rows[key] = m
						if strings.HasPrefix(key, "testpin:") {
							t.Logf("registry index%d %s %#v", id, key, m)
						}
					}
				}
				catalog[key] = rows
			}
			for key, kind := range map[string]string{"testpin:function": "minecraft:function", "testpin:block": "minecraft:block_based", "testpin:unknown_function": "minecraft:function"} {
				row := catalog["minecraft:test_instance"][key]
				if row["type"] != kind || row["environment"] != "testpin:empty" || row["structure"] != "testpin:shared" {
					t.Errorf("definition%s %#v", key, row)
				}
			}
			for key, function := range map[string]string{"testpin:function": "minecraft:always_pass", "testpin:block": "", "testpin:unknown_function": "testpin:missing"} {
				want := map[string]any{"type": "minecraft:function", "environment": "testpin:empty", "structure": "testpin:shared", "max_ticks": int32(1)}
				if function == "" {
					want["type"] = "minecraft:block_based"
					want["max_ticks"] = int32(5)
				} else {
					want["function"] = function
				}
				if key == "testpin:function" {
					want["required"] = int8(0)
				}
				if !reflect.DeepEqual(catalog["minecraft:test_instance"][key], want) {
					t.Fatalf("canonical registry %s: %#v, want %#v", key, catalog["minecraft:test_instance"][key], want)
				}
			}
			if row := catalog["minecraft:test_environment"]["testpin:empty"]; !reflect.DeepEqual(row, map[string]any{"type": "minecraft:all_of", "definitions": nbt.List{Elem: nbt.TagEnd, Items: []any{}}}) {
				t.Fatalf("canonical environment %#v", row)
			}
			if err := pw.WritePacket(server.IDConfigFinishAck, nil); err != nil {
				t.Fatal(err)
			}
			_ = c.SetDeadline(time.Time{})
			if err := pw.WritePacket(server.IDPlayPlayerLoadedSB, nil); err != nil {
				t.Fatal(err)
			}
			drainToJoinBlackbox(t, tc.name, pr)
			awaitPlayReadyBlackbox(t, tc.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			command := func(text string) []string {
				serial++
				marker := fmt.Sprintf("TIP_END_%d", serial)
				sendCommand(t, pw, text)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			check := func(condition string) {
				serial++
				marker := fmt.Sprintf("TIP_PASS_%d", serial)
				if !structureSaidBB(command("execute "+condition+" run say "+marker), marker) {
					t.Errorf("failed %s", condition)
				}
			}
			command("tick freeze")
			command("setblock 0 69 0 stone")
			command("tp @s 0.5 70 0.5")
			pos := [3]int32{4, 70, 4}
			lines := command("setblock 4 70 4 test_instance_block")
			waitController := func(lines []string, test string) []string {
				deadline := time.Now().Add(3 * time.Second)
				for {
					for _, u := range structureBEUpdatesBB(t, lines) {
						data, _ := u.tag["data"].(map[string]any)
						if u.pos == pos && (test == "" || data["test"] == test) {
							return lines
						}
					}
					if time.Now().After(deadline) {
						t.Fatal("controller receipt missing")
					}
					time.Sleep(10 * time.Millisecond)
					lines = append(lines, sink.take()...)
				}
			}
			waitController(lines, "")
			request := testInstanceRequestBB{test: "testpin:function", size: [3]int32{2, 1, 2}, ignore: true}
			action := func(id int32) {
				sink.take()
				writeTestInstanceActionBB(t, pw, pos, id, request)
				lines := command("say TIP_ACTION")
				if id >= 2 {
					lines = waitController(lines, request.test)
				} else {
					deadline := time.Now().Add(3 * time.Second)
					for {
						received := false
						for _, l := range lines {
							if strings.HasPrefix(l, "0x7e:") {
								received = true
							}
						}
						if received {
							break
						}
						if time.Now().After(deadline) {
							t.Fatal("query status receipt missing")
						}
						time.Sleep(10 * time.Millisecond)
						lines = append(lines, sink.take()...)
					}
				}
				time.Sleep(100 * time.Millisecond)
				lines = append(lines, sink.take()...)
				for _, u := range structureBEUpdatesBB(t, lines) {
					t.Logf("action%d BE%#v", id, u.tag)
				}
				for _, l := range lines {
					p, body, _ := strings.Cut(l, ":")
					if p == "0x7e" || p == "0x79" {
						r := bytes.NewReader([]byte(body))
						value, err := nbt.ReadUnnamed(r)
						t.Logf("action%d %s %#v tail%x err%v", id, p, value, []byte(body)[len(body)-r.Len():], err)
					}
				}
			}

			clock := func() (int64, []string) {
				sendCommand(t, pw, "time query gametime")
				deadline := time.Now().Add(10 * time.Second)
				var history []string
				for time.Now().Before(deadline) {
					lines := sink.take()
					history = append(history, lines...)
					for _, l := range lines {
						key, args, ok := componentArgs(l)
						if ok && key == "commands.time.query.gametime" && len(args) > 0 {
							if value, ok := args[0].(int64); ok {
								return value, history
							}
						}
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("clock receipt missing")
				return 0, nil
			}
			advance := func(n int) []string {
				before, history := clock()
				history = append(history, command(fmt.Sprintf("tick step %d", n))...)
				deadline := time.Now().Add(10 * time.Second)
				for {
					after, lines := clock()
					history = append(history, lines...)
					if after >= before+int64(n) {
						if after != before+int64(n) {
							t.Fatalf("step advanced%d want%d", after-before, n)
						}
						return history
					}
					if time.Now().After(deadline) {
						t.Fatal("step timeout")
					}
				}
			}
			expectedStatus := map[string]string{
				"function1": "cleared", "function2": "finished", "function3": "finished",
				"block1": "cleared", "block2": "finished", "block3": "finished", "block4": "finished", "block5": "finished",
				"accept_before_fail": "finished", "missing_start": "finished", "multiple_start": "finished", "fail_block": "finished",
				"external_wait": "running", "external_frozen": "running", "external_finish": "finished",
				"external_fail_wait": "running", "external_fail1": "running", "external_fail2": "finished", "external_fail3": "finished",
				"unknown_function1": "cleared", "unknown_function2": "running", "unknown_function3": "finished",
			}
			for i := 1; i <= 10; i++ {
				status := "running"
				if i == 1 {
					status = "cleared"
				}
				if i >= 9 {
					status = "finished"
				}
				expectedStatus[fmt.Sprintf("timeout%d", i)] = status
			}
			tickError := func(message any, tick int32) any {
				return map[string]any{"translate": "test.error.tick", "with": nbt.List{Elem: nbt.TagCompound, Items: []any{message, map[string]any{"": tick}}}}
			}
			missing := func(mode string) any {
				return map[string]any{"translate": "test_block.error.missing", "with": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"translate": "test_block.mode." + mode}}}}
			}
			expectedError := map[string]any{
				"missing_start":     tickError(missing("start"), 0),
				"multiple_start":    tickError(map[string]any{"translate": "test_block.error.too_many", "with": nbt.List{Elem: nbt.TagCompound, Items: []any{map[string]any{"translate": "test_block.mode.start"}}}}, 0),
				"fail_block":        tickError(map[string]any{"": "failure λ"}, 1),
				"timeout9":          "Didn't succeed or fail within 5 ticks",
				"external_fail2":    tickError(map[string]any{"": "external failure"}, 2),
				"unknown_function3": "Trying to access missing test function: testpin:missing",
			}
			probe := func(label string, captures ...[]string) {
				var history []string
				for _, lines := range captures {
					history = append(history, lines...)
				}
				matched := 0
				for _, status := range []string{"cleared", "running", "finished"} {
					marker := "TIP_STATE_" + label + "_" + status
					lines := command(`execute if data block 4 70 4 {data:{status:"` + status + `"}} run say ` + marker)
					history = append(history, lines...)
					if structureSaidBB(lines, marker) {
						t.Logf("%s status%s", label, status)
						if want, known := expectedStatus[label]; known && status != want {
							t.Fatalf("%s status %s, want %s", label, status, want)
						}
						matched++
					}
				}
				time.Sleep(100 * time.Millisecond)
				history = append(history, sink.take()...)
				errorFound := false
				for _, u := range structureBEUpdatesBB(t, history) {
					t.Logf("%s BE%#v", label, u.tag)
					if u.pos != [3]int32{4, 70, 4} {
						continue
					}
					data, _ := u.tag["data"].(map[string]any)
					if want, ok := expectedError[label]; ok && data["status"] == "finished" {
						if !reflect.DeepEqual(data["error_message"], want) {
							t.Fatalf("%s error %#v, want %#v", label, data["error_message"], want)
						}
						errorFound = true
					}
				}
				if _, ok := expectedError[label]; ok && !errorFound {
					t.Fatalf("%s missing typed completion receipt", label)
				}
				if matched != 1 {
					t.Fatalf("%s matched%d status records", label, matched)
				}
			}
			action(0)
			action(1)
			action(6)
			command("fill 4 71 5 5 71 6 air")
			command("setblock 4 71 5 gold_block")
			action(4)
			action(1)
			action(6)
			for i := 1; i <= 3; i++ {
				probe(fmt.Sprintf("function%d", i), advance(1))
			}
			// A START adjacent to ACCEPT is an actual signal-driven test, not a
			// synthetic completion callback. Native mode/powered fields are witnessed.
			command(`setblock 4 71 5 test_block[mode=start]{mode:"start",message:"native start"}`)
			command(`setblock 5 71 5 test_block[mode=accept]{mode:"accept",message:"native accept"}`)
			check(`if data block 4 71 5 {mode:"start",powered:0b}`)
			check(`if data block 5 71 5 {mode:"accept",powered:0b}`)
			request.test = "testpin:block"
			action(4)
			action(0)
			action(1)
			action(6)
			for i := 1; i <= 5; i++ {
				advance(1)
				probe(fmt.Sprintf("block%d", i))
				power := "1b"
				if i == 1 {
					power = "0b"
				}
				check(`if data block 4 71 5 {mode:"start",powered:` + power + `}`)
				check(`if data block 5 71 5 {mode:"accept",powered:` + power + `}`)
				for _, mode := range []string{"start", "accept"} {
					for _, powered := range []string{"0b", "1b"} {
						marker := fmt.Sprintf("TIP_POWER_%d_%s_%s", i, mode, powered)
						x := 4
						if mode == "accept" {
							x = 5
						}
						if structureSaidBB(command(fmt.Sprintf(`execute if data block %d 71 5 {mode:"%s",powered:%s} run say %s`, x, mode, powered, marker)), marker) {
							t.Logf("block%d %s powered%s", i, mode, powered)
						}
					}
				}
			}
			// Both receiving blocks are powered by the same START in one runner tick.
			request.size = [3]int32{3, 1, 2}
			command("fill 4 71 5 6 71 6 air")
			command(`setblock 5 71 5 test_block[mode=start]{mode:"start"}`)
			command(`setblock 4 71 5 test_block[mode=accept]{mode:"accept"}`)
			command(`setblock 6 71 5 test_block[mode=fail]{mode:"fail",message:"must lose to accept"}`)
			action(4)
			action(6)
			probe("accept_before_fail", advance(2))
			check(`if data block 4 70 4 {data:{status:"finished"}}`)
			check(`unless data block 4 70 4 data.error_message`)
			request.size = [3]int32{2, 1, 2}
			// Negative controls establish failure state/components and the timeout clock.
			command("fill 4 71 5 6 71 6 air")
			command("setblock 4 71 5 gold_block")
			action(4)
			action(6)
			probe("missing_start", advance(3))
			for _, state := range []string{"air", "barrier"} {
				marker := "TIP_MISSING_SHELL_" + state
				if structureSaidBB(command("execute if block 3 71 5 "+state+" run say "+marker), marker) {
					t.Logf("missing_start shell%s", state)
				}
			}
			command(`setblock 4 71 5 test_block[mode=start]{mode:"start"}`)
			command(`setblock 5 71 5 test_block[mode=start]{mode:"start"}`)
			action(4)
			action(6)
			probe("multiple_start", advance(3))
			command(`setblock 5 71 5 test_block[mode=fail]{mode:"fail",message:"failure λ"}`)
			request.size = [3]int32{3, 1, 2}
			command(`setblock 6 71 5 test_block[mode=accept]{mode:"accept"}`)
			action(4)
			action(6)
			probe("fail_block", advance(3))
			for _, state := range []string{"air", "barrier"} {
				marker := "TIP_FAIL_SHELL_" + state
				if structureSaidBB(command("execute if block 3 71 5 "+state+" run say "+marker), marker) {
					t.Logf("fail_block shell%s", state)
				}
			}
			command("setblock 5 71 5 air")
			action(4)
			action(6)
			for i := 1; i <= 10; i++ {
				probe(fmt.Sprintf("timeout%d", i), advance(1))
			}
			request.size = [3]int32{3, 1, 2}
			command("fill 4 71 5 6 71 6 air")
			command(`setblock 4 71 5 test_block[mode=start]{mode:"start"}`)
			command(`setblock 6 71 5 test_block[mode=accept]{mode:"accept"}`)
			action(4)
			action(6)
			probe("external_wait", advance(2))
			check(`if data block 6 71 5 {mode:"accept",powered:0b}`)
			command("setblock 7 71 5 redstone_block")
			check(`if data block 6 71 5 {mode:"accept",powered:1b}`)
			probe("external_frozen")
			command(`data merge block 6 71 5 {message:"edited while triggered"}`)
			check(`if data block 6 71 5 {mode:"accept",powered:1b,message:"edited while triggered"}`)
			probe("external_finish", advance(1))
			command("setblock 7 71 5 air")
			command("setblock 5 71 7 air")
			command("fill 4 71 5 6 71 6 air")
			command(`setblock 4 71 5 test_block[mode=start]{mode:"start"}`)
			command(`setblock 6 71 5 test_block[mode=accept]{mode:"accept"}`)
			command(`setblock 5 71 6 test_block[mode=fail]{mode:"fail",message:"external failure"}`)
			check(`if data block 6 71 5 {powered:0b}`)
			check(`if data block 5 71 6 {powered:0b}`)
			action(4)
			action(6)
			probe("external_fail_wait", advance(2))
			command("setblock 5 71 7 redstone_block")
			check(`if data block 5 71 6 {mode:"fail",powered:1b}`)
			for i := 1; i <= 3; i++ {
				probe(fmt.Sprintf("external_fail%d", i), advance(1))
			}
			request.test = "testpin:unknown_function"
			action(6)
			for i := 1; i <= 3; i++ {
				probe(fmt.Sprintf("unknown_function%d", i), advance(1))
			}

		})
	}
}
