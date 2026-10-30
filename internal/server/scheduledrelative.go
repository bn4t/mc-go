package server

type scheduledChunk struct {
	dim  string
	x, z int
}

func scheduledChunkOf(key schedKey) scheduledChunk {
	return scheduledChunk{dim: key.dim, x: key.pos.X >> 4, z: key.pos.Z >> 4}
}

type relativeScheduledTick struct {
	key      schedKey
	delay    int32
	priority int8
}

// A saved record has no absolute deadline until its chunk starts ticking.
// Keep its list order and duplicates; ordinary requests still use membership.
func (t *tickScheduler) deferSavedTick(key schedKey, delay int32, priority int8) {
	key.recordID = 0
	t.mu.Lock()
	defer t.mu.Unlock()
	chunk := scheduledChunkOf(key)
	t.relative[chunk] = append(t.relative[chunk], relativeScheduledTick{key: key, delay: delay, priority: priority})
	t.relativeOwners[key] = true
}

func (t *tickScheduler) activateSavedChunk(chunk scheduledChunk, now int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, row := range t.relative[chunk] {
		delete(t.relativeOwners, row.key)
		t.restoreTickLocked(row.key, now+int64(row.delay), row.priority)
	}
	delete(t.relative, chunk)
}
