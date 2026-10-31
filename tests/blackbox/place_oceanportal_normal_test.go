//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"runtime"
	"testing"
	"time"
)

func TestPlaceOceanPortalNormalParity(t *testing.T) {
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
		{1, [3]int{9, 40, 3}, 8, 3, 30, 50, true, []cell{{[3]int{6, 40, 3}, "obsidian"}, {[3]int{3, 43, 3}, "obsidian"}, {[3]int{5, 44, 3}, "obsidian"}}},
		{0, [3]int{-4, 61, 0}, 10, 2, 50, 75, false, []cell{{[3]int{-7, 61, 3}, "obsidian"}, {[3]int{-4, 61, 3}, "crying_obsidian"}, {[3]int{-4, 65, 4}, "obsidian"}}},
	} {
		t.Run(fmt.Sprintf("seed%d", tc.seed), func(t *testing.T) {
			const name = "NormalPortal"
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
						mark := fmt.Sprintf("NORMAL_PORTAL_DONE_%d", seq)
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
						mark := fmt.Sprintf("NORMAL_PORTAL_IF_%d", seq)
						return structureSaidBB(command(text+" run say "+mark), mark)
					}
					score := func(holder, want string) {
						t.Helper()
						if !said("execute if score " + holder + " normalPortal matches " + want) {
							t.Fatalf("%s want%s", holder, want)
						}
					}
					count := func(holder, block, want string) {
						t.Helper()
						command(fmt.Sprintf("execute store result score %s normalPortal run fill -10 %d -11 20 %d 19 air replace %s", holder, tc.low, tc.high, block))
						score(holder, want)
					}
					command("tick freeze")
					command("gamerule minecraft:spawn_mobs false")
					command("scoreboard objectives add normalPortal dummy")
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
					feedback("place structure ruined_portal_ocean 1000000 100 1000000", "argument.pos.unloaded")
					feedback("place structure ruined_portal_ocean 0 -1000 0", "argument.pos.outofworld")
					feedback("place structure ruined_portal_ocean 0 100 0 extra", "command.unknown.argument")
					stages := 1
					restore := func() {
						feedback("clone -10 -40 -11 20 -10 19 -10 20 -11 strict replace force", "commands.clone.success")
						if !said("execute if blocks -10 20 -11 20 50 19 -10 -40 -11 all") {
							t.Fatalf("normal terrain snapshot not restored; source reflexive=%t backup reflexive=%t", said("execute if blocks -10 20 -11 20 50 19 -10 20 -11 all"), said("execute if blocks -10 -40 -11 20 -10 19 -10 -40 -11 all"))
						}
					}
					if tc.control {
						visible := "execute if blocks -10 20 -11 20 50 19 -10 20 -11 all"
						limit := time.Now().Add(2 * time.Minute)
						for !said(visible) {
							if time.Now().After(limit) {
								t.Fatal("source columns not visible for block-comparison receipt")
							}
						}
						feedback("clone -10 20 -11 20 50 19 -10 -40 -11 strict replace force", "commands.clone.success")
						for _, block := range []string{"magma_block", "obsidian", "crying_obsidian", "chest", "lava"} {
							count("baseline", block, "0")
						}
						restore()
						stages = 2
					}
					if tc.seed == 0 {
						// Match the loaded column used by the native fixture's
						// oak canopy: this sample excludes the low frame from
						// debris spread. Terrain decoration itself is separate.
						feedback("setblock -7 70 3 minecraft:oak_leaves[persistent=true]", "commands.setblock.success")
						if !said("execute if block -7 70 3 minecraft:oak_leaves[persistent=true]") {
							t.Fatal("seed0 canopy sentinel absent")
						}
					}
					for stage := 0; stage < stages; stage++ {
						if tc.control {
							if stage == 1 {
								feedback("fillbiome -16 24 -16 15 55 15 minecraft:snowy_plains", "commands.fillbiome.success.count")
							}
							snowy := said("execute if biome 0 39 0 minecraft:snowy_plains")
							if snowy != (stage == 1) {
								t.Fatalf("stage%d live snowy biome=%t", stage, snowy)
							}
						}
						command("execute store result score placed normalPortal run place structure ruined_portal_ocean 0 100 0")
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
							count("lava", "lava", "0")
							restore()
						}
					}
					if tc.control {
						if leg.name == "mc-go" {
							runtime.GC()
						}
						// Change the live floor independently: the first-use
						// FULL-chunk _WG samples must survive this edit.
						for _, band := range [][2]int{{20, 50}, {51, 80}, {81, 85}} {
							feedback(fmt.Sprintf("fill -10 %d -11 20 %d 19 stone", band[0], band[1]), "commands.fill.success")
						}
						if !said("execute if block 0 85 3 stone if block 0 86 3 air") {
							t.Fatal("raised terrain sentinel absent")
						}
						outside := func(holder, block string) {
							command("execute store result score " + holder + " normalPortal run fill 0 30 -11 0 50 19 air replace " + block)
						}
						outside("outsideN", "netherrack")
						score("outsideN", "0")
						outside("outsideM", "magma_block")
						score("outsideM", "0")
						command("execute store result score placed normalPortal run place structure ruined_portal_ocean 0 100 0")
						score("placed", "1")
						if !said(`execute if data block 9 40 3 {LootTable:"minecraft:chests/ruined_portal"}`) {
							t.Fatal("buried repeat chest absent")
						}
						outside("outsideN", "netherrack")
						outside("outsideM", "magma_block")
						command("scoreboard players operation outsideN normalPortal += outsideM normalPortal")
						score("outsideN", "1..")
					}
					command("execute store success score failed normalPortal run place structure ruined_portal_ocean 1000000 100 1000000")
					score("failed", "0")
				})
			}
		})
	}
}
