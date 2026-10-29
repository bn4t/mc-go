package server

import (
	"encoding/json"
	"fmt"
	"runtime"
	"sync"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Level types the overworld can be generated with.
const (
	LevelTypeFlat   = "flat"
	LevelTypeNormal = "normal"
)

// terrainSource feeds a provided world from the noise generator.
//
// Vanilla decorates a chunk (FEATURES) only once its neighbours have
// their terrain, its features may write one chunk into each neighbour,
// and a chunk is lit and finished only after all eight neighbours were
// decorated — so nothing writes into a finished chunk. The source keeps
// the same invariant: a requested chunk is handed to the world once the
// 3x3 around it is decorated, and proto chunks are dropped when no
// pending decoration can still read them.
//
// Terrain is a pure function of the seed, so it is generated in
// parallel and ahead of need (a request warms the 7x7 around it, which
// covers the next requests of a player's window); decoration writes
// across chunk borders and runs one chunk at a time.
type terrainSource struct {
	g       *worldgen.Generator
	workers int

	tmu    sync.Mutex
	terr   map[[2]int32]*terrainEntry
	pruned map[[2]int32]bool

	dmu      sync.Mutex
	deco     map[[2]int32]bool
	provided map[[2]int32]bool
}

type terrainEntry struct {
	once sync.Once
	p    *worldgen.ProtoChunk
}

func newTerrainSource(seed int64, overlay map[string]json.RawMessage) (*terrainSource, error) {
	data := worldgen.Vanilla()
	if len(overlay) != 0 {
		data = worldgen.Overlay(data, overlay)
	}
	g, err := worldgen.NewGenerator(data, "overworld", "overworld", seed)
	if err != nil {
		return nil, err
	}
	return &terrainSource{
		g: g, workers: max(1, runtime.NumCPU()),
		terr: map[[2]int32]*terrainEntry{}, pruned: map[[2]int32]bool{}, deco: map[[2]int32]bool{}, provided: map[[2]int32]bool{},
	}, nil
}

// terrain returns k's terrain-stage proto chunk, generating it once.
func (t *terrainSource) terrain(k [2]int32) *worldgen.ProtoChunk {
	t.tmu.Lock()
	e := t.terr[k]
	if e == nil {
		e = &terrainEntry{}
		t.terr[k] = e
	}
	t.tmu.Unlock()
	e.once.Do(func() { e.p = t.g.Terrain(k[0], k[1]) })
	return e.p
}

// prefetch generates the terrain of keys on the worker pool.
func (t *terrainSource) prefetch(keys [][2]int32) {
	var todo [][2]int32
	t.tmu.Lock()
	for _, k := range keys {
		if t.terr[k] == nil && !t.pruned[k] {
			todo = append(todo, k)
		}
	}
	t.tmu.Unlock()
	var wg sync.WaitGroup
	next := make(chan [2]int32)
	for i := 0; i < min(t.workers, len(todo)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range next {
				t.terrain(k)
			}
		}()
	}
	for _, k := range todo {
		next <- k
	}
	close(next)
	wg.Wait()
}

func around(cx, cz, r int32) [][2]int32 {
	var out [][2]int32
	for dz := -r; dz <= r; dz++ {
		for dx := -r; dx <= r; dx++ {
			out = append(out, [2]int32{cx + dx, cz + dz})
		}
	}
	return out
}

// ProvideChunk is world.ChunkProvider: decorate the 3x3 around the
// chunk (each needing its own 3x3 of terrain) and return it finished.
func (t *terrainSource) ProvideChunk(x, z int) *world.BaseChunk {
	cx, cz := int32(x), int32(z)
	t.prefetch(around(cx, cz, 3))
	t.dmu.Lock()
	defer t.dmu.Unlock()
	for _, k := range around(cx, cz, 1) {
		if t.deco[k] {
			continue
		}
		near := map[[2]int32]*worldgen.ProtoChunk{}
		for _, n := range around(k[0], k[1], 1) {
			near[n] = t.terrain(n)
		}
		t.g.Decorate(t.g.NewRegion(near[k], near))
		t.deco[k] = true
	}
	base := protoToBase(t.terrain([2]int32{cx, cz}))
	t.provided[[2]int32{cx, cz}] = true
	t.prune(cx, cz)
	return base
}

// prune drops protos nothing can read any more: one already handed
// out whose whole 5x5 neighbourhood is decorated.
func (t *terrainSource) prune(cx, cz int32) {
	t.tmu.Lock()
	defer t.tmu.Unlock()
	for _, k := range around(cx, cz, 2) {
		if _, ok := t.terr[k]; !ok || !t.provided[k] {
			continue
		}
		done := true
		for _, n := range around(k[0], k[1], 2) {
			if !t.deco[n] {
				done = false
				break
			}
		}
		if done {
			delete(t.terr, k)
			t.pruned[k] = true
			t.g.ForgetChunk(k[0], k[1])
		}
	}
}

func protoToBase(p *worldgen.ProtoChunk) *world.BaseChunk {
	n := int(p.Height) / 16
	b := &world.BaseChunk{MinY: int(p.MinY), Height: int(p.Height), Sections: make([][]world.Block, n), Biomes: make([]string, len(p.Biomes))}
	for s := 0; s < n; s++ {
		sec := p.Blocks[s*4096 : (s+1)*4096]
		empty := true
		for _, v := range sec {
			if v != world.Air {
				empty = false
				break
			}
		}
		if !empty {
			b.Sections[s] = append([]world.Block(nil), sec...)
		}
	}
	for i, name := range p.Biomes {
		b.Biomes[i] = "minecraft:" + name
	}
	return b
}

// applyLevelType rebuilds the overworld for the configured level type
// before anything reads it. Flat stays the default.
func (s *Server) applyLevelType() error {
	if s.LevelType == "" || s.LevelType == LevelTypeFlat {
		return nil
	}
	if s.LevelType != LevelTypeNormal {
		return fmt.Errorf("unknown level type %q", s.LevelType)
	}
	src, err := newTerrainSource(s.WorldSeed, s.WorldgenOverlay)
	if err != nil {
		return err
	}
	old := s.world
	w := world.NewProvided(s.bus, 8, src)
	w.SetBiome("minecraft:plains")
	s.world = w
	d := s.dims[dimOverworld]
	d.w = w
	d.flat = false
	d.seaLevel = src.g.RS.SeaLevel()
	d.biome = "minecraft:plains"
	s.terrain = src
	old.Stop()
	s.spawnMu.Lock()
	have := s.haveSpawn
	s.spawnMu.Unlock()
	if !have {
		x, y, z := s.findInitialSpawn(src)
		s.setWorldSpawn(x, y, z)
	}
	return nil
}

// findInitialSpawn is MinecraftServer.setInitialSpawn: the climate
// spawn finder's chunk, then an 11x11 chunk spiral for the first column
// whose surface is solid ground (PlayerSpawnFinder.getSpawnPosInChunk).
func (s *Server) findInitialSpawn(src *terrainSource) (int, int, int) {
	sx, sz := src.g.RS.FindSpawnPosition()
	cx, cz := int(sx)>>4, int(sz)>>4
	spawnX, spawnY, spawnZ := cx*16+8, 64, cz*16+8
	x, z, dx, dz := 0, 0, 0, -1
	for i := 0; i < 11*11; i++ {
		if x >= -5 && x <= 5 && z >= -5 && z <= 5 {
			if px, py, pz, ok := s.spawnPosInChunk(cx+x, cz+z); ok {
				return px, py, pz
			}
		}
		if x == z || (x < 0 && x == -z) || (x > 0 && x == 1-z) {
			dx, dz = -dz, dx
		}
		x += dx
		z += dz
	}
	return spawnX, spawnY, spawnZ
}

func (s *Server) spawnPosInChunk(cx, cz int) (int, int, int, bool) {
	c := s.world.BaseChunk(cx, cz)
	if c == nil {
		return 0, 0, 0, false
	}
	top := func(lx, lz int, pred func(world.Block) bool) int {
		for y := c.MinY + c.Height - 1; y >= c.MinY; y-- {
			if pred(c.At(lx, y, lz)) {
				return y
			}
		}
		return c.MinY - 1
	}
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			mb := top(lx, lz, worldgen.MotionBlocking)
			if mb < c.MinY {
				continue
			}
			ws := top(lx, lz, func(b world.Block) bool { return !worldgen.IsAir(b) })
			if ws <= mb && ws > top(lx, lz, worldgen.BlocksMotion) {
				continue
			}
			for y := mb + 1; y >= c.MinY; y-- {
				b := c.At(lx, y, lz)
				if worldgen.HasFluid(b) {
					break
				}
				if worldgen.SturdyUp(b) {
					return cx*16 + lx, y + 1, cz*16 + lz, true
				}
			}
		}
	}
	return 0, 0, 0, false
}
