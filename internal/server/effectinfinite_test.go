package server

import (
	"math"
	"testing"
)

func TestInfiniteEffectPlayerUsesEntityAge(t *testing.T) {
	s := simulationTestServer(t)
	p := entityViewer(t, s, 776, "InfiniteAge")
	s.simulationAction(func() {
		s.clock.count = 503
		p.health, p.entityTicks = 10, 49
		inst := mobEffectInst{expiry: -1, born: 0}
		s.applyEffectTickSession(p, effectRegeneration, inst)
		if p.health != 10 {
			t.Fatal("infinite predicate used world age instead of entity age")
		}
		p.entityTicks = 50
		s.applyEffectTickSession(p, effectRegeneration, inst)
		if p.health != 11 {
			t.Fatal("due entity age did not heal")
		}
		// Finite duration retains rem+1 independently of the entity clock.
		finite := mobEffectInst{expiry: 553, born: 0}
		s.applyEffectTickSession(p, effectRegeneration, finite)
		if p.health != 11 {
			t.Fatal("finite duration lost its existing alignment")
		}
		s.clock.count = 504
		s.applyEffectTickSession(p, effectRegeneration, finite)
		if p.health != 12 {
			t.Fatal("finite remaining-duration due boundary lost")
		}
		s.clock.frozen = true
		p.entityTicks = 49
		s.applyEffectTickSession(p, effectRegeneration, inst)
		if p.health != 13 || p.entityTicks != 49 || s.clock.count != 504 {
			t.Fatal("frozen upcoming entity age changed finite/world clocks")
		}
		if got := effectTickArgument(inst, 504, int64(math.MaxInt32)+1); got != math.MinInt32 {
			t.Fatalf("signed entity age%v", got)
		}
		if got := effectTickArgument(inst, 504, 1<<32); got != 0 {
			t.Fatalf("wrapped entity age%v", got)
		}
	})
}

func TestInfiniteEffectMobConsumesActualTick(t *testing.T) {
	s := simulationTestServer(t)
	s.simulationAction(func() {
		s.clock.count = 503
		e := s.entities.Spawn(&Entity{Type: mustEntityType("villager"), Dim: dimOverworld, Living: true, NoAI: true, NoGravity: true, PersistenceRequired: true, Health: 10, MaxHealth: 20, TickCount: 49, Effects: map[int32]mobEffectInst{effectRegeneration: {expiry: -1, born: 0}}})
		s.mobEffectTickStep()
		if e.Health != 10 {
			t.Fatal("an unticked body pulsed")
		}
		s.mobTickStep()
		if e.TickCount != 50 {
			t.Fatalf("actual age%d", e.TickCount)
		}
		s.mobEffectTickStep()
		if e.Health != 11 {
			t.Fatal("due actual entity tick did not heal")
		}
		s.mobEffectTickStep()
		s.clock.count = 504
		s.mobEffectTickStep()
		if e.Health != 11 {
			t.Fatal("unchanged age pulsed again")
		}
		// An actual new tick whose age is not due must ignore world phase550.
		s.clock.count = 550
		s.mobTickStep()
		s.mobEffectTickStep()
		if e.TickCount != 51 || e.Health != 11 {
			t.Fatal("mob cadence used global phase")
		}
		e.TickCount = 100 // parked at another due boundary
		e.Dormant = true
		e.livingEffectTickSerial++ // stale receipt must not bypass eligibility
		s.mobEffectTickStep()
		e.Dormant = false
		s.mobEffectTickStep()
		if e.Health != 11 {
			t.Fatal("parked receipt replayed after activation")
		}
	})
}

func TestInfiniteEffectPlayerTickReceipt(t *testing.T) {
	s := simulationTestServer(t)
	p := entityViewer(t, s, 776, "InfiniteReceipt")
	s.simulationAction(func() {
		if s.consumeInfinitePlayerTick(p) {
			t.Fatal("constructor age zero counted as a tick")
		}
		p.entityTicks = 50
		if !s.consumeInfinitePlayerTick(p) || s.consumeInfinitePlayerTick(p) {
			t.Fatal("player tick was not consumed exactly once")
		}
		p.entityTicks = 1 << 32
		if !s.consumeInfinitePlayerTick(p) {
			t.Fatal("signed zero wrap was mistaken for constructor age")
		}
	})
}

func TestInfiniteEffectBodyClockLifecycle(t *testing.T) {
	s := simulationTestServer(t)
	p := entityViewer(t, s, 776, "InfiniteBody")
	p.sendCh = make(chan sendPkt, 4096)
	s.simulationAction(func() {
		p.entityTicks = 137
		p.infiniteEffectAge, p.infiniteEffectAgeSet = 137, true
		p.effects = map[int32]mobEffectInst{effectRegeneration: {expiry: -1}}
		s.changeDimension(p, s.dims[dimNether], .5, 64, .5, 0)
		if p.entityTicks != 137 || !p.infiniteEffectAgeSet || p.effects[effectRegeneration].expiry != -1 {
			t.Fatal("dimension transfer replaced current body clock")
		}
		p.dead = true
		s.respawnPlayer(p)
		if p.entityTicks != 0 || p.infiniteEffectAgeSet || len(p.effects) != 0 {
			t.Fatal("ordinary respawn retained old body clock/effects")
		}
	})
}
