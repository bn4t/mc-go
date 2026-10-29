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

// schedKey separates block and fluid domains in the shared Go queue. Block
// dispatch variants retain their own kind, plus the expected registered type.
type schedKey struct {
	dim  string
	pos  world.Pos
	kind tickKind
	// Registered block owner captured when queued; fluid/unbound keys leave it empty.
	block string
}

// tickScheduler is the pending block-tick queue.
//
// Pending block ticks retain their registered block type. Duplicate requests
// keep the first deadline and priority; the untyped fluid domain retains its
// existing policy. Due entries retain the existing
// priority/dimension/position ordering; native sub-tick ordering is separate.
type tickScheduler struct {
	mu     sync.Mutex
	due    map[int64][]schedKey
	queued map[schedKey]int64
	prio   map[schedKey]int8
	// Bound once by Server.New before the scheduler is exposed.
	blockType func(schedKey) string
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

func (t *tickScheduler) typedKey(k schedKey) schedKey {
	if k.kind != tickFluid && k.block == "" && t.blockType != nil {
		k.block = t.blockType(k)
	}
	return k
}

// schedule makes k due at tick `at` with NORMAL priority.
func (t *tickScheduler) schedule(k schedKey, at int64) {
	t.schedulePrio(k, at, tickPrioNormal)
}

// schedulePrio follows LevelChunkTicks.schedule's first-request rule: a
// duplicate pending block position/type does not replace timing or priority.
// Fluid keys do not yet retain their native type, so their existing policy
// remains separate from this block-tick correction.
func (t *tickScheduler) schedulePrio(k schedKey, at int64, prio int8) {
	k = t.typedKey(k)
	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.queued[k]; ok {
		if k.kind != tickFluid {
			return
		}
		if prev <= at {
			t.prio[k] = prio
			return
		}
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
	k = t.typedKey(k)
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.queued[k]
	return ok
}

// queuedAt returns the due tick of a pending entry — the
// willTickThisTick half of LevelTickAccess.
func (t *tickScheduler) queuedAt(k schedKey) (int64, bool) {
	k = t.typedKey(k)
	t.mu.Lock()
	defer t.mu.Unlock()
	at, ok := t.queued[k]
	return at, ok
}

// hasScheduled is LevelTickAccess.hasScheduledTick — true while the
// same position and current registered block type are still queued.
func (t *tickScheduler) hasScheduled(k schedKey) bool {
	k = t.typedKey(k)
	t.mu.Lock()
	defer t.mu.Unlock()
	_, ok := t.queued[k]
	return ok
}
