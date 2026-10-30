package server

import "testing"

func TestPeacefulExhaustionPreservesOnlyFood(t *testing.T) {
	s := simulationTestServer(t)
	p := survivalSession(t, s, "PeacefulPool")
	s.simulationAction(func() {
		s.difficulty = DifficultyPeaceful
		p.food, p.saturation, p.exhaustion = 17, 0, 4.1
		s.hungerTickStep()
		if p.food != 17 || p.exhaustion >= .2 {
			t.Fatalf("peaceful food/exhaustion %d/%v", p.food, p.exhaustion)
		}
		p.saturation, p.exhaustion = 5, 4.1
		s.hungerTickStep()
		if p.saturation != 4 || p.food != 17 {
			t.Fatalf("peaceful saturation/food %v/%d", p.saturation, p.food)
		}
		s.difficulty = DifficultyNormal
		p.saturation, p.exhaustion = 0, 4.1
		s.hungerTickStep()
		if p.food != 16 {
			t.Fatalf("normal food %d", p.food)
		}
	})
}
