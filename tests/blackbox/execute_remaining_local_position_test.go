//go:build blackbox

package blackbox

// TestExecuteRemainingLocalPositionsParity pins local (^) coordinates for the
// remaining /execute position consumers: if blocks, if items block, if data
// block, store ... block, positioned and facing. The fixture uses one
// positioned/rotated frame per assertion and observes results through block
// predicates, block-entity reads and the player's own rotation.

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestExecuteRemainingLocalPositionsParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("LocalRest")}, func(s *server.Server) {
		s.ViewDistance = 2
		s.SimulationDistance = 2
	})
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, "LocalRest")
			defer c.Close()
			awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				serial++
				mark := fmt.Sprintf("LOCALRESTEND%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+mark)
				return waitForExactSayBB(t, sink, mark, 30*time.Second)
			}
			expect := func(cmd, label string) {
				mark := "LOCALREST_" + label
				if !structureSaidBB(command(cmd+" run say "+mark), mark) {
					t.Errorf("missing %s", label)
				}
			}
			absent := func(cmd, label string) {
				mark := "LOCALREST_" + label
				if structureSaidBB(command(cmd+" run say "+mark), mark) {
					t.Errorf("unexpected %s", label)
				}
			}
			errorKey := func(cmd, key, label string) {
				mark := "LOCALRESTERR_" + label
				lines := command(cmd + " run say " + mark)
				keys := executePlanFeedbackKeysBB(lines)
				if structureSaidBB(lines, mark) || !slices.Contains(keys, key) {
					t.Errorf("%s executed or missing %s: %v", label, key, keys)
				}
			}
			errorKeyLeaf := func(cmd, key, label string) {
				lines := command(cmd)
				keys := executePlanFeedbackKeysBB(lines)
				if !slices.Contains(keys, key) {
					t.Errorf("%s missing %s: %v", label, key, keys)
				}
			}
			ifScore := func(holder string, lo, hi int, label string) {
				expect(fmt.Sprintf("execute if score %s localpos matches %d..%d", holder, lo, hi), label)
			}

			command("tick freeze")
			command("gamemode spectator @s")
			command("forceload add -32 -32 47 31")
			command("tp @s 8.5 -60 8.5 0 0")
			for _, cmd := range []string{
				"scoreboard objectives add localpos dummy",
				"setblock 8 -58 8 stone",
				"setblock 8 -58 9 diamond_block",
				"setblock 8 -58 10 emerald_block",
				"setblock 8 -58 12 stone",
				"setblock 8 -58 13 diamond_block",
				"setblock 8 -58 14 emerald_block",
				`setblock 4 -58 4 chest{Items:[{Slot:0b,id:"minecraft:stone",count:2}]}`,
				"setblock 4 -58 5 oak_sign[rotation=0]",
				`data merge storage localrest:data {a:9b}`,
			} {
				command(cmd)
			}

			// if blocks: three BlockPos arguments, each resolved from the same
			// source frame, with absolute and local destinations.
			frame := "execute positioned 8.5 -58 8.5 rotated 0 0 "
			expect(frame+"if blocks ^ ^ ^ ^ ^ ^2 8 -58 12 all", "blocks_local_absolute_dest")
			expect(frame+"if blocks ^ ^ ^ ^ ^ ^2 ^ ^ ^4 all", "blocks_all_local")
			absent(frame+"if blocks ^ ^ ^ ^ ^ ^2 8 -58 13 all", "blocks_mismatch")
			command("setblock 8 -58 9 minecraft:air")
			absent(frame+"if blocks ^ ^ ^ ^ ^ ^2 8 -58 12 all", "blocks_air_all")
			expect(frame+"if blocks ^ ^ ^ ^ ^ ^2 8 -58 12 masked", "blocks_air_masked")
			command("setblock 8 -58 9 diamond_block")

			// if items block: local source position, including a rotation that
			// points back at the chest.
			expect("execute positioned 4.5 -58 4.5 rotated 0 0 if items block ^ ^ ^ container.0 minecraft:stone", "items_local")
			expect("execute positioned 4.5 -58 5.5 rotated 180 0 if items block ^ ^ ^1 container.0 minecraft:stone", "items_local_rotated")
			absent("execute positioned 4.5 -58 4.5 rotated 0 0 if items block ^ ^ ^ container.1 minecraft:stone", "items_wrong_slot")
			absent("execute positioned 4.5 -58 4.5 rotated 0 0 if items block ^ ^ ^1 container.0 minecraft:stone", "items_wrong_position")

			// if data block.
			expect("execute positioned 4.5 -58 4.5 rotated 0 0 if data block ^ ^ ^ Items", "data_local")
			expect("execute positioned 4.5 -58 5.5 rotated 180 0 if data block ^ ^ ^1 Items", "data_local_rotated")
			absent("execute positioned 4.5 -58 4.5 rotated 0 0 if data block ^ ^ ^1 Items", "data_wrong_position")

			// store ... block writes through the local position. A sign's
			// is_waxed byte is live, saveable block-entity state, unlike an
			// arbitrary unknown tag which the block entity discards on load.
			command("execute positioned 4.5 -58 4.5 rotated 0 0 store result block ^ ^ ^1 is_waxed byte 1 run data get storage localrest:data a")
			command("execute store result score stored localpos run data get block 4 -58 5 is_waxed")
			ifScore("stored", 1, 1, "store_local_value")
			command("execute positioned 4.5 -58 6.5 rotated 180 0 store result block ^ ^ ^1 is_waxed byte 0 run data get storage localrest:data a")
			command("execute store result score stored2 localpos run data get block 4 -58 5 is_waxed")
			ifScore("stored2", 0, 0, "store_local_rotated_value")

			// positioned: local vec3 keeps fractional coordinates; the following
			// ~ resolves from the resolved point.
			command("execute positioned 8.5 -58.5 8.5 rotated 90 0 positioned ^1 ^2 ^3 run setblock ~ ~ ~ redstone_block")
			expect("execute if block 5 -57 9 redstone_block", "positioned_rotated")
			command("execute positioned 8.5 -58.5 8.5 rotated 0 0 positioned ^1.5 ^0.25 ^-2.75 run setblock ~ ~ ~ gold_block")
			expect("execute if block 10 -59 5 gold_block", "positioned_fractional")
			command("execute positioned 8.5 -58.5 8.5 rotated 0 0 anchored eyes positioned ^ ^ ^ run setblock ~ ~ ~ diamond_block")
			expect("execute if block 8 -57 8 diamond_block", "positioned_eyes")
			command("execute positioned 5.5 -57.5 9.5 run setblock ~ ~ ~ iron_block")
			expect("execute if block 5 -58 9 iron_block", "positioned_absolute_control")

			// facing: a local target resolves in the source rotation frame; the
			// following teleport copies the resulting source rotation. The 45°
			// frame avoids the measured yaw-0 singularity.
			rotation := func(label, cmd string, yawLo, yawHi, pitchLo, pitchHi int) {
				command(cmd + " run tp @s ~ ~ ~ ~ ~")
				command("execute store result score yaw localpos run data get entity @s Rotation[0] 1000")
				command("execute store result score pitch localpos run data get entity @s Rotation[1] 1000")
				ifScore("yaw", yawLo, yawHi, label+"_yaw")
				ifScore("pitch", pitchLo, pitchHi, label+"_pitch")
			}
			base := "execute positioned 8.5 -58.5 8.5 "
			rotation("facing_local_45", base+"rotated 45 0 facing ^ ^ ^1", 44990, 45010, -1, 1)
			rotation("facing_absolute_45", base+"rotated 45 0 facing 7.792893218813452 -58.5 9.207106781186548", 44990, 45010, -1, 1)
			rotation("facing_local_ninety", base+"rotated 90 0 facing ^ ^ ^1", 89990, 90010, -1, 1)
			rotation("facing_absolute_ninety", base+"rotated 90 0 facing 7.5 -58.5 8.5", 89990, 90010, -1, 1)
			rotation("facing_local_pitch", base+"rotated 0 30 facing ^ ^ ^1", -1, 1, 29990, 30010)
			rotation("facing_absolute_pitch", base+"rotated 0 30 facing 8.5 -58.9999723136425 9.365993440151215", -1, 1, 29990, 30010)

			// Local grammar shares the source-frame error contract.
			errorKey("execute positioned ^ ~ ^1", "argument.pos.mixed", "positioned_mixed")
			errorKey("execute positioned ^NaN ^ ^", "parsing.double.expected", "positioned_nan")
			errorKey("execute positioned ^ ^ ^1e2", "command.expected.separator", "positioned_exponent")
			errorKey("execute positioned 8.5 -58.5 8.5 rotated 0 0 facing ^ ~ ^1", "argument.pos.mixed", "facing_mixed")
			errorKey("execute positioned 8.5 -58.5 8.5 rotated 0 0 facing ^NaN ^ ^", "parsing.double.expected", "facing_nan")
			errorKey("execute positioned 8.5 -58 8.5 rotated 0 0 if blocks ^ ~ ^ ^ ^ ^2 8 -58 12 all", "argument.pos.mixed", "blocks_mixed")
			errorKey("execute positioned 8.5 -58 8.5 rotated 0 0 if blocks ^ ^1e2 ^ ^ ^ ^2 8 -58 12 all", "argument.pos3d.incomplete", "blocks_exponent")
			errorKey("execute positioned 4.5 -58 4.5 rotated 0 0 if items block ^ ~ ^ container.0 minecraft:stone", "argument.pos.mixed", "items_mixed")
			errorKey("execute positioned 4.5 -58 4.5 rotated 0 0 if data block ^ ~ ^ Items", "argument.pos.mixed", "data_mixed")
			errorKeyLeaf("execute positioned 4.5 -58 4.5 rotated 0 0 store result block ^ ~ ^ foo int 1 run data get storage localrest:data a", "argument.pos.mixed", "store_mixed")
			errorKeyLeaf("execute positioned 4.5 -58 4.5 rotated 0 0 store result block ^+1 ^ ^ foo int 1 run data get storage localrest:data a", "parsing.double.expected", "store_leading_plus")
		})
	}
}
