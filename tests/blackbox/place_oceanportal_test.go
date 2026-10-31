//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"strings"
	"testing"
	"time"
)

func TestPlaceOceanPortalParity(t *testing.T) {
	for _, tc := range []struct {
		name, biome, edited string
		cold                bool
	}{
		{"cold_generator", "snowy_plains", "desert", true},
		{"warm_generator", "the_void", "snowy_plains", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const name = "OceanPortal"
			targets := requireBlackboxFiles(t, map[string]string{"level-type": "flat", "generator-settings": strings.ReplaceAll(pyramidFlatSettings, "minecraft:the_void", "minecraft:"+tc.biome)}, map[string]string{"ops.json": opsJSONFor(name)}, func(s *server.Server) {
				s.WorldSeed = 1
				s.FlatPreset = pyramidFlatPreset()
				s.FlatPreset.Biome = "minecraft:" + tc.biome
			})
			defer targets.cleanup()
			for _, leg := range []struct{ name, address string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
				t.Run(leg.name, func(t *testing.T) {
					c, pr, pw := joinPlayBlackbox(t, leg.address, name)
					defer c.Close()
					drainToJoinBlackbox(t, leg.name, pr)
					awaitPlayReadyBlackbox(t, leg.name, c, pr, pw)
					sink := newChatSinkResolvedTP(pr, pw)
					sequence := 0
					command := func(text string) []string {
						sequence++
						sendCommand(t, pw, text)
						mark := fmt.Sprintf("OCEAN_DONE_%d", sequence)
						sendCommand(t, pw, "say "+mark)
						return waitForExactSayBB(t, sink, mark, 30*time.Second)
					}
					feedback := func(text, want string) {
						t.Helper()
						for _, line := range command(text) {
							if key, _, ok := componentArgs(line); ok && key == want {
								return
							}
						}
						t.Fatalf("%s missing %s", text, want)
					}
					said := func(text string) bool {
						t.Helper()
						sequence++
						mark := fmt.Sprintf("OCEAN_MARK_%d", sequence)
						return structureSaidBB(command(text+" run say "+mark), mark)
					}
					score := func(holder, values string) {
						t.Helper()
						if !said("execute if score " + holder + " oceanpin matches " + values) {
							t.Fatalf("%s want%s", holder, values)
						}
					}
					command("tick freeze")
					feedback("tp @s 0 64 0", "commands.teleport.success.location.single")
					command("scoreboard objectives add oceanpin dummy")
					feedback("place structure no_such_structure", "commands.place.structure.invalid")
					feedback("place structure ruined_portal_ocean 1000000 64 1000000", "argument.pos.unloaded")
					feedback("place structure ruined_portal_ocean 0 -1000 0", "argument.pos.outofworld")
					feedback("place structure ruined_portal_ocean 0 64 0 extra", "command.unknown.argument")
					for stage := 0; stage < 2; stage++ {
						biome := tc.biome
						if stage == 1 {
							feedback("fillbiome -16 48 -16 15 79 15 minecraft:"+tc.edited, "commands.fillbiome.success.count")
							biome = tc.edited
						}
						if !said("execute if biome 0 60 0 minecraft:" + biome) {
							t.Fatalf("stage%d live biome absent: %s", stage, biome)
						}
						command("execute store result score placed oceanpin run place structure ruined_portal_ocean 0 64 0")
						score("placed", "1")
						if !said(`execute if data block 9 61 3 {LootTable:"minecraft:chests/ruined_portal"}`) {
							t.Fatal("portal loot chest absent")
						}
						for _, pos := range [][3]int{{6, 61, 3}, {3, 64, 3}, {5, 65, 3}} {
							if !said(fmt.Sprintf("execute if block %d %d %d obsidian", pos[0], pos[1], pos[2])) {
								t.Errorf("frame cell absent at%v", pos)
							}
						}
						for _, count := range []struct{ holder, block, want string }{{"obs", "obsidian", "11"}, {"crying", "crying_obsidian", "1"}, {"chests", "chest", "1"}, {"magma", "magma_block", ""}, {"lava", "lava", "0"}} {
							command("execute store result score " + count.holder + " oceanpin run fill -10 50 -11 20 70 19 air replace " + count.block)
							want := count.want
							if count.block == "magma_block" {
								want = "1.."
								if tc.cold {
									want = "0"
								}
							}
							score(count.holder, want)
						}
						command("fill -10 50 -11 20 59 19 stone")
						command("fill -10 60 -11 20 60 19 grass_block")
						command("fill -10 61 -11 20 70 19 air")
					}
					command("execute store success score failed oceanpin run place structure ruined_portal_ocean 1000000 64 1000000")
					score("failed", "0")
				})
			}
		})
	}
}
