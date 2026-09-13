package server

import (
	"math"
	"sync"
	"time"
)

// DefaultTickRate is vanilla's 20 ticks per second.
const DefaultTickRate = 20.0

// MaxTickRate is vanilla's /tick rate ceiling (10000 TPS).
const MaxTickRate = 10000.0

// tickClock is the server's game clock and the state behind vanilla's
// /tick command surface. A tick is the atomic unit of simulation:
// everything the world advances through (items, mobs, furnaces,
// fluids, block ticks) happens inside GameTick, so freezing the clock
// freezes all of it, exactly as /tick freeze does in vanilla.
//
// The clock is deliberately separate from the world shards: shards own
// block storage and are driven once per tick from here.
type tickClock struct {
	mu     sync.Mutex
	frozen bool
	rate   float64
	count  int64
	// frozenAt is TickCount when the freeze began, so /tick query can
	// report how long the game has been frozen.
	frozenAt int64
	// sprinting is set while /tick sprint is running.
	sprinting bool

	// runMu serializes GameTick. The wall-clock loop and a synchronous
	// /tick step (or a sprint) must never advance the world at the same
	// time, or a stepped run would not be the deterministic N-tick run
	// the tests depend on.
	runMu sync.Mutex
}

// next decides what the game loop should do now. wait is how long to
// sleep before acting; run reports whether a tick should then be
// advanced. A frozen clock returns run=false, so the loop keeps sleeping
// without touching the simulation.
func (c *tickClock) next() (wait time.Duration, run bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return 5 * time.Millisecond, false
	}
	rate := c.rate
	if rate <= 0 {
		rate = DefaultTickRate
	}
	return time.Duration(float64(time.Second) / rate), true
}

func (c *tickClock) tickRate() float64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rate <= 0 {
		return DefaultTickRate
	}
	return c.rate
}

// setRate applies /tick rate, clamped to vanilla's [1, 10000].
func (c *tickClock) setRate(rate float64) {
	if math.IsNaN(rate) || rate < 1 {
		rate = 1
	}
	if rate > MaxTickRate {
		rate = MaxTickRate
	}
	c.mu.Lock()
	c.rate = rate
	c.mu.Unlock()
}

// setFrozen applies /tick freeze and /tick unfreeze.
func (c *tickClock) setFrozen(frozen bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen == frozen {
		return
	}
	if frozen {
		c.frozenAt = c.count
	} else {
		c.sprinting = false
	}
	c.frozen = frozen
}

// status is a snapshot for /tick query.
type tickStatus struct {
	Count     int64
	Rate      float64
	Frozen    bool
	Sprinting bool
	FrozenFor int64
}

func (c *tickClock) status() tickStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	rate := c.rate
	if rate <= 0 {
		rate = DefaultTickRate
	}
	st := tickStatus{
		Count:     c.count,
		Rate:      rate,
		Frozen:    c.frozen,
		Sprinting: c.sprinting,
	}
	if c.frozen {
		st.FrozenFor = c.count - c.frozenAt
	}
	return st
}

// TickCount is the number of completed game ticks.
func (s *Server) TickCount() int64 {
	s.clock.mu.Lock()
	defer s.clock.mu.Unlock()
	return s.clock.count
}

// SetTickRate applies /tick rate.
func (s *Server) SetTickRate(rate float64) { s.clock.setRate(rate) }

// SetFrozen halts (or resumes) the game tick loop, vanilla's
// /tick freeze and /tick unfreeze.
func (s *Server) SetFrozen(frozen bool) { s.clock.setFrozen(frozen) }

// StepTicks advances the simulation exactly n ticks, synchronously:
// when it returns, the world has taken those n ticks and nothing else.
// That is vanilla's /tick step, and it is how blackbox tests drive
// mc-go deterministically against a frozen vanilla server.
func (s *Server) StepTicks(n int64) {
	if n < 1 {
		n = 1
	}
	s.clock.runMu.Lock()
	defer s.clock.runMu.Unlock()
	for i := int64(0); i < n; i++ {
		s.GameTick()
	}
}

// SprintTicks runs n ticks back to back in the background, stopping
// early if StopSprint is called. Vanilla's /tick sprint returns at once
// and simulates as fast as the machine allows.
func (s *Server) SprintTicks(n int64) {
	if n < 1 {
		n = 1
	}
	s.clock.mu.Lock()
	s.clock.sprinting = true
	s.clock.mu.Unlock()
	go func() {
		defer func() {
			s.clock.mu.Lock()
			s.clock.sprinting = false
			s.clock.mu.Unlock()
		}()
		s.clock.runMu.Lock()
		defer s.clock.runMu.Unlock()
		for i := int64(0); i < n; i++ {
			s.clock.mu.Lock()
			stop := !s.clock.sprinting
			s.clock.mu.Unlock()
			if stop {
				return
			}
			s.GameTick()
		}
	}()
}

// StopSprint halts a running /tick sprint.
func (s *Server) StopSprint() {
	s.clock.mu.Lock()
	s.clock.sprinting = false
	s.clock.mu.Unlock()
}

// GameTick advances the whole simulation by exactly one tick.
//
// Everything time-driven belongs here, in a fixed order, so that
// freezing the clock is a complete freeze and so that tests can step
// the world deterministically instead of sleeping and hoping.
func (s *Server) GameTick() {
	s.itemTickStep()
	s.mobTickStep()
	s.furnaceTickStep()
	s.hungerTickStep()
	s.combatTickStep()
	s.hopperTickStep()
	s.eatTickStep()
	s.voidTickStep()
	s.portalTickStep()
	s.scheduledTickStep()
	s.randomTickStep()

	s.clock.mu.Lock()
	s.clock.count++
	s.clock.mu.Unlock()
}

// runGameLoop drives GameTick on the clock's schedule until stop
// closes. Delays are recomputed every iteration so /tick rate takes
// effect on the next tick, and a frozen clock simply never advances.
func (s *Server) runGameLoop(stop chan struct{}) {
	for {
		wait, run := s.clock.next()
		if wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-stop:
				t.Stop()
				return
			case <-t.C:
			}
		}
		select {
		case <-stop:
			return
		default:
		}
		if run {
			s.clock.runMu.Lock()
			s.GameTick()
			s.clock.runMu.Unlock()
		}
	}
}

// startGameTicks launches the game loop. Replaces the old item-only
// ticker now that every subsystem hangs off GameTick.
func (s *Server) startGameTicks() chan struct{} {
	stop := make(chan struct{})
	go s.runGameLoop(stop)
	return stop
}
