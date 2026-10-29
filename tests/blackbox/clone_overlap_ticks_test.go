package blackbox

import "testing"

func TestCloneOverlappingScheduledTicksParity(t *testing.T) {
	withScheduledProbeBB(t, func(p *scheduledProbeBB) {
		p.command("setblock 4 -60 4 stone_button[face=floor]")
		p.command("setblock 6 -60 4 oak_button[face=floor]")
		p.pressScheduledStone(4, 4, 1, "overlap_stone_armed")
		p.step(7)
		p.command("tp @s 6.5 -60 5.5 180 56")
		p.moveReady(6.6, -60, 5.5)
		sendUseItemOnCursor(p.t, p.pw, 6, -60, 4, 1, 0.5, 0.125, 0.5, 2)
		p.await("if block 6 -60 4 oak_button[powered=true]", "overlap_oak_armed")
		p.command("clone 4 -60 4 6 -60 4 5 -60 4 strict replace force")
		p.expect("if block 5 -60 4 stone_button[powered=true]", "overlap_stone_copied")
		p.expect("if block 6 -60 4 air", "overlap_original_oak_replaced")
		p.expect("if block 7 -60 4 oak_button[powered=true]", "overlap_oak_copied")
		// A snapshot must not recursively copy newly added target ticks at x5
		// back into the source box. The old x6 tick still belongs to oak.
		p.command("setblock 6 -60 4 stone_button[face=floor,powered=true] strict")
		p.step(12)
		p.expect("if block 5 -60 4 stone_button[powered=true]", "overlap_stone_before_20")
		p.expect("if block 7 -60 4 oak_button[powered=true]", "overlap_oak_before_20")
		p.step(1)
		p.expect("if block 4 -60 4 stone_button[powered=false]", "overlap_original_stone_due")
		p.expect("if block 5 -60 4 stone_button[powered=false]", "overlap_stone_at_20")
		p.expect("if block 6 -60 4 stone_button[powered=true]", "no_recursive_copy_tick")
		p.expect("if block 7 -60 4 oak_button[powered=true]", "overlap_oak_not_early")
		p.step(16)
		p.expect("if block 7 -60 4 oak_button[powered=true]", "overlap_oak_before_37")
		p.step(1)
		p.expect("if block 7 -60 4 oak_button[powered=false]", "overlap_oak_at_37")
		p.expect("if block 6 -60 4 stone_button[powered=true]", "overlap_old_type_not_revived")
	})
}
