//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestPlaceFortressParity(t *testing.T) {
	const name = "FortProbe"
	targets := requireBlackboxFiles(t, map[string]string{"level-seed": "5"}, map[string]string{"ops.json": opsJSONFor(name)}, func(s *server.Server) { s.WorldSeed = 5 })
	defer targets.cleanup()
	chests := [][3]int{{-56, 53, 73}, {-37, 61, 58}, {-2, 61, 115}, {11, 53, 105}, {23, 54, 88}, {23, 54, 95}}
	spawners := [][3]int{{-4, 70, 30}, {43, 76, 58}}
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
				marker := fmt.Sprintf("FORT_DONE_%d", sequence)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 90*time.Second)
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
				marker := fmt.Sprintf("FORT_MARK_%d", sequence)
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
			feedback("tp @s 0 100 0", "commands.teleport.success.location.single")
			command("forceload add -80 -40 140 140")
			time.Sleep(2 * time.Second)
			command("scoreboard objectives add placepin dummy")
			feedback("place structure no_such_structure", "commands.place.structure.invalid")
			feedback("place structure fortress 1000000 100 1000000", "argument.pos.unloaded")
			feedback("place structure fortress 0 -1000 0", "argument.pos.outofworld")
			feedback("place structure fortress 0 100 0 extra", "command.unknown.argument")
			command("execute store result score placed placepin run place structure fortress 0 100 0")
			score("placed", 1)
			for _, p := range chests {
				marker := fmt.Sprintf("FORT_CHEST_%d_%d_%d", p[0], p[1], p[2])
				if !structureSaidBB(command(fmt.Sprintf(`execute if data block %d %d %d {LootTable:"minecraft:chests/nether_bridge"} run say %s`, p[0], p[1], p[2], marker)), marker) {
					t.Errorf("fortress chest absent at %v", p)
				}
			}
			for _, p := range spawners {
				marker := fmt.Sprintf("FORT_SPAWNER_%d_%d_%d", p[0], p[1], p[2])
				if !structureSaidBB(command(fmt.Sprintf(`execute if block %d %d %d spawner run say %s`, p[0], p[1], p[2], marker)), marker) {
					t.Errorf("fortress spawner absent at %v", p)
				}
				marker += "_DATA"
				if !structureSaidBB(command(fmt.Sprintf(`execute if data block %d %d %d {SpawnData:{entity:{id:"minecraft:blaze"}}} run say %s`, p[0], p[1], p[2], marker)), marker) {
					t.Errorf("fortress blaze data absent at %v", p)
				}
			}
			command("execute store success score failed placepin run place structure fortress 1000000 100 1000000")
			score("failed", 0)
		})
	}
}
