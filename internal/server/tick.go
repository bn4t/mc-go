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
	// base is the game time a loaded world resumes from: the count a
	// previous run wrote into level.dat. TickCount reports count+base
	// so /time query gametime and the world-age packets continue where
	// the saved world left off instead of restarting at zero.
	base int64
	// day is the world's daytime counter, the clock the client draws
	// its sky from. It advances with the game clock (/time set and
	// /time add move it) and stops when the game is frozen.
	day int64
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
//
// since is how long the tick that has just finished took, and the wait
// is what is left of the next tick's budget after it. That is the real
// server's own arithmetic, and it is not the same thing as sleeping a
// whole period after each tick: a flat sleep makes the period the tick's
// own duration plus the sleep, so a tick that takes as long as a tick
// halves the rate. Measured on 26.2 with a client asking for batches of
// 25: the real server's batches came one tick apart, and a loop that
// slept a flat period sent them two apart, because serializing the
// twenty-five columns inside the tick took about as long as the tick.
func (c *tickClock) next(since time.Duration) (wait time.Duration, run bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.frozen {
		return 5 * time.Millisecond, false
	}
	rate := c.rate
	if rate <= 0 {
		rate = DefaultTickRate
	}
	period := time.Duration(float64(time.Second) / rate)
	if wait := period - since; wait > 0 {
		return wait, true
	}
	// The tick overran its budget: the next one starts at once. The rate
	// is then the fastest the server can manage, which is what the real
	// server reports as a tick rate below twenty.
	return 0, true
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

// isFrozen reports whether the game is frozen, which /tick step needs.
func (c *tickClock) isFrozen() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.frozen
}

// isSprinting reports whether a /tick sprint is running.
func (c *tickClock) isSprinting() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sprinting
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

// gameTime is the number of completed game ticks: vanilla's game time,
// what /time query gametime reports and the world age the client is
// synced with. It stops while the game is frozen, as vanilla's does.
func (s *Server) gameTime() int64 { return s.TickCount() }

// dayTime is the world's daytime counter, in ticks since the world
// began. /time set and /time add move it, and the client's sky follows
// it.
func (s *Server) dayTime() int64 {
	s.clock.mu.Lock()
	defer s.clock.mu.Unlock()
	return s.clock.day
}

// setDayTime applies /time set and /time add.
func (s *Server) setDayTime(day int64) {
	s.clock.mu.Lock()
	s.clock.day = day
	s.clock.mu.Unlock()
	s.broadcastUpdateTime()
}

// TickCount is the number of completed game ticks: this run's ticks
// plus the base a loaded world resumed from.
func (s *Server) TickCount() int64 {
	s.clock.mu.Lock()
	defer s.clock.mu.Unlock()
	return s.clock.count + s.clock.base
}

// setGameTimeBase seeds the game time from a loaded world's level.dat.
// It runs at boot, before any tick, so nothing has to be rewound.
func (s *Server) setGameTimeBase(base int64) {
	s.clock.mu.Lock()
	s.clock.base = base
	s.clock.mu.Unlock()
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
//
// A sprint of n simulates n+1 ticks, which is the real server's own
// count and not a rounding of it: measured on 26.2, three sprints of ten
// in a row moved the game clock by exactly eleven each and a sprint of
// forty moved it by forty-one, read off /time query gametime and off the
// cook and burn counters of a furnace that had never been lit, which can
// only move on a simulated tick. The command is handled inside a tick and
// the sprint applies to the rest of that tick too, so the tick it arrived
// on is simulated along with the n it asks for. /tick step is answered
// the same way and simulates exactly n.
func (s *Server) SprintTicks(n int64) {
	if n < 1 {
		n = 1
	}
	n++
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
	s.digTickStep()
	s.mobTickStep()
	s.furnaceTickStep()
	s.containerTickStep()
	s.hungerTickStep()
	s.combatTickStep()
	s.hopperTickStep()
	s.eatTickStep()
	s.voidTickStep()
	s.portalTickStep()
	s.scheduledTickStep()
	s.randomTickStep()
	// Chunk streaming is paced on the tick, like everything else: one
	// batch per tick per player, at the rate their client asked for.
	s.chunkTickStep()

	s.clock.mu.Lock()
	s.clock.count++
	// The day clock is the one game time can pause: with
	// advance_time off (doDaylightCycle in older versions) the sun
	// stops, while the world age - the tick counter - keeps counting.
	if s.gameRuleBool("minecraft:advance_time") {
		s.clock.day++
	}
	s.clock.mu.Unlock()
}

// runGameLoop drives GameTick on the clock's schedule until stop
// closes. Delays are recomputed every iteration so /tick rate takes
// effect on the next tick, and a frozen clock simply never advances.
// Each sleep is what is left of the tick's budget once the tick itself
// has run, so a slow tick eats into its own period rather than adding to
// it (see tickClock.next).
func (s *Server) runGameLoop(stop chan struct{}) {
	var since time.Duration
	for {
		wait, run := s.clock.next(since)
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
		if !run {
			since = 0
			continue
		}
		start := time.Now()
		s.clock.runMu.Lock()
		s.GameTick()
		s.clock.runMu.Unlock()
		since = time.Since(start)
	}
}

// startGameTicks launches the game loop. Replaces the old item-only
// ticker now that every subsystem hangs off GameTick.
func (s *Server) startGameTicks() chan struct{} {
	stop := make(chan struct{})
	go s.runGameLoop(stop)
	return stop
}
