package blackbox

import "testing"

// The shared queue's first-request correction must retain ordinary water and
// lava delays; block identity metadata does not apply to the fluid domain.
func TestScheduledFluidRegressionParity(t *testing.T) {
	withScheduledProbeBB(t, func(p *scheduledProbeBB) {
		p.command("setblock 4 -60 4 water")
		p.command("setblock 12 -60 12 lava")
		p.step(4)
		p.expect("if block 5 -60 4 air", "water_before_5")
		p.expect("if block 13 -60 12 air", "lava_before_5")
		p.step(1)
		p.expect("if block 5 -60 4 water", "water_at_5")
		p.expect("if block 13 -60 12 air", "lava_after_5")
		p.step(24)
		p.expect("if block 13 -60 12 air", "lava_before_30")
		p.step(1)
		p.expect("if block 13 -60 12 lava", "lava_at_30")
	})
}
