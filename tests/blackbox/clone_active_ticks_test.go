package blackbox

import "testing"

// A command block clones after the repeater's callback in the same tick.
// Native copyAreaFrom also captures the already-run callback, which is then
// due at the copied repeater on the following world tick.
func TestCloneActiveScheduledTicksParity(t *testing.T) {
	withScheduledProbeBB(t, func(p *scheduledProbeBB) {
		p.command("setblock 4 -60 4 repeater[facing=north,delay=4]")
		p.command("setblock 4 -60 3 redstone_block")
		p.command("setblock 12 -60 12 command_block")
		p.command(`data merge block 12 -60 12 {Command:"clone 4 -60 4 4 -60 4 4 -60 8 strict replace",auto:0b,TrackOutput:0b}`)
		p.expect(`if block 12 -60 12 command_block{Command:"clone 4 -60 4 4 -60 4 4 -60 8 strict replace"}`, "active_command_configured")
		p.step(7)
		p.expect("if block 4 -60 4 repeater[powered=false]", "active_source_before_due")
		p.command("setblock 13 -60 12 redstone_block")
		p.step(1)
		p.await("if block 4 -60 4 repeater[powered=true]", "active_source_callback")
		p.await("if block 4 -60 8 repeater[powered=true]", "active_clone_callback")
		p.await(`if block 12 -60 12 command_block{SuccessCount:1}`, "active_command_success")
		p.step(1)
		p.expect("if block 4 -60 4 repeater[powered=true]", "active_source_stays_powered")
		p.await("if block 4 -60 8 repeater[powered=false]", "already_run_tick_copied")
	})
}
