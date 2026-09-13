package world

import (
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	"mc-go/internal/event"
)

// Block is a global block-state id (the same numbering the client
// hardcodes and chunk palettes carry). The ids and the common Air /
// Stone / Dirt / Grass constants are generated from the official
// report; see blocks_gen.go.
type Block uint16

// Pos is an absolute block position.
type Pos struct{ X, Y, Z int }

// World owns N shard goroutines plus a 20 TPS tick coordinator.
// Shards own their block overrides; the generator is pure so
// reads outside overrides never touch shard state.
//
// Shard workers start in New so Get/Set/Fill work immediately; Start
// adds the 20 TPS tick loop, Stop halts everything. Start is safe to
// call once; Stop is idempotent.
type World struct {
	bus     *event.Bus
	shards   []*shard
	gen      func(Pos) Block
	biome    string
	tick     atomic.Int64
	ticking  atomic.Bool
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// SetBiome pins the single biome written into region-file palettes.
// mc-go chunks are single-biome until the noise generator lands.
func (w *World) SetBiome(name string) { w.biome = name }

// New builds a sharded world with live workers generating the vanilla
// flat preset. shards<=0 defaults to 8.
func New(bus *event.Bus, shards int) *World {
	return NewGen(bus, shards, flat)
}

// NewGen is New with a caller-supplied terrain generator, used by
// non-overworld dimensions whose presets differ from flat.
func NewGen(bus *event.Bus, shards int, gen func(Pos) Block) *World {
	if shards <= 0 {
		shards = 8
	}
	if gen == nil {
		gen = flat
	}
	w := &World{bus: bus, gen: gen, stop: make(chan struct{})}
	for i := 0; i < shards; i++ {
		s := newShard(gen)
		w.shards = append(w.shards, s)
		s.start()
	}
	return w
}

// Start launches the tick loop (no-op if already ticking).
func (w *World) Start() {
	if !w.ticking.CompareAndSwap(false, true) {
		return
	}
	w.wg.Add(1)
	go w.tickLoop()
}

// Stop halts ticks and shard workers. Safe to call from multiple
// goroutines: the close runs once and every caller waits out the
// workers.
func (w *World) Stop() {
	w.stopOnce.Do(func() { close(w.stop) })
	for _, s := range w.shards {
		s.stop()
	}
	w.wg.Wait()
}

// TickCount is the number of completed ticks.
func (w *World) TickCount() int64 { return w.tick.Load() }

// ShardCount exposes sizing for tests/metrics.
func (w *World) ShardCount() int { return len(w.shards) }

func (w *World) tickLoop() {
	defer w.wg.Done()
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-w.stop:
			return
		case <-t.C:
			n := w.tick.Add(1)
			var wg sync.WaitGroup
			for _, s := range w.shards {
				wg.Add(1)
				go func(s *shard) {
					defer wg.Done()
					s.onTick(n)
				}(s)
			}
			wg.Wait()
			if w.bus != nil {
				w.bus.Publish(event.TopicTick, n)
			}
		}
	}
}

// shardFor routes a column to its owner shard.
func (w *World) shardFor(x, z int) *shard {
	h := fnv.New32a()
	var b [16]byte
	putInt(&b, 0, x)
	putInt(&b, 8, z)
	_, _ = h.Write(b[:])
	return w.shards[h.Sum32()%uint32(len(w.shards))]
}

// GetBlock reads an override or the flat generator.
func (w *World) GetBlock(p Pos) Block {
	return w.shardFor(p.X, p.Z).get(p)
}

// SetBlock stores an override on the owning shard.
func (w *World) SetBlock(p Pos, b Block) {
	w.shardFor(p.X, p.Z).set(p, b)
}

// Spawn returns the flat-world spawn point, matching vanilla's spawn
// for a default flat world: on top of the grass layer, centered.
func (w *World) Spawn() (x, y, z float64) { return 8.5, -60, 0.5 }

// FillBox reads every block in the inclusive box [min, max] into out,
// ordered x-fastest: index = ((y-min.Y)*dz + (z-min.Z))*dx + (x-min.X).
// Positions are grouped by owning shard so a chunk costs one round
// trip per shard instead of one per block.
func (w *World) FillBox(min, max Pos, out []Block) {
	dx := max.X - min.X + 1
	dy := max.Y - min.Y + 1
	dz := max.Z - min.Z + 1
	type req struct {
		idx []int
		pos []Pos
	}
	byShard := make(map[*shard]*req)
	for y := 0; y < dy; y++ {
		for z := 0; z < dz; z++ {
			for x := 0; x < dx; x++ {
				p := Pos{min.X + x, min.Y + y, min.Z + z}
				s := w.shardFor(p.X, p.Z)
				r := byShard[s]
				if r == nil {
					r = &req{}
					byShard[s] = r
				}
				r.idx = append(r.idx, (y*dz+z)*dx+x)
				r.pos = append(r.pos, p)
			}
		}
	}
	type res struct {
		idx []int
		got []Block
	}
	ch := make(chan res, len(byShard))
	for s, r := range byShard {
		go func(s *shard, r *req) {
			resp := make(chan []Block, 1)
			select {
			case s.ops <- shardOp{fill: r.pos, fillResp: resp}:
				ch <- res{r.idx, <-resp}
			case <-s.done:
				got := make([]Block, len(r.pos))
				for i, p := range r.pos {
					got[i] = w.gen(p)
				}
				ch <- res{r.idx, got}
			}
		}(s, r)
	}
	for range byShard {
		r := <-ch
		for i, o := range r.idx {
			out[o] = r.got[i]
		}
	}
}

func putInt(b *[16]byte, off, v int) {
	for i := 0; i < 8; i++ {
		b[off+i] = byte(v >> (56 - 8*i))
	}
}

// flat returns the vanilla default flat preset value: bedrock at the
// world floor, two dirt layers, then grass; air above. Pure function,
// no locking.
func flat(p Pos) Block {
	switch p.Y {
	case -64:
		if b, ok := BlockState("bedrock"); ok {
			return b
		}
		return Stone
	case -63, -62:
		return Dirt
	case -61:
		return Grass
	default:
		return Air
	}
}

// BlockStateMust returns the default state id for a known block name,
// panicking on unknown names. Generated tables are exact, so a missing
// block here is a programming error.
func BlockStateMust(name string) Block {
	if b, ok := blockStates[name]; ok {
		return b
	}
	panic("world: unknown block state " + name)
}
