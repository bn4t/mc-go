package server

import "testing"

func TestPlayerEffectTimeFreezeStepAndResume(t *testing.T) {
	s := simulationTestServer(t)
	p := entityViewer(t, s, 776, "FrozenClock")
	p.active.Store(true)
	s.simulationAction(func() {
		s.clock.frozen = true
		p.effects = map[int32]mobEffectInst{
			effectSpeed:      {amplifier: 1, expiry: 2, flags: 14, flagsSet: true, hidden: &mobEffectInst{expiry: 10, flags: 14, flagsSet: true}},
			effectAbsorption: {expiry: -1, flags: 14, flagsSet: true},
		}
		s.frozenPlayerEffectTick()
		if p.effectNow() != 1 {
			t.Fatalf("frozen player age%d want1", p.effectNow())
		}
		for range 20 {
			s.clock.count++
			s.compensateFrozenPlayerEffectStep()
			s.mobEffectTickStep()
		}
		if p.effectNow() != 1 || p.effects[effectSpeed].amplifier != 1 {
			t.Fatal("instant frozen world steps advanced player effects")
		}
		s.frozenPlayerEffectTick()
		e := p.effects[effectSpeed]
		if e.amplifier != 0 || e.expiry-p.effectNow() != 8 || e.flags != 6 {
			t.Fatalf("hidden resume%+v at%d", e, p.effectNow())
		}
		if p.effects[effectAbsorption].expiry != -1 {
			t.Fatal("infinite sentinel changed")
		}
		s.clock.frozen = false
		s.clock.count++
		s.mobEffectTickStep()
		if e := p.effects[effectSpeed]; e.expiry-p.effectNow() != 7 {
			t.Fatalf("unfrozen remaining%d want7", e.expiry-p.effectNow())
		}
	})
}

func TestPlayerEffectTimeSprintFromFreeze(t *testing.T) {
	s := simulationTestServer(t)
	p := entityViewer(t, s, 776, "SprintEffectClock")
	p.active.Store(true)
	s.simulationAction(func() {
		s.clock.frozen = true
		s.applyMobEffectFlags(p, effectSpeed, 0, 20, false)
	})
	// The real sprint driver temporarily clears frozen, simulates n+1 ticks,
	// and restores the previous frozen state at its completion.
	s.SprintTicks(2)
	s.simulationAction(func() {
		if !s.clock.isFrozen() || s.clock.isSprinting() {
			t.Fatal("sprint did not restore frozen state")
		}
		if p.effectNow() != 3 || p.effects[effectSpeed].expiry-p.effectNow() != 17 {
			t.Fatalf("sprint age%d effect%+v", p.effectNow(), p.effects[effectSpeed])
		}
		s.frozenPlayerEffectTick()
		if p.effects[effectSpeed].expiry-p.effectNow() != 16 {
			t.Fatal("paced frozen effect clock did not resume")
		}
	})
}

func TestPlayerEffectTimeIgnoresUnadmittedSessions(t *testing.T) {
	s := simulationTestServer(t)
	p := entityViewer(t, s, 776, "UnadmittedEffectClock")
	s.simulationAction(func() {
		s.clock.frozen = true
		p.effects = map[int32]mobEffectInst{effectSpeed: {expiry: 0}}
		s.frozenPlayerEffectTick()
		if p.effectFrozenTicks != 0 || len(p.effects) != 1 {
			t.Fatal("unadmitted session ran player effect ticks")
		}
		s.playerEffectTickStep()
		if len(p.effects) != 1 {
			t.Fatal("unadmitted session effect expired")
		}
	})
}
