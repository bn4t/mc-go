package world

import (
	"sync"
	"sync/atomic"
	"time"

	"mc-go/api/registry"
	"mc-go/internal/event"
)

// Block is a catalog-local canonical state handle. The initial builtin
// order preserves the generated behavior tables; adapters must map handles
// to their protocol numeric IDs. Persist canonical StateKey identities.
type Block = registry.BlockStateID

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
	bus    *event.Bus
	shards []*shard
	gen    func(Pos) Block
	biome  string
	// biomeSrc, when set, gives per-quart biomes (a noise generator's
	// storage); otherwise every quart carries biome.
	biomeSrc func(qx, qy, qz int) string
	tick     atomic.Int64
	ticking  atomic.Bool
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
	changes  blockChangeJournal
	// blockEntities holds the block entity compounds the last load read
	// out of the region files - a chest's contents, a sign's text -
	// which the server turns back into its own stores. The world owns
	// only the blocks; this is the pass-through for everything the
	// blocks do not say.
	blockEntities []map[string]any
}

// BlockEntities returns the block entities read by the last load, in
// the order the region files held them. Reading again replaces them, so
// a caller that loads twice never sees the same chest twice.
func (w *World) BlockEntities() []map[string]any { return w.blockEntities }

// SetBiome pins the single biome written into region-file palettes.
// mc-go chunks are single-biome until the noise generator lands.
func (w *World) SetBiome(name string) { w.biome = name }

// Biome is the dimension's single biome name (the flat world palette).
func (w *World) Biome() string { return w.biomeName() }

// SetBiomeSource makes quart biomes come from src. Call before the
// world serves reads.
func (w *World) SetBiomeSource(src func(qx, qy, qz int) string) { w.biomeSrc = src }

// QuartBiome is the stored biome of quart (qx, qy, qz).
func (w *World) QuartBiome(qx, qy, qz int) string {
	if w.biomeSrc != nil {
		return w.biomeSrc(qx, qy, qz)
	}
	return w.biomeName()
}

// SetGenerator replaces the terrain generator behind every position
// without an override. Call before the world serves reads.
func (w *World) SetGenerator(gen func(Pos) Block) {
	w.gen = gen
	for _, s := range w.shards {
		s.mu.Lock()
		s.gen = gen
		s.tops = nil
		s.mu.Unlock()
	}
}

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
//
// The mix is deliberately arithmetic rather than hash.Hash: this is on
// the path of every block read, and a chunk's worth of reads is nearly a
// hundred thousand calls - allocating an fnv digest for each of them cost
// more than everything else a chunk packet does put together (see
// FillBox, which now hashes once per column rather than once per block).
func (w *World) shardFor(x, z int) *shard {
	h := uint32(x)*0x9E3779B1 ^ uint32(z)*0x85EBCA77
	h ^= h >> 15
	h *= 0xC2B2AE35
	h ^= h >> 13
	return w.shards[h%uint32(len(w.shards))]
}

// GetBlock reads an override or the flat generator.
func (w *World) GetBlock(p Pos) Block {
	return w.shardFor(p.X, p.Z).get(p)
}

// SetBlock stores an override on the owning shard.
func (w *World) SetBlock(p Pos, b Block) {
	w.shardFor(p.X, p.Z).set(p, b)
	w.invalidateBlock(p)
}

// ColumnVersion counts the writes column (x, z) has seen. A value that
// has not changed since a caller derived something from the column means
// no block in it changed in between.
func (w *World) ColumnVersion(x, z int) uint32 {
	return w.shardFor(x, z).columnVersion(x, z)
}

// ChunkVersion sums the write versions of chunk (cx, cz)'s 256
// columns. Every write bumps exactly one column, so any write anywhere
// in the chunk raises it, and an unchanged value means the chunk's
// blocks are as they were.
func (w *World) ChunkVersion(cx, cz int) uint64 {
	var v uint64
	for z := cz * 16; z < cz*16+16; z++ {
		for x := cx * 16; x < cx*16+16; x++ {
			v += uint64(w.ColumnVersion(x, z))
		}
	}
	return v
}

// TopMotionBlocking returns the highest motion-blocking cell's Y in
// the column within [minY, maxY] — the value vanilla keeps in its
// MOTION_BLOCKING heightmap, minus one; minY-1 for an all-air column.
func (w *World) TopMotionBlocking(x, z, minY, maxY int) int {
	return w.shardFor(x, z).topAt(x, z, minY, maxY)
}

// Spawn returns the flat-world spawn point, matching vanilla's for a
// default flat world: block (0,-60,0), which the real 26.2 server
// writes into level.dat's spawn.pos, centered on that block - the
// world origin, because the flat world is solid there and vanilla's
// spawn search stops at the first position it tries.
func (w *World) Spawn() (x, y, z float64) { return 0.5, -60, 0.5 }

// FillBox reads every block in the inclusive box [min, max] into out,
// ordered x-fastest: index = ((y-min.Y)*dz + (z-min.Z))*dx + (x-min.X).
// Positions are grouped by owning shard so a box costs one round trip
// per shard instead of one per block.
//
// The grouping is by column: every block of one (x, z) belongs to the
// same shard, so the owning shard is looked up once per column rather
// than once per block, and the shard is handed the columns it owns
// rather than a position for every block. A chunk column is nearly a
// hundred thousand blocks, so the difference is most of what the packet
// costs to build.
func (w *World) FillBox(min, max Pos, out []Block) {
	dx := max.X - min.X + 1
	dy := max.Y - min.Y + 1
	dz := max.Z - min.Z + 1
	byShard := make(map[*shard][]colRef)
	for z := 0; z < dz; z++ {
		for x := 0; x < dx; x++ {
			s := w.shardFor(min.X+x, min.Z+z)
			byShard[s] = append(byShard[s], colRef{x: x, z: z})
		}
	}
	type res struct {
		cols []colRef
		got  []Block
	}
	results := make([]res, 0, len(byShard))
	for s, cols := range byShard {
		got := make([]Block, len(cols)*dy)
		s.readColumns(cols, min.X, min.Y, min.Z, dy, got)
		results = append(results, res{cols, got})
	}
	// A column's blocks are one layer apart in out: each is dy slots
	// from the next, and a layer is dx slots wide.
	stride := dx * dz
	for _, r := range results {
		for i, c := range r.cols {
			base := c.z*dx + c.x
			for y := 0; y < dy; y++ {
				out[base+y*stride] = r.got[i*dy+y]
			}
		}
	}
}

// colRef is one column of a FillBox, as offsets from the box's minimum.
type colRef struct{ x, z int }

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
