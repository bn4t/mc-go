//go:build blackbox

package blackbox

import (
	"fmt"
	"testing"
	"time"
)

func TestScoreboardNumericResultsParity(t *testing.T) {
	const name = "ScoreNumeric"
	tg := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(name, "ScoreTwo")}, nil)
	defer tg.cleanup()
	controls := []struct {
		command         string
		result, success int
	}{
		{"scoreboard objectives add score dummy", 2, 1},
		{"scoreboard objectives add other dummy", 3, 1},
		{"scoreboard players set @a score 7", 14, 1},
		{"scoreboard players add @a score 3", 20, 1},
		{"scoreboard players remove @a score 2", 16, 1},
		{"scoreboard players get ScoreNumeric score", 8, 1},
		{"scoreboard players get Absent score", 0, 0},
		{"scoreboard objectives list", 3, 1},
		{"scoreboard players list", 4, 1},
		{"scoreboard players list ScoreNumeric", 1, 1},
		{"scoreboard objectives modify score displayname \"Named\"", 0, 1},
		{"scoreboard objectives modify score rendertype hearts", 0, 1},
		{"scoreboard objectives modify score displayautoupdate true", 0, 1},
		{"scoreboard objectives modify score numberformat blank", 0, 1},
		{"scoreboard objectives setdisplay sidebar score", 0, 1},
		{"scoreboard objectives setdisplay sidebar score", 0, 0},
		{"scoreboard objectives setdisplay sidebar", 0, 1},
		{"scoreboard players display name @a score \"Labels\"", 2, 1},
		{"scoreboard players display name @a score", 2, 1},
		{"scoreboard players display numberformat @a score blank", 2, 1},
		{"scoreboard players display numberformat @a score", 2, 1},
		{"scoreboard players operation @a score += src other", 20, 1},
		{"scoreboard players reset @a score", 2, 1},
		{"scoreboard players reset ScoreNumeric score", 1, 1},
		{"scoreboard players reset @a", 2, 1},
		{"scoreboard players reset Unknown", 1, 1},
		{"scoreboard objectives add trig trigger", 4, 1},
		{"scoreboard players enable @a trig", 2, 1},
		{"scoreboard players enable @a trig", 0, 0},
		{"scoreboard objectives remove other", 3, 1},
		{"scoreboard objectives remove trig", 2, 1},
		{"scoreboard players set @a score 2147483647", -2, 1},
		{"scoreboard players add @a score 1", 0, 1},
		{"scoreboard players remove @a score 1", -2, 1},
		{"scoreboard players set Target score nope", 999, 999},
	}
	for _, target := range []struct{ name, addr string }{{"vanilla", tg.vanilla}, {"mc-go", tg.mcgo}} {
		t.Run(target.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, target.addr, name)
			defer c.Close()
			drainToJoinBlackbox(t, target.name, pr)
			awaitPlayReadyBlackbox(t, target.name, c, pr, pw)
			sink := newChatSinkTP(pr, pw)
			other, orr, ow := joinPlayBlackbox(t, target.addr, "ScoreTwo")
			defer other.Close()
			drainToJoinBlackbox(t, "other", orr)
			awaitPlayReadyBlackbox(t, "other", other, orr, ow)
			_ = newChatSinkTP(orr, ow)
			serial := 0
			command := func(cmd string) []string {
				serial++
				sendCommand(t, pw, cmd)
				marker := fmt.Sprintf("SCORE_NUM_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			command("tick freeze")
			command("scoreboard objectives add out dummy")
			for n, control := range controls {
				if control.command == "scoreboard players operation @a score += src other" {
					command("scoreboard players set src other 2")
				}
				command("scoreboard players set result out 999")
				command("scoreboard players set success out 999")
				command("execute store success score success out store result score result out run " + control.command)
				for _, field := range []struct {
					holder string
					want   int
				}{{"result", control.result}, {"success", control.success}} {
					marker := fmt.Sprintf("SCORE_NUM_CASE_%d_%s", n, field.holder)
					if !structureSaidBB(command(fmt.Sprintf("execute if score %s out matches %d run say %s", field.holder, field.want, marker)), marker) {
						t.Errorf("%s %s want%d", control.command, field.holder, field.want)
					}
				}
			}
		})
	}
}
