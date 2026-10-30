//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/nbt"
	"mc-go/internal/protocol"
	"mc-go/internal/server"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestScoreboardTrackingParity(t *testing.T) {
	const name = "ScoreTrack"
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(name)}, nil)
	defer tg.cleanup()
	cases := []struct {
		command string
		want    []string
	}{
		{"scoreboard objectives add diag dummy", []string{}},
		{"scoreboard players set one diag 1", []string{}},
		{"scoreboard players get one diag", []string{}},
		{"scoreboard objectives modify diag displayname \"HiddenName\"", []string{}},
		{"scoreboard objectives setdisplay sidebar diag", []string{"objective:diag:0", "display:1:diag", "score:one:diag:1"}},
		{"scoreboard players set one diag 2", []string{"score:one:diag:2"}},
		{"scoreboard objectives setdisplay below_name diag", []string{"display:2:diag"}},
		{"scoreboard objectives setdisplay sidebar", []string{"display:1:"}},
		{"scoreboard players set one diag 3", []string{"score:one:diag:3"}},
		{"scoreboard objectives setdisplay below_name", []string{"objective:diag:1"}},
		{"scoreboard players set one diag 4", []string{}},
		{"scoreboard players reset one diag", []string{"reset:one:*"}},
		{"scoreboard players set one diag 5", []string{}},
		{"scoreboard objectives setdisplay sidebar diag", []string{"objective:diag:0", "display:1:diag", "score:one:diag:5"}},
		{"scoreboard players reset one diag", []string{"reset:one:*"}},
		{"scoreboard players set one diag 6", []string{"score:one:diag:6"}},
		{"scoreboard objectives add second dummy", []string{}},
		{"scoreboard players set one second 40", []string{}},
		{"scoreboard objectives modify diag displayname \"ShownName\"", []string{"objective:diag:2"}},
		{"scoreboard players reset absent diag", []string{}},
		{"scoreboard players reset one diag", []string{"reset:one:diag"}},
		{"scoreboard players set one diag 6", []string{"score:one:diag:6"}},
		{"scoreboard players reset one second", []string{}},
		{"scoreboard players set one second 40", []string{}},
		{"scoreboard objectives setdisplay below_name diag", []string{"display:2:diag"}},
		{"scoreboard objectives setdisplay sidebar second", []string{"display:1:second", "objective:second:0", "display:1:second", "score:one:second:40"}},
		{"scoreboard objectives setdisplay list diag", []string{"display:0:diag"}},
		{"scoreboard objectives setdisplay sidebar diag", []string{"objective:second:1", "display:1:diag"}},
		{"scoreboard objectives setdisplay below_name second", []string{"display:2:second", "objective:second:0", "display:2:second", "score:one:second:40"}},
		{"scoreboard objectives setdisplay list second", []string{"display:0:second", "display:0:second"}},
		{"scoreboard objectives remove diag", []string{"objective:diag:1"}},
		{"scoreboard objectives remove second", []string{"display:0:", "objective:second:1"}},
		{"scoreboard objectives add hidden2 dummy", []string{}},
		{"scoreboard players set one hidden2 9", []string{}},
		{"scoreboard players reset one", []string{"reset:one:*"}},
		{"scoreboard players reset one", []string{}},
		{"scoreboard players reset absent hidden2", []string{}},
		{"scoreboard objectives remove hidden2", []string{}},
	}
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, name)
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			command := func(cmd string, n int) []string {
				sendCommand(t, pw, cmd)
				marker := fmt.Sprintf("SCORE_TRACK_END_%d", n)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			command("tick freeze", 0)
			for n, control := range cases {
				got := []string{}
				for _, line := range command(control.command, n+1) {
					i := strings.IndexByte(line, ':')
					if i < 0 {
						continue
					}
					r := protocol.NewPacketReader([]byte(line[i+1:]))
					switch line[:i] {
					case fmt.Sprintf("0x%x", server.IDPlayObjectiveCB):
						name, _ := r.String()
						mode, _ := r.Byte()
						got = append(got, fmt.Sprintf("objective:%s:%d", name, mode))
					case fmt.Sprintf("0x%x", server.IDPlayDisplayObjective):
						slot, _ := r.VarInt()
						name, _ := r.String()
						got = append(got, fmt.Sprintf("display:%d:%s", slot, name))
					case fmt.Sprintf("0x%x", server.IDPlayUpdateScore):
						holder, _ := r.String()
						obj, _ := r.String()
						value, _ := r.VarInt()
						got = append(got, fmt.Sprintf("score:%s:%s:%d", holder, obj, value))
					case fmt.Sprintf("0x%x", server.IDPlayResetScore):
						holder, _ := r.String()
						has, _ := r.Bool()
						obj := "*"
						if has {
							obj, _ = r.String()
						}
						got = append(got, fmt.Sprintf("reset:%s:%s", holder, obj))
					}
					if _, _, ok := componentArgs(line); ok {
						root, _ := nbt.ReadUnnamed(strings.NewReader(line[i+1:]))
						if m, ok := root.(map[string]any); ok && m["color"] == "red" {
							t.Errorf("command rejected: %s", control.command)
						}
					}
				}
				if !reflect.DeepEqual(got, control.want) {
					t.Errorf("%s: got %v want %v", control.command, got, control.want)
				}
			}
		})
	}
}
