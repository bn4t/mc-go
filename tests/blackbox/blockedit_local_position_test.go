//go:build blackbox

package blackbox

// TestBlockEditLocalPositionParity pins caret block positions for the direct
// block-editing commands /setblock, /fill and /clone. It covers a direct
// player source (including a non-zero view rotation), execute-framed rotation
// and eye anchors, fractional local amounts, and the shared mixed/invalid
// error contract, while retaining absolute-coordinate controls.

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestBlockEditLocalPositionParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("EditLocal")}, func(s *server.Server) {
		s.ViewDistance = 2
		s.SimulationDistance = 2
	})
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, "EditLocal")
			defer c.Close()
			awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				serial++
				mark := fmt.Sprintf("EDITLOCALEND%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+mark)
				return waitForExactSayBB(t, sink, mark, 30*time.Second)
			}
			expect := func(cmd, label string) {
				mark := "EDITLOCAL_" + label
				if !structureSaidBB(command(cmd+" run say "+mark), mark) {
					t.Errorf("missing %s", label)
				}
			}
			absent := func(cmd, label string) {
				mark := "EDITLOCAL_" + label
				if structureSaidBB(command(cmd+" run say "+mark), mark) {
					t.Errorf("unexpected %s", label)
				}
			}
			errorKey := func(cmd, key, label string) {
				lines := command(cmd)
				keys := executePlanFeedbackKeysBB(lines)
				if !slices.Contains(keys, key) {
					t.Errorf("%s missing %s: %v", label, key, keys)
				}
			}
			block := func(x, y, z int, name, label string) {
				expect(fmt.Sprintf("execute if block %d %d %d minecraft:%s", x, y, z, name), label)
			}

			command("tick freeze")
			command("gamemode spectator @s")
			command("forceload add -32 -32 47 31")
			command("tp @s 8.5 -60 8.5 0 0")

			// Direct player source: yaw 0 faces +Z.
			command("setblock ^ ^ ^1 minecraft:gold_block")
			block(8, -60, 9, "gold_block", "setblock_direct_yaw0")
			// A direct player's own view rotation drives the local frame.
			command("tp @s 8.5 -60 8.5 90 0")
			command("setblock ^ ^ ^1 minecraft:diamond_block")
			block(7, -60, 8, "diamond_block", "setblock_direct_yaw90")
			command("tp @s 8.5 -60 8.5 0 0")

			// Fractional local amounts floor after the frame transform.
			command("setblock ^1.5 ^2.25 ^-2.75 minecraft:redstone_block")
			block(10, -58, 5, "redstone_block", "setblock_fractional")

			// Execute-framed position/rotation and an eyes anchor.
			command("execute positioned 8.5 -58 8.5 rotated 180 0 run setblock ^ ^ ^1 minecraft:emerald_block")
			block(8, -58, 7, "emerald_block", "setblock_execute_rotated")
			command("execute positioned 8.5 -58.5 8.5 anchored eyes run setblock ^ ^ ^ minecraft:gold_block")
			block(8, -57, 8, "gold_block", "setblock_execute_eyes")

			// fill: a direct line and an execute-rotated line in separate cells.
			command("fill ^4 ^ ^ ^4 ^ ^2 minecraft:stone")
			for z := 8; z <= 10; z++ {
				block(12, -60, z, "stone", fmt.Sprintf("fill_direct_%d", z))
			}
			command("execute rotated 90 0 run fill ^ ^ ^2 ^ ^ ^4 minecraft:granite")
			for x := 4; x <= 6; x++ {
				block(x, -60, 8, "granite", fmt.Sprintf("fill_rotated_%d", x))
			}

			// clone: a dedicated local source line copied to a local
			// destination, preserving the middle gold block.
			command("setblock 16 -60 8 minecraft:stone")
			command("setblock 16 -60 9 minecraft:gold_block")
			command("setblock 16 -60 10 minecraft:emerald_block")
			command("execute positioned 16.5 -60 8.5 rotated 0 0 run clone ^ ^ ^ ^ ^ ^2 ^ ^ ^4")
			block(16, -60, 12, "stone", "clone_dest_start")
			block(16, -60, 13, "gold_block", "clone_dest_middle")
			block(16, -60, 14, "emerald_block", "clone_dest_end")
			command("execute positioned 16.5 -60 9.5 rotated 180 0 run clone ^ ^ ^-1 ^ ^ ^1 16 -60 16")
			block(16, -60, 16, "stone", "clone_rotated_dest_start")
			block(16, -60, 17, "gold_block", "clone_rotated_dest_middle")
			block(16, -60, 18, "emerald_block", "clone_rotated_dest_end")

			// Absolute-coordinate controls keep the existing parser.
			command("setblock 20 -60 20 minecraft:stone")
			block(20, -60, 20, "stone", "setblock_absolute_control")
			command("fill 20 -60 20 20 -60 22 minecraft:stone")
			block(20, -60, 22, "stone", "fill_absolute_control")
			command("clone 20 -60 20 20 -60 22 20 -60 24")
			block(20, -60, 24, "stone", "clone_absolute_control")

			// Mixed and invalid caret syntax shares the execute error contract.
			errorKey("setblock ^ ~ ^1 minecraft:stone", "argument.pos.mixed", "setblock_mixed")
			errorKey("setblock ^NaN ^ ^ minecraft:stone", "parsing.double.expected", "setblock_nan")
			errorKey("setblock ^ ^ ^1e2 minecraft:stone", "command.expected.separator", "setblock_exponent")
			errorKey("fill ^ ~ ^ ^ ^ ^2 minecraft:stone", "argument.pos.mixed", "fill_mixed")
			errorKey("fill ^ ^1e2 ^ ^ ^ ^2 minecraft:stone", "argument.pos3d.incomplete", "fill_exponent")
			errorKey("clone ^ ^ ^ ^ ~ ^2 ^ ^ ^5", "argument.pos.mixed", "clone_mixed")
			errorKey("clone ^ ^ ^ ^ ^ ^2 ^+1 ^ ^5", "parsing.double.expected", "clone_leading_plus")

			// No error case may edit the world.
			absent("execute if block 20 -60 20 minecraft:air", "absolute_control_still_present")
		})
	}
}
