package server

import "testing"

func TestSaturationDirectDoseIgnoresScale(t *testing.T) {
	s := simulationTestServer(t)
	p := survivalSession(t, s, "SatDirect")
	s.simulationAction(func() {
		for _, scale := range []float64{0, .1, .5, 1} {
			p.food, p.saturation = 18, 5
			p.effects = nil
			s.applyInstantEffectScaled(p, effectSaturation, 1, scale)
			if p.food != 20 || p.saturation != 9 || len(p.effects) != 0 {
				t.Fatalf("scale%g: food%d saturation%g effects%v", scale, p.food, p.saturation, p.effects)
			}
		}
	})
}
