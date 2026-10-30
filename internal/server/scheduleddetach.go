package server

import "sort"

// A snapshot is independent of the resident container. Serialization alone
// does not change that container's clocks; discard is a separate, fenced step.
type detachedTickSave struct {
	chunk      scheduledChunk
	generation uint64
	rows       []relativeScheduledTick
}

// Detach all requested containers in one pass over the queue. Already-collected
// callbacks stay in active/activeOrder and remain runnable and copyable.
func (t *tickScheduler) detachChunks(chunks map[scheduledChunk]bool) map[scheduledChunk]uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	ids := make(map[scheduledChunk]uint64, len(chunks))
	for chunk, selected := range chunks {
		if !selected {
			continue
		}
		state := t.detached[chunk]
		if state == nil {
			t.nextDetachID++
			state = &detachedTickContainer{generation: t.nextDetachID, members: map[schedKey]bool{}, pending: t.relative[chunk]}
			if t.detached == nil {
				t.detached = map[scheduledChunk]*detachedTickContainer{}
			}
			t.detached[chunk] = state
			delete(t.relative, chunk)
			for _, row := range state.pending {
				delete(t.relativeOwners, row.key)
			}
		}
		if !state.serialized {
			t.residentDetached[chunk] = true
		}
		delete(t.registered, chunk)
		ids[chunk] = state.generation
	}
	if len(ids) == 0 {
		return ids
	}
	for at, list := range t.due {
		kept := list[:0]
		for _, key := range list {
			if !chunks[scheduledChunkOf(key)] {
				kept = append(kept, key)
				continue
			}
			state := t.detached[scheduledChunkOf(key)]
			owner := key
			owner.recordID = 0
			if _, saved := state.members[owner]; !saved {
				state.members[owner] = t.owners[owner]
			}
			state.absolute = append(state.absolute, blockTickRecord{key: key, at: at, priority: t.prio[key]})
			delete(t.queued, key)
			delete(t.prio, key)
			delete(t.owners, owner)
			// Every record of an owner belongs to this same chunk, so the
			// complete owner index is removed in O(1), including duplicates.
			delete(t.ownerDeadlines, owner)
		}
		if len(kept) == 0 {
			delete(t.due, at)
		} else {
			t.due[at] = kept
		}
	}
	return ids
}

// Reattaching the same resident object keeps its absolute deadlines and exact
// owner membership. A discarded object instead registers its saved pending
// rows, which are still not unpacked merely by FULL registration.
func (t *tickScheduler) reattachChunk(chunk scheduledChunk) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.detached[chunk]
	if state == nil {
		return false
	}
	for _, record := range state.absolute {
		t.queued[record.key] = record.at
		t.prio[record.key] = record.priority
		t.due[record.at] = append(t.due[record.at], record.key)
		t.indexTick(record.key, record.at)
	}
	for owner, present := range state.members {
		if present {
			t.owners[owner] = true
		}
	}
	if len(state.pending) != 0 {
		t.relative[chunk] = append(t.relative[chunk], state.pending...)
		for _, row := range state.pending {
			t.relativeOwners[row.key] = true
		}
	}
	delete(t.residentDetached, chunk)
	delete(t.detached, chunk)
	t.registered[chunk] = true
	return true
}

func (t *tickScheduler) snapshotDetachedChunk(chunk scheduledChunk, now int64) (detachedTickSave, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.snapshotDetachedChunkLocked(chunk, now)
}

func (t *tickScheduler) snapshotDetachedChunkLocked(chunk scheduledChunk, now int64) (detachedTickSave, bool) {
	state := t.detached[chunk]
	if state == nil {
		return detachedTickSave{}, false
	}
	out := detachedTickSave{chunk: chunk, generation: state.generation, rows: append([]relativeScheduledTick(nil), state.pending...)}
	records := append([]blockTickRecord(nil), state.absolute...)
	// Retain the current queue model's deterministic packing order. Native
	// normal-request sub-tick ordering remains a separate existing limit.
	sort.SliceStable(records, func(i, j int) bool {
		a, b := records[i], records[j]
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
		if a.key.block != b.key.block {
			return a.key.block < b.key.block
		}
		if a.key.fluid != b.key.fluid {
			return a.key.fluid < b.key.fluid
		}
		return a.key.recordID < b.key.recordID
	})
	for _, record := range records {
		key := record.key
		key.recordID = 0
		out.rows = append(out.rows, relativeScheduledTick{key: key, delay: int32(record.at - now), priority: record.priority})
	}
	return out, true
}

// The owner calls this only after the snapshot write succeeded AND the
// resident object is still being discarded. Reticketing, including a second
// detach after reattachment, makes an earlier completion stale.
func (t *tickScheduler) discardSerializedChunk(saved detachedTickSave) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.detached[saved.chunk]
	if state == nil || state.generation != saved.generation || state.serialized {
		return false
	}
	state.pending = append([]relativeScheduledTick(nil), saved.rows...)
	state.absolute = nil
	state.members = nil
	state.serialized = true
	delete(t.residentDetached, saved.chunk)
	return true
}
