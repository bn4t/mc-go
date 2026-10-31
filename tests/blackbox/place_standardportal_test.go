//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestPlaceStandardPortalSurfaceParity(t *testing.T) {
	defer func(d time.Duration) { clientDeadline = d }(clientDeadline)
	clientDeadline = 3 * time.Minute
	type cell struct {
		pos   [3]int
		block string
	}
	for _, tc := range []struct {
		seed                   int64
		chest                  [3]int
		obs, crying, low, high int
		control                bool
		cells                  []cell
	}{
		{0, [3]int{10, 66, 6}, 16, 1, 45, 85, true, []cell{{[3]int{5, 63, 3}, "obsidian"}, {[3]int{9, 66, 2}, "obsidian"}, {[3]int{9, 65, 5}, "crying_obsidian"}}},
		{1, [3]int{3, 65, 2}, 9, 1, 45, 85, true, []cell{{[3]int{4, 65, 2}, "obsidian"}, {[3]int{4, 68, 2}, "obsidian"}, {[3]int{4, 67, 5}, "crying_obsidian"}}},
	} {
		t.Run(fmt.Sprintf("seed%d", tc.seed), func(t *testing.T) {
			const name = "StandardPortal"
			targets := requireBlackboxFiles(t, map[string]string{"level-type": "minecraft:normal", "generator-settings": "{}", "level-seed": fmt.Sprint(tc.seed), "generate-structures": "false", "view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor(name)}, func(s *server.Server) {
				s.LevelType = server.LevelTypeNormal
				s.WorldSeed = tc.seed
				s.GenerateStructures = false
				s.ViewDistance = 2
				s.SimulationDistance = 2
			})
			defer targets.cleanup()
			for _, leg := range []struct{ name, addr string }{{"vanilla", targets.vanilla}, {"mc-go", targets.mcgo}} {
				t.Run(leg.name, func(t *testing.T) {
					c, pr, pw := joinPlayBlackbox(t, leg.addr, name)
					defer c.Close()
					drainToJoinBlackbox(t, leg.name, pr)
					awaitPlayReadyBlackbox(t, leg.name, c, pr, pw)
					sink := newChatSinkResolvedTP(pr, pw)
					seq := 0
					command := func(text string) []string {
						seq++
						sendCommand(t, pw, text)
						mark := fmt.Sprintf("STANDARD_PORTAL_DONE_%d", seq)
						sendCommand(t, pw, "say "+mark)
						return waitForExactSayBB(t, sink, mark, 60*time.Second)
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
						seq++
						mark := fmt.Sprintf("STANDARD_PORTAL_IF_%d", seq)
						return structureSaidBB(command(text+" run say "+mark), mark)
					}
					score := func(holder, want string) {
						t.Helper()
						if !said("execute if score " + holder + " standardPortal matches " + want) {
							t.Fatalf("%s want%s", holder, want)
						}
					}
					count := func(holder, block, want string) {
						t.Helper()
						command("scoreboard players set " + holder + " standardPortal 0")
						for _, band := range [][2]int{{45, 65}, {66, 85}} {
							lines := command(fmt.Sprintf("execute store result score countPart standardPortal run fill -10 %d -11 20 %d 19 air replace %s", band[0], band[1], block))
							receipt := false
							for _, line := range lines {
								if key, _, ok := componentArgs(line); ok && (key == "commands.fill.success" || key == "commands.fill.failed") {
									receipt = true
								}
							}
							if !receipt {
								t.Fatalf("count band%v lacks admitted fill receipt: %v", band, lines)
							}
							command("scoreboard players operation " + holder + " standardPortal += countPart standardPortal")
						}
						score(holder, want)
					}

					command("tick freeze")
					command("gamerule minecraft:spawn_mobs false")
					command("scoreboard objectives add standardPortal dummy")
					command("forceload add -16 -16 31 31")

					feedback("tp @s 0 100 0", "commands.teleport.success.location.single")
					loaded := "execute"
					minChunk := -1

					for cx := minChunk; cx <= 1; cx++ {
						for cz := -1; cz <= 1; cz++ {
							loaded += fmt.Sprintf(" if loaded %d 64 %d", cx*16, cz*16)
						}
					}
					deadline := time.Now().Add(2 * time.Minute)
					for !said(loaded) {
						if time.Now().After(deadline) {
							t.Fatal("source/backup chunks not ready")
						}
					}
					feedback("place structure no_such_structure", "commands.place.structure.invalid")
					feedback("place structure ruined_portal 1000000 100 1000000", "argument.pos.unloaded")
					feedback("place structure ruined_portal 0 -1000 0", "argument.pos.outofworld")
					feedback("place structure ruined_portal 0 100 0 extra", "command.unknown.argument")
					restore := func() {
						for _, band := range [][2]int{{45, 65}, {66, 85}} {
							feedback(fmt.Sprintf("clone -10 %d -11 20 %d 19 -10 %d -11 strict replace force", band[0]-85, band[1]-85, band[0]), "commands.clone.success")
							if !said(fmt.Sprintf("execute if blocks -10 %d -11 20 %d 19 -10 %d -11 all", band[0], band[1], band[0]-85)) {
								t.Fatal("standard terrain snapshot not restored")
							}
						}
					}
					for _, band := range [][2]int{{45, 65}, {66, 85}} {
						visible := fmt.Sprintf("execute if blocks -10 %d -11 20 %d 19 -10 %d -11 all", band[0], band[1], band[0])
						limit := time.Now().Add(2 * time.Minute)
						for !said(visible) {
							if time.Now().After(limit) {
								t.Fatal("snapshot source unavailable")
							}
						}
						feedback(fmt.Sprintf("clone -10 %d -11 20 %d 19 -10 %d -11 strict replace force", band[0], band[1], band[0]-85), "commands.clone.success")
					}
					for _, block := range []string{"magma_block", "obsidian", "crying_obsidian", "chest", "lava"} {
						count("baseline", block, "0")
					}
					restore()
					stages := 2
					for stage := 0; stage < stages; stage++ {
						if tc.control {
							if stage == 1 {
								feedback("fillbiome -16 48 -16 15 79 15 minecraft:snowy_plains", "commands.fillbiome.success.count")
							}
							snowy := said("execute if biome 0 62 0 minecraft:snowy_plains")
							if snowy != (stage == 1) {
								t.Fatalf("stage%d live snowy biome=%t", stage, snowy)
							}
						}
						command("execute store result score placed standardPortal run place structure ruined_portal 0 100 0")
						score("placed", "1")
						if !said(fmt.Sprintf(`execute if data block %d %d %d {LootTable:"minecraft:chests/ruined_portal"}`, tc.chest[0], tc.chest[1], tc.chest[2])) {
							t.Fatalf("loot chest absent at%v", tc.chest)
						}
						for _, cell := range tc.cells {
							if !said(fmt.Sprintf("execute if block %d %d %d %s", cell.pos[0], cell.pos[1], cell.pos[2], cell.block)) {
								t.Errorf("frame absent at%v want%s", cell.pos, cell.block)
							}
						}
						count("obs", "obsidian", fmt.Sprint(tc.obs))
						count("crying", "crying_obsidian", fmt.Sprint(tc.crying))
						count("chests", "chest", "1")
						if tc.control {
							count("magma", "magma_block", "1..")
							count("lava", "lava", "1..")
							restore()
						}
					}
					command("execute store success score failed standardPortal run place structure ruined_portal 1000000 100 1000000")
					score("failed", "0")
				})
			}
		})
	}
}
