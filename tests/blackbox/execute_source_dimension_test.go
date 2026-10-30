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

func TestExecuteSourceDimensionParity(t *testing.T) {
	const player = "SourceDimension"
	files := map[string]string{"ops.json": opsJSONFor(player), "world/datapacks/source/pack.mcmeta": `{"pack":{"description":"source dimension","min_format":107,"max_format":107}}`, "world/datapacks/source/data/source/function/return.mcfunction": "execute in minecraft:overworld run tp SourceDimension 8.5 -58.0 8.5\nscoreboard players set returned source_dim 1\n"}
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
			c, pr, pw := joinPlayBlackbox(t, leg.addr, player)
			defer c.Close()
			drainToJoinBlackbox(t, leg.name, pr)
			awaitPlayReadyBlackbox(t, leg.name, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			command := func(text string) []string {
				serial++
				sendCommand(t, pw, text)
				marker := fmt.Sprintf("SOURCE_DIM_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 20*time.Second)
			}
			expect := func(condition, label string) {
				t.Helper()
				marker := "SOURCE_DIM_EXPECT_" + label
				if !structureSaidBB(command("execute "+condition+" run say "+marker), marker) {
					t.Errorf("missing %s", label)
				}
			}
			command("tick freeze")
			command("tp @s 8.5 -58.0 8.5 35 20")
			command("scoreboard objectives add source_dim dummy")
			command("execute in minecraft:the_nether run tp @s 8.5 70.5 8.5")
			expect(`if entity @s[nbt={Dimension:"minecraft:the_nether",Rotation:[35f,20f]}]`, "player_hop_keeps_rotation")
			command("function source:return")
			expect(`if entity @s[nbt={Dimension:"minecraft:overworld"}]`, "function_return")
			command("execute in minecraft:the_nether run tp @s 8.5 70.5 8.5")
			command("scoreboard players set returned source_dim 0")
			command("schedule function source:return 1t")
			command("tick step 1")
			done := false
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				marker := "SOURCE_DIM_SCHEDULE_DONE"
				if structureSaidBB(command("execute if score returned source_dim matches 1 run say "+marker), marker) {
					done = true
					break
				}
			}
			if !done {
				t.Fatal("scheduled neutral function did not complete")
			}
			expect(`if entity @s[nbt={Dimension:"minecraft:overworld"}]`, "neutral_function_return")
		})
	}
}
