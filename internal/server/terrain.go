package server

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sync"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Level types the overworld can be generated with.
const (
	LevelTypeFlat        = "flat"
	LevelTypeNormal      = "normal"
	LevelTypeAmplified   = "amplified"
	LevelTypeLargeBiomes = "large_biomes"
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

	// fresh lists handed-out chunks whose post-processing has not run.
	freshMu sync.Mutex
	fresh   [][2]int
	// genEntities are the feature-placed entities (end spike crystals)
	// of chunks handed out since the last drain (drainGenEntities).
	genEntities []worldgen.GenEntity
}

type terrainEntry struct {
	once sync.Once
	p    *worldgen.ProtoChunk
}

// newTerrainSource builds a source for one dimension: its noise
// settings, biome source ("the_end" for the end's) and level bounds.
func newTerrainSource(seed int64, overlay map[string]json.RawMessage, settings, biomes string, minY, height int, structures bool) (*terrainSource, error) {
	data := worldgen.Vanilla()
	if len(overlay) != 0 {
		data = worldgen.Overlay(data, overlay)
	}
	g, err := worldgen.NewGenerator(data, settings, biomes, seed)
	if err != nil {
		return nil, err
	}
	g.RS.SetLevelBounds(int32(minY), int32(height))
	g.Structures = structures
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
	p := t.terrain([2]int32{cx, cz})
	worldgen.PostProcessBubbleColumns(p)
	base := protoToBase(p)
	if t.g.Structures {
		base.Structures = t.g.StructuresTag(cx, cz)
	}
	t.provided[[2]int32{cx, cz}] = true
	if len(base.PostProcess) > 0 || len(p.Entities) > 0 {
		t.freshMu.Lock()
		if len(base.PostProcess) > 0 {
			t.fresh = append(t.fresh, [2]int{x, z})
		}
		t.genEntities = append(t.genEntities, p.Entities...)
		t.freshMu.Unlock()
	}
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
	for _, m := range p.PostProcessFluids() {
		b.PostProcess = append(b.PostProcess, world.Pos{X: int(m[0]), Y: int(m[1]), Z: int(m[2])})
	}
	return b
}

// postProcessStep is the fluid half of LevelChunk.postProcessGeneration
// for generated chunks: each marked fluid ticks once, immediately, as
// the chunk enters the ticking world — that is what starts cave
// waterfalls and lava falls. A chunk waits until its eight neighbours
// exist, as a ticking chunk's do; the flow itself then continues
// through the scheduled fluid ticks.
func (s *Server) postProcessStep() {
	for _, d := range s.dims {
		t := d.terrain
		if t == nil {
			continue
		}
		t.freshMu.Lock()
		keys := t.fresh
		t.fresh = nil
		t.freshMu.Unlock()
		var wait [][2]int
		for _, k := range keys {
			ready := true
			for dz := -1; dz <= 1 && ready; dz++ {
				for dx := -1; dx <= 1; dx++ {
					if d.w.PeekBaseChunk(k[0]+dx, k[1]+dz) == nil {
						ready = false
						break
					}
				}
			}
			if !ready {
				wait = append(wait, k)
				continue
			}
			for _, p := range d.w.PeekBaseChunk(k[0], k[1]).PostProcess {
				s.fluidTick(d, p)
			}
		}
		if len(wait) > 0 {
			t.freshMu.Lock()
			t.fresh = append(t.fresh, wait...)
			t.freshMu.Unlock()
		}
	}
}

// applyLevelType rebuilds the overworld for the configured level type
// before anything reads it. Flat stays the default.
func (s *Server) applyLevelType() error {
	if s.LevelType == "" || s.LevelType == LevelTypeFlat {
		if s.FlatPreset != nil {
			s.applyFlatPreset(s.FlatPreset)
		}
		return nil
	}
	settings, ok := map[string]string{
		LevelTypeNormal: "overworld", LevelTypeAmplified: "amplified", LevelTypeLargeBiomes: "large_biomes",
	}[s.LevelType]
	if !ok {
		return fmt.Errorf("unknown level type %q", s.LevelType)
	}
	d := s.dims[dimOverworld]
	src, err := newTerrainSource(s.WorldSeed, s.WorldgenOverlay, settings, "overworld", d.minY, d.height, s.GenerateStructures)
	if err != nil {
		return err
	}
	// The nether and end generate from their own noise settings.
	for _, o := range []struct{ dim, settings, biomes string }{
		{dimNether, "nether", "nether"}, {dimEnd, "end", "the_end"},
	} {
		od := s.dims[o.dim]
		osrc, err := newTerrainSource(s.WorldSeed, s.WorldgenOverlay, o.settings, o.biomes, od.minY, od.height, s.GenerateStructures)
		if err != nil {
			return err
		}
		ow := world.NewProvided(s.bus, 8, osrc)
		ow.SetBiome(od.biome)
		prev := od.w
		od.w = ow
		od.terrain = osrc
		od.flat = false
		od.seaLevel = osrc.g.RS.SeaLevel()
		prev.Stop()
	}
	old := s.world
	w := world.NewProvided(s.bus, 8, src)
	w.SetBiome("minecraft:plains")
	s.world = w
	d.w = w
	d.terrain = src
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
	for lx := 0; lx < 16; lx++ {
		for lz := 0; lz < 16; lz++ {
			if y, ok := s.levelRespawnY(cx*16+lx, cz*16+lz); ok {
				return cx*16 + lx, y, cz*16 + lz, true
			}
		}
	}
	return 0, 0, 0, false
}

// levelRespawnY is PlayerSpawnFinder.getLevelRespawnPos for a column of
// the generated overworld: the cell above the first full-topped block
// at or below the motion-blocking top, unless a fluid comes first or
// the surface is a non-blocking cover over the ocean floor.
func (s *Server) levelRespawnY(x, z int) (int, bool) {
	c := s.world.BaseChunk(x>>4, z>>4)
	if c == nil {
		return 0, false
	}
	at := func(y int) world.Block { return s.world.GetBlock(world.Pos{X: x, Y: y, Z: z}) }
	col := make([]world.Block, c.Height)
	s.world.FillBox(world.Pos{X: x, Y: c.MinY, Z: z}, world.Pos{X: x, Y: c.MinY + c.Height - 1, Z: z}, col)
	top := func(pred func(world.Block) bool) int {
		for i := len(col) - 1; i >= 0; i-- {
			if pred(col[i]) {
				return c.MinY + i
			}
		}
		return c.MinY - 1
	}
	mb := top(worldgen.MotionBlocking)
	if mb < c.MinY {
		return 0, false
	}
	ws := top(func(b world.Block) bool { return !worldgen.IsAir(b) })
	if ws <= mb && ws > top(worldgen.BlocksMotion) {
		return 0, false
	}
	for y := mb + 1; y >= c.MinY; y-- {
		b := at(y)
		if worldgen.HasFluid(b) {
			return 0, false
		}
		if worldgen.SturdyUp(b) {
			return y + 1, true
		}
	}
	return 0, false
}

// playerSpawn is PlayerSpawnFinder.findSpawn for a new or bed-less
// player in the generated overworld: candidates over the square of
// respawn_radius around the world spawn, visited from a random offset
// with a stride coprime to their count, take the first column whose
// level respawn spot leaves the player's box free of blocks and fluid;
// adventure mode and an exhausted search keep the spawn with its height
// fixed up. A flat world keeps the exact spawn.
func (s *Server) playerSpawn() (float64, float64, float64) {
	x, y, z := s.WorldSpawn()
	if !s.world.Provided() {
		return x, y, z
	}
	sx, sy, sz := int(math.Floor(x)), int(math.Floor(y)), int(math.Floor(z))
	if s.GameMode != GameModeAdventure {
		r := max(0, s.gameRuleInt("minecraft:respawn_radius"))
		// The world border is 29999984 out; only its distance clamps.
		if dist := 29999984 - max(absInt(sx), absInt(sz)); dist < r {
			r = dist
		}
		if r < 1 {
			r = 1
		}
		side := 2*r + 1
		count := min(1024, side*side)
		stride := 17
		if count <= 16 {
			stride = count - 1
		}
		off := rand.Intn(count)
		for i := 0; i < count; i++ {
			idx := (off + stride*i) % count
			cx, cz := sx+idx%side-r, sz+idx/side-r
			if py, ok := s.levelRespawnY(cx, cz); ok && s.spawnBoxFree(cx, py, cz) {
				return float64(cx) + 0.5, float64(py), float64(cz) + 0.5
			}
		}
	}
	return s.fixupSpawnHeight(sx, sy, sz)
}

// spawnBoxFree approximates noCollisionNoLiquid for the player's box
// standing at (x, y, z): its two cells neither block motion nor hold
// fluid.
func (s *Server) spawnBoxFree(x, y, z int) bool {
	for dy := 0; dy < 2; dy++ {
		b := s.world.GetBlock(world.Pos{X: x, Y: y + dy, Z: z})
		if worldgen.BlocksMotion(b) || worldgen.HasFluid(b) {
			return false
		}
	}
	return true
}

// fixupSpawnHeight is PlayerSpawnFinder.fixupSpawnHeight: rise out of
// anything solid, then drop through free cells to the ground.
func (s *Server) fixupSpawnHeight(x, y, z int) (float64, float64, float64) {
	d := s.dims[dimOverworld]
	maxY, minY := d.minY+d.height-1, d.minY
	for !s.spawnBoxFree(x, y, z) && y < maxY {
		y++
	}
	y--
	for s.spawnBoxFree(x, y, z) && y > minY {
		y--
	}
	y++
	return float64(x) + 0.5, float64(y), float64(z) + 0.5
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// applyFlatPreset swaps the flat overworld for a custom preset's layers
// and biome; a world without a saved spawn spawns on top of the layers
// at the origin column, as the real server's flat spawn search finds.
func (s *Server) applyFlatPreset(p *world.FlatPreset) {
	d := s.dims[dimOverworld]
	gen, top := world.FlatGen(p, d.minY)
	old := s.world
	w := world.NewGen(s.bus, 8, gen)
	w.SetBiome(p.Biome)
	s.world = w
	d.w = w
	d.biome = p.Biome
	old.Stop()
	s.spawnMu.Lock()
	have := s.haveSpawn
	s.spawnMu.Unlock()
	if !have {
		s.setWorldSpawn(0, top, 0)
	}
}
