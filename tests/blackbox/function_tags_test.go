//go:build blackbox

package blackbox

import (
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

func TestCustomFunctionTagsParity(t *testing.T) {
	const name = "FunctionTags"
	bodies := map[string]string{
		"pack.mcmeta":                                     `{"pack":{"description":"function tags","min_format":107,"max_format":107}}`,
		"data/tagpin/function/a.mcfunction":               "scoreboard players operation order tagstate *= ten tagstate\nscoreboard players add order tagstate 1\n",
		"data/tagpin/function/b.mcfunction":               "scoreboard players operation order tagstate *= ten tagstate\nscoreboard players add order tagstate 2\n",
		"data/tagpin/function/file_override.mcfunction":   "scoreboard players add invalid_executed tagstate 1\n",
		"data/tagpin/function/bad_unknown.mcfunction":     "scoreboard players add invalid_executed tagstate 1\nunknown_function_literal\nscoreboard players add invalid_executed tagstate 1\n",
		"data/tagpin/function/bad_permission.mcfunction":  "scoreboard players add invalid_executed tagstate 1\ntick query\nscoreboard players add invalid_executed tagstate 1\n",
		"data/tagpin/function/bad_slash.mcfunction":       "scoreboard players add invalid_executed tagstate 1\n/say forbidden_slash\nscoreboard players add invalid_executed tagstate 1\n",
		"data/tagpin/function/bad_bare.mcfunction":        "scoreboard players add invalid_executed tagstate 1\nsetblock\nscoreboard players add invalid_executed tagstate 1\n",
		"data/tagpin/tags/function/required_bad.json":     `{"values":["tagpin:bad_unknown"]}`,
		"data/tagpin/function/load.mcfunction":            "scoreboard objectives add tagstate dummy\nscoreboard players add load_runs tagstate 1\n",
		"data/tagpin/function/tick.mcfunction":            "scoreboard players add automatic tagstate 1\nexecute if entity @s run scoreboard players add executors tagstate 1\nexecute store result score tick_seen_age tagstate run time query gametime\nschedule function tagpin:after_tick 1t replace\n",
		"data/tagpin/function/after_tick.mcfunction":      "scoreboard players add scheduled tagstate 1\n",
		"data/tagpin/tags/function/group.json":            `{"values":["tagpin:a","#tagpin:nested","tagpin:a"]}`,
		"data/tagpin/tags/function/nested.json":           `{"values":["tagpin:b","tagpin:a"]}`,
		"data/tagpin/tags/function/optional.json":         `{"values":[{"id":"tagpin:missing","required":false},{"id":"#tagpin:missing","required":false},"tagpin:b"]}`,
		"data/tagpin/tags/function/merge.json":            `{"values":["tagpin:a"]}`,
		"data/tagpin/tags/function/replace.json":          `{"values":["tagpin:a"]}`,
		"data/tagpin/tags/function/invalid_override.json": `{"values":["tagpin:a"]}`,
		"data/tagpin/tags/function/empty.json":            `{"values":[]}`,
		"data/tagpin/tags/function/broken.json":           `{"values":["tagpin:missing"]}`,
		"data/tagpin/tags/function/mixed_a.json":          `{"values":["tagpin:a",{"id":"#tagpin:mixed_b","required":false}]}`,
		"data/tagpin/tags/function/mixed_b.json":          `{"values":["tagpin:b","#tagpin:mixed_a"]}`,
		"data/tagpin/tags/function/cycle_a.json":          `{"values":["#tagpin:cycle_b"]}`,
		"data/tagpin/tags/function/cycle_b.json":          `{"values":["#tagpin:cycle_a"]}`,
		"data/minecraft/tags/function/load.json":          `{"values":["tagpin:load"]}`,
		"data/minecraft/tags/function/tick.json":          `{"values":["tagpin:tick"]}`,
	}
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(name)}
	for path, body := range bodies {
		rel := "datapacks/tagpin/" + path
		files["world/"+rel] = body
		local := filepath.Join(directory, rel)
		if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	overlay := map[string]string{
		"pack.mcmeta": `{"pack":{"description":"function tag overlay","min_format":107,"max_format":107}}`,
		"data/tagpin/function/file_override.mcfunction":   "unknown_function_literal\n",
		"data/tagpin/tags/function/merge.json":            `{"values":["tagpin:b"]}`,
		"data/tagpin/tags/function/replace.json":          `{"replace":true,"values":["tagpin:b"]}`,
		"data/tagpin/tags/function/invalid_override.json": `{"replace":true,"values":["tagpin:b",7]}`,
	}
	for path, body := range overlay {
		rel := "datapacks/z_tagpin/" + path
		files["world/"+rel] = body
		local := filepath.Join(directory, rel)
		if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tg := requireBlackboxFiles(t, nil, files, func(s *server.Server) { s.WorldDir = directory })
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, name)
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			clock := int32(-1)
			serial := 0
			fold := func(lines []string) {
				for _, line := range lines {
					prefix := fmt.Sprintf("%#x:", server.IDPlayUpdateScore)
					if !strings.HasPrefix(line, prefix) {
						continue
					}
					r := protocol.NewPacketReader([]byte(strings.TrimPrefix(line, prefix)))
					holder, e1 := r.String()
					objective, e2 := r.String()
					value, e3 := r.VarInt()
					if e1 == nil && e2 == nil && e3 == nil && holder == name && objective == "tagclock" {
						clock = value
					}
				}
			}
			command := func(cmd string) []string {
				sendCommand(t, pw, cmd)
				serial++
				marker := fmt.Sprintf("FUNCTION_TAG_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				lines := waitForExactSayBB(t, sink, marker, 10*time.Second)
				fold(lines)
				return lines
			}
			probe := func(condition, label string) bool {
				marker := "FUNCTION_TAG_RESULT_" + label
				return structureSaidBB(command("execute "+condition+" run say "+marker), marker)
			}
			expect := func(condition, label string) {
				if !probe(condition, label) {
					t.Errorf("%s: %s", label, condition)
				}
			}
			step := func(n int) {
				before := gameTimeOf(t, pw, sink)
				command(fmt.Sprintf("tick step %d", n))
				deadline := time.Now().Add(15 * time.Second)
				for time.Now().Before(deadline) {
					if gameTimeOf(t, pw, sink) == before+int64(n) {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatalf("step%d not complete", n)
			}
			command("tick freeze")
			command("scoreboard objectives add tagstate dummy")
			for _, holder := range []string{"load_runs", "automatic", "scheduled", "executors"} {
				command("scoreboard players add " + holder + " tagstate 0")
			}
			command("scoreboard players set ten tagstate 10")
			expect("if score load_runs tagstate matches 1", "load_once")
			command("scoreboard objectives add tagclock minecraft.custom:minecraft.play_time")
			command("scoreboard objectives setdisplay sidebar tagclock")
			command("scoreboard players operation automatic_before tagstate = automatic tagstate")
			beforeClock := clock
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				fold(sink.take())
				if clock >= 0 && beforeClock >= 0 && clock >= beforeClock+2 {
					break
				}
				if beforeClock < 0 && clock >= 0 {
					beforeClock = clock
				}
				time.Sleep(10 * time.Millisecond)
			}
			if clock < beforeClock+2 {
				t.Fatal("player clock did not advance while frozen")
			}
			frozenEqual := probe("if score automatic tagstate = automatic_before tagstate", "timing_frozen_equal")
			if !frozenEqual {
				t.Error("automatic tick function ran while frozen")
			}

			command("execute store result score current_age tagstate run time query gametime")
			command("scoreboard players operation age_delta tagstate = current_age tagstate")
			command("scoreboard players operation age_delta tagstate -= tick_seen_age tagstate")
			t.Logf("timing frozen_equal=%v age_delta_zero=%v age_delta_one=%v scheduled_equal=%v", frozenEqual, probe("if score age_delta tagstate matches 0", "timing_age0"), probe("if score age_delta tagstate matches 1", "timing_age1"), probe("if score scheduled tagstate = automatic tagstate", "timing_schedule_equal"))
			expect("if score age_delta tagstate matches 1", "automatic_pre_world_age")
			expect("if score scheduled tagstate = automatic tagstate", "automatic_schedule_same_world_tick")

			expect("if score executors tagstate matches 0", "automatic_no_executor")
			expect("if score automatic tagstate matches 1..", "automatic_ran")
			expect("if score tick_seen_age tagstate matches 0..", "automatic_recorded_age")
			command("scoreboard players set order tagstate 0")
			command("execute store result score result tagstate run function #tagpin:group")
			expect("if score order tagstate matches 12", "nested_order_dedup")
			expect("unless score result tagstate matches -2147483648..2147483647", "no_implicit_result")
			for _, line := range command("scoreboard players get result tagstate") {
				if key, args, ok := componentArgs(line); ok {
					t.Logf("function result key=%s args=%v", key, args)
				}
			}
			command("scoreboard players set order tagstate 0")
			command("function #tagpin:optional")
			expect("if score order tagstate matches 2", "optional_missing")
			for _, control := range []struct {
				tag  string
				want int
			}{{"merge", 12}, {"replace", 2}, {"invalid_override", 1}} {
				command("scoreboard players set order tagstate 0")
				feedback := false
				for _, line := range command("function #tagpin:" + control.tag) {
					if !strings.HasPrefix(line, fmt.Sprintf("%#x:", server.IDPlaySystemChatCB)) {
						continue
					}
					key, args, ok := componentArgs(line)
					if !ok {
						continue
					}
					t.Logf("validtag %s key=%s args=%v", control.tag, key, args)
					if control.tag == "merge" {
						want := map[string]any{"text": "", "extra": nbt.List{Elem: 10, Items: []any{map[string]any{"": "tagpin:a"}, map[string]any{"color": "gray", "text": ", "}, map[string]any{"": "tagpin:b"}}}}
						feedback = key == "commands.function.scheduled.multiple" && len(args) == 1 && reflect.DeepEqual(args[0], want)
					} else {
						want := "tagpin:a"
						if control.tag == "replace" {
							want = "tagpin:b"
						}
						feedback = key == "commands.function.scheduled.single" && len(args) == 1 && args[0] == want
					}
				}
				if !feedback {
					t.Errorf("tag %s feedback component mismatch", control.tag)
				}
				expect(fmt.Sprintf("if score order tagstate matches %d", control.want), "overlay_"+control.tag)
			}
			for _, control := range []struct {
				tag  string
				want int
			}{{"mixed_a", 1}, {"mixed_b", 21}} {
				command("scoreboard players set order tagstate 0")
				command("function #tagpin:" + control.tag)
				expect(fmt.Sprintf("if score order tagstate matches %d", control.want), "mixed_"+control.tag)
			}
			command("scoreboard players set order tagstate 0")
			command("scoreboard players set success tagstate 7")
			command("execute store success score success tagstate run function #tagpin:group")
			expect("if score order tagstate matches 12", "success_store_executed")
			expect("if score success tagstate matches 7", "no_implicit_success")
			command("scoreboard players set direct_success tagstate 7")
			command("execute store success score direct_success tagstate run function tagpin:a")
			command("execute store result score direct_result tagstate run function tagpin:a")
			expect("if score direct_success tagstate matches 7", "direct_no_implicit_success")
			expect("unless score direct_result tagstate matches -2147483648..2147483647", "direct_no_implicit_result")
			for _, holder := range []string{"direct_success", "direct_result"} {
				for _, line := range command("scoreboard players get " + holder + " tagstate") {
					if key, args, ok := componentArgs(line); ok {
						t.Logf("direct %s key=%s args=%v", holder, key, args)
					}
				}
			}
			for _, line := range command("scoreboard players get success tagstate") {
				if key, args, ok := componentArgs(line); ok {
					t.Logf("function success key=%s args=%v", key, args)
				}
			}
			for _, tag := range []string{"empty", "missing", "broken", "cycle_a", "required_bad"} {
				found := false
				for _, line := range command("function #tagpin:" + tag) {
					if key, args, ok := componentArgs(line); ok {
						t.Logf("tag %s key=%s args=%v", tag, key, args)
						if key == "commands.function.scheduled.no_functions" && len(args) == 1 && args[0] == "tagpin:"+tag {
							found = true
							root, err := nbt.ReadUnnamed(strings.NewReader(line[strings.IndexByte(line, ':')+1:]))
							want := map[string]any{"text": "", "color": "red", "extra": nbt.List{Elem: 10, Items: []any{map[string]any{"translate": key, "with": nbt.List{Elem: 8, Items: []any{"tagpin:" + tag}}}}}}
							if err != nil || !reflect.DeepEqual(root, want) {
								t.Errorf("tag %s failure component=%v err=%v", tag, root, err)
							}
						}
					}
				}
				if !found {
					t.Errorf("tag %s missing no-functions feedback", tag)
				}
			}
			command("scoreboard players set empty_success tagstate 7")
			command("execute store success score empty_success tagstate run function #tagpin:missing")
			command("execute store result score empty_result tagstate run function #tagpin:missing")
			expect("if score empty_success tagstate matches 0", "empty_success_callback")
			expect("if score empty_result tagstate matches 0", "empty_zero_result")
			for _, line := range command("execute as @s run function #tagpin:missing") {
				if key, _, ok := componentArgs(line); ok && key == "commands.function.scheduled.no_functions" {
					t.Error("forked empty tag leaked runtime failure feedback")
				}
			}
			command("scoreboard players set fork_empty tagstate 7")
			command("execute as @s store success score fork_empty tagstate run function #tagpin:missing")
			expect("if score fork_empty tagstate matches 0", "fork_empty_false_result")
			command("scoreboard players set invalid_executed tagstate 0")
			for _, id := range []string{"bad_unknown", "bad_permission", "bad_slash", "bad_bare", "file_override"} {
				found := false
				for _, line := range command("function tagpin:" + id) {
					if key, args, ok := componentArgs(line); ok && key == "arguments.function.unknown" && len(args) == 1 && args[0] == "tagpin:"+id {
						found = true
					}
				}
				if !found {
					t.Errorf("parse-invalid function %s remained registered", id)
				}
			}
			expect("if score invalid_executed tagstate matches 0", "invalid_resource_never_started")
			command("scoreboard players set order tagstate 0")
			command("schedule function #tagpin:empty 100t append")
			command("execute store result score cleared_empty tagstate run schedule clear #tagpin:empty")
			expect("if score cleared_empty tagstate matches 1", "schedule_empty_tag")
			command("schedule function #tagpin:missing 100t append")
			command("execute store result score cleared_missing tagstate run schedule clear #tagpin:missing")
			expect("if score cleared_missing tagstate matches 1", "schedule_missing_tag")
			command("schedule function #tagpin:group 3t append")
			step(2)
			expect("if score order tagstate matches 0", "tag_before_deadline")
			command("scoreboard players operation automatic_delta tagstate = automatic tagstate")
			command("scoreboard players operation automatic_delta tagstate -= automatic_before tagstate")
			expect("if score automatic_delta tagstate matches 2", "automatic_once_per_step")
			step(1)
			expect("if score order tagstate matches 12", "tag_at_deadline")
			command("schedule function #tagpin:group 3t append")
			command("schedule function #tagpin:group 4t append")
			command("execute store result score cleared tagstate run schedule clear #tagpin:group")
			expect("if score cleared tagstate matches 2", "tag_clear_name")
			expect("if score load_runs tagstate matches 1", "load_still_once")
		})
	}
}
