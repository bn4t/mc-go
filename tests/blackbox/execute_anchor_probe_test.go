//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestExecuteAnchorParity(t *testing.T) {
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor("AnchorProbe")}, nil)
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			addr := leg.addr
			c, pr, pw := joinPlayBlackbox(t, addr, "AnchorProbe")
			defer c.Close()
			drainToJoinBlackbox(t, "native", pr)
			awaitPlayReadyBlackbox(t, "native", c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			command := func(text string) []string {
				serial++
				sendCommand(t, pw, text)
				marker := fmt.Sprintf("ANCHOR_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			command("tick freeze")
			command("tp @s 10 -58 20 0 0")
			command("scoreboard objectives add anchor dummy")
			command(`summon minecraft:armor_stand 0 -58 0 {Tags:["anchor_dest"],Marker:1b,NoGravity:1b}`)
			cases := []struct{ typ, nbt string }{{"armor_stand", ""}, {"armor_stand", "Small:1b,"}, {"armor_stand", "Marker:1b,"}, {"cow", ""}, {"cow", "Age:-24000,"}, {"zombie", ""}, {"zombie", "IsBaby:1b,"}, {"item", `Item:{id:"minecraft:stone",count:1},`}, {"bat", ""}, {"slime", "Size:3,"}, {"bee", "Age:-24000,"}, {"donkey", "Age:-24000,"}, {"glow_squid", "Age:-24000,"}, {"trader_llama", "Age:-24000,"}, {"camel", "Age:-24000,"}}
			wants := []int{-56222500, -57012500, -58000000, -56700001, -57310001, -56260000, -57225001, -57787500, -57550001, -56700001, -57850000, -57287501, -57630000, -57111751, -56620001}
			for i, tc := range cases {
				command("kill @e[tag=anchor_source]")
				command(fmt.Sprintf(`summon minecraft:%s 0 -58 0 {%sTags:["anchor_source"],NoGravity:1b}`, tc.typ, tc.nbt))
				marker := fmt.Sprintf("ANCHOR_SOURCE_%d", i)
				if !structureSaidBB(command("execute if entity @e[tag=anchor_source,limit=1] run say "+marker), marker) {
					t.Fatalf("source%d missing", i)
				}
				tpOK := false
				for _, line := range command("execute as @e[tag=anchor_source,limit=1] positioned 10 -58 20 anchored eyes run tp @e[tag=anchor_dest,limit=1] ^ ^ ^") {
					key, _, ok := componentArgs(line)
					if ok && strings.HasPrefix(key, "commands.teleport.success") {
						tpOK = true
					}
				}
				if !tpOK {
					t.Fatalf("case%d no TP success", i)
				}
				command("execute store result score value anchor run data get entity @e[tag=anchor_dest,limit=1] Pos[1] 1000000")
				want := wants[i]
				marker = fmt.Sprintf("ANCHOR_EXPECT_%d", i)
				if !structureSaidBB(command(fmt.Sprintf("execute if score value anchor matches %d..%d run say %s", want-1, want+1, marker)), marker) {
					t.Errorf("case%d %s eye offset mismatch; want%d", i, tc.typ, want)
				}

			}
		})
	}
}
