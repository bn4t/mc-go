package server

import (
	"math"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"mc-go/internal/event"
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
	// sprintRunning reserves the single runner through stop/finish as well as
	// active ticking. A request arriving while stop waits updates that runner;
	// it must never start a second runner waiting under gameplay ownership.
	sprintRunning bool
	// The sprint fields mirror ServerTickRateManager: the scheduled
	// count, the ticks the running sprint still owes (the +1 is the
	// tick the request arrived on), the frozen flag a sprint restores
	// when it ends, and when the current leg began — the report's
	// elapsed, reset when a new request interrupts.
	sprintSchedule   int64
	sprintRemain     int64
	sprintPrevFrozen bool
	sprintStart      time.Time
	// sprintRan is the GameTicks the last sprint actually ran — the
	// report's completed count (n+1 for an uninterrupted sprint n).
	sprintRan int64
	// stepRemain mirrors frozenTicksToRun: a /tick step request fills
	// it, the running step drains it a tick at a time, and step stop
	// zeroes it early.
	stepRemain int64

	// times mirrors MinecraftServer.tickTimesNanos: a 100-entry ring
	// of per-tick durations plus its running sum — /tick query's
	// average and percentile lines are read off it.
	times    [100]int64
	timesAgg int64
	timesIdx int64

	// runMu gives a synchronous step/sprint exclusive control of world ticks
	// while it yields gameplay ownership between complete ticks. Acquire it
	// before the simulation lock (or temporarily release ownership to wait).
	runMu sync.Mutex

	// sprintMu is held for a sprint's whole run plus its finish, so
	// stopSprinting can behave the way the real server's does — its
	// finishTickSprint is synchronous on the caller's thread, meaning
	// the report and the frozen restore land before the caller's next
	// act (a freeze, an ack).
	sprintMu sync.Mutex
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

// gameTime is the current world age: vanilla's game time,
// what /time query gametime reports and the world age the client is
// synced with. It stops while the game is frozen, as vanilla's does.
func (s *Server) gameTime() int64 { return s.TickCount() }

// dataSyncReadyTick returns the game tick a freshly queued dirty pack
// may flush from. Vanilla's sendChanges runs at the end of the tick
// that dispatched the mutation, so a write from inside the tick loop
// can go out on this tick's boundary; a command-path write must wait
// for the next one so the command's own reply reaches the client
// first — otherwise a boundary landing mid-command interleaves the
// pack with the chat (the multi-kill ordering).
func (s *Server) dataSyncReadyTick() int64 {
	if s.inGameTick.Load() {
		return s.gameTime()
	}
	return s.gameTime() + 1
}

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
	s.broadcastTimeSet(day)
}

// TickCount is the current world age: this run's ticks plus the base a
// loaded world resumed from. It advances before a game tick's simulation.
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
func (s *Server) SetTickRate(rate float64) {
	s.clock.setRate(rate)
	// Vanilla's setTickRate refreshes the clients on every call.
	s.broadcastTickingState()
}

// SetFrozen halts (or resumes) the game tick loop, vanilla's
// /tick freeze and /tick unfreeze. The client update goes out on
// every call — a second /tick freeze still emits ticking_state.
func (s *Server) SetFrozen(frozen bool) {
	s.clock.setFrozen(frozen)
	s.broadcastTickingState()
}

// StepTicks advances the simulation exactly n ticks, synchronously:
// when it returns, the world has taken those n ticks and nothing else.
// That is vanilla's /tick step, and it is how blackbox tests drive
// mc-go deterministically against a frozen vanilla server. The owed
// count lives in stepRemain, vanilla's frozenTicksToRun: the /tick
// step path fills it, a mid-step "step stop" zeroes it early, and a
// caller that never filled it gets a fresh n.
func (s *Server) StepTicks(n int64) {
	s.simulationAction(func() { s.stepTicksOwned(n) })
}

func (s *Server) stepTicksOwned(n int64) {
	s.lockTickRunOwned()
	defer s.clock.runMu.Unlock()
	if n < 1 {
		n = 1
	}
	s.clock.mu.Lock()
	if s.clock.stepRemain == 0 {
		s.clock.stepRemain = n
	}
	s.clock.mu.Unlock()
	for i := int64(0); i < n; i++ {
		s.clock.mu.Lock()
		if s.clock.stepRemain <= 0 || s.shuttingDown.Load() {
			s.clock.mu.Unlock()
			return
		}
		s.clock.stepRemain--
		s.clock.mu.Unlock()
		start := time.Now()
		s.gameTickOwned()
		s.recordTick(time.Since(start))
		s.yieldSimulationOwned()
	}
}

// lockTickRunOwned releases gameplay ownership while waiting for another tick
// driver. That driver must be able to regain ownership to complete its tick.
func (s *Server) lockTickRunOwned() {
	s.flushWorldUpdatesOwned()
	s.flushPlayerUpdatesOwned()
	s.simulation.mu.Unlock()
	s.clock.runMu.Lock()
	s.simulation.mu.Lock()
}

// yieldSimulationOwned admits commands only at complete tick boundaries. The
// driving leg retains runMu so the paced loop cannot insert additional ticks.
func (s *Server) yieldSimulationOwned() {
	s.drainSimulationActionsOwned()
	s.flushWorldUpdatesOwned()
	s.flushPlayerUpdatesOwned()
	s.simulation.mu.Unlock()
	runtime.Gosched()
	s.simulation.mu.Lock()
}

// stopStep mirrors stopStepping: zero the owed ticks and refresh the
// clients' ticking-step counter. False when no step is in progress.
func (s *Server) stopStep() bool {
	s.clock.mu.Lock()
	if s.clock.stepRemain <= 0 {
		s.clock.mu.Unlock()
		return false
	}
	s.clock.stepRemain = 0
	s.clock.mu.Unlock()
	s.broadcastTickingStep(0)
	return true
}

// requestSprint applies requestGameToSprint: the sprint owes the n it
// was asked for plus the tick the request arrived on, the clock unfreezes
// for the duration (a client-visible change), and an already-running
// sprint keeps the caller that owns it — a second request only resets
// what it owes and reports the interruption. True when it interrupted.
func (s *Server) requestSprint(n int64) (interrupted bool) {
	if n < 1 {
		n = 1
	}
	s.clock.mu.Lock()
	interrupted = s.clock.sprinting || s.clock.sprintRunning
	s.clock.sprintRunning = true
	s.clock.sprintSchedule = n
	s.clock.sprintRemain = n + 1
	s.clock.sprintPrevFrozen = s.clock.frozen
	s.clock.sprintStart = time.Now()
	s.clock.frozen = false
	s.clock.sprinting = true
	s.clock.mu.Unlock()
	// requestGameToSprint's setFrozen(false) refreshes the clients.
	s.broadcastTickingState()
	return interrupted
}

// SprintTicks is requestSprint plus the run, for callers that want a
// one-shot sprint with no interruption handling of their own.
func (s *Server) SprintTicks(n int64) {
	s.simulationAction(func() {
		if !s.requestSprint(n) {
			s.sprintRunOwned()
		}
	})
}

// sprintRun simulates the sprint the caller owns. Vanilla's sprint runs
// on the server thread, so everything queued behind the command waits
// it out; the deferred finish mirrors finishTickSprint.
//
// A sprint of n simulates n+1 ticks, which is the real server's own
// count and not a rounding of it: measured on 26.2, three sprints of ten
// in a row moved the game clock by exactly eleven each and a sprint of
// forty moved it by forty-one, read off /time query gametime and off the
// cook and burn counters of a furnace that had never been lit, which can
// only move on a simulated tick. The command is handled inside a tick and
// the sprint applies to the rest of that tick too, so the tick it arrived
// on is simulated along with the n it asks for.
func (s *Server) sprintRun() {
	s.simulationAction(s.sprintRunOwned)
}

// sprintRunOwned yields ownership between complete ticks, allowing a queued
// stop/freeze or other player action to interleave as on the server thread.
func (s *Server) sprintRunOwned() {
	s.clock.sprintMu.Lock()
	defer s.clock.sprintMu.Unlock()
	defer s.finishSprint()
	s.clock.mu.Lock()
	s.clock.sprintRan = 0
	s.clock.mu.Unlock()
	if !s.lockSprintTickRunOwned() {
		return
	}
	defer s.clock.runMu.Unlock()
	var ran int64
	for {
		s.clock.mu.Lock()
		done := s.clock.sprintRemain <= 0 || !s.clock.sprinting || s.shuttingDown.Load()
		if !done {
			s.clock.sprintRemain--
		}
		s.clock.mu.Unlock()
		if done {
			break
		}
		start := time.Now()
		s.gameTickOwned()
		s.recordTick(time.Since(start))
		ran++
		s.yieldSimulationOwned()
	}
	s.clock.mu.Lock()
	s.clock.sprintRan = ran
	s.clock.mu.Unlock()
}

// lockSprintTickRunOwned also permits stop/freeze while a step owns runMu.
// Lock order is driver -> gameplay owner; no blocking driver wait holds the
// owner. The runner retains sprintMu so stop waits for its report, but a stopped
// pending runner cancels this wait instead of waiting for the step to complete.
func (s *Server) lockSprintTickRunOwned() bool {
	for !s.clock.runMu.TryLock() {
		if !s.clock.isSprinting() || s.shuttingDown.Load() {
			return false
		}
		s.flushWorldUpdatesOwned()
		s.flushPlayerUpdatesOwned()
		s.simulation.mu.Unlock()
		time.Sleep(time.Millisecond)
		s.simulation.mu.Lock()
	}
	return true
}

// finishSprint mirrors finishTickSprint: the report (ticks per second
// and ms per tick, the %.2f format) goes to every op through the
// server's own command source — broadcastToOps, so the wire form is
// the gray italic admin line — then the frozen flag saved at request
// time goes back on, itself a client-visible setFrozen.
func (s *Server) finishSprint() {
	s.clock.mu.Lock()
	s.clock.sprinting = false
	s.clock.sprintRunning = false
	completed := s.clock.sprintRan
	s.clock.sprintRemain = 0
	start := s.clock.sprintStart
	s.clock.frozen = s.clock.sprintPrevFrozen
	s.clock.mu.Unlock()
	ms := float64(time.Since(start).Nanoseconds()) / 1e6
	if ms < 1 {
		ms = 1
	}
	tps := int32(float64(completed) * 1000 / ms)
	mspt := ms / math.Max(1, float64(completed))
	s.sendOpsEcho(nil, translatable("commands.tick.sprint.report",
		tps, strconv.FormatFloat(mspt, 'f', 2, 64)))
	s.broadcastTickingState()
}

// StopSprint halts a running /tick sprint — vanilla's stopSprinting:
// false when no sprint is in progress, otherwise the running leg ends
// and its finish (report + frozen restore) still happens, before this
// returns.
func (s *Server) StopSprint() bool {
	s.clock.mu.Lock()
	running := s.clock.sprintRemain > 0
	if running {
		s.clock.sprinting = false
	}
	s.clock.mu.Unlock()
	// The sprint's own goroutine does the finish — an interrupting stop
	// still lands the report ahead of the caller's next act, which is
	// why vanilla runs it inside stopSprinting. Waiting on the leg's
	// lock reproduces that ordering; a leg at its finish already drains
	// instantly.
	s.clock.sprintMu.Lock()
	s.clock.sprintMu.Unlock()
	return running
}

// stopSprintOwned must let the running sprint acquire ownership to finish.
// Its caller reacquires ownership before it continues the command.
func (s *Server) stopSprintOwned() bool {
	s.flushWorldUpdatesOwned()
	s.flushPlayerUpdatesOwned()
	s.simulation.mu.Unlock()
	defer s.simulation.mu.Lock()
	return s.StopSprint()
}

// GameTick advances the world simulation by exactly one tick.
//
// Everything world-driven belongs here, in a fixed order, so that
// freezing the clock is a complete freeze and so that tests can step
// the world deterministically instead of sleeping and hoping. Per-player
// connection-tick work is deliberately not here: see playerTick.
func (s *Server) GameTick() {
	s.clock.runMu.Lock()
	defer s.clock.runMu.Unlock()
	s.simulationAction(s.gameTickOwned)
}

func (s *Server) gameTickOwned() {
	if s.shuttingDown.Load() {
		return
	}
	s.inGameTick.Store(true)
	defer s.inGameTick.Store(false)
	// File load/tick functions run before the level's time pass. Their
	// one-tick deadlines become due in the same ensuing world tick.
	s.functionTagsStep()
	// ServerLevel.tickTime advances world age before block/fluid deadlines
	// and the rest of the simulation. Callbacks that schedule another tick
	// must read this same current age when computing their new deadlines.
	s.clock.mu.Lock()
	s.clock.count++
	if s.gameRuleBool("minecraft:advance_time") {
		s.clock.day++
	}
	s.clock.mu.Unlock()
	s.functionScheduleStep()
	s.compensateFrozenPlayerEffectStep()
	// LivingEntity.baseTick's air pass plus the inside-block fluid
	// cells run on the entity game tick: a sprint or step advances
	// them, and players are exempt from /tick freeze so the loop
	// still calls this step on frozen wall ticks.
	s.playerFluidTickStep()
	s.itemTickStep()
	s.cloudTickStep()
	s.tntTickStep()
	s.mobItemPickupStep()
	// ServerChunkCache.tickChunks runs the natural spawner before the
	// level's entity pass, so a fresh mob ticks the tick it spawns.
	s.chunkGenSpawnStep()
	s.naturalSpawnTickStep()
	// The crystal trackers flush, then EnderDragonFight.tick runs before
	// the End's entities tick.
	s.crystalSyncStep()
	s.dragonFightTickStep()
	s.mobTickStep()
	s.dragonTickStep()
	s.crystalTickStep()
	// Entity.baseTick's handlePortal opens with
	// processPortalCooldown — every ticking entity's cooldown
	// decrements one per tick while positive, no matter its type.
	s.portalCooldownStep()
	s.vehicleTickStep()
	s.minecartTickStep()
	s.cureTickStep()
	s.windChargeTickStep()
	s.crafterTickStep()
	s.allayTickStep()
	s.jukeboxTickStep()
	s.arrowTickStep()
	s.ephemeralTickStep()
	s.fishTickStep()
	s.sleepTickStep()
	s.furnaceTickStep()
	s.containerTickStep()
	s.hopperTickStep()
	s.spawnerTickStep()
	s.scheduledTickStep()
	s.postProcessStep()
	// VibrationSystem$Ticker.tick: sculk sensors select and travel
	// their queued vibrations once per level tick.
	s.vibrationTickStep()
	s.blockEventStep()
	s.movingPistonStep()
	s.entityInsideStep()
	s.knotTickStep()
	s.randomTickStep()
	s.weatherTickStep()
	s.beaconTickStep()
	s.mobEffectTickStep()
	// Raids tick once per level tick after the living-entity pass —
	// cooldown, wave spawns, the event bar, victory.
	s.raidTickStep()
	// CustomSpawners tick with the level — the natural patrol cycle is
	// the first one implemented.
	s.patrolTickStep()
	// Entity pairing re-evaluates once entities have settled into
	// their tick's positions — vanilla's updateEntitySections runs in
	// ChunkMap.tick after the level tick.
	s.entityTrackStep()
	// ServerWaypointManager: receivers' connection tables tick after
	// entities moved — broken conns remake or untrack, new transmitters
	// connect, moved contents emit UPDATE.
	s.waypointTickStep()
	// Vanilla flushes each chunk's accumulated block changes at the end
	// of the tick (ChunkHolder.broadcastChanges) — after entities and
	// block ticks ran, so the explode packet precedes the flips' section
	// update on the wire. It runs before chunk streaming: during the
	// join burst the batch encode can stretch a tick past 50ms, and a
	// queued piggyback (a sign's block_entity_data) would otherwise
	// land several wall ticks late.
	// Vanilla's ChunkHolder.broadcastChanges sends the light filter
	// first, then the block updates — flush the tick's accumulated
	// light marks just before the block changes.
	s.lightChangesFlushStep()
	s.blockChangesFlushStep()
	// Chunk streaming is paced on the tick, like everything else: one
	// batch per tick per player, at the rate their client asked for.
	s.chunkTickStep()
	// GameTestTicker advances once per world tick, after level work.
	s.testInstanceTickStep()

	if s.bus != nil {
		s.bus.Publish(event.TopicTick, s.TickCount())
	}
}

// playerTick is the per-player connection tick, driven from the packet
// listener once per wall-clock tick: it is NOT part of the freeze-gated
// game tick, so it keeps running under /tick freeze and /tick step
// never advances it. Measured on 26.2: a hurt player healed nothing
// across hundreds of stepped ticks. Void damage is not here: vanilla's
// onBelowWorld is entity baseTick business, so it lives in
// playerFluidTickStep alongside the rest of the entity-tick pass.
func (s *Server) playerTick() {
	s.simulationAction(s.playerTickOwned)
}

// gameplayTickReady excludes configuring, unadmitted and retired connections.
// Keepalive and other wall-clock transport work have their own lifecycle.
func (sess *Session) gameplayTickReady() bool {
	return sess.state.Load() == StatePlay && sess.active.Load()
}

func (s *Server) playerTickOwned() {
	if s.shuttingDown.Load() {
		return
	}
	s.frozenPlayerEffectTick()
	// Chunk-generation spawns land as chunks generate, frozen or not.
	s.chunkGenSpawnStep()
	s.digTickStep()
	s.hungerTickStep()
	s.combatTickStep()
	s.eatTickStep()
	s.portalTickStep()
	s.sessions.Range(func(_, v any) bool {
		sess := v.(*Session)
		if !sess.gameplayTickReady() {
			return true
		}
		if sess.attackTicker < 200 {
			sess.attackTicker++
		}
		if sess.locTicks++; sess.locTicks%20 == 0 && sess.spawned.Load() {
			s.fireLocation(sess)
		}
		s.flushMenuCommandUpdates(sess)
		s.validateVehicleCargo(sess)
		s.validateChestMenu(sess)
		s.fireTickAdvancements(sess)
		s.playerItemTouchTick(sess)
		s.validateChestMenu(sess)
		s.bundleUseTick(sess)
		s.enderEyeUseTick(sess)
		if sess.usingItemID != 0 {
			s.fireUsingItem(sess)
			s.spearJabTick(sess)
		}
		if sess.levitationStart != nil {
			s.fireLevitation(sess)
		}
		// Vanilla ticks the time-of-play family every live player tick.
		if !sess.dead && sess.spawned.Load() {
			sess.addCustomStat("play_time", 1)
			sess.addCustomStat("total_world_time", 1)
			sess.addCustomStat("time_since_death", 1)
			sess.addCustomStat("time_since_rest", 1)
			if sess.sneaking {
				sess.addCustomStat("sneak_time", 1)
			}
		}
		// One Set Health per tick, after every mutator ran, carrying the
		// end-of-tick health/food/saturation.
		if sess.healthDirty.Swap(false) {
			_ = s.sendHealth(sess)
		}
		// The inventory listener normally fires from addStack the moment
		// a slot mutates; slot writers that skip addStack still trip the
		// criteria scan here on the tick boundary.
		sess.invMu.RLock()
		inv := sess.inv
		sess.invMu.RUnlock()
		if !sess.invSnapOK || inv != sess.invSnap {
			sess.invSnap = inv
			sess.invSnapOK = true
			s.fireInventoryChanged(sess)
		}
		s.fireEnterBlock(sess)
		s.rideTickStep(sess)
		s.fallFlyingTick(sess)
		s.poseFlush(sess)
		if sess.spawned.Load() && !sess.dead {
			s.updatePlayerPose(sess)
		}
		s.flushAdvancements(sess)
		// LivingEntity.detectEquipmentUpdates runs every living tick —
		// inventory mutations emit one delta carrying only the slots
		// that failed ItemStack.matches.
		if sess.spawned.Load() {
			s.detectEquipmentUpdates(sess)
		}
		return true
	})
	s.playerCollisionScaleTick()
}

// runGameLoop drives GameTick on the clock's schedule until stop
// closes. Delays are recomputed every iteration so /tick rate takes
// effect on the next tick, and a frozen clock simply never advances.
// Each sleep is what is left of the tick's budget once the tick itself
// has run, so a slow tick eats into its own period rather than adding to
// it (see tickClock.next). The player tick rides the same cadence but is
// gated only by elapsed wall time, so it also runs while frozen.
func (s *Server) runGameLoop(stop chan struct{}) {
	s.ticking.Store(true)
	defer s.ticking.Store(false)
	var since time.Duration
	lastPlayerTick := time.Now()
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
		period := time.Duration(float64(time.Second) / s.clock.tickRate())
		if time.Since(lastPlayerTick) >= period {
			lastPlayerTick = time.Now()
			s.simulationAction(func() {
				s.playerTickOwned()
				// debug_sample rides the wall tick (vanilla's
				// ServerDebugSubscribers.tick), not the game tick: subscribers
				// keep seeing it while the world is frozen.
				s.debugSampleTick(since)
			})
		}
		// A sprint owns every simulated tick while it runs — vanilla's
		// server loop is the sprint — so the paced loop idles beside it
		// rather than adding ticks of its own. The run flag next()
		// handed out is a snapshot: a sprint that ends while this
		// iteration slept restores the freeze behind it, so re-read
		// frozen now — a stale run=true must not fire one extra tick
		// per sprint boundary.
		if !run || s.clock.isSprinting() || s.clock.isFrozen() {
			// /tick freeze skips the level's entity and block ticks
			// but not the players' — tickRateManager only freezes
			// non-player entities — so their fluid pass still runs
			// once per wall tick while the world is frozen.
			if s.clock.isFrozen() && !s.clock.isSprinting() {
				s.simulationAction(func() {
					if !s.shuttingDown.Load() && s.clock.isFrozen() && !s.clock.isSprinting() {
						s.playerFluidTickStep()
						// ServerLevel.tick runs chunkSource.tick whether or
						// not the tick rate manager runs normally: the
						// chunks' accumulated light and block changes still
						// broadcast every wall tick of a freeze.
						s.lightChangesFlushStep()
						s.blockChangesFlushStep()
					}
				})
			}
			since = 0
			continue
		}
		start := time.Now()
		s.clock.runMu.Lock()
		s.simulationAction(func() {
			// Recheck after acquiring ownership: a command may have frozen
			// or begun a sprint while this wall tick was waiting.
			if !s.clock.isFrozen() && !s.clock.isSprinting() {
				s.gameTickOwned()
			}
		})
		s.clock.runMu.Unlock()
		since = time.Since(start)
		s.recordTick(since)
	}
}

// recordTick adds one tick's duration to the ring — vanilla writes it
// at the end of every simulated tick, sprint and step runs included.
func (s *Server) recordTick(d time.Duration) {
	s.clock.mu.Lock()
	i := int(s.clock.timesIdx % 100)
	s.clock.timesAgg -= s.clock.times[i]
	s.clock.times[i] = d.Nanoseconds()
	s.clock.timesAgg += d.Nanoseconds()
	s.clock.timesIdx++
	s.clock.mu.Unlock()
}

// avgTickNanos mirrors getAverageTickTimeNanos: the ring's sum over
// min(100, ticks recorded so far).
func (c *tickClock) avgTickNanos() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := c.timesIdx
	if n > 100 {
		n = 100
	}
	if n < 1 {
		n = 1
	}
	return c.timesAgg / n
}

// tickPercentiles mirrors /tick query's percentile report: a sorted
// copy of the ring read at len/2, len*0.95 and len*0.99, with the
// ring's size as the sample. Unwritten slots read as zero, exactly
// like a server younger than a hundred ticks.
func (c *tickClock) tickPercentiles() (p50, p95, p99 string, sample int) {
	c.mu.Lock()
	sorted := c.times
	c.mu.Unlock()
	sort.Slice(sorted[:], func(i, j int) bool { return sorted[i] < sorted[j] })
	return nanosToMillis(sorted[50]), nanosToMillis(sorted[95]), nanosToMillis(sorted[99]), 100
}

// nanosToMillis formats a duration the way the real server does in
// /tick query replies: nanos through float32, then "%.1f".
func nanosToMillis(n int64) string {
	return strconv.FormatFloat(float64(float32(n)/float32(1e6)), 'f', 1, 32)
}

// startGameTicks launches the game loop. Replaces the old item-only
// ticker now that every subsystem hangs off GameTick.
func (s *Server) startGameTicks() chan struct{} {
	stop := make(chan struct{})
	go s.runGameLoop(stop)
	return stop
}

// startRuntimeTicks returns a joined, idempotent stop for the shared runtime.
func (s *Server) startRuntimeTicks() func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.runGameLoop(stop)
	}()
	var once sync.Once
	return func() {
		once.Do(func() { close(stop) })
		<-done
	}
}
