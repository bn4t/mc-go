//go:build blackbox

package blackbox

import (
	"fmt"
	"math"
	"mc-go/internal/server"
	"slices"
	"testing"
	"time"
)

func TestExecuteBlockLocalPositionParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("LocalBlock")}, func(s *server.Server) { s.ViewDistance = 2; s.SimulationDistance = 2 })
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, "LocalBlock")
			defer c.Close()
			awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				serial++
				mark := fmt.Sprintf("BLOCKLOCALEND%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+mark)
				return waitForExactSayBB(t, sink, mark, 30*time.Second)
			}
			expect := func(condition, label string) {
				mark := "BLOCKLOCAL_" + label
				if !structureSaidBB(command("execute "+condition+" run say "+mark), mark) {
					t.Errorf("block source-frame%s absent", label)
				}
			}
			absent := func(condition, label string) {
				mark := "BLOCKLOCAL_" + label
				if structureSaidBB(command("execute "+condition+" run say "+mark), mark) {
					t.Errorf("unexpected block predicate%s executed", label)
				}
			}
			loaded := func(dim string, x, z int) {
				ready := false
				deadline := time.Now().Add(60 * time.Second)
				for !ready && time.Now().Before(deadline) {
					ready = structureSaidBB(command(fmt.Sprintf("execute in %s if loaded %d 70 %d run say BLOCKLOCALLOADED", dim, x, z)), "BLOCKLOCALLOADED")
				}
				if !ready {
					t.Fatal("target area unloaded", dim, x, z)
				}
			}
			command("tick freeze")
			command("gamemode spectator @s")
			command("forceload add -32 -32 47 31")
			loaded("minecraft:overworld", -8, 11)
			loaded("minecraft:overworld", 40, 19)
			command("execute in minecraft:the_nether run forceload add 0 0 31 31")
			command("execute in minecraft:the_nether run tp @s 8.5 70.5 11.5")
			loaded("minecraft:the_nether", 8, 11)
			command("setblock 8 70 11 minecraft:gold_block")
			command("forceload add 56 80 79 103")
			command("tp @s 64.5 70.5 88.5")
			loaded("minecraft:the_nether", 64, 88)
			command("setblock 64 70 88 minecraft:emerald_block")
			command("execute in minecraft:overworld run tp @s 0.5 -60 0.5")
			command("setblock 8 70 11 minecraft:stone")
			command("forceload add 56 80 79 103")
			loaded("minecraft:overworld", 64, 88)
			command("setblock 64 70 88 minecraft:diamond_block")
			rows := []struct {
				origin                    [3]float64
				rotation, relative, local string
			}{
				{[3]float64{-7.5, -57.5, 11.5}, "0 0", "~16 ~ ~", "^16 ^ ^"},
				{[3]float64{24.5, -57.5, 11.5}, "90 0", "~-16 ~ ~", "^ ^ ^16"},
				{[3]float64{8.5, -57.5, 27.5}, "180 0", "~ ~ ~-16", "^ ^ ^16"},
				{[3]float64{-7.5, -57.5, 11.5}, "270 0", "~16 ~ ~", "^ ^ ^16"},
				{[3]float64{8.5, -41.5, 11.5}, "0 90", "~ ~-16 ~", "^ ^ ^16"},
				{[3]float64{8.5, -61.5, 11.5}, "0 -90", "~ ~4 ~", "^ ^ ^4"},
				{[3]float64{10.25, -59.2, 4.65}, "33 -27", "~-2 ~2 ~7", "^2.25 ^-1.5 ^6.75"},
			}
			for _, r := range rows {
				command(fmt.Sprintf("setblock %d %d %d minecraft:air", int(math.Floor(r.origin[0])), int(math.Floor(r.origin[1])), int(math.Floor(r.origin[2]))))
			}
			for _, cmd := range []string{`setblock 8 -58 11 minecraft:diamond_block`, `setblock 8 -59 11 minecraft:emerald_block`, `setblock 8 -60 11 minecraft:iron_block`, `setblock 8 -58 15 minecraft:oak_log[axis=x]`, `setblock 8 -58 19 minecraft:chest{Items:[{Slot:0b,id:"minecraft:stone",count:2}]}`} {
				command(cmd)
			}
			before := gameTimeOf(t, pw, sink)
			for i, r := range rows {
				frame := fmt.Sprintf("positioned %g %g %g rotated %s", r.origin[0], r.origin[1], r.origin[2], r.rotation)
				for j, coords := range []string{"8 -58 11", r.relative, r.local} {
					expect(frame+" if block "+coords+" minecraft:diamond_block", fmt.Sprintf("target%d_%d", i, j))
					expect(frame+" unless block "+coords+" minecraft:emerald_block", fmt.Sprintf("unless_other%d_%d", i, j))
					absent(frame+" unless block "+coords+" minecraft:diamond_block", fmt.Sprintf("unless_target%d_%d", i, j))
				}
				expect(frame+" if block ~ ~ ~ minecraft:air unless block ~ ~ ~ minecraft:diamond_block", fmt.Sprintf("source%d", i))
			}
			frame := "positioned -7.5 -57.5 11.5 rotated 0 0"
			expect(frame+" if block ^16 ^ ^ #minecraft:mineable/pickaxe", "tag")
			frame = "positioned 8.5 -57.5 11.5 rotated 0 0"
			expect(frame+" if block ^ ^ ^4 minecraft:oak_log[axis=x] unless block ^ ^ ^4 minecraft:oak_log[axis=y]", "state")
			expect(frame+` if block ^ ^ ^8 minecraft:chest{Items:[{Slot:0b,id:"minecraft:stone",count:2}]} unless block ^ ^ ^8 minecraft:chest{Items:[{Slot:0b,id:"minecraft:stone",count:3}]}`, "nbt")
			for _, r := range []struct{ frame, coords, block, label string }{
				{"positioned 8.5 -59.12 11.5 rotated 0 0 anchored eyes", "^ ^ ^", "diamond_block", "eyes"},
				{"positioned 8.5 -59.12 11.5 rotated 0 0 anchored feet", "^ ^ ^", "iron_block", "feet"},
				{"anchored eyes positioned 8.5 -59.12 11.5 rotated 0 0", "^ ^ ^", "iron_block", "reset"},
				{"positioned 8.5 -59.12 11.5 anchored eyes", "8 -60 11", "iron_block", "literal_anchor"},
				{"positioned 64.5 70.5 88.5 in minecraft:the_nether rotated 0 0", "^ ^ ^", "gold_block", "scaled"},
				{"in minecraft:the_nether positioned 64.5 70.5 88.5 rotated 0 0", "^ ^ ^", "emerald_block", "position_after_dimension"},
				{"positioned 192.5 70.5 88.5 in minecraft:the_nether rotated 90 0", "^ ^ ^16", "gold_block", "local_distance"},
				{"in minecraft:overworld positioned 8.5 70.5 11.5", "^ ^ ^", "stone", "overworld"},
				{"in minecraft:overworld positioned 64.5 70.5 88.5", "^ ^ ^", "diamond_block", "overworld_other"},
			} {
				expect(r.frame+" if block "+r.coords+" minecraft:"+r.block, r.label)
			}
			for i, r := range []struct{ coords, key string }{{"^ ~ ^", "argument.pos.mixed"}, {"~ ^ ~", "argument.pos.mixed"}, {"^NaN ^ ^", "parsing.double.expected"}, {"^-Inf ^ ^", "parsing.double.invalid"}, {"^1e2 ^ ^", "argument.pos3d.incomplete"}, {"^+1 ^ ^", "parsing.double.expected"}, {"^ ^ ^Inf", "parsing.double.expected"}, {"^ ^ ^1e2", "command.expected.separator"}} {
				for _, condition := range []string{"if", "unless"} {
					mark := fmt.Sprintf("BLOCKLOCALINVALID%d%s", i, condition)
					lines := command("execute positioned 8.5 -57.5 11.5 " + condition + " block " + r.coords + " minecraft:diamond_block run say " + mark)
					keys := executePlanFeedbackKeysBB(lines)
					if structureSaidBB(lines, mark) || !slices.Contains(keys, r.key) {
						t.Errorf("%s %s executed or missing%s: %v", condition, r.coords, r.key, keys)
					}
				}
			}
			expect("if dimension minecraft:overworld if entity @s[x=0.5,y=-60,z=0.5,distance=..0.01]", "actor_unchanged")
			if after := gameTimeOf(t, pw, sink); after != before {
				t.Fatalf("read-only predicates advanced age%d->%d", before, after)
			}
		})
	}
}
