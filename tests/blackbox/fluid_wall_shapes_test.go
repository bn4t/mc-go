//go:build blackbox

package blackbox

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

type fluidWallCase struct {
	name, source, target string
	direction            [3]int
	filled, exit         bool
	plugExit             bool
	supportScaffold      bool
	flipSource           string
	oneSource            bool
}

// Shared-face obstruction can be assembled from both cells' shapes. A dry
// waterloggable cell is filled only when the opposing faces leave an opening.
func TestFluidWallShapesParity(t *testing.T) {
	const player = "FluidFaces"
	tg := requireBlackboxFiles(t, map[string]string{"command-spam-threshold-seconds": "0", "view-distance": "2"}, map[string]string{"ops.json": opsJSONFor(player)}, nil)
	defer tg.cleanup()
	var cases []fluidWallCase
	for i, dir := range [][3]int{{1, 0, 0}, {-1, 0, 0}, {0, 0, 1}, {0, 0, -1}} {
		cases = append(cases,
			fluidWallCase{name: fmt.Sprintf("complementary_slabs_%d", i), source: "stone_slab[type=bottom,waterlogged=true]", target: "stone_slab[type=top,waterlogged=false]", direction: dir},
			fluidWallCase{name: fmt.Sprintf("matching_slabs_%d", i), source: "stone_slab[type=bottom,waterlogged=true]", target: "stone_slab[type=bottom,waterlogged=false]", direction: dir, filled: true, exit: true})
	}
	east, down := [3]int{1, 0, 0}, [3]int{0, -1, 0}
	cases = append(cases,
		fluidWallCase{name: "top_to_bottom", source: "stone_slab[type=top,waterlogged=true]", target: "stone_slab[type=bottom,waterlogged=false]", direction: east},
		fluidWallCase{name: "top_to_top", source: "stone_slab[type=top,waterlogged=true]", target: "stone_slab[type=top,waterlogged=false]", direction: east, filled: true, exit: true},
		fluidWallCase{name: "trapdoor_near_plate", source: "water[level=0]", target: "oak_trapdoor[open=true,facing=east,waterlogged=false]", direction: east},
		fluidWallCase{name: "trapdoor_far_plate", source: "water[level=0]", target: "oak_trapdoor[open=true,facing=west,waterlogged=false]", direction: east, filled: true},
		fluidWallCase{name: "trapdoor_blocked_source", source: "oak_trapdoor[open=true,facing=west,waterlogged=true]", target: "stone_slab[type=bottom,waterlogged=false]", direction: east},
		fluidWallCase{name: "trapdoor_open_source", source: "oak_trapdoor[open=true,facing=east,waterlogged=true]", target: "stone_slab[type=bottom,waterlogged=false]", direction: east, filled: true, exit: true},
		fluidWallCase{name: "stairs_near_full_face", source: "water[level=0]", target: "oak_stairs[facing=west,waterlogged=false]", direction: east},
		fluidWallCase{name: "stairs_far_full_face", source: "water[level=0]", target: "oak_stairs[facing=east,waterlogged=false]", direction: east, filled: true},
		fluidWallCase{name: "ladder_container", source: "water[level=0]", target: "ladder[facing=north,waterlogged=false]", direction: east, filled: true, exit: true},
		fluidWallCase{name: "chest_container", source: "water[level=0]", target: "chest[waterlogged=false]", direction: east, filled: true, exit: true},
		fluidWallCase{name: "copper_chest_container", source: "water[level=0]", target: "copper_chest[waterlogged=false]", direction: east, filled: true, exit: true},
		fluidWallCase{name: "iron_chain_container", source: "water[level=0]", target: "iron_chain[waterlogged=false]", direction: east, filled: true, exit: true},
		fluidWallCase{name: "copper_chain_container", source: "water[level=0]", target: "copper_chain[waterlogged=false]", direction: east, filled: true, exit: true},
		fluidWallCase{name: "pot_container", source: "water[level=0]", target: "decorated_pot[waterlogged=false]", direction: east, filled: true, exit: true},
		fluidWallCase{name: "roots_full_target", source: "water[level=0]", target: "mangrove_roots[waterlogged=false]", direction: east},
		fluidWallCase{name: "roots_full_source", source: "mangrove_roots[waterlogged=true]", target: "air", direction: east},
		fluidWallCase{name: "leaves_full_source", source: "oak_leaves[persistent=true,waterlogged=true]", target: "air", direction: east},
		fluidWallCase{name: "bottom_source_down", source: "stone_slab[type=bottom,waterlogged=true]", target: "air", direction: down},
		fluidWallCase{name: "top_source_down", source: "stone_slab[type=top,waterlogged=true]", target: "air", direction: down, filled: true, exit: true},
		fluidWallCase{name: "bottom_target_down", source: "water[level=0]", target: "stone_slab[type=bottom,waterlogged=false]", direction: down},
		fluidWallCase{name: "top_target_down", source: "water[level=0]", target: "stone_slab[type=top,waterlogged=false]", direction: down},
		fluidWallCase{name: "bottom_trapdoor_down", source: "water[level=0]", target: "oak_trapdoor[half=bottom,waterlogged=false]", direction: down},
		fluidWallCase{name: "top_trapdoor_down", source: "water[level=0]", target: "oak_trapdoor[half=top,waterlogged=false]", direction: down},
		fluidWallCase{name: "scaffold_top_face", source: "water[level=0]", target: "scaffolding[waterlogged=false]", direction: down, plugExit: true},
		fluidWallCase{name: "hanging_scaffold_bottom", source: "scaffolding[distance=1,bottom=true,waterlogged=true]", target: "air", direction: down, filled: true, exit: true, supportScaffold: true},
		fluidWallCase{name: "flowing_water_slab", source: "water[level=0]", target: "stone_slab[type=bottom,waterlogged=false]", direction: east, oneSource: true},
		fluidWallCase{name: "flowing_water_chest", source: "water[level=0]", target: "chest[waterlogged=false]", direction: east, oneSource: true},
		fluidWallCase{name: "flowing_water_pot", source: "water[level=0]", target: "decorated_pot[waterlogged=false]", direction: east, oneSource: true},
		fluidWallCase{name: "flowing_water_ladder", source: "water[level=0]", target: "ladder[facing=north,waterlogged=false]", direction: east, oneSource: true},
		fluidWallCase{name: "lava_above_logged_fence", source: "lava[level=0]", target: "oak_fence[waterlogged=true]", direction: down, filled: true, exit: true},
		fluidWallCase{name: "lava_above_logged_chest", source: "lava[level=0]", target: "chest[waterlogged=true]", direction: down, filled: true, exit: true},
		fluidWallCase{name: "horizontal_supply_cut", source: "oak_trapdoor[open=true,facing=east,waterlogged=true]", target: "air", direction: east, filled: true, exit: true, flipSource: "oak_trapdoor[open=true,facing=west,waterlogged=true]"},
		fluidWallCase{name: "vertical_supply_cut", source: "stone_slab[type=top,waterlogged=true]", target: "air", direction: down, filled: true, exit: true, flipSource: "stone_slab[type=bottom,waterlogged=true]"},
	)
	run := func(addr string) []bool {
		c, pr, pw := joinPlayBlackbox(t, addr, player)
		defer c.Close()
		awaitPlayReadyBlackbox(t, addr, c, pr, pw)
		_ = c.SetDeadline(time.Time{})
		sink := newChatSinkTP(pr, pw)
		wait := func(marker string) []string {
			var out []string
			deadline := time.Now().Add(30 * time.Second)
			for time.Now().Before(deadline) {
				lines := sink.take()
				out = append(out, lines...)
				for _, line := range lines {
					if strings.Contains(line, marker) {
						return out
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("%s missing marker%s", addr, marker)
			return nil
		}
		serial := 0
		query := func(pos [3]int, block string) bool {
			serial++
			hit, end := fmt.Sprintf("FACESHIT%dYES", serial), fmt.Sprintf("FACESEND%dDONE", serial)
			sendCommand(t, pw, fmt.Sprintf("execute if block %d %d %d minecraft:%s run say %s", pos[0], pos[1], pos[2], block, hit))
			sendCommand(t, pw, "say "+end)
			for _, line := range wait(end) {
				if strings.Contains(line, hit) {
					return true
				}
			}
			return false
		}
		sendCommand(t, pw, "gamemode creative @s")
		sendCommand(t, pw, "tp @s 6.5 108 6.5")
		sendCommand(t, pw, "gamerule minecraft:random_tick_speed 0")
		sendCommand(t, pw, "gamerule minecraft:water_source_conversion true")
		sendCommand(t, pw, "tick freeze")
		sendCommand(t, pw, "say FACESREADY")
		wait("FACESREADY")
		var out []bool
		for _, tc := range cases {
			d := tc.direction
			source := [3]int{-d[0], 100 - d[1], -d[2]}
			target := [3]int{0, 100, 0}
			exit := [3]int{d[0], 100 + d[1], d[2]}
			set := func(p [3]int, b string) {
				sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d minecraft:%s", p[0], p[1], p[2], b))
			}
			sendCommand(t, pw, "fill -2 98 -2 2 102 2 stone")
			set(source, "air")
			set(target, "air")
			if !tc.plugExit {
				set(exit, "air")
			}
			if tc.supportScaffold {
				set([3]int{1, 101, 0}, "scaffolding[distance=0]")
			}
			set(target, tc.target)
			// Source water can enter a container; flowing water cannot. A
			// second accessible horizontal source renews a source in the
			// target when its two collision faces both permit supply.
			if d[1] == 0 && tc.target != "air" && !tc.oneSource {
				set([3]int{d[2], 100, -d[0]}, "water[level=0]")
			}
			set(source, tc.source)
			set([3]int{-2 * d[0], 100 - 2*d[1], -2 * d[2]}, "obsidian")
			sprintTicks(t, pw, sink, 40)
			if !query(source, tc.source) {
				t.Fatalf("%s %s source did not persist: %s", addr, tc.name, tc.source)
			}
			targetPredicate := strings.Split(tc.target, "[")[0] + "[waterlogged=true]"
			if tc.target == "air" {
				targetPredicate = "water"
			}
			filled, flow := query(target, targetPredicate), query(exit, "water")
			out = append(out, filled, flow)
			if !tc.filled && tc.target != "air" && !query(target, tc.target) {
				t.Errorf("%s %s changed blocked/rejected container", addr, tc.name)
			}
			t.Logf("%s %s filled=%t exit=%t", addr, tc.name, filled, flow)
			if filled != tc.filled || flow != tc.exit {
				t.Errorf("%s %s got(%t,%t) want(%t,%t)", addr, tc.name, filled, flow, tc.filled, tc.exit)
			}
			if tc.flipSource != "" {
				set(source, tc.flipSource)
				sprintTicks(t, pw, sink, 80)
				remain, tail := query(target, "water"), query(exit, "water")
				out = append(out, remain, tail)
				t.Logf("%s %s after closing face water=%t tail=%t", addr, tc.name, remain, tail)
				if remain || tail {
					t.Errorf("%s %s blocked source kept downstream flow", addr, tc.name)
				}
			}
		}

		setCell := func(p [3]int, b string) {
			sendCommand(t, pw, fmt.Sprintf("setblock %d %d %d minecraft:%s", p[0], p[1], p[2], b))
		}
		// The same below-fluid source is a hole only when its top face
		// permits passage. Falling fluid fans sideways when that route is
		// sealed, including a dry container that rejects Flowing Water.
		for _, tc := range []struct {
			name, block string
			side        bool
		}{
			{"falling_above_logged_bottom", "stone_slab[type=bottom,waterlogged=true]", false},
			{"falling_above_logged_top", "stone_slab[type=top,waterlogged=true]", true},
			{"falling_above_dry_bottom", "stone_slab[type=bottom,waterlogged=false]", true},
			{"falling_above_scaffold", "scaffolding[distance=0,waterlogged=true]", true},
		} {
			sendCommand(t, pw, "fill -2 98 -2 2 102 2 stone")
			setCell([3]int{0, 101, 0}, "air")
			setCell([3]int{1, 101, 0}, "air")
			setCell([3]int{0, 100, 0}, tc.block)
			setCell([3]int{0, 102, 0}, "water[level=0]")
			setCell([3]int{0, 99, 0}, "obsidian")
			sprintTicks(t, pw, sink, 40)
			side := query([3]int{1, 101, 0}, "water")
			intact := query([3]int{0, 100, 0}, tc.block)
			out = append(out, side, intact)
			t.Logf("%s %s side=%t target_intact=%t", addr, tc.name, side, intact)
			if side != tc.side || !intact {
				t.Errorf("%s %s side=%t want%t intact=%t", addr, tc.name, side, tc.side, intact)
			}
		}
		// A source behind a full collision face cannot contribute to
		// renewal. Opening that face restores the two-source conversion.
		sendCommand(t, pw, "fill -2 98 -2 2 102 2 stone")
		setCell([3]int{0, 100, 0}, "air")
		setCell([3]int{-1, 100, 0}, "mangrove_roots[waterlogged=true]")
		setCell([3]int{1, 100, 0}, "water[level=0]")
		sprintTicks(t, pw, sink, 40)
		one := query([3]int{0, 100, 0}, "water[level=1]")
		setCell([3]int{-1, 100, 0}, "stone_slab[type=bottom,waterlogged=true]")
		sprintTicks(t, pw, sink, 40)
		two := query([3]int{0, 100, 0}, "water[level=0]")
		out = append(out, one, two)
		t.Logf("%s renewal blocked_source_flow=%t open_sources_source=%t", addr, one, two)
		if !one || !two {
			t.Errorf("%s renewal ignored collision face: flow=%t source=%t", addr, one, two)
		}
		sendCommand(t, pw, "tick unfreeze")
		return out
	}
	v, m := run(tg.vanilla), run(tg.mcgo)
	if fmt.Sprint(v) != fmt.Sprint(m) {
		t.Fatalf("vanilla%v mcgo%v", v, m)
	}
}
