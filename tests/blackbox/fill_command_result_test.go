//go:build blackbox

package blackbox

import (
	"fmt"
	"testing"
	"time"
)

func TestFillCommandResultParity(t *testing.T) {
	const name = "FillResults"
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(name)}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, address string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.address, name)
			defer c.Close()
			drainToJoinBlackbox(t, leg.name, pr)
			awaitPlayReadyBlackbox(t, leg.name, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			seq := 0
			cmd := func(text string) []string {
				seq++
				sendCommand(t, pw, text)
				marker := fmt.Sprintf("FILL_RESULT_DONE_%d", seq)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 15*time.Second)
			}
			score := func(holder string, want int) {
				t.Helper()
				marker := "FILL_EXPECT_" + holder
				if !structureSaidBB(cmd(fmt.Sprintf("execute if score %s fillpin matches %d run say %s", holder, want, marker)), marker) {
					t.Errorf("%s want%d", holder, want)
				}
			}
			cmd("tick freeze")
			cmd("tp @s 0 -58 0")
			cmd("scoreboard objectives add fillpin dummy")
			for i, tc := range []struct {
				command string
				result  int
			}{{"fill 0 -58 0 1 -58 1 stone", 4}, {"fill 0 -58 0 1 -58 1 stone", 0}, {"fill 2 -58 0 3 -58 1 stone", 4}, {"fill 1000000 -58 1000000 1000001 -58 1000001 stone", 0}} {
				holder := fmt.Sprintf("result%d", i)
				cmd("scoreboard players set " + holder + " fillpin 99")
				wantKey := "commands.fill.success"
				if i == 1 {
					wantKey = "commands.fill.failed"
				}
				if i == 3 {
					wantKey = "argument.pos.unloaded"
				}
				matched := false
				for _, line := range cmd("execute store result score " + holder + " fillpin run " + tc.command) {
					if key, _, ok := componentArgs(line); ok && key == wantKey {
						matched = true
					}
				}
				if !matched {
					t.Errorf("%s missing typed %s", holder, wantKey)
				}
				score(holder, tc.result)
			}
			for i, tc := range []struct {
				command string
				success int
			}{{"fill 4 -58 0 5 -58 1 stone", 1}, {"fill 4 -58 0 5 -58 1 stone", 0}, {"fill 1000000 -58 1000000 1000001 -58 1000001 stone", 0}} {
				holder := fmt.Sprintf("success%d", i)
				cmd("scoreboard players set " + holder + " fillpin 99")
				cmd("execute store success score " + holder + " fillpin run " + tc.command)
				score(holder, tc.success)
			}
			cmd("scoreboard players set syntax fillpin 99")
			cmd("execute store result score syntax fillpin run fill 0 -58 0 1 -58 1 no_such_block")
			score("syntax", 99)
			cmd("setblock 0 -58 0 dirt")
			cmd("execute store result score filtered fillpin run fill 0 -58 0 1 -58 1 gold_block replace dirt")
			score("filtered", 1)
		})
	}
}
