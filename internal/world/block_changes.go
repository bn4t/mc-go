package world

import (
	"fmt"
	"sync"
	"sync/atomic"
)

// BlockChange is a detached canonical block value at an owned commit boundary.
type BlockChange struct {
	Position Pos
	State    Block
}

type blockInterest struct{ min, max Pos }
type interestedCell struct {
	refs  uint32
	dirty bool
}
type blockChangeJournal struct {
	enabled   atomic.Bool
	mu        sync.Mutex
	next      uint64
	interests map[uint64]blockInterest
	cells     map[Pos]*interestedCell
	dirty     []Pos
}

// AddBlockInterest registers inclusive bounds after the host has reserved its
// watch and cell quotas. Each watch is limited to 65,536 cells. Shared cells are
// indexed once, so writes take one lookup and pending storage cannot exceed the
// union of the host's reserved finite watches. Snapshot capture and registration
// require the same simulation owner that serializes gameplay writes.
func (w *World) AddBlockInterest(min, max Pos) (uint64, error) {
	if max.X < min.X || max.Y < min.Y || max.Z < min.Z {
		return 0, fmt.Errorf("world: inverted block interest")
	}
	dx, dy, dz := int64(max.X)-int64(min.X)+1, int64(max.Y)-int64(min.Y)+1, int64(max.Z)-int64(min.Z)+1
	if dx <= 0 || dy <= 0 || dz <= 0 || dx > 65536 || dy > 65536 || dz > 65536 || dx*dy > 65536 || dx*dy*dz > 65536 {
		return 0, fmt.Errorf("world: block interest must contain 1..65536 cells")
	}
	j := &w.changes
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.interests == nil {
		j.interests = make(map[uint64]blockInterest)
		j.cells = make(map[Pos]*interestedCell)
	}
	j.next++
	if j.next == 0 {
		panic("world: exhausted block interest identities")
	}
	id := j.next
	j.interests[id] = blockInterest{min, max}
	forEachInterestCell(min, max, func(pos Pos) {
		cell := j.cells[pos]
		if cell == nil {
			cell = &interestedCell{}
			j.cells[pos] = cell
		}
		cell.refs++
	})
	j.enabled.Store(true)
	return id, nil
}

func forEachInterestCell(min, max Pos, visit func(Pos)) {
	// Iterate offsets to avoid overflowing after an inclusive maximum.
	for z := 0; z <= max.Z-min.Z; z++ {
		for y := 0; y <= max.Y-min.Y; y++ {
			for x := 0; x <= max.X-min.X; x++ {
				visit(Pos{min.X + x, min.Y + y, min.Z + z})
			}
		}
	}
}

func (w *World) RemoveBlockInterest(id uint64) {
	j := &w.changes
	j.mu.Lock()
	defer j.mu.Unlock()
	interest, ok := j.interests[id]
	if !ok {
		return
	}
	delete(j.interests, id)
	forEachInterestCell(interest.min, interest.max, func(pos Pos) {
		cell := j.cells[pos]
		cell.refs--
		if cell.refs == 0 {
			delete(j.cells, pos)
		}
	})
	if len(j.interests) == 0 {
		j.enabled.Store(false)
		j.interests, j.cells, j.dirty = nil, nil, nil
		return
	}
	kept := j.dirty[:0]
	for _, pos := range j.dirty {
		if j.cells[pos] != nil {
			kept = append(kept, pos)
		}
	}
	j.dirty = kept
}

// invalidateBlock runs on the caller after the shard accepts the mutation. It
// does not assume that acceptance means the worker has applied the write. The
// final batched read in DrainBlockChanges supplies that per-shard barrier.
func (w *World) invalidateBlock(pos Pos) {
	j := &w.changes
	if !j.enabled.Load() {
		return
	}
	j.mu.Lock()
	if cell := j.cells[pos]; cell != nil && !cell.dirty {
		cell.dirty = true
		j.dirty = append(j.dirty, pos)
	}
	j.mu.Unlock()
}

// DrainBlockChanges coalesces every interested position to its final stored
// value. Call under the gameplay owner after an action or complete tick. Raw
// concurrent Restore/SetBlock calls outside that owner are not transactions.
func (w *World) DrainBlockChanges() []BlockChange {
	j := &w.changes
	if !j.enabled.Load() {
		return nil
	}
	j.mu.Lock()
	positions := j.dirty
	j.dirty = nil
	for _, pos := range positions {
		j.cells[pos].dirty = false
	}
	j.mu.Unlock()
	if len(positions) == 0 {
		return nil
	}
	blocks := make([]Block, len(positions))
	w.ReadBlocks(positions, blocks)
	changes := make([]BlockChange, len(positions))
	for i, pos := range positions {
		changes[i] = BlockChange{Position: pos, State: blocks[i]}
	}
	return changes
}

// ReadBlocks batches sparse reads by storage shard. Besides avoiding one worker
// round trip per cell, each request follows all writes already accepted by that
// shard, making it the commit barrier used by the mutation journal.
func (w *World) ReadBlocks(positions []Pos, out []Block) {
	if len(out) < len(positions) {
		panic("world: short ReadBlocks destination")
	}
	byShard := make(map[*shard][]int)
	for i, pos := range positions {
		sh := w.shardFor(pos.X, pos.Z)
		byShard[sh] = append(byShard[sh], i)
	}
	var wg sync.WaitGroup
	for sh, indices := range byShard {
		wg.Add(1)
		go func(sh *shard, indices []int) {
			defer wg.Done()
			requested := make([]Pos, len(indices))
			for i, index := range indices {
				requested[i] = positions[index]
			}
			response := make(chan []Block, 1)
			select {
			case sh.ops <- shardOp{positions: requested, blocksResp: response}:
				blocks := <-response
				for i, index := range indices {
					out[index] = blocks[i]
				}
			case <-sh.done:
				for _, index := range indices {
					out[index] = sh.get(positions[index])
				}
			}
		}(sh, indices)
	}
	wg.Wait()
}
