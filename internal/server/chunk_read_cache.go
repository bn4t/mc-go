package server

import (
	"sync"

	"mc-go/internal/world"
)

// chunkReadCache keeps the last few whole-chunk reads of a dimension.
// Preparing one column for a client reads the chunk for its sky tops,
// its non-empty section counts, its column facts and its snapshot; the
// shared read serves all of them while the chunk's write version holds.
type chunkReadCache struct {
	mu      sync.Mutex
	entries [8]chunkReadEntry
	next    int
}

type chunkReadEntry struct {
	cx, cz int
	ver    uint64
	blocks []world.Block
}

// chunkBlocks returns chunk (cx, cz)'s blocks in FillBox order (x
// fastest, then z, then y) over the dimension's full height. The slice
// is shared: callers must not modify it.
func (d *dimension) chunkBlocks(cx, cz int) []world.Block {
	// The version is read before the blocks: a write racing the read
	// leaves the entry tagged older than its contents, so it misses.
	ver := d.w.ChunkVersion(cx, cz)
	c := &d.chunkReads
	c.mu.Lock()
	for i := range c.entries {
		e := &c.entries[i]
		if e.blocks != nil && e.cx == cx && e.cz == cz && e.ver == ver {
			b := e.blocks
			c.mu.Unlock()
			return b
		}
	}
	c.mu.Unlock()
	buf := make([]world.Block, 256*d.height)
	d.w.FillBox(
		world.Pos{X: cx * 16, Y: d.minY, Z: cz * 16},
		world.Pos{X: cx*16 + 15, Y: d.minY + d.height - 1, Z: cz*16 + 15},
		buf,
	)
	c.mu.Lock()
	c.entries[c.next] = chunkReadEntry{cx: cx, cz: cz, ver: ver, blocks: buf}
	c.next = (c.next + 1) % len(c.entries)
	c.mu.Unlock()
	return buf
}
