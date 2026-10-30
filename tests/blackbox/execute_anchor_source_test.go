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

func TestExecuteAnchorSourceParity(t *testing.T) {
	files := map[string]string{"ops.json": opsJSONFor("SpatialProbe"), "world/datapacks/spatial/pack.mcmeta": `{"pack":{"description":"spatial","min_format":107,"max_format":107}}`, "world/datapacks/spatial/data/spatial/function/outer.mcfunction": "function spatial:inner\n", "world/datapacks/spatial/data/spatial/function/inner.mcfunction": "tp @e[tag=spatialtarget,limit=1] ^1 ^2 ^3\n"}
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
			addr := leg.addr
			c, pr, pw := joinPlayBlackbox(t, addr, "SpatialProbe")
			defer c.Close()
			drainToJoinBlackbox(t, "native", pr)
			awaitPlayReadyBlackbox(t, "native", c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			command := func(text string) []string {
				serial++
				sendCommand(t, pw, text)
				marker := fmt.Sprintf("SPATIAL_END_%d", serial)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 10*time.Second)
			}
			command("tick freeze")
			command("tp @s 10 -58 20 0 0")
			command("scoreboard objectives add spatial dummy")
			command(`summon minecraft:armor_stand 0 -58 0 {Tags:["spatialtarget"],NoGravity:1b,Marker:1b}`)
			cases := []string{
				"execute positioned 10 -58 20 anchored eyes run function spatial:outer",
				"execute anchored eyes positioned 10 -58 20 run function spatial:outer",
				"execute positioned 10 -58 20 anchored eyes positioned ~ ~ ~ run function spatial:outer",
				"execute positioned 10 -58 20 anchored eyes at @s run function spatial:outer",
				"execute positioned 10 -58 20 anchored eyes as @e[tag=spatialtarget,limit=1] run function spatial:outer",
				"execute positioned 10 -58 20 anchored eyes anchored feet run function spatial:outer",
				"execute positioned 10 -58 20 anchored eyes run tp @e[tag=spatialtarget,limit=1] ~1 ~2 ~3",
				"execute positioned 10 -58 20 anchored eyes rotated 90 0 run function spatial:outer",
			}
			wants := [][3]int{{11500000, -54380000, 23500000}, {11500000, -56000000, 23500000}, {11500000, -56000000, 23500000}, {11500000, -54380000, 23500000}, {11500000, -56000000, 23500000}, {11500000, -56000000, 23500000}, {11500000, -56000000, 23500000}, {7500000, -54380000, 21500000}}

			for i, line := range cases {
				command(line)
				for axis := 0; axis < 3; axis++ {
					command(fmt.Sprintf("execute store result score v spatial run data get entity @e[tag=spatialtarget,limit=1] Pos[%d] 1000000", axis))
					want := wants[i][axis]
					marker := fmt.Sprintf("SPATIAL_WANT_%d_%d", i, axis)
					if !structureSaidBB(command(fmt.Sprintf("execute if score v spatial matches %d..%d run say %s", want-3, want+3, marker)), marker) {
						t.Errorf("case%d axis%d expected%d: %v", i, axis, want, command("scoreboard players get v spatial"))
					}
				}
			}
		})
	}
}
