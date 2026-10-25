package world

import (
	"sync"
	"sync/atomic"
)

// shard owns block overrides for its columns. Exactly one worker
// goroutine touches data, so no mutex is needed: callers rendezvous
// through the ops channel. Single-owner-per-shard plus N shards is
// what makes concurrent region access race-free.
type shard struct {
	data map[Pos]Block
	gen  func(Pos) Block
	// tops caches each column's highest motion-blocking Y — the live
	// MOTION_BLOCKING heightmap, populated on first query and dropped
	// when a write lands at or above it.
	tops     map[[2]int]int
	ops      chan shardOp
	stopCh   chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	tick     atomic.Int64
}

type shardOp struct {
	get  bool
	del  bool
	pos  Pos
	val  Block
	resp chan Block
	// Bulk read: whole columns of a FillBox box in, their blocks out,
	// colDY per column, columns in the order they were asked for.
	cols       []colRef
	boxX       int
	boxY       int
	boxZ       int
	colDY      int
	colsResp   chan []Block
	positions  []Pos
	blocksResp chan []Block
	// Snapshot: copy of the override map out.
	snapResp chan map[Pos]Block
	// Column-top query: highest motion-blocking Y in column (pos.X,
	// pos.Z) within [topMinY, topMaxY], or topMinY-1 for all air.
	topQ    bool
	topMinY int
	topMaxY int
	topResp chan int
}

func newShard(gen func(Pos) Block) *shard {
	return &shard{
		data:   make(map[Pos]Block),
		gen:    gen,
		ops:    make(chan shardOp),
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
}

func (s *shard) start() {
	go func() {
		defer close(s.done)
		for {
			select {
			case <-s.stopCh:
				return
			case op := <-s.ops:
				if op.topResp != nil {
					op.topResp <- s.topAt(op.pos.X, op.pos.Z, op.topMinY, op.topMaxY)
				} else if op.snapResp != nil {
					cp := make(map[Pos]Block, len(s.data))
					for p, b := range s.data {
						cp[p] = b
					}
					op.snapResp <- cp
				} else if op.blocksResp != nil {
					out := make([]Block, len(op.positions))
					for i, pos := range op.positions {
						b, ok := s.data[pos]
						if !ok {
							b = s.gen(pos)
						}
						out[i] = b
					}
					op.blocksResp <- out
				} else if op.colsResp != nil {
					out := make([]Block, len(op.cols)*op.colDY)
					for i, c := range op.cols {
						x, z := op.boxX+c.x, op.boxZ+c.z
						for y := 0; y < op.colDY; y++ {
							p := Pos{x, op.boxY + y, z}
							b, ok := s.data[p]
							if !ok {
								b = s.gen(p)
							}
							out[i*op.colDY+y] = b
						}
					}
					op.colsResp <- out
				} else if op.get {
					b, ok := s.data[op.pos]
					if !ok {
						b = s.gen(op.pos)
					}
					op.resp <- b
				} else {
					if s.tops != nil {
						key := [2]int{op.pos.X, op.pos.Z}
						if t, ok := s.tops[key]; ok {
							switch {
							case op.del && op.pos.Y >= t:
								delete(s.tops, key)
							case op.pos.Y > t:
								// Above the surface a write can only
								// raise the top, and only if it blocks.
								if motionBlockingBlock(op.val) {
									s.tops[key] = op.pos.Y
								}
							case op.pos.Y == t && !motionBlockingBlock(op.val):
								// The surface cell itself unblocked.
								delete(s.tops, key)
							}
						}
					}
					if op.del {
						delete(s.data, op.pos)
					} else {
						s.data[op.pos] = op.val
					}
				}
			}
		}
	}()
}

// topAt scans the column top-down once and caches the highest
// motion-blocking cell's Y; later writes at or above it invalidate.
// minY-1 reports a column with nothing blocking at all.
func (s *shard) topAt(x, z, minY, maxY int) int {
	if s.tops == nil {
		s.tops = make(map[[2]int]int)
	}
	key := [2]int{x, z}
	if t, ok := s.tops[key]; ok {
		return t
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

// stop halts the shard worker exactly once; concurrent callers share
// the same close and wait.
func (s *shard) stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
	<-s.done
}

func (s *shard) get(p Pos) Block {
	resp := make(chan Block, 1)
	select {
	case s.ops <- shardOp{get: true, pos: p, resp: resp}:
		return <-resp
	case <-s.done:
		b, ok := s.data[p]
		if !ok {
			return s.gen(p)
		}
		return b
	}
}

func (s *shard) set(p Pos, b Block) {
	select {
	case s.ops <- shardOp{pos: p, val: b}:
	case <-s.done:
		s.data[p] = b
	}
}

// snapshot returns a copy of the override map via rendezvous.
func (s *shard) snapshot() map[Pos]Block {
	resp := make(chan map[Pos]Block, 1)
	select {
	case s.ops <- shardOp{snapResp: resp}:
		return <-resp
	case <-s.done:
		cp := make(map[Pos]Block, len(s.data))
		for p, b := range s.data {
			cp[p] = b
		}
		return cp
	}
}

func (s *shard) delete(p Pos) {
	select {
	case s.ops <- shardOp{del: true, pos: p}:
	case <-s.done:
		delete(s.data, p)
	}
}

func (s *shard) onTick(n int64) { s.tick.Store(n) }
