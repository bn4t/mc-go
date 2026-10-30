package server

import (
	"math"
	"testing"
)

func TestFoodExhaustionStrictBoundary(t *testing.T) {
	s := simulationTestServer(t)
	p := survivalSession(t, s, "FoodBoundary")
	s.simulationAction(func() {
		s.difficulty = DifficultyNormal
		below := math.Nextafter32(4, float32(math.Inf(-1)))
		above := math.Nextafter32(4, float32(math.Inf(1)))
		for _, c := range []struct {
			exhaustion, remaining, saturation float32
			food                              int32
		}{
			{3, 3, 5, 17},
			{below, below, 5, 17},
			{4, 4, 5, 17},
			{above, 0x1p-21, 4, 16},
			{5, 1, 4, 16},
		} {
			p.food, p.saturation, p.exhaustion = 17, 5, c.exhaustion
			s.hungerTickStep()
			if p.food != 17 || p.saturation != c.saturation || p.exhaustion != c.remaining {
				t.Fatalf("saturation budget exhaustion%g: food%d sat%g ex%g", c.exhaustion, p.food, p.saturation, p.exhaustion)
			}
			p.food, p.saturation, p.exhaustion = 17, 0, c.exhaustion
			s.hungerTickStep()
			if p.food != c.food || p.saturation != 0 || p.exhaustion != c.remaining {
				t.Fatalf("food budget exhaustion%g: food%d sat%g ex%g", c.exhaustion, p.food, p.saturation, p.exhaustion)
			}
		}
		p.food, p.saturation, p.exhaustion = 17, 5, 4
		for i := 0; i < 3; i++ {
			s.hungerTickStep()
		}
		if p.food != 17 || p.saturation != 5 || p.exhaustion != 4 {
			t.Fatal("exact boundary drained on a later tick")
		}
	})
}
