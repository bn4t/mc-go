//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestPlaceOceanRuinParity(t *testing.T) {
	const name = "RuinProbe"
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(name)}, func(s *server.Server) { s.WorldSeed = 1 })
	defer targets.cleanup()
	type chest struct {
		pos  [3]int
		loot string
	}
	cases := []struct {
		structure string
		chests    []chest
	}{
		{"ocean_ruin_cold", []chest{
			{[3]int{-27, -60, -10}, "minecraft:chests/underwater_ruin_small"},
			{[3]int{-31, -60, 6}, "minecraft:chests/underwater_ruin_small"},
			{[3]int{-13, -60, -23}, "minecraft:chests/underwater_ruin_small"},
			{[3]int{-9, -59, -10}, "minecraft:chests/underwater_ruin_big"},
			{[3]int{1, -60, -21}, "minecraft:chests/underwater_ruin_small"},
			{[3]int{6, -60, -10}, "minecraft:chests/underwater_ruin_small"},
		}},
		{"ocean_ruin_warm", []chest{
			{[3]int{-33, -59, 4}, "minecraft:chests/underwater_ruin_small"},
			{[3]int{-25, -60, -10}, "minecraft:chests/underwater_ruin_small"},
			{[3]int{-13, -60, -24}, "minecraft:chests/underwater_ruin_small"},
			{[3]int{-2, -59, -20}, "minecraft:chests/underwater_ruin_small"},
			{[3]int{-7, -60, -7}, "minecraft:chests/underwater_ruin_big"},
			{[3]int{5, -60, -10}, "minecraft:chests/underwater_ruin_small"},
		}},
	}
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
				marker := fmt.Sprintf("RUIN_DONE_%d", sequence)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 25*time.Second)
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
				marker := fmt.Sprintf("RUIN_MARK_%d", sequence)
				sequence++
				return structureSaidBB(command(text+" run say "+marker), marker)
			}
			score := func(holder string, want int) {
				t.Helper()
				if !said(fmt.Sprintf("execute if score %s placepin matches %d", holder, want)) {
					t.Fatalf("%s want%d", holder, want)
				}
			}
			command("tick freeze")
			feedback("tp @s 0 -58 0", "commands.teleport.success.location.single")
			command("scoreboard objectives add placepin dummy")
			feedback("place structure no_such_structure", "commands.place.structure.invalid")
			feedback("place structure ocean_ruin_cold 1000000 -58 1000000", "argument.pos.unloaded")
			feedback("place structure ocean_ruin_cold 0 -1000 0", "argument.pos.outofworld")
			feedback("place structure ocean_ruin_cold 0 -58 0 extra", "command.unknown.argument")
			for i, tc := range cases {
				if i > 0 {
					// Clear the above-ground ruins between variants; the
					// flat ground and its heightmap stay.
					command("fill -35 -60 -28 8 -50 9 air")
				}
				command("execute store result score placed placepin run place structure " + tc.structure + " 0 -58 0")
				score("placed", 1)
				for _, ch := range tc.chests {
					marker := fmt.Sprintf("RUIN_CHEST_%d_%d_%d", ch.pos[0], ch.pos[1], ch.pos[2])
					if !structureSaidBB(command(fmt.Sprintf(`execute if data block %d %d %d {LootTable:"%s"} run say %s`, ch.pos[0], ch.pos[1], ch.pos[2], ch.loot, marker)), marker) {
						t.Errorf("%s: %s absent at %v", tc.structure, ch.loot, ch.pos)
					}
				}
				command("execute store result score ruins placepin run fill -35 -60 -28 8 -50 9 air replace chest")
				if !said(fmt.Sprintf("execute if score ruins placepin matches %d", len(tc.chests))) {
					t.Errorf("%s: chest count != %d", tc.structure, len(tc.chests))
				}
			}
			command("execute store success score failed placepin run place structure ocean_ruin_cold 1000000 -58 1000000")
			score("failed", 0)
		})
	}
}
