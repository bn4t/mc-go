package blackbox

import "testing"

// Native copies block ticks even when the filter selects no blocks and the
// command reports failure. Destination block data itself remains unchanged.
func TestCloneFilteredMissScheduledTicksParity(t *testing.T) {
	withScheduledProbeBB(t, func(p *scheduledProbeBB) {
		p.command("setblock 4 -60 4 stone_button[face=floor]")
		p.command("setblock 4 -60 8 stone_button[face=floor,powered=true] strict")
		p.pressScheduledStone(4, 4, 1, "miss_source_armed")
		lines := p.command("clone 4 -60 4 4 -60 4 4 -60 8 strict filtered gold_block")
		failed := false
		for _, line := range lines {
			key, _, ok := componentArgs(line)
			failed = failed || (ok && key == "commands.clone.failed")
		}
		if !failed {
			p.t.Error("filtered miss did not report commands.clone.failed")
		}
		p.expect("if block 4 -60 4 stone_button[powered=true]", "miss_source_unchanged")
		p.expect("if block 4 -60 8 stone_button[powered=true]", "miss_destination_unchanged")
		p.step(19)
		p.expect("if block 4 -60 8 stone_button[powered=true]", "miss_before_deadline")
		p.step(1)
		p.expect("if block 4 -60 4 stone_button[powered=false]", "miss_source_due")
		p.expect("if block 4 -60 8 stone_button[powered=false]", "failed_command_tick_copied")
	})
}
