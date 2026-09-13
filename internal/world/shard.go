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
	data   map[Pos]Block
	gen    func(Pos) Block
	ops    chan shardOp
	stopCh   chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	tick   atomic.Int64
}

type shardOp struct {
	get  bool
	del  bool
	pos  Pos
	val  Block
	resp chan Block
	// Bulk read: positions in, blocks out, same order.
	fill     []Pos
	fillResp chan []Block
	// Snapshot: copy of the override map out.
	snapResp chan map[Pos]Block
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
				if op.snapResp != nil {
					cp := make(map[Pos]Block, len(s.data))
					for p, b := range s.data {
						cp[p] = b
					}
					op.snapResp <- cp
				} else if op.fillResp != nil {
					out := make([]Block, len(op.fill))
					for i, p := range op.fill {
						b, ok := s.data[p]
						if !ok {
							b = s.gen(p)
						}
						out[i] = b
					}
					op.fillResp <- out
				} else if op.get {
					b, ok := s.data[op.pos]
					if !ok {
						b = s.gen(op.pos)
					}
					op.resp <- b
				} else if op.del {
					delete(s.data, op.pos)
				} else {
					s.data[op.pos] = op.val
				}
			}
		}
	}()
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
