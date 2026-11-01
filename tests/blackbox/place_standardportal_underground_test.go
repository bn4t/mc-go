//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestPlaceStandardPortalUndergroundParity(t *testing.T) {
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
		{4096, [3]int{4, 18, -3}, 11, 2, 0, 40, true, []cell{{[3]int{5, 18, -9}, "obsidian"}, {[3]int{5, 18, -8}, "crying_obsidian"}, {[3]int{4, 18, -10}, "lava"}, {[3]int{4, 18, -12}, "air"}}},
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
						replacement := "air"
						if block == "air" {
							replacement = "stone"
						}
						for _, band := range [][2]int{{0, 20}, {21, 40}} {
							lines := command(fmt.Sprintf("execute store result score countPart standardPortal run fill -10 %d -20 20 %d 10 %s replace %s", band[0], band[1], replacement, block))
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
						if want != "" {
							score(holder, want)
						}
					}

					command("tick freeze")
					command("gamerule minecraft:spawn_mobs false")
					command("scoreboard objectives add standardPortal dummy")
					command("forceload add -16 -32 31 15")

					feedback("tp @s 0 100 0", "commands.teleport.success.location.single")
					loaded := "execute"
					minChunk := -1

					for cx := minChunk; cx <= 1; cx++ {
						for cz := -2; cz <= 0; cz++ {
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
						for _, band := range [][2]int{{0, 20}, {21, 40}} {
							feedback(fmt.Sprintf("clone -10 %d -20 20 %d 10 -10 %d -20 strict replace force", band[0]-60, band[1]-60, band[0]), "commands.clone.success")
							if !said(fmt.Sprintf("execute if blocks -10 %d -20 20 %d 10 -10 %d -20 all", band[0], band[1], band[0]-60)) {
								t.Fatal("standard terrain snapshot not restored")
							}
						}
					}
					for _, band := range [][2]int{{0, 20}, {21, 40}} {
						visible := fmt.Sprintf("execute if blocks -10 %d -20 20 %d 10 -10 %d -20 all", band[0], band[1], band[0])
						limit := time.Now().Add(2 * time.Minute)
						for !said(visible) {
							if time.Now().After(limit) {
								t.Fatal("snapshot source unavailable")
							}
						}
						feedback(fmt.Sprintf("clone -10 %d -20 20 %d 10 -10 %d -20 strict replace force", band[0], band[1], band[0]-60), "commands.clone.success")
					}
					for _, block := range []string{"obsidian", "crying_obsidian", "chest", "lava"} {
						count("baseline", block, "0")
					}
					count("baseline", "magma_block", "3")
					restore()
					for _, pos := range []string{"4 18 -12", "5 18 -12", "6 18 -12"} {
						if !said("execute if block " + pos + " stone") {
							t.Fatalf("expected original stone at %s", pos)
						}
					}
					count("airBefore", "air", "")
					restore()
					stages := 2
					for stage := 0; stage < stages; stage++ {
						if tc.control {
							if stage == 1 {
								feedback("fillbiome -16 0 -16 15 31 15 minecraft:snowy_plains", "commands.fillbiome.success.count")
							}
							snowy := said("execute if biome 0 17 0 minecraft:snowy_plains")
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
						count("airAfter", "air", "")
						command("scoreboard players operation airAfter standardPortal -= airBefore standardPortal")
						score("airAfter", "701")
						count("obs", "obsidian", fmt.Sprint(tc.obs))
						count("crying", "crying_obsidian", fmt.Sprint(tc.crying))
						count("chests", "chest", "1")
						if tc.control {
							count("magma", "magma_block", "4..")
							count("lava", "lava", "17")
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
