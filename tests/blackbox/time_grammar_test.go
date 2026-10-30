//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/server"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTimeGrammarParity(t *testing.T) {
	const name = "TimeGrammar"
	tg := requireBlackboxFiles(t, map[string]string{"gamemode": "creative"}, map[string]string{"ops.json": opsJSONFor(name)}, func(s *server.Server) { s.GameMode = server.GameModeCreative; s.SetOpLevel(name, 4) })
	defer tg.cleanup()
	for _, tc := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(tc.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackboxView(t, tc.addr, name, 1)
			defer c.Close()
			drainToJoinBlackbox(t, tc.name, pr)
			awaitPlayReadyBlackbox(t, tc.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				serial++
				marker := fmt.Sprintf("TIME_GRAMMAR_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			command("tick freeze")
			for _, tc := range []struct {
				cmd, key, bad string
				end           bool
				total         *int64
			}{
				{"time add 1t!", "command.expected.separator", "!", false, nil},
				{"time add 1s/", "command.expected.separator", "/", false, nil},
				{"time add 1t:x", "command.expected.separator", ":x", false, nil},
				{"time set 1t!", "command.unknown.argument", "1t!", false, nil},
				{"time set 1t:x", "commands.time.no_time_marker_found", "", false, timeGrammarTotalBB(50000)},
				{"time set day extra", "command.unknown.argument", "extra", false, nil},
				{"time add 1d extra", "command.unknown.argument", "extra", false, nil},
				{"time add 1q extra", "argument.time.invalid_unit", "1q", true, nil},
				{"time set 1q extra", "command.unknown.argument", "extra", false, nil},
				{"time set a:b:c", "command.unknown.argument", "a:b:c", false, nil},
				{"time set ..:noon", "command.unknown.argument", "..:noon", false, nil},
				{"execute in the_nether run time set +1", "command.unknown.argument", "+1", false, nil},
				{"execute in the_nether run time set NOON", "command.unknown.argument", "NOON", false, nil},
				{"time of overworld add 1t!", "command.expected.separator", "!", false, nil},
				{"time of overworld set day extra", "command.unknown.argument", "extra", false, nil},
				{"time set 1.00000006d", "commands.time.set.absolute", "", false, timeGrammarTotalBB(24000)},
			} {
				command("time set 50000")
				lines := command(tc.cmd)
				var clocks []worldClockPacketBB
				for _, packet := range worldClockPacketsBB(t, lines) {
					if len(packet.rows) > 0 {
						clocks = append(clocks, packet)
					}
				}
				if tc.total == nil {
					if len(clocks) > 0 {
						t.Errorf("%s unexpectedclock%#v", tc.cmd, clocks)
					}
				} else if len(clocks) != 1 || len(clocks[0].rows) != 1 || clocks[0].rows[0] != (worldClockRowBB{id: 0, total: *tc.total, rate: 1}) {
					t.Errorf("%s clock%#v want%d", tc.cmd, clocks, *tc.total)
				}
				var replies []string
				for _, line := range lines {
					if tree, ok := componentTree(line, true); ok {
						replies = append(replies, tree)
					}
				}
				message := map[string]any{"translate": tc.key}
				var want []string
				switch tc.key {
				case "commands.time.set.absolute":
					message["with"] = nbt.List{Items: []any{map[string]any{"": "minecraft:overworld"}, map[string]any{"": int32(24000)}}}
					want = append(want, renderNBT(message, true))
				default:
					if tc.key == "commands.time.no_time_marker_found" {
						message["with"] = nbt.List{Items: []any{"ResourceKey[minecraft:clock_time_marker / 1t:x]", "minecraft:overworld"}}
					}
					want = append(want, renderNBT(map[string]any{"color": "red", "text": "", "extra": nbt.List{Items: []any{message}}}, true))
				}
				if tc.bad != "" {
					cursor := strings.LastIndex(tc.cmd, tc.bad)
					if tc.end {
						cursor += len(tc.bad)
					}
					want = append(want, timeGrammarContextBB(tc.cmd, cursor))
				}
				if !reflect.DeepEqual(replies, want) {
					t.Errorf("%s replies%q want%q", tc.cmd, replies, want)
				}
			}
		})
	}
}

func timeGrammarTotalBB(total int64) *int64 { return &total }

// Context expectations name a native-measured cursor independently of the
// production token parser, including the ten-character preview and styles.
func timeGrammarContextBB(command string, cursor int) string {
	start := cursor - 10
	if start < 0 {
		start = 0
	}
	var parts []any
	if cursor > 10 {
		parts = append(parts, map[string]any{"": "..."})
	}
	parts = append(parts, map[string]any{"": command[start:cursor]}, map[string]any{"color": "red", "text": command[cursor:], "underlined": int8(1)}, map[string]any{"color": "red", "italic": int8(1), "translate": "command.context.here"})
	context := map[string]any{"click_event": map[string]any{"action": "suggest_command", "command": "/" + command}, "color": "gray", "text": "", "extra": nbt.List{Items: parts}}
	return renderNBT(map[string]any{"color": "red", "text": "", "extra": nbt.List{Items: []any{context}}}, true)
}
