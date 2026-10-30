//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestPlaceIglooParity(t *testing.T) {
	const name = "IglooProbe"
	targets := requireBlackboxFiles(t, map[string]string{
		"level-type":         "flat",
		"generator-settings": pyramidFlatSettings,
	}, map[string]string{"ops.json": opsJSONFor(name)}, func(s *server.Server) {
		s.WorldSeed = 1
		s.FlatPreset = pyramidFlatPreset()
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
				marker := fmt.Sprintf("IGLOO_DONE_%d", sequence)
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
				marker := fmt.Sprintf("IGLOO_MARK_%d", sequence)
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
				for _, line := range command("fill -1 30 -1 8 70 14 air replace " + material) {
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
				return "0"
			}
			command("tick freeze")
			feedback("tp @s 0 70 0", "commands.teleport.success.location.single")
			command("scoreboard objectives add placepin dummy")
			feedback("place structure no_such_structure", "commands.place.structure.invalid")
			feedback("place structure igloo 1000000 70 1000000", "argument.pos.unloaded")
			feedback("place structure igloo 0 -1000 0", "argument.pos.outofworld")
			feedback("place structure igloo 0 70 0 extra", "command.unknown.argument")
			command("execute store result score placed placepin run place structure igloo 0 70 0")
			score("placed", 1)

			// Seed 1 has the laboratory; the top and lab share the surface at
			// first-free y 61, so the template positions are absolute.
			for _, check := range []struct {
				pos   [3]int
				block string
			}{
				{[3]int{5, 37, 6}, "chest"},
				{[3]int{1, 38, 6}, "brewing_stand"},
				{[3]int{3, 38, 9}, "oak_wall_sign"},
				{[3]int{5, 61, 7}, "furnace"},
			} {
				marker := fmt.Sprintf("IGLOO_LAYOUT_%d_%d_%d", check.pos[0], check.pos[1], check.pos[2])
				if !structureSaidBB(command(fmt.Sprintf("execute if block %d %d %d %s run say %s", check.pos[0], check.pos[1], check.pos[2], check.block, marker)), marker) {
					t.Errorf("layout want %s at %v", check.block, check.pos)
				}
			}
			marker := "IGLOO_CHEST_DATA"
			if !structureSaidBB(command(`execute if data block 5 37 6 {LootTable:"minecraft:chests/igloo_chest"} run say `+marker), marker) {
				t.Error("igloo chest loot table absent")
			}
			for _, material := range []string{"snow_block", "ice", "chest", "ladder", "cobweb", "brewing_stand", "cauldron"} {
				t.Logf("%s=%s", material, count(material))
			}
			command("execute store success score failed placepin run place structure igloo 1000000 70 1000000")
			score("failed", 0)
		})
	}
}
