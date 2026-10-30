//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestScheduleTimeGrammarParity(t *testing.T) {
	const name = "ScheduleGrammar"
	directory := t.TempDir()
	files := map[string]string{"ops.json": opsJSONFor(name)}
	for path, body := range map[string]string{"pack.mcmeta": `{"pack":{"description":"time grammar","min_format":107,"max_format":107}}`, "data/timearg/function/noop.mcfunction": "say SCHEDULE_NOOP\n"} {
		relative := "datapacks/timearg/" + path
		files["world/"+relative] = body
		local := filepath.Join(directory, relative)
		if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, files, func(s *server.Server) {
		s.WorldDir = directory
		s.GameMode = server.GameModeCreative
		s.SetOpLevel(name, 4)
	})
	defer tg.cleanup()
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackboxView(t, target.addr, name, 1)
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				serial++
				marker := fmt.Sprintf("SCHEDULE_GRAMMAR_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			render := func(lines []string) []string {
				var out []string
				for _, line := range lines {
					if tree, ok := componentTree(line, true); ok {
						out = append(out, tree)
					}
				}
				return out
			}
			command("tick freeze")
			age := gameTimeOf(t, pw, sink)
			cases := []struct {
				cmd, key, bad string
				end           bool
				with          []any
				ticks         int32
			}{
				{"schedule function timearg:noop 1t!", "command.expected.separator", "!", false, nil, 0},
				{"schedule function timearg:noop 1s/", "command.expected.separator", "/", false, nil, 0},
				{"schedule function timearg:noop 1t:x", "command.expected.separator", ":x", false, nil, 0},
				{"schedule function timearg:noop 1q extra", "argument.time.invalid_unit", "1q", true, nil, 0},
				{"schedule function timearg:noop 1d extra", "command.unknown.argument", "extra", false, nil, 0},
				{"schedule function timearg:noop -1t", "argument.time.tick_count_too_low", "-1t", true, []any{int32(0), int32(-1)}, 0},
				{"schedule function timearg:noop -.5t", "commands.schedule.same_tick", "", false, nil, 0},
				{"schedule function missing:noop 1t!", "command.expected.separator", "!", false, nil, 0},
				{"schedule function missing:noop 1q", "argument.time.invalid_unit", "1q", true, nil, 0},
				{"schedule function missing:noop -1t", "argument.time.tick_count_too_low", "-1t", true, []any{int32(0), int32(-1)}, 0},
				{"schedule function missing:noop 1t extra", "command.unknown.argument", "extra", false, nil, 0},
				{"schedule function missing:noop 1t", "arguments.function.unknown", "", false, []any{"missing:noop"}, 0},
				{"schedule function timearg:noop 1t append extra", "command.unknown.argument", "extra", false, nil, 0},
				{"schedule function timearg:noop 1t replace extra", "command.unknown.argument", "extra", false, nil, 0},
				{"schedule function timearg:noop 1t garbage", "command.unknown.argument", "garbage", false, nil, 0},
				{"schedule function #missing:noop 1t!", "command.expected.separator", "!", false, nil, 0},
				{"schedule function #missing:noop 1t", "commands.schedule.created.tag", "", false, nil, 1},
				{"schedule function timearg:noop 1.5t", "commands.schedule.created.function", "", false, nil, 2},
				{"schedule function timearg:noop 1t append", "commands.schedule.created.function", "", false, nil, 1},
			}
			for _, tc := range cases {
				target := strings.Fields(tc.cmd)[2]
				command("schedule clear " + target)
				got := render(command(tc.cmd))
				var want []string
				if tc.ticks > 0 {
					name := strings.TrimPrefix(target, "#")
					want = []string{renderNBT(map[string]any{"translate": tc.key, "with": nbt.List{Items: []any{map[string]any{"": name}, map[string]any{"": tc.ticks}, map[string]any{"": age + int64(tc.ticks)}}}}, true)}
				} else {
					message := map[string]any{"translate": tc.key}
					if tc.with != nil {
						message["with"] = nbt.List{Items: tc.with}
					}
					want = []string{scheduleGrammarErrorBB(message)}
					if tc.bad != "" {
						cursor := strings.LastIndex(tc.cmd, tc.bad)
						if tc.end {
							cursor += len(tc.bad)
						}
						want = append(want, scheduleGrammarContextBB(tc.cmd, cursor))
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s reply%q want%q", tc.cmd, got, want)
				}
				cleared := render(command("schedule clear " + target))
				clearWant := []string{scheduleGrammarErrorBB(map[string]any{"translate": "commands.schedule.cleared.failure", "with": nbt.List{Items: []any{target}}})}
				if tc.ticks > 0 {
					clearWant = []string{renderNBT(map[string]any{"translate": "commands.schedule.cleared.success", "with": nbt.List{Items: []any{map[string]any{"": int32(1)}, map[string]any{"": target}}}}, true)}
				}
				if !reflect.DeepEqual(cleared, clearWant) {
					t.Errorf("after%s queue clear%q want%q", tc.cmd, cleared, clearWant)
				}
			}
		})
	}
}

func scheduleGrammarErrorBB(message map[string]any) string {
	return renderNBT(map[string]any{"color": "red", "text": "", "extra": nbt.List{Items: []any{message}}}, true)
}
func scheduleGrammarContextBB(command string, cursor int) string {
	start := cursor - 10
	if start < 0 {
		start = 0
	}
	var parts []any
	if cursor > 10 {
		parts = append(parts, map[string]any{"": "..."})
	}
	parts = append(parts, map[string]any{"": command[start:cursor]})
	if cursor < len(command) {
		parts = append(parts, map[string]any{"color": "red", "text": command[cursor:], "underlined": int8(1)})
	}
	parts = append(parts, map[string]any{"color": "red", "italic": int8(1), "translate": "command.context.here"})
	context := map[string]any{"click_event": map[string]any{"action": "suggest_command", "command": "/" + command}, "color": "gray", "text": "", "extra": nbt.List{Items: parts}}
	return scheduleGrammarErrorBB(context)
}
