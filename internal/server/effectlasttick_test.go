package server

import "testing"

func TestFiniteEffectFinalTickRequiresActualMobTick(t *testing.T) {
	s := simulationTestServer(t)
	s.simulationAction(func() {
		s.clock.count = 6
		e := s.entities.Spawn(&Entity{Type: mustEntityType("villager"), Dim: dimOverworld, Living: true, NoAI: true, NoGravity: true, PersistenceRequired: true, Health: 10, MaxHealth: 20, Effects: map[int32]mobEffectInst{effectRegeneration: {expiry: 6, born: 5, amplifier: 5}}})
		s.mobTickStep()
		s.mobEffectTickStep()
		if e.Health != 11 || len(e.Effects) != 0 {
			t.Fatalf("final pulse/lifetime %v/%v", e.Health, e.Effects)
		}
		s.mobEffectTickStep()
		if e.Health != 11 {
			t.Fatal("expired last pulse replayed")
		}
		parked := s.entities.Spawn(&Entity{Type: mustEntityType("villager"), Dim: dimOverworld, Living: true, NoAI: true, Health: 10, MaxHealth: 20, Effects: map[int32]mobEffectInst{effectRegeneration: {expiry: 6, born: 5, amplifier: 5}}})
		s.mobEffectTickStep()
		if parked.Health != 10 {
			t.Fatal("last-tick extension pulsed an unticked body")
		}
	})
}

func TestFiniteZeroDurationDoesNotApply(t *testing.T) {
	s := simulationTestServer(t)
	s.simulationAction(func() {
		s.clock.count = 6
		e := s.entities.Spawn(&Entity{Type: mustEntityType("villager"), Dim: dimOverworld, Living: true, NoAI: true, NoGravity: true, PersistenceRequired: true, Health: 10, MaxHealth: 20, Effects: map[int32]mobEffectInst{effectRegeneration: {expiry: 6, born: 6, amplifier: 5}}})
		s.mobTickStep()
		s.mobEffectTickStep()
		if e.Health != 10 {
			t.Fatal("zero-duration instance applied")
		}
	})
}

func TestFiniteEffectFinalPlayerTick(t *testing.T) {
	s := simulationTestServer(t)
	p := survivalSession(t, s, "LastPlayerTick")
	s.simulationAction(func() {
		s.clock.count = 6
		p.health = 10
		p.effects = map[int32]mobEffectInst{effectRegeneration: {expiry: 6, born: 5, amplifier: 5}}
		s.playerFluidTickStep()
		s.playerEffectTickStep()
		if p.health != 11 || len(p.effects) != 0 {
			t.Fatalf("player final pulse/lifetime %v/%v", p.health, p.effects)
		}
	})
}
