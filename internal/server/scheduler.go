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
// duplicating it — but runs everything due on a tick in a
// deterministic order (dimension, then x, y, z). Determinism matters
// more than matching vanilla's internal priority order: it is what
// makes a stepped blackbox run reproducible.
type tickScheduler struct {
	mu     sync.Mutex
	due    map[int64][]schedKey
	queued map[schedKey]int64
}

func newTickScheduler() *tickScheduler {
	return &tickScheduler{
		due:    map[int64][]schedKey{},
		queued: map[schedKey]int64{},
	}
}

// schedule makes k due at tick `at`. A position already queued for an
// earlier tick keeps its earlier time; one queued for a later tick (or
// never) is moved to `at`. Vanilla behaves the same way, so a fluid
// that is re-disturbed does not postpone its own tick.
func (t *tickScheduler) schedule(k schedKey, at int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if prev, ok := t.queued[k]; ok && prev <= at {
		return
	}
	t.queued[k] = at
	t.due[at] = append(t.due[at], k)
}

// poll removes and returns everything due on tick `now`, in a stable
// order. Entries scheduled into the past (which cannot happen through
// schedule, but can through a restored save) are returned too.
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
