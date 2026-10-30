//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTimeArgumentMarkersParity(t *testing.T) {
	const name = "TimeArguments"
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
				marker := fmt.Sprintf("TIME_ARGUMENT_%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			command("tick freeze")
			assert := func(label string, lines []string, total *int64, key string, args []any) {
				t.Helper()
				var clocks []worldClockPacketBB
				for _, packet := range worldClockPacketsBB(t, lines) {
					if len(packet.rows) > 0 {
						clocks = append(clocks, packet)
					}
				}
				if total == nil {
					if len(clocks) != 0 {
						t.Errorf("%s unexpected clocks%#v", label, clocks)
					}
				} else {
					if len(clocks) != 1 || len(clocks[0].rows) != 1 || clocks[0].rows[0] != (worldClockRowBB{id: 0, total: *total, rate: 1}) {
						t.Errorf("%s clocks%#v want total%d", label, clocks, *total)
					}
				}
				count := 0
				for _, line := range lines {
					k, a, ok := componentArgs(line)
					if !ok || !(strings.HasPrefix(k, "commands.time.") || strings.HasPrefix(k, "argument.time.") || strings.HasPrefix(k, "parsing.") || k == "command.unknown.argument") {
						continue
					}
					count++
					if k != key || args != nil && !reflect.DeepEqual(a, args) {
						t.Errorf("%s reply %s %#v want%s %#v", label, k, a, key, args)
					}
				}
				if count != 1 {
					t.Errorf("%s reply count%d want1", label, count)
				}
			}
			for _, arg := range []struct {
				raw   string
				ticks int64
				bad   bool
			}{
				{"1d", 24000, false}, {"1s", 20, false}, {"1t", 1, false}, {"2.5s", 50, false}, {".5t", 1, false}, {".025s", 1, false}, {"-.5t", 0, false}, {"-.51t", -1, false}, {"-1.5t", -1, false}, {"16777217", 16777216, false}, {"0.49999999t", 1, false}, {"1q", 0, true}, {"1e2", 0, true}, {"+1", 0, true},
			} {
				for _, action := range []string{"set", "add"} {
					command("time set 50000")
					lines := command("time " + action + " " + arg.raw)
					total, key := arg.ticks, "commands.time.set.absolute"
					if action == "add" {
						total += 50000
					}
					var expected *int64 = &total
					var args []any
					if action == "set" && (arg.bad || arg.ticks < 0) {
						if arg.raw == "+1" {
							expected = nil
							key = "command.unknown.argument"
						} else {
							total = 50000
							key = "commands.time.no_time_marker_found"
							args = []any{"ResourceKey[minecraft:clock_time_marker / minecraft:" + arg.raw + "]", "minecraft:overworld"}
						}
					} else if arg.bad {
						expected = nil
						key = "argument.time.invalid_unit"
						if arg.raw == "+1" {
							key = "parsing.float.expected"
						}
					}
					assert(action+" "+arg.raw, lines, expected, key, args)
				}
			}
			for _, marker := range []struct {
				name            string
				from50k, from6k int64
			}{
				{"day", 73000, 25000}, {"noon", 54000, 30000}, {"night", 61000, 13000}, {"midnight", 66000, 18000}, {"minecraft:roll_village_siege", 66000, 18000}, {"minecraft:wake_up_from_sleep", 72000, 24000}, {":noon", 54000, 30000}, {":wake_up_from_sleep", 72000, 24000},
			} {
				for _, start := range []int64{50000, 6000} {
					command(fmt.Sprintf("time set %d", start))
					total := marker.from50k
					if start == 6000 {
						total = marker.from6k
					}
					id := marker.name
					if strings.HasPrefix(id, ":") {
						id = "minecraft" + id
					}
					if !strings.Contains(id, ":") {
						id = "minecraft:" + id
					}
					assert(fmt.Sprintf("marker%s from%d", id, start), command("time set "+marker.name), &total, "commands.time.set.time_marker", []any{"minecraft:overworld", id})
				}
			}
		})
	}
}
