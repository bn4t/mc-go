//go:build blackbox

package blackbox

// TestDataItemLocalPositionParity pins caret block positions for /data block
// accessors (including the from-block source form) and /item block targets and
// sources. The fixture uses direct-player and execute-framed rotations,
// observes the resulting block-entity state, and checks the shared mixed and
// invalid-token errors.

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"mc-go/internal/server"
)

func TestDataItemLocalPositionParity(t *testing.T) {
	targets := requireBlackboxFiles(t, map[string]string{"view-distance": "2", "simulation-distance": "2"}, map[string]string{"ops.json": opsJSONFor("DataLocal")}, func(s *server.Server) {
		s.ViewDistance = 2
		s.SimulationDistance = 2
	})
	defer targets.cleanup()
	for _, leg := range []struct{ name, addr string }{{"native", targets.vanilla}, {"go", targets.mcgo}} {
		t.Run(leg.name, func(t *testing.T) {
			c, pr, pw := joinPlayBlackbox(t, leg.addr, "DataLocal")
			defer c.Close()
			awaitPlayReadyBlackbox(t, leg.addr, c, pr, pw)
			sink := newChatSinkResolvedTP(pr, pw)
			serial := 0
			command := func(cmd string) []string {
				serial++
				mark := fmt.Sprintf("DATALOCALEND%d", serial)
				sendCommand(t, pw, cmd)
				sendCommand(t, pw, "say "+mark)
				return waitForExactSayBB(t, sink, mark, 30*time.Second)
			}
			expect := func(cmd, label string) {
				mark := "DATALOCAL_" + label
				if !structureSaidBB(command(cmd+" run say "+mark), mark) {
					t.Errorf("missing %s", label)
				}
			}
			absent := func(cmd, label string) {
				mark := "DATALOCAL_" + label
				if structureSaidBB(command(cmd+" run say "+mark), mark) {
					t.Errorf("unexpected %s", label)
				}
			}
			hasKey := func(cmd, key, label string) {
				keys := executePlanFeedbackKeysBB(command(cmd))
				if !slices.Contains(keys, key) {
					t.Errorf("%s missing %s: %v", label, key, keys)
				}
			}
			errorKey := func(cmd, key, label string) {
				keys := executePlanFeedbackKeysBB(command(cmd))
				if !slices.Contains(keys, key) {
					t.Errorf("%s missing %s: %v", label, key, keys)
				}
			}

			command("tick freeze")
			command("gamemode spectator @s")
			command("forceload add -32 -32 47 31")
			command("tp @s 8.5 -60 8.5 0 0")
			for _, cmd := range []string{
				`setblock 8 -60 9 chest{Items:[{Slot:0b,id:"minecraft:stone",count:2}]}`,
				`setblock 8 -60 11 chest{Items:[{Slot:0b,id:"minecraft:stone",count:2}]}`,
				`setblock 8 -60 13 chest{Items:[{Slot:0b,id:"minecraft:stone",count:2}]}`,
				"setblock 8 -60 15 oak_sign[rotation=0]",
				`setblock 8 -60 17 chest{Items:[{Slot:0b,id:"minecraft:stone",count:2}]}`,
				`setblock 8 -60 19 chest{Items:[{Slot:0b,id:"minecraft:gold_block",count:4}]}`,
				`setblock 8 -60 21 chest`,
				`setblock 8 -60 23 chest{Items:[{Slot:0b,id:"minecraft:gold_block",count:4}]}`,
			} {
				command(cmd)
			}

			// /data block accessors: local get, merge, remove and sign state.
			hasKey("data get block ^ ^ ^1 Items", "commands.data.block.query", "data_get")
			hasKey("data get block ^ ^ ^3 Items", "commands.data.block.query", "data_get_far")
			hasKey(`data merge block ^ ^ ^3 {Items:[{Slot:0b,id:"minecraft:diamond",count:3}]}`, "commands.data.block.modified", "data_merge")
			expect("execute if items block 8 -60 11 container.0 minecraft:diamond", "data_merge_applied")
			hasKey(`data merge block ^ ^ ^5 {CustomName:"LocalChest"}`, "commands.data.block.modified", "data_merge_custom")
			expect(`execute if data block 8 -60 13 {CustomName:"LocalChest"}`, "data_merge_custom_applied")
			hasKey("data remove block ^ ^ ^5 CustomName", "commands.data.block.modified", "data_remove")
			absent(`execute if data block 8 -60 13 {CustomName:"LocalChest"}`, "data_remove_applied")
			hasKey("data merge block ^ ^ ^7 {is_waxed:1b}", "commands.data.block.modified", "data_sign_merge")
			expect("execute if data block 8 -60 15 is_waxed", "data_sign_applied")

			// /data modify with a local block source and destination.
			hasKey("data modify block ^ ^ ^13 Items set from block ^ ^ ^15 Items", "commands.data.block.modified", "data_modify_source")
			expect("execute if items block 8 -60 21 container.0 minecraft:gold_block[count=4]", "data_modify_source_applied")

			// /item block target and from-block source.
			hasKey("item replace block ^ ^ ^9 container.0 with minecraft:diamond 5", "commands.item.block.set.success", "item_replace")
			expect("execute if items block 8 -60 17 container.0 minecraft:diamond[count=5]", "item_replace_applied")
			hasKey("item replace block ^ ^ ^9 container.1 from block ^ ^ ^11 container.0", "commands.item.block.set.success", "item_replace_from")
			expect("execute if items block 8 -60 17 container.1 minecraft:gold_block[count=4]", "item_replace_from_applied")
			hasKey(`item modify block ^ ^ ^9 container.0 {function:"set_count",count:2}`, "commands.item.block.set.success", "item_modify")

			// Execute-framed rotation resolves the same block target.
			hasKey("execute positioned 8.5 -60 10.5 rotated 180 0 run data get block ^ ^ ^1 Items", "commands.data.block.query", "data_get_rotated")

			// Mixed and invalid caret syntax shares the execute error contract.
			errorKey("data get block ^ ~ ^1 Items", "argument.pos.mixed", "data_mixed")
			errorKey("data get block ^NaN ^ ^ Items", "parsing.double.expected", "data_nan")
			errorKey("data get block ^ ^ ^1e2 Items", "command.expected.separator", "data_exponent")
			errorKey("data modify block ^ ^ ^13 Items set from block ^ ~ ^ Items", "argument.pos.mixed", "data_source_mixed")
			errorKey("item replace block ^ ~ ^1 container.0 with minecraft:stone", "argument.pos.mixed", "item_mixed")
			errorKey("item replace block ^NaN ^ ^ container.0 with minecraft:stone", "parsing.double.expected", "item_nan")
			errorKey("item replace block ^ ^ ^1e2 container.0 with minecraft:stone", "command.expected.separator", "item_exponent")
			errorKey("item replace block ^ ^ ^9 container.0 from block ^ ~ ^ container.0", "argument.pos.mixed", "item_source_mixed")
		})
	}
}
