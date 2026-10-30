//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestForceLoadSourceParity(t *testing.T) {
	const name = "ForceSource"
	files := map[string]string{"ops.json": opsJSONFor(name), "world/datapacks/forcesource/pack.mcmeta": `{"pack":{"description":"source force tickets","min_format":107,"max_format":107}}`,
		"world/datapacks/forcesource/data/forcesource/function/outer.mcfunction":     "forceload add ~ ~\nexecute positioned 200.75 -58 -200.25 run function forcesource:inner\nfunction forcesource:inner\nscoreboard players set done source_scope 1\n",
		"world/datapacks/forcesource/data/forcesource/function/inner.mcfunction":     "forceload add ~16 ~32\n",
		"world/datapacks/forcesource/data/forcesource/function/scheduled.mcfunction": "execute in minecraft:the_nether positioned 300.75 -58 -300.25 run function forcesource:outer\nscoreboard players set neutral_done source_scope 1\n",
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
	var scalar func(any) string
	scalar = func(value any) string {
		if m, ok := value.(map[string]any); ok {
			if v, ok := m[""]; ok {
				return scalar(v)
			}
			if v, ok := m["text"]; ok {
				return scalar(v)
			}
		}
		return fmt.Sprint(value)
	}
	for _, leg := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, name)
			defer c.Close()
			drainToJoinBlackbox(t, leg.name, pr)
			awaitPlayReadyBlackbox(t, leg.name, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			command := func(text string) []string {
				serial++
				sendCommand(t, pw, text)
				marker := fmt.Sprintf("FORCE_FRAME_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 20*time.Second)
			}
			feedback := func(text, key string, want ...string) {
				t.Helper()
				for _, line := range command(text) {
					actual, args, ok := componentArgs(line)
					if ok && actual == key {
						values := make([]string, len(args))
						for i, v := range args {
							values[i] = scalar(v)
						}
						if !reflect.DeepEqual(values, want) {
							t.Errorf("%s %s args%v want%v", text, key, values, want)
						}
						return
					}
				}
				t.Errorf("%s missing typed %s", text, key)
			}
			expect := func(condition, label string) bool {
				t.Helper()
				marker := "FORCE_FRAME_EXPECT_" + label
				return structureSaidBB(command("execute "+condition+" run say "+marker), marker)
			}
			command("tick freeze")
			command("tp @s 1.5 -58 1.5")
			command("scoreboard objectives add source_scope dummy")
			for _, dim := range []string{"minecraft:overworld", "minecraft:the_nether", "minecraft:the_end"} {
				command("execute in " + dim + " run forceload remove all")
			}
			cases := []struct {
				command, key string
				args         []string
			}{
				{"execute in minecraft:the_nether run forceload add 80 96", "added.single", []string{"[5, 6]", "minecraft:the_nether"}},
				{"execute in minecraft:the_nether run forceload query 80 96", "query.success", []string{"[5, 6]", "minecraft:the_nether"}},
				{"execute in minecraft:overworld run forceload query 80 96", "query.failure", []string{"[5, 6]", "minecraft:overworld"}},
				{"execute positioned 127.75 -58 -0.25 run forceload add ~ ~", "added.single", []string{"[7, -1]", "minecraft:overworld"}},
				{"forceload query 127 -1", "query.success", []string{"[7, -1]", "minecraft:overworld"}},
				{"forceload query 0 0", "query.failure", []string{"[0, 0]", "minecraft:overworld"}},
				{"execute in minecraft:the_nether positioned 127.75 -58 -0.25 run forceload add ~ ~ ~1 ~-16", "added.multiple", []string{"4", "minecraft:the_nether", "[7, -2]", "[8, -1]"}},
				{"execute in minecraft:the_nether run forceload query 112 -32", "query.success", []string{"[7, -2]", "minecraft:the_nether"}},
				{"execute in minecraft:the_nether run forceload query 128 -16", "query.success", []string{"[8, -1]", "minecraft:the_nether"}},
				{"forceload query 112 -32", "query.failure", []string{"[7, -2]", "minecraft:overworld"}},
				{"execute in minecraft:the_nether positioned 127.75 -58 -0.25 run forceload remove ~ ~", "removed.single", []string{"[7, -1]", "minecraft:the_nether"}},
				{"execute in minecraft:the_nether run forceload query 127 -1", "query.failure", []string{"[7, -1]", "minecraft:the_nether"}},
				{"forceload query 127 -1", "query.success", []string{"[7, -1]", "minecraft:overworld"}},
				{"execute in minecraft:the_nether run forceload remove all", "removed.all", []string{"minecraft:the_nether"}},
				{"execute in minecraft:the_nether run forceload query 80 96", "query.failure", []string{"[5, 6]", "minecraft:the_nether"}},
				{"forceload query 127 -1", "query.success", []string{"[7, -1]", "minecraft:overworld"}},
				{"execute positioned 128 -58 256 in minecraft:the_nether run forceload add ~ ~", "added.single", []string{"[1, 2]", "minecraft:the_nether"}},
				{"execute in minecraft:the_nether run forceload query 16 32", "query.success", []string{"[1, 2]", "minecraft:the_nether"}},
				{"execute in minecraft:the_nether run forceload query 128 256", "query.failure", []string{"[8, 16]", "minecraft:the_nether"}},
				{"execute in minecraft:the_nether positioned 128 -58 256 in minecraft:overworld run forceload add ~ ~", "added.single", []string{"[64, 128]", "minecraft:overworld"}},
				{"forceload query 1028 2052", "query.success", []string{"[64, 128]", "minecraft:overworld"}},
				{"forceload query 128 256", "query.failure", []string{"[8, 16]", "minecraft:overworld"}},
			}
			for _, tc := range cases {
				feedback(tc.command, "commands.forceload."+tc.key, tc.args...)
			}
			// Function leaves take the batch-plan path, independently of the
			// direct forceload leaf's legacy chain evaluated above.
			command("execute positioned 256 -58 -128 in minecraft:the_nether run function forcesource:inner")
			feedback("execute in minecraft:the_nether run forceload query 48 16", "commands.forceload.query.success", "[3, 1]", "minecraft:the_nether")
			feedback("execute in minecraft:the_nether run forceload query 272 -96", "commands.forceload.query.failure", "[17, -6]", "minecraft:the_nether")
			command("execute in minecraft:the_nether positioned 15.25 -58 -1.25 in minecraft:overworld run function forcesource:inner")
			feedback("forceload query 138 22", "commands.forceload.query.success", "[8, 1]", "minecraft:overworld")
			feedback("forceload query 31 30", "commands.forceload.query.failure", "[1, 1]", "minecraft:overworld")
			command("execute in minecraft:the_nether positioned 47.75 -58 63.75 run function forcesource:outer")
			if !expect("if score done source_scope matches 1", "nested_done") {
				t.Fatal("nested function missing")
			}
			for _, tc := range []struct{ query, chunk string }{{"47 63", "[2, 3]"}, {"216 -169", "[13, -11]"}, {"63 95", "[3, 5]"}} {
				feedback("execute in minecraft:the_nether run forceload query "+tc.query, "commands.forceload.query.success", tc.chunk, "minecraft:the_nether")
			}
			feedback("forceload query 47 63", "commands.forceload.query.failure", "[2, 3]", "minecraft:overworld")
			command("scoreboard players set neutral_done source_scope 0")
			command("schedule function forcesource:scheduled 1t")
			command("tick step 1")
			done := false
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				if expect("if score neutral_done source_scope matches 1", "neutral_done") {
					done = true
					break
				}
			}
			if !done {
				t.Fatal("neutral function missing")
			}
			feedback("execute in minecraft:the_nether run forceload query 300 -301", "commands.forceload.query.success", "[18, -19]", "minecraft:the_nether")
			feedback("execute in minecraft:the_nether run forceload query 316 -269", "commands.forceload.query.success", "[19, -17]", "minecraft:the_nether")
			feedback("forceload query 300 -301", "commands.forceload.query.failure", "[18, -19]", "minecraft:overworld")
			if !expect(`if entity @s[nbt={Dimension:"minecraft:overworld"}]`, "physical_dimension") {
				t.Error("source mutation moved player dimension")
			}
		})
	}
}
