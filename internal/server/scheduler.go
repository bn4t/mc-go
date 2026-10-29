package server

import (
	"sort"
	"sync"

	"mc-go/internal/world"
)

// tickKind distinguishes what a due position should be asked to do.
// A fluid tick re-evaluates the fluid there; a block tick runs the
// block's own scheduled behavior (currently none; redstone and crops
// will land here).
type tickKind uint8

const (
	tickFluid tickKind = iota
	tickBlock
	// Command variants have distinct expected block types in vanilla's
	// queue. A mode conversion must not consume the previous mode's tick.
	tickCommandImpulse
	tickCommandRepeat
	tickChestRecheck
	tickTrappedChestRecheck
)

// schedKey identifies one scheduled block position in one dimension.
// Fluid ticks and scheduled (redstone-style) block ticks share the
// queue, as they do in vanilla's LevelTicks, because both are "run this
// position at tick N" events.
type schedKey struct {
	dim  string
	pos  world.Pos
	kind tickKind
}

// tickScheduler is the pending block-tick queue.
//
// Vanilla keeps a per-level priority queue ordered by (tick, priority,
// position). This keeps the same contract — a position is due at
// exactly one tick, and re-scheduling moves it earlier rather than
// duplicating it — and orders everything due on a tick by vanilla's
// TickPriority first (redstone diodes lean on it: a repeater that
// turns off outranks one that turns on), then (dimension, x, y, z)
// for determinism.
type tickScheduler struct {
	mu     sync.Mutex
	due    map[int64][]schedKey
	queued map[schedKey]int64
	prio   map[schedKey]int8
}

// Vanilla TickPriority values — the schedulePrio callers' range; lower
// runs earlier within a tick.
const (
	tickPrioExtremelyHigh int8 = -3
	tickPrioVeryHigh      int8 = -2
	tickPrioHigh          int8 = -1
	tickPrioNormal        int8 = 0
)

func newTickScheduler() *tickScheduler {
	return &tickScheduler{
		due:    map[int64][]schedKey{},
		queued: map[schedKey]int64{},
		prio:   map[schedKey]int8{},
	}
}

// schedule makes k due at tick `at` with NORMAL priority.
func (t *tickScheduler) schedule(k schedKey, at int64) {
	t.schedulePrio(k, at, tickPrioNormal)
}

// schedulePrio is the priority overload of Level.scheduleTick. A
// position already queued for an earlier tick keeps its earlier time
// (mc-go's keep-earliest dedup — vanilla removes and re-adds the tick
// object, which can move it later); the latest call's priority always
// wins, matching vanilla's wholesale entry replacement.
func (t *tickScheduler) schedulePrio(k schedKey, at int64, prio int8) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.queued[k]; ok && prev <= at {
		t.prio[k] = prio
		return
	}
	t.queued[k] = at
	t.prio[k] = prio
	t.due[at] = append(t.due[at], k)
}

// poll removes and returns everything due on tick `now`, ordered by
// (priority, dimension, x, y, z). Entries scheduled into the past
// (which cannot happen through schedule, but can through a restored
// save) are returned too.
func (t *tickScheduler) poll(now int64) []schedKey {
	t.mu.Lock()
	defer t.mu.Unlock()
	var keys []schedKey
	for at, list := range t.due {
		if at > now {
			continue
		}
		keys = append(keys, list...)
		delete(t.due, at)
	}
	if len(keys) == 0 {
		return nil
	}
	for _, k := range keys {
		delete(t.queued, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if a, b := t.prio[keys[i]], t.prio[keys[j]]; a != b {
			return a < b
		}
		if keys[i].dim != keys[j].dim {
			return keys[i].dim < keys[j].dim
		}
		a, b := keys[i].pos, keys[j].pos
		if a.X != b.X {
			return a.X < b.X
		}
		if a.Y != b.Y {
			return a.Y < b.Y
		}
		return a.Z < b.Z
	})
	for _, k := range keys {
		delete(t.prio, k)
	}
	return keys
}

// pending is how many positions are queued (for tests and /tick query).
func (t *tickScheduler) pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.queued)
}

// isQueued reports whether a position already has a tick pending.
func (t *tickScheduler) isQueued(k schedKey) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.queued[k]
	return ok
}

// queuedAt returns the due tick of a pending entry — the
// willTickThisTick half of LevelTickAccess.
func (t *tickScheduler) queuedAt(k schedKey) (int64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.queued[k]
	return at, ok
}

// hasScheduled is LevelTickAccess.hasScheduledTick — true while the
// same pos+kind entry is still queued.
func (t *tickScheduler) hasScheduled(k schedKey) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.queued[k]
	return ok
}
