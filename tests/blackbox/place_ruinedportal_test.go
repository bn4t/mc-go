//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestPlaceRuinedPortalParity(t *testing.T) {
	const name = "PortalProbe"
	targets := requireBlackboxFiles(t, nil, map[string]string{"ops.json": opsJSONFor(name)}, func(s *server.Server) { s.WorldSeed = 1 })
	defer targets.cleanup()
	cases := []struct {
		structure string
		chest     [3]int
		obsidian  string
	}{
		{"ruined_portal_jungle", [3]int{9, -57, 1}, "15"},
		{"ruined_portal_swamp", [3]int{9, -60, 3}, "11"},
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
				marker := fmt.Sprintf("PORTAL_DONE_%d", sequence)
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
				marker := fmt.Sprintf("PORTAL_MARK_%d", sequence)
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
			feedback("place structure ruined_portal_jungle 1000000 -58 1000000", "argument.pos.unloaded")
			feedback("place structure ruined_portal_jungle 0 -1000 0", "argument.pos.outofworld")
			feedback("place structure ruined_portal_jungle 0 -58 0 extra", "command.unknown.argument")
			for i, tc := range cases {
				if i > 0 {
					command("fill -1 -60 -5 12 -40 12 air")
				}
				command("execute store result score placed placepin run place structure " + tc.structure + " 0 -58 0")
				score("placed", 1)
				marker := fmt.Sprintf("PORTAL_CHEST_%d_%d_%d", tc.chest[0], tc.chest[1], tc.chest[2])
				if !structureSaidBB(command(fmt.Sprintf(`execute if data block %d %d %d {LootTable:"minecraft:chests/ruined_portal"} run say %s`, tc.chest[0], tc.chest[1], tc.chest[2], marker)), marker) {
					t.Errorf("%s chest absent at %v", tc.structure, tc.chest)
				}
				command("execute store result score chests placepin run fill -1 -60 -5 12 -40 12 air replace chest")
				if !said("execute if score chests placepin matches 1") {
					t.Errorf("%s chest count != 1", tc.structure)
				}
				command("execute store result score obs placepin run fill -1 -60 -5 12 -40 12 air replace obsidian")
				if !said("execute if score obs placepin matches " + tc.obsidian) {
					t.Errorf("%s obsidian != %s", tc.structure, tc.obsidian)
				}
			}
			command("execute store success score failed placepin run place structure ruined_portal_jungle 1000000 -58 1000000")
			score("failed", 0)
		})
	}
}
