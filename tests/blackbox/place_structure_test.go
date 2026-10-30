//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"testing"
	"time"
)

func TestPlaceStructureParity(t *testing.T) {
	const name = "PlaceProbe"
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
				marker := fmt.Sprintf("PLACE_DONE_%d", sequence)
				sendCommand(t, pw, "say "+marker)
				return waitForExactSayBB(t, sink, marker, 15*time.Second)
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
			score := func(holder string, want int) {
				t.Helper()
				marker := "PLACE_SCORE_" + holder
				if !structureSaidBB(command(fmt.Sprintf("execute if score %s placepin matches %d run say %s", holder, want, marker)), marker) {
					t.Fatalf("%s want%d", holder, want)
				}
			}
			command("tick freeze")
			feedback("tp @s 0 -58 0", "commands.teleport.success.location.single")
			command("scoreboard objectives add placepin dummy")
			feedback("place structure no_such_structure", "commands.place.structure.invalid")
			feedback("place structure swamp_hut 1000000 -58 1000000", "argument.pos.unloaded")
			feedback("place structure swamp_hut 0 -1000 0", "argument.pos.outofworld")
			feedback("place structure swamp_hut 0 -58 0 extra", "command.unknown.argument")
			command("execute store result score placed placepin run place structure swamp_hut 0 -58 0")
			score("placed", 1)
			for _, kind := range []string{"witch", "cat"} {
				selector := "@e[type=" + kind + "]"
				command("execute store result score count placepin run execute if entity " + selector)
				score("count", 1)
				for axis, want := range []int{2500, -58000, 5500} {
					command(fmt.Sprintf("execute store result score position placepin run data get entity @e[type=%s,limit=1] Pos[%d] 1000", kind, axis))
					score("position", want)
				}
				marker := "PLACE_PERSIST_" + kind
				if !structureSaidBB(command("execute if entity @e[type="+kind+",nbt={PersistenceRequired:1b}] run say "+marker), marker) {
					t.Fatal("resident persistence", kind)
				}
			}
			// Registry variant draws need not agree between independent random streams.
			command("execute store result score valid_cat placepin run execute if entity @e[type=cat,nbt={Health:10.0f,Age:0}]")
			score("valid_cat", 1)

			for index, block := range []string{"1 -59 1 spruce_planks", "3 -58 6 crafting_table", "4 -58 6 cauldron", "1 -57 5 potted_red_mushroom"} {
				marker := fmt.Sprintf("PLACE_LAYOUT_%d", index)
				if !structureSaidBB(command("execute if block "+block+" run say "+marker), marker) {
					t.Fatal("layout", block)
				}
			}
			for _, material := range []struct {
				name  string
				count int
			}{{"spruce_planks", 84}, {"oak_log", 16}, {"spruce_stairs", 26}, {"oak_fence", 4}, {"crafting_table", 1}, {"cauldron", 1}, {"potted_red_mushroom", 1}} {
				matched := false
				for _, line := range command("fill -8 -64 -8 23 -40 23 air replace " + material.name) {
					key, args, ok := componentArgs(line)
					if !ok || key != "commands.fill.success" || len(args) != 1 {
						continue
					}
					value := args[0]
					if compound, ok := value.(map[string]any); ok {
						value = compound[""]
					}
					if fmt.Sprint(value) == fmt.Sprint(material.count) {
						matched = true
					}
				}
				if !matched {
					t.Fatalf("%s material count want %d", material.name, material.count)
				}
			}
			// A non-default source position selects the source chunk, not actor feet.
			feedback("execute positioned 32 -58 0 run place structure swamp_hut", "commands.place.structure.success")
			command("execute store result score count placepin run execute if entity @e[type=witch]")
			score("count", 2)
			for axis, want := range []int{35500, -58000, 2500} {
				command(fmt.Sprintf("execute store result score position placepin run data get entity @e[type=witch,sort=furthest,limit=1] Pos[%d] 1000", axis))
				score("position", want)
			}
			// Outside a registered natural hut, moon phase controls all_black eligibility.
			command("kill @e[type=cat]")
			command("time set 24000")
			for i := 0; i < 12; i++ {
				feedback("place structure swamp_hut 32 -58 0", "commands.place.structure.success")
			}
			command("execute store result score cats placepin run execute if entity @e[type=cat]")
			score("cats", 12)
			command(`execute store result score black_cat placepin run execute if entity @e[type=cat,nbt={variant:"minecraft:all_black"}]`)
			score("black_cat", 0)
			command("execute store success score failed placepin run place structure swamp_hut 1000000 -58 1000000")
			score("failed", 0)
		})
	}
}
