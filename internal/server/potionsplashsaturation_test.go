package server

import "testing"

func TestSaturationSplashDispatch(t *testing.T) {
	s := simulationTestServer(t)
	p := survivalSession(t, s, "SatSplashUnit")
	s.simulationAction(func() {
		source := &Entity{}
		mob := s.entities.Spawn(&Entity{Type: mustEntityType("villager"), Dim: dimOverworld, Living: true, Health: 20})
		for _, duration := range []int32{0, 200} {
			for _, proximity := range []float64{.125, .5, 1} {
				p.food, p.saturation = 18, 5
				p.effects = nil
				mob.Effects = nil
				effects := []splashEffect{{name: "saturation", dur: duration, amp: 1, custom: true, flags: 14, scale: .5}}
				s.splashApplyPlayer(source, p, effects, proximity)
				s.splashApplyMob(source, mob, effects, proximity)
				if p.food != 20 || p.saturation != 9 || len(p.effects) != 0 {
					t.Fatalf("duration%d proximity%g: food%d saturation%g effects%v", duration, proximity, p.food, p.saturation, p.effects)
				}
				if mob.Health != 20 || len(mob.Effects) != 0 {
					t.Fatalf("duration%d proximity%g: mobhealth%g effects%v", duration, proximity, mob.Health, mob.Effects)
				}
			}
		}
	})
}
