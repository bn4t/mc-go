package server

import "testing"

// Native FoodData.tick facts: one strict-above-four budget per invocation;
// a positive fractional saturation pool pays that budget without food spill.
func TestFoodBudgetTemporalAndFractional(t *testing.T) {
	s := simulationTestServer(t)
	p := survivalSession(t, s, "FoodBudget")
	s.simulationAction(func() {
		s.difficulty = DifficultyNormal
		p.food, p.saturation, p.exhaustion = 17, .4, 13
		for i, want := range []struct {
			food    int32
			sat, ex float32
		}{{17, 0, 9}, {16, 0, 5}, {15, 0, 1}, {15, 0, 1}} {
			s.hungerTickStep()
			if p.food != want.food || p.saturation != want.sat || p.exhaustion != want.ex {
				t.Fatalf("frame%d food/saturation/exhaustion=%d/%g/%g want %d/%g/%g", i+1, p.food, p.saturation, p.exhaustion, want.food, want.sat, want.ex)
			}
		}
		p.food, p.saturation, p.exhaustion = 17, .4, 5
		s.hungerTickStep()
		if p.food != 17 || p.saturation != 0 || p.exhaustion != 1 {
			t.Fatalf("fractional budget: %d/%g/%g", p.food, p.saturation, p.exhaustion)
		}
	})
}
