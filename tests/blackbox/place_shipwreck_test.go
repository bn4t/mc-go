//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestPlaceShipwreckParity(t *testing.T) {
	const name = "ShipProbe"
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
				marker := fmt.Sprintf("SHIP_DONE_%d", sequence)
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
				marker := fmt.Sprintf("SHIP_MARK_%d", sequence)
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
				for _, line := range command("fill -1 -64 -1 9 -40 31 air replace " + material) {
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
			feedback("tp @s 0 -58 0", "commands.teleport.success.location.single")
			command("scoreboard objectives add placepin dummy")
			feedback("place structure no_such_structure", "commands.place.structure.invalid")
			feedback("place structure shipwreck 1000000 -58 1000000", "argument.pos.unloaded")
			feedback("place structure shipwreck 0 -1000 0", "argument.pos.outofworld")
			feedback("place structure shipwreck 0 -58 0 extra", "command.unknown.argument")
			command("execute store result score placed placepin run place structure shipwreck 0 -58 0")
			score("placed", 1)

			// Seed 1, chunk (0,0): rightsideup_fronthalf, rotation 2.
			marker := "SHIP_SUPPLY"
			if !structureSaidBB(command(`execute if data block 4 -58 22 {LootTable:"minecraft:chests/shipwreck_supply"} run say `+marker), marker) {
				t.Error("shipwreck supply chest absent")
			}
			for material, want := range map[string]string{"chest": "1", "spruce_planks": "128", "dark_oak_planks": "58"} {
				if got := count(material); got != want {
					t.Errorf("ocean %s=%s want%s", material, got, want)
				}
			}

			// Clear the above-ground wreck before the beached variant; the
			// ground layer (and its heightmap) stays.
			command("fill -1 -60 -1 9 -40 31 air")
			command("execute store result score placed placepin run place structure shipwreck_beached 0 -58 0")
			score("placed", 1)
			// The beached piece sinks by 0..2 blocks from the level random;
			// the template/rotation are large-feature-seeded. Its chest x/z
			// stay fixed while the y follows that offset.
			for _, c := range []struct {
				pos  [3]int
				loot string
			}{
				{[3]int{2, -61, 18}, "minecraft:chests/shipwreck_treasure"},
				{[3]int{3, -63, 24}, "minecraft:chests/shipwreck_map"},
			} {
				found := false
				for dy := -2; dy <= 2 && !found; dy++ {
					marker := fmt.Sprintf("SHIP_LOOT_%d_%d_%d_%d", c.pos[0], c.pos[1]+dy, c.pos[2], dy)
					if structureSaidBB(command(fmt.Sprintf(`execute if data block %d %d %d {LootTable:"%s"} run say %s`, c.pos[0], c.pos[1]+dy, c.pos[2], c.loot, marker)), marker) {
						found = true
					}
				}
				if !found {
					t.Errorf("%s absent around %v", c.loot, c.pos)
				}
			}
			if got := count("chest"); got != "2" {
				t.Errorf("beached chest=%s want2", got)
			}
			command("execute store success score failed placepin run place structure shipwreck 1000000 -58 1000000")
			score("failed", 0)
		})
	}
}
