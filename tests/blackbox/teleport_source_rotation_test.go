//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTeleportSourceRotationParity(t *testing.T) {
	const name = "TPRotationProbe"
	files := map[string]string{"ops.json": opsJSONFor(name), "world/datapacks/tprot/pack.mcmeta": `{"pack":{"description":"source angles","min_format":107,"max_format":107}}`,
		"world/datapacks/tprot/data/tprot/function/outer.mcfunction": "tp TPRotationProbe 0 -58 0 40 50\nfunction tprot:inner\n",
		"world/datapacks/tprot/data/tprot/function/inner.mcfunction": "tp TPRotationProbe ~ ~ ~ ~5 ~-10\nscoreboard players set function_done tprot 1\n",
	}
	directory := t.TempDir()
	for path, body := range files {
		if !strings.HasPrefix(path, "world/") {
			continue
		}
		local := filepath.Join(directory, strings.TrimPrefix(path, "world/"))
		if err := os.MkdirAll(filepath.Dir(local), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(local, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	targets := requireBlackboxFiles(t, nil, files, func(s *server.Server) { s.WorldDir = directory })
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, name)
			defer c.Close()
			drainToJoinBlackbox(t, leg.name, pr)
			awaitPlayReadyBlackbox(t, leg.name, c, pr, pw)
			// This command-state pin emits no movement packets that could normalize the
			// player's stored yaw after the command. No block action/chunk ticking needed.
			sink := newChatSink(pr, pw)
			serial := 0
			command := func(text string) []string {
				serial++
				sendCommand(t, pw, text)
				marker := fmt.Sprintf("TPROT_PIN_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 15*time.Second)
			}
			teleport := func(text string) {
				t.Helper()
				for _, line := range command(text) {
					key, _, ok := componentArgs(line)
					if ok && strings.HasPrefix(key, "commands.teleport.success") {
						return
					}
				}
				t.Fatalf("TP failed %s", text)
			}
			expectScore := func(holder string, want int, label string) {
				t.Helper()
				marker := "TPROT_PIN_EXPECT_" + label
				if !structureSaidBB(command(fmt.Sprintf("execute if score %s tprot matches %d run say %s", holder, want, marker)), marker) {
					t.Errorf("%s want%d: %v", label, want, command("scoreboard players get "+holder+" tprot"))
				}
			}
			angles := func(selector string, want [2]int, label string) {
				t.Helper()
				for axis, path := range []string{"Rotation[0]", "Rotation[1]"} {
					command("execute store result score v tprot run data get entity " + selector + " " + path + " 1000000")
					expectScore("v", want[axis]*1000000, fmt.Sprintf("%s_%d", label, axis))
				}
			}
			command("tick freeze")
			teleport("tp @s 10.5 -58.0 20.5 35 20")
			command("scoreboard objectives add tprot dummy")
			command(`summon minecraft:armor_stand 0 -58 0 {Tags:["tp_rotation"],Marker:1b,NoGravity:1b}`)
			cases := []struct {
				source, target, args string
				want                 [2]int
			}{
				{"35 20", "40 50", "~7 ~-10", [2]int{42, 10}}, {"90 30", "40 50", "~5 ~-10", [2]int{95, 20}}, {"180 0", "0 0", "~ ~", [2]int{-180, 0}}, {"0 0", "-180 0", "~ ~", [2]int{-360, 0}}, {"90 0", "-90 0", "~ ~", [2]int{-270, 0}}, {"-170 0", "170 0", "~ ~", [2]int{190, 0}}, {"170 0", "-170 0", "~ ~", [2]int{-190, 0}}, {"90 30", "-90 50", "~5 -15", [2]int{-265, -15}}, {"90 30", "40 50", "-45 ~5", [2]int{-45, 35}}, {"0 0", "0 0", "0 100", [2]int{0, 90}}, {"0 0", "0 0", "0 -100", [2]int{0, -90}}, {"0 80", "0 -80", "~ ~30", [2]int{0, -90}}, {"0 0", "0 0", "0 270", [2]int{0, -90}},
			}
			for kind, selector := range []string{"@e[tag=tp_rotation,limit=1]", "@s"} {
				for i, tc := range cases {
					teleport("tp " + selector + " 0 -58 0 " + tc.target)
					teleport("execute rotated " + tc.source + " run tp " + selector + " ~ ~ ~ " + tc.args)
					want := tc.want
					if kind == 1 && i == 3 {
						want[0] = 0
					}
					angles(selector, want, fmt.Sprintf("%d_%d", kind, i))
				}
			}
			// Direct multi-target TP must capture source rotation before any target moves.
			teleport("tp @s 0 -58 0 35 20")
			teleport("tp @e[tag=tp_rotation,limit=1] 0 -58 0 0 50")
			command("tag @s add tprot_group")
			command("tag @e[tag=tp_rotation,limit=1] add tprot_group")
			command("execute store result score count tprot run execute if entity @e[tag=tprot_group]")
			expectScore("count", 2, "group_domain")
			teleport("tp @e[tag=tprot_group] ~ ~ ~ ~7 ~-10")
			angles("@s", [2]int{42, 10}, "group_player")
			angles("@e[tag=tp_rotation,limit=1]", [2]int{42, 10}, "group_stand")
			command("execute rotated 90 30 run function tprot:outer")
			expectScore("function_done", 1, "nested_done")
			angles("@s", [2]int{95, 20}, "nested_snapshot")
			command("scoreboard players set function_done tprot 0")
			command("schedule function tprot:outer 1t")
			command("tick step 1")
			done := false
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				marker := "TPROT_NEUTRAL_DONE"
				if structureSaidBB(command("execute if score function_done tprot matches 1 run say "+marker), marker) {
					done = true
					break
				}
			}
			if !done {
				t.Fatal("neutral schedule incomplete")
			}
			angles("@s", [2]int{5, -10}, "neutral_snapshot")
		})
	}
}
