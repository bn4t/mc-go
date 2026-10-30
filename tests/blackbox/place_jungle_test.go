//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestPlaceJungleTempleParity(t *testing.T) {
	const name = "JungleProbe"
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(name)}, func(s *server.Server) { s.WorldSeed = 1 })
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
				marker := fmt.Sprintf("JUNGLE_DONE_%d", sequence)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 20*time.Second)
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
				marker := fmt.Sprintf("JUNGLE_MARK_%d", sequence)
				sequence++
				return structureSaidBB(command(text+" run say "+marker), marker)
			}
			score := func(holder string, want int) {
				t.Helper()
				if !said(fmt.Sprintf("execute if score %s placepin matches %d", holder, want)) {
					t.Fatalf("%s want%d", holder, want)
				}
			}
			count := func(material string) string {
				t.Helper()
				for _, line := range command("fill -1 -64 -1 12 -50 15 air replace " + material) {
					key, args, ok := componentArgs(line)
					if !ok || key != "commands.fill.success" || len(args) != 1 {
						continue
					}
					value := args[0]
					if compound, ok := value.(map[string]any); ok {
						value = compound[""]
					}
					return fmt.Sprint(value)
				}
				t.Fatalf("%s fill count absent", material)
				return ""
			}
			command("tick freeze")
			feedback("tp @s 0 -58 0", "commands.teleport.success.location.single")
			command("scoreboard objectives add placepin dummy")
			feedback("place structure no_such_structure", "commands.place.structure.invalid")
			feedback("place structure jungle_pyramid 1000000 -58 1000000", "argument.pos.unloaded")
			feedback("place structure jungle_pyramid 0 -1000 0", "argument.pos.outofworld")
			feedback("place structure jungle_pyramid 0 -58 0 extra", "command.unknown.argument")
			command("execute store result score placed placepin run place structure jungle_pyramid 0 -58 0")
			score("placed", 1)

			// The flat comparison world's first free y is -60; the temple's
			// average-ground move is exact there, so positions are absolute.
			for _, check := range []struct {
				pos   [3]int
				block string
			}{
				{[3]int{8, -62, 11}, "chiseled_stone_bricks"},
				{[3]int{9, -62, 11}, "chiseled_stone_bricks"},
				{[3]int{10, -62, 11}, "chiseled_stone_bricks"},
				{[3]int{8, -62, 12}, "lever"},
				{[3]int{9, -62, 12}, "lever"},
				{[3]int{10, -62, 12}, "lever"},
				{[3]int{1, -63, 8}, "tripwire_hook"},
				{[3]int{4, -63, 8}, "tripwire_hook"},
				{[3]int{7, -63, 1}, "tripwire_hook"},
				{[3]int{7, -63, 5}, "tripwire_hook"},
				{[3]int{9, -62, 8}, "sticky_piston"},
				{[3]int{10, -62, 10}, "repeater"},
			} {
				marker := fmt.Sprintf("JUNGLE_LAYOUT_%d_%d_%d", check.pos[0], check.pos[1], check.pos[2])
				if !structureSaidBB(command(fmt.Sprintf("execute if block %d %d %d %s run say %s", check.pos[0], check.pos[1], check.pos[2], check.block, marker)), marker) {
					t.Errorf("layout want %s at %v", check.block, check.pos)
				}
			}
			for _, c := range []struct {
				pos   [3]int
				block string
				loot  string
			}{
				{[3]int{3, -62, 1}, "dispenser", "minecraft:chests/jungle_temple_dispenser"},
				{[3]int{9, -62, 3}, "dispenser", "minecraft:chests/jungle_temple_dispenser"},
				{[3]int{8, -63, 3}, "chest", "minecraft:chests/jungle_temple"},
				{[3]int{9, -63, 10}, "chest", "minecraft:chests/jungle_temple"},
			} {
				marker := fmt.Sprintf("JUNGLE_BLOCK_%d_%d_%d", c.pos[0], c.pos[1], c.pos[2])
				if !structureSaidBB(command(fmt.Sprintf("execute if block %d %d %d %s run say %s", c.pos[0], c.pos[1], c.pos[2], c.block, marker)), marker) {
					t.Errorf("%s absent at %v", c.block, c.pos)
				}
				marker += "_DATA"
				if !structureSaidBB(command(fmt.Sprintf(`execute if data block %d %d %d {LootTable:"%s"} run say %s`, c.pos[0], c.pos[1], c.pos[2], c.loot, marker)), marker) {
					t.Errorf("%s loot table absent at %v", c.block, c.pos)
				}
			}

			// Count attached levers before removing the wall blocks they sit on.
			for _, want := range []struct {
				material string
				count    string
			}{
				{"lever", "3"}, {"chiseled_stone_bricks", "3"},
				{"cobblestone_stairs", "24"}, {"tripwire", "5"},
				{"tripwire_hook", "4"}, {"redstone_wire", "15"},
				{"sticky_piston", "3"}, {"repeater", "1"},
				{"chest", "2"}, {"dispenser", "2"},
			} {
				if got := count(want.material); got != want.count {
					t.Errorf("%s=%s want%s", want.material, got, want.count)
				}
			}
			// The moss selector chooses per cell; the selected total is fixed.
			cobble, mossy := count("cobblestone"), count("mossy_cobblestone")
			t.Logf("cobblestone=%s mossy_cobblestone=%s", cobble, mossy)
			var a, b int
			fmt.Sscan(cobble, &a)
			fmt.Sscan(mossy, &b)
			if a+b != 1157 {
				t.Errorf("cobblestone+mossy=%d want1157", a+b)
			}
			command("execute store success score failed placepin run place structure jungle_pyramid 1000000 -58 1000000")
			score("failed", 0)
		})
	}
}
