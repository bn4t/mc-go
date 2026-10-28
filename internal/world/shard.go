package world

import (
	"sync"
	"sync/atomic"
)

// shard owns block overrides for its columns behind one RWMutex: reads
// of a shard proceed in parallel and never wait on another shard, and a
// write is visible to every read that starts after it returns. Reads are
// plain map lookups — no allocation and no goroutine handoff — because
// hot paths (random ticks, chunk encoding, collision) issue millions per
// tick. The generator function is pure, so it runs outside the lock.
type shard struct {
	mu   sync.RWMutex
	data map[Pos]Block
	gen  func(Pos) Block
	// tops caches each column's highest motion-blocking Y — the live
	// MOTION_BLOCKING heightmap, populated on first query and dropped
	// when a write lands at or above it. Guarded by mu like data; a
	// cache fill takes the write lock.
	tops map[[2]int]int
	// colVer counts writes per column, so a caller's derived per-column
	// fact can tell it is stale without hearing about each write.
	colVer   map[[2]int]uint32
	done     chan struct{}
	stopOnce sync.Once
	tick     atomic.Int64
}

func newShard(gen func(Pos) Block) *shard {
	return &shard{
		data: make(map[Pos]Block),
		gen:  gen,
		done: make(chan struct{}),
	}
}

// start is kept for the World lifecycle; a lock-guarded shard has no
// worker to launch.
func (s *shard) start() {}

// stop marks the shard stopped exactly once. Its data stays readable and
// writable: a stopped world still answers saves and late readers.
func (s *shard) stop() {
	s.stopOnce.Do(func() { close(s.done) })
}

func (s *shard) get(p Pos) Block {
	s.mu.RLock()
	b, ok := s.data[p]
	s.mu.RUnlock()
	if !ok {
		return s.gen(p)
	}
	return b
}

// readPositions answers a batch of point reads under one read lock.
func (s *shard) readPositions(positions []Pos, out []Block) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i, pos := range positions {
		b, ok := s.data[pos]
		if !ok {
			b = s.gen(pos)
		}
		out[i] = b
	}
}

// readColumns answers whole columns of a box: colDY blocks per column,
// columns in the order asked.
func (s *shard) readColumns(cols []colRef, boxX, boxY, boxZ, colDY int, out []Block) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i, c := range cols {
		x, z := boxX+c.x, boxZ+c.z
		for y := 0; y < colDY; y++ {
			p := Pos{x, boxY + y, z}
			b, ok := s.data[p]
			if !ok {
				b = s.gen(p)
			}
			out[i*colDY+y] = b
		}
	}
}

func (s *shard) has(p Pos) bool {
	s.mu.RLock()
	_, ok := s.data[p]
	s.mu.RUnlock()
	return ok
}

func (s *shard) set(p Pos, b Block) {
	s.mu.Lock()
	s.invalidateTop(p, b, false)
	s.bumpColumn(p)
	s.data[p] = b
	s.mu.Unlock()
}

// bumpColumn advances the write version of p's column. Caller holds
// the write lock.
func (s *shard) bumpColumn(p Pos) {
	if s.colVer == nil {
		s.colVer = make(map[[2]int]uint32)
	}
	s.colVer[[2]int{p.X, p.Z}]++
}

// columnVersion reports how many writes column (x, z) has seen.
func (s *shard) columnVersion(x, z int) uint32 {
	s.mu.RLock()
	v := s.colVer[[2]int{x, z}]
	s.mu.RUnlock()
	return v
}

// snapshot returns a copy of the override map.
func (s *shard) snapshot() map[Pos]Block {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := make(map[Pos]Block, len(s.data))
	for p, b := range s.data {
		cp[p] = b
	}
	return cp
}

func (s *shard) delete(p Pos) {
	s.mu.Lock()
	s.invalidateTop(p, Air, true)
	s.bumpColumn(p)
	delete(s.data, p)
	s.mu.Unlock()
}

// invalidateTop keeps the cached column top coherent with a write at p:
// above the surface a write can only raise the top, and only if it
// blocks; unblocking or deleting the surface cell drops the entry.
// Caller holds the write lock.
func (s *shard) invalidateTop(p Pos, val Block, del bool) {
	if s.tops == nil {
		return
	}
	key := [2]int{p.X, p.Z}
	t, ok := s.tops[key]
	if !ok {
		return
	}
	switch {
	case del && p.Y >= t:
		delete(s.tops, key)
	case !del && p.Y > t:
		if motionBlockingBlock(val) {
			s.tops[key] = p.Y
		}
	case !del && p.Y == t && !motionBlockingBlock(val):
		delete(s.tops, key)
	}
}

// topAt scans the column top-down once and caches the highest
// motion-blocking cell's Y; later writes at or above it invalidate.
// minY-1 reports a column with nothing blocking at all.
func (s *shard) topAt(x, z, minY, maxY int) int {
	key := [2]int{x, z}
	s.mu.RLock()
	t, ok := s.tops[key]
	s.mu.RUnlock()
	if ok {
		return t
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.tops[key]; ok {
		return t
	}
	if s.tops == nil {
		s.tops = make(map[[2]int]int)
	}
	for y := maxY; y >= minY; y-- {
		p := Pos{x, y, z}
		b, ok := s.data[p]
		if !ok {
			b = s.gen(p)
		}
		if motionBlockingBlock(b) {
			s.tops[key] = y
			return y
		}
	}
	s.tops[key] = minY - 1
	return minY - 1
}

// motionBlockingBlock is the MOTION_BLOCKING heightmap predicate: a
// collision shape or a fluid. Full snow collides; thinner layers do
// not. Waterlogged non-solids carry a fluid either way.
func motionBlockingBlock(b Block) bool {
	if b == Air {
		return false
	}
	name, ok := StateOwner(b)
	if !ok {
		return false
	}
	if !NonSolid(b) {
		return true
	}
	if isFluid(name) {
		return true
	}
	if name == "snow" {
		if l, _ := StateProperty(b, "layers"); l == "8" {
			return true
		}
	}
	if wl, _ := StateProperty(b, "waterlogged"); wl == "true" {
		return true
	}
	return false
}

func (s *shard) onTick(n int64) { s.tick.Store(n) }
