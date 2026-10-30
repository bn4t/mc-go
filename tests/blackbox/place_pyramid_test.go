//go:build blackbox

package blackbox

import (
	"fmt"
	"mc-go/internal/server"
	"mc-go/internal/world"
	"testing"
	"time"
)

// A desert pyramid needs a level deep enough for its cellar (local y -14).
// The comparison flat preset puts the ground at -61, so this fixture raises
// the layers to grass at y 60 while keeping the flat generator's -63 sea
// level. Both servers then receive the same chunk generator settings.
const pyramidFlatSettings = `{"layers":[{"block":"minecraft:bedrock","height":1},{"block":"minecraft:stone","height":120},{"block":"minecraft:dirt","height":3},{"block":"minecraft:grass_block","height":1}],"biome":"minecraft:the_void"}`

func pyramidFlatPreset() *world.FlatPreset {
	return &world.FlatPreset{Biome: "minecraft:the_void", Layers: []world.FlatLayer{
		{Block: "minecraft:bedrock", Height: 1},
		{Block: "minecraft:stone", Height: 120},
		{Block: "minecraft:dirt", Height: 3},
		{Block: "minecraft:grass_block", Height: 1},
	}}
}

func TestPlaceDesertPyramidParity(t *testing.T) {
	const name = "PyramidProbe"
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
				marker := fmt.Sprintf("PYRAMID_DONE_%d", sequence)
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
				marker := "PYRAMID_MARK_" + fmt.Sprint(sequence)
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
			feedback("tp @s 0 70 0", "commands.teleport.success.location.single")
			command("scoreboard objectives add placepin dummy")
			feedback("place structure no_such_structure", "commands.place.structure.invalid")
			feedback("place structure desert_pyramid 1000000 70 1000000", "argument.pos.unloaded")
			feedback("place structure desert_pyramid 0 -1000 0", "argument.pos.outofworld")
			feedback("place structure desert_pyramid 0 70 0 extra", "command.unknown.argument")
			command("execute store result score placed placepin run place structure desert_pyramid 0 70 0")
			score("placed", 1)

			// The piece lowers itself by 0..2 blocks from the level random;
			// the offset is random, the blueprint relative to it is not.
			base := -1
			for _, y := range []int{61, 60, 59} {
				marker := fmt.Sprintf("PYRAMID_BASE_%d", y)
				if structureSaidBB(command(fmt.Sprintf("execute if block 10 %d 10 blue_terracotta run say %s", y, marker)), marker) {
					base = y
				}
			}
			if base < 0 {
				t.Fatal("pyramid base sandstone column absent")
			}
			t.Logf("base y=%d", base)
			for _, check := range []struct {
				rel   [3]int
				block string
			}{
				{[3]int{0, 0, 0}, "sandstone"},
				{[3]int{10, 0, 10}, "blue_terracotta"},
				{[3]int{9, 0, 9}, "orange_terracotta"},
				{[3]int{1, 10, 1}, "sandstone"},
				{[3]int{10, -11, 10}, "stone_pressure_plate"},
				{[3]int{9, -13, 9}, "tnt"},
				{[3]int{8, -14, 8}, "cut_sandstone"},
			} {
				p := [3]int{check.rel[0], base + check.rel[1], check.rel[2]}
				marker := fmt.Sprintf("PYRAMID_LAYOUT_%d_%d_%d", p[0], p[1], p[2])
				if !structureSaidBB(command(fmt.Sprintf("execute if block %d %d %d %s run say %s", p[0], p[1], p[2], check.block, marker)), marker) {
					t.Errorf("layout want %s at %v", check.block, p)
				}
			}
			for _, rel := range [][3]int{{8, -11, 10}, {12, -11, 10}, {10, -11, 8}, {10, -11, 12}} {
				p := [3]int{rel[0], base + rel[1], rel[2]}
				marker := fmt.Sprintf("PYRAMID_CHEST_%d_%d_%d", p[0], p[1], p[2])
				if !structureSaidBB(command(fmt.Sprintf("execute if block %d %d %d chest run say %s", p[0], p[1], p[2], marker)), marker) {
					t.Errorf("chest absent at %v", p)
				}
				marker += "_DATA"
				if !structureSaidBB(command(fmt.Sprintf(`execute if data block %d %d %d {LootTable:"minecraft:chests/desert_pyramid"} run say %s`, p[0], p[1], p[2], marker)), marker) {
					t.Errorf("chest loot table absent at %v", p)
				}
			}

			counts := map[string]string{}
			// The trap room's pressure plate is unsupported in the blueprint;
			// count it before removing the TNT below it triggers a support update.
			for _, material := range []string{"stone_pressure_plate", "cut_sandstone", "chiseled_sandstone", "orange_terracotta", "blue_terracotta", "sandstone_slab", "sandstone_stairs", "tnt", "chest"} {
				matched := false
				replies := command("fill -1 40 -1 21 80 21 air replace " + material)
				for _, line := range replies {
					key, args, ok := componentArgs(line)
					if !ok || key != "commands.fill.success" || len(args) != 1 {
						continue
					}
					value := args[0]
					if compound, ok := value.(map[string]any); ok {
						value = compound[""]
					}
					counts[material] = fmt.Sprint(value)
					matched = true
				}
				if !matched {
					var keys []string
					for _, line := range replies {
						if key, args, ok := componentArgs(line); ok {
							keys = append(keys, fmt.Sprintf("%s%v", key, args))
						}
					}
					t.Fatalf("%s fill count absent: %v", material, keys)
				}
			}
			want := map[string]int{"cut_sandstone": 238, "chiseled_sandstone": 62, "orange_terracotta": 66, "blue_terracotta": 2, "sandstone_slab": 2, "sandstone_stairs": 17, "tnt": 9, "stone_pressure_plate": 1, "chest": 4}
			for material, n := range want {
				if counts[material] != fmt.Sprint(n) {
					t.Errorf("%s=%s want%d", material, counts[material], n)
				}
			}
			t.Logf("counts=%v", counts)
			command("execute store result score suspicious placepin run fill -1 40 -1 21 80 21 air replace suspicious_sand")
			if !said("execute if score suspicious placepin matches 6..8") {
				t.Fatal("suspicious sand count outside 6..8")
			}
			command("execute store success score failed placepin run place structure desert_pyramid 1000000 70 1000000")
			score("failed", 0)
		})
	}
}
