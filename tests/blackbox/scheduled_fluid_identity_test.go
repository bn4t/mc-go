package blackbox

import "testing"

func TestScheduledFluidIdentityParity(t *testing.T) {
	withScheduledProbeBB(t, func(p *scheduledProbeBB) {
		p.command("setblock 4 -60 4 water")
		p.command("setblock 8 -60 4 water[level=1]")
		p.command("setblock 12 -60 4 lava[level=1]")
		p.command("setblock 20 -60 4 water")
		p.command("setblock 20 -60 12 lava")
		p.step(2)
		p.command("setblock 4 -60 4 lava strict")
		p.command("setblock 8 -60 4 water strict")
		p.command("setblock 12 -60 4 lava strict")
		p.expect("if block 4 -60 4 lava[level=0]", "new_family_installed")
		p.expect("if block 8 -60 4 water[level=0]", "new_water_source_installed")
		p.expect("if block 12 -60 4 lava[level=0]", "new_lava_source_installed")
		p.step(3)
		p.expect("if block 4 -60 5 air", "old_water_did_not_tick_lava")
		p.expect("if block 8 -60 5 air", "old_flowing_water_did_not_tick_source")
		p.expect("if block 20 -60 5 water", "ordinary_water_fired")
		p.step(24)
		p.expect("if block 20 -60 13 air", "ordinary_lava_before_deadline")
		p.step(1)
		p.expect("if block 12 -60 5 air", "old_flowing_lava_did_not_tick_source")
		p.expect("if block 4 -60 5 air", "strict_lava_has_no_new_request")
		p.expect("if block 8 -60 5 air", "strict_water_has_no_new_request")
		p.expect("if block 20 -60 13 lava", "ordinary_lava_fired")
	})
}
