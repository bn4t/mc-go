package server

import "testing"

func TestWaterAnimalAirRefillBoundaries(t *testing.T) {
	for _, name := range []string{"axolotl", "nautilus", "dolphin"} {
		if got := mobIncreaseAirSupply(name, 32767); got != mobMaxAirSupply(name) {
			t.Fatalf("%s helper overflow%d", name, got)
		}
	}
	s := simulationTestServer(t)
	entityViewer(t, s, 776, "AirBoundary")
	s.simulationAction(func() {
		for _, name := range []string{"axolotl", "nautilus"} {
			e := s.entities.Spawn(&Entity{Type: mustEntityType(name), Dim: dimOverworld, X: 2, Y: -60, Z: 2, Living: true, Health: 20, MaxHealth: 20, NoAI: true, NoGravity: true})
			e.AirSupply = 32767
			s.mobTickStep()
			if e.AirSupply != 32767 {
				t.Fatalf("%s overmax supply changed%d", name, e.AirSupply)
			}
		}
	})
}
