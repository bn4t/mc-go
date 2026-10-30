package server

import (
	"mc-go/internal/world"
	"sort"
)

type blockTickRecord struct {
	key      schedKey
	at       int64
	priority int8
	pending  bool
}

func blockTickInside(k schedKey, dim string, low, high world.Pos) bool {
	p := k.pos
	return k.dim == dim && k.kind != tickFluid && p.X >= low.X && p.X <= high.X && p.Y >= low.Y && p.Y <= high.Y && p.Z >= low.Z && p.Z <= high.Z
}

// Copy takes a snapshot before adding any destination entries. Active entries
// retain their original type/time/priority even after their callback ran.
func (t *tickScheduler) copyBlockTicks(src, dst string, low, high, offset world.Pos) {
	t.mu.Lock()
	var records []blockTickRecord
	for _, k := range t.activeOrder {
		if r, ok := t.active[k]; ok && blockTickInside(k, src, low, high) {
			records = append(records, r)
		}
	}
	var queued []blockTickRecord
	for k, at := range t.queued {
		if blockTickInside(k, src, low, high) {
			queued = append(queued, blockTickRecord{key: k, at: at, priority: t.prio[k]})
		}
	}
	// Preserve the existing queue model's ordering, not map iteration order.
	sort.Slice(queued, func(i, j int) bool {
		a, b := queued[i], queued[j]
		if a.at != b.at {
			return a.at < b.at
		}
		if a.priority != b.priority {
			return a.priority < b.priority
		}
		if a.key.pos.X != b.key.pos.X {
			return a.key.pos.X < b.key.pos.X
		}
		if a.key.pos.Y != b.key.pos.Y {
			return a.key.pos.Y < b.key.pos.Y
		}
		if a.key.pos.Z != b.key.pos.Z {
			return a.key.pos.Z < b.key.pos.Z
		}
		if a.key.kind != b.key.kind {
			return a.key.kind < b.key.kind
		}
		return a.key.block < b.key.block
	})
	records = append(records, queued...)
	t.mu.Unlock()
	for _, r := range records {
		k := r.key
		k.dim = dst
		k.pos = world.Pos{X: k.pos.X + offset.X, Y: k.pos.Y + offset.Y, Z: k.pos.Z + offset.Z}
		t.schedulePrio(k, r.at, r.priority)
	}
}

// A canceled collected entry must not run later from the dispatcher's local
// list. Fluids retain their independent existing dispatch behavior.
func (t *tickScheduler) claimBlockTick(k schedKey) bool {
	if k.kind == tickFluid {
		return true
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	r, ok := t.active[k]
	if !ok || !r.pending {
		return false
	}
	r.pending = false
	t.active[k] = r
	return true
}
func (t *tickScheduler) finishBlockTicks() {
	t.mu.Lock()
	t.active = nil
	t.activeOrder = nil
	t.mu.Unlock()
}

// StructureUtils clears block work only. Both queued and collected records
// are removed, while remaining callbacks retain their original order.
func (t *tickScheduler) clearBlockArea(dim string, low, high world.Pos) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.queued {
		if blockTickInside(k, dim, low, high) {
			delete(t.queued, k)
			delete(t.prio, k)
			owner := k
			owner.recordID = 0
			delete(t.owners, owner)
			delete(t.ownerDeadlines, owner)
		}
	}
	for at, list := range t.due {
		kept := list[:0]
		for _, k := range list {
			if !blockTickInside(k, dim, low, high) {
				kept = append(kept, k)
			}
		}
		if len(kept) == 0 {
			delete(t.due, at)
		} else {
			t.due[at] = kept
		}
	}
	for k := range t.active {
		if blockTickInside(k, dim, low, high) {
			delete(t.active, k)
		}
	}
}
