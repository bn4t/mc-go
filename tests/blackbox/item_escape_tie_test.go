package blackbox

import (
	"fmt"
	"math"
	"testing"
)

func TestItemEscapeTiePriorityParity(t *testing.T) {
	const player = "ItemTieReader"
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "command-spam-threshold-seconds": "0"}, map[string]string{"ops.json": opsJSONFor(player)}, nil)
	defer targets.cleanup()
	run := func(addr string) {
		c, pr, pw := joinPlayBlackbox(t, addr, player)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		sink := newChatSinkTP(pr, pw)
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s 10.0 165.0 10.0")
		sendCommand(t, pw, "tick freeze")
		allAir := map[byte]string{'u': "air", 'n': "air", 's': "air", 'w': "air", 'e': "air"}
		for i, tc := range []struct {
			name  string
			cells map[byte]string
			x, z  float64
			axis  int
			sign  float64
		}{
			{"all_air", allAir, .5, .5, 2, -1},
			{"horizontal", map[byte]string{'n': "air", 's': "air", 'w': "air", 'e': "air"}, .5, .5, 2, -1},
			{"south_west", map[byte]string{'s': "air", 'w': "air"}, .5, .5, 2, 1},
			{"west_east", map[byte]string{'w': "air", 'e': "air"}, .5, .5, 0, -1},
			{"up_air_north_slab", map[byte]string{'u': "air", 'n': "oak_slab[type=bottom]"}, .5, .5, 2, -1},
			{"up_slab_north_air", map[byte]string{'u': "oak_slab[type=bottom]", 'n': "air"}, .5, .5, 2, -1},
			{"all_slab", map[byte]string{'u': "oak_slab[type=bottom]", 'n': "oak_slab[type=bottom]", 's': "oak_slab[type=bottom]", 'w': "oak_slab[type=bottom]", 'e': "oak_slab[type=bottom]"}, .5, .5, 2, -1},
			{"nearest_west", allAir, .25, .5, 0, -1},
			{"near_north", map[byte]string{'u': "air", 'n': "air"}, .5, .5 - 5e-8, 2, -1},
			{"near_up", map[byte]string{'u': "air", 'n': "air"}, .5, .5 + 5e-8, 1, 1},
			{"up_south", map[byte]string{'u': "air", 's': "air"}, .5, .5, 2, 1},
			{"up_west", map[byte]string{'u': "air", 'w': "air"}, .5, .5, 0, -1},
			{"up_east", map[byte]string{'u': "air", 'e': "air"}, .5, .5, 0, 1},
		} {
			sendCommand(t, pw, "kill @e[type=item]")
			sendCommand(t, pw, "fill -1 161 -1 1 163 1 stone")
			for _, d := range []struct {
				key        byte
				dx, dy, dz int
			}{{'u', 0, 1, 0}, {'n', 0, 0, -1}, {'s', 0, 0, 1}, {'w', -1, 0, 0}, {'e', 1, 0, 0}} {
				if block, ok := tc.cells[d.key]; ok {
					sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d %s", d.dx, 162+d.dy, d.dz, block))
				}
			}
			tag := fmt.Sprintf("ItemTie%d", i)
			sel := "@e[type=item,tag=" + tag + ",limit=1]"
			sendCommand(t, pw, fmt.Sprintf(`summon item %.17g 162.375 %.17g {Tags:["%s"],Item:{id:"minecraft:stone",count:1},NoGravity:1b,Motion:[0d,0d,0d],PickupDelay:32767s,Age:0s}`, tc.x, tc.z, tag))
			age := float64(0)
			for tick := 0; tick < 3 && age == 0; tick++ {
				shulkerStepTicks(t, pw, sink, 1)
				state, n := dataGetSel(t, sink, pw, sel, "Age")
				if state != "query" {
					t.Fatal("missing age")
				}
				age = n
			}
			if age != 1 {
				t.Fatalf("age%v", age)
			}
			x, y, z, ok := dataGetPos(t, sink, pw, sel)
			if !ok {
				t.Fatal("missing escaped item")
			}
			delta := [3]float64{x - tc.x, y - 162.375, z - tc.z}
			parsed, ok := parseSNBT(dataGetLine(t, sink, pw, sel, "Motion"))
			values, isList := parsed.([]any)
			if !ok || !isList || len(values) != 3 {
				t.Fatal("missing Motion")
			}
			var motion [3]float64
			for axis := range motion {
				n, ok := hrNum(values[axis])
				if !ok {
					t.Fatal("invalid Motion")
				}
				motion[axis] = n
			}
			speed := tc.sign * delta[tc.axis]
			valid := speed >= .1-1e-7 && speed <= .3+1e-7
			for axis := range delta {
				valid = valid && (axis == tc.axis || math.Abs(delta[axis]) < 1e-7) && math.Abs(motion[axis]-delta[axis]*float64(float32(.98))) < 1e-10
			}
			t.Logf("%s %s delta%v Motion%v", addr, tc.name, delta, motion)
			if !valid {
				t.Errorf("%s %s wrong escape priority", addr, tc.name)
			}
		}
	}
	run(targets.vanilla)
	run(targets.mcgo)
}
