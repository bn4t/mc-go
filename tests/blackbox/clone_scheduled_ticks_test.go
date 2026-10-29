package blackbox

import (
	"fmt"
	"testing"
)

func (p *scheduledProbeBB) pressScheduledStone(x, z int32, seq int32, label string) {
	p.command(fmt.Sprintf("tp @s %g -60 %g 180 56", float64(x)+0.5, float64(z)+1.5))
	p.moveReady(float64(x)+0.6, -60, float64(z)+1.5)
	sendUseItemOnCursor(p.t, p.pw, x, -60, z, 1, 0.5, 0.125, 0.5, seq)
	p.await(fmt.Sprintf("if block %d -60 %d stone_button[powered=true]", x, z), label)
}

func TestCloneScheduledBlockTicksParity(t *testing.T) {
	withScheduledProbeBB(t, func(p *scheduledProbeBB) {
		p.command("setblock 4 -60 4 stone_button[face=floor]")
		p.command("setblock 8 -60 4 stone_button[face=floor]")
		p.command("setblock 4 -60 8 stone_button[face=floor]")
		p.command("setblock 8 -60 12 stone_button[face=floor,powered=true] strict")
		p.pressScheduledStone(4, 4, 1, "source_armed")
		p.pressScheduledStone(8, 4, 2, "outside_source_armed")
		p.step(7)
		p.expect("if block 4 -60 4 stone_button[powered=true]", "source_remaining")
		p.pressScheduledStone(4, 8, 3, "existing_target_armed")
		p.command("clone 4 -60 4 4 -60 4 4 -60 8 strict replace")
		p.command("clone 4 -60 4 4 -60 4 4 -60 12 strict replace")
		p.expect("if block 4 -60 12 stone_button[powered=true]", "copied_state")
		p.command("setblock 12 -60 4 water")
		p.command("clone 12 -60 4 12 -60 4 12 -60 12 strict replace")
		p.step(12)
		p.expect("if block 4 -60 4 stone_button[powered=true]", "source_before_20")
		p.expect("if block 4 -60 12 stone_button[powered=true]", "copy_before_20")
		p.expect("if block 4 -60 8 stone_button[powered=true]", "existing_before_20")
		p.expect("if block 13 -60 4 water", "source_fluid_ticked")
		p.expect("if block 13 -60 12 air", "fluid_tick_not_copied")
		p.step(1)
		p.expect("if block 4 -60 4 stone_button[powered=false]", "source_at_20")
		p.expect("if block 8 -60 4 stone_button[powered=false]", "outside_source_at_20")
		p.expect("if block 4 -60 12 stone_button[powered=false]", "copy_at_original_20")
		p.expect("if block 4 -60 8 stone_button[powered=true]", "existing_later_deadline")
		p.expect("if block 8 -60 12 stone_button[powered=true]", "outside_box_not_copied")
		// Keep later type/selection probes independent of expanding water.
		p.command("fill 0 -60 0 15 -60 15 air replace water")
		p.pressScheduledStone(4, 4, 4, "move_source_armed")
		p.step(7)
		p.expect("if block 4 -60 8 stone_button[powered=false]", "existing_at_27")
		p.command("clone 4 -60 4 4 -60 4 4 -60 12 strict replace move")
		p.expect("if block 4 -60 4 air", "moved_source_removed")
		p.expect("if block 4 -60 12 stone_button[powered=true]", "moved_state")
		p.step(12)
		p.expect("if block 4 -60 12 stone_button[powered=true]", "moved_before_40")
		p.step(1)
		p.expect("if block 4 -60 12 stone_button[powered=false]", "moved_at_original_40")
		// Ticks in the whole source box are copied even at filtered-out cells.
		// The queued stone tick keeps its original type after replacement by oak.
		p.command("setblock 4 -60 14 stone_button[face=floor]")
		p.pressScheduledStone(4, 14, 5, "filtered_old_type_armed")
		p.command("setblock 4 -60 14 oak_button[face=floor,powered=true] strict")
		p.command("setblock 6 -60 14 gold_block")
		p.command("setblock 8 -60 14 stone_button[face=floor,powered=true] strict")
		p.command("clone 4 -60 14 6 -60 14 8 -60 14 strict filtered gold_block")
		p.expect("if block 10 -60 14 gold_block", "filtered_block_copied")
		p.expect("if block 8 -60 14 stone_button[powered=true]", "filtered_out_state_retained")
		p.step(19)
		p.expect("if block 8 -60 14 stone_button[powered=true]", "filtered_tick_before_60")
		p.step(1)
		p.expect("if block 4 -60 14 oak_button[powered=true]", "stale_source_type_ignored")
		p.expect("if block 8 -60 14 stone_button[powered=false]", "unselected_tick_original_type_at_60")
	})
}
