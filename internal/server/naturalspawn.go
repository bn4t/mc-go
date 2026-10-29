package server

import (
	"encoding/json"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Natural mob spawning — NaturalSpawner, measured on 26.2 by bytecode
// inspection (javap of NaturalSpawner, NaturalSpawner$SpawnState,
// LocalMobCapCalculator, PotentialCalculator, ServerChunkCache
// .tickChunks, ChunkMap/DistanceManager spawn-chunk bookkeeping).
//
// Per level tick, ServerChunkCache.tickChunks:
//   - builds a SpawnState: the natural-spawn chunk count (chunks at
//     Chebyshev distance <= 8 from a tracked player's chunk — 289 for
//     a lone player), the per-category count of every non-persistent,
//     non-MISC mob in a loaded chunk, the spawn-cost charges of the
//     biomes those mobs stand in, and each nearby player's local mob
//     counts (LocalMobCapCalculator — every mob counts for every
//     non-spectator within 128 blocks of its chunk's centre);
//   - when spawn_mobs is on, filters the spawning categories
//     (MobCategory order minus MISC): monsters only with spawnEnemies
//     (spawn_mobs && spawn_monsters, ServerLevel.isSpawningMonsters),
//     the persistent CREATURE category only when gameTime % 400 == 0,
//     and each only while its count is below max * chunks / 289;
//   - shuffles the spawn-candidate chunks (natural-spawn chunks that
//     are ticking and have a player within 128 blocks of their centre)
//     and runs spawnForChunk on each: every filtered category whose
//     local cap still has room for some nearby player gets one
//     spawnCategoryForChunk.
//
// The level random is not reproducible across servers, so mc-go uses
// its own source; the shape of every roll matches vanilla's.

// spawnRandom is the subset of RandomSource the spawner rolls on —
// the level random for per-tick spawning, the chunk's WorldgenRandom
// for chunk-generation spawns.
type spawnRandom interface {
	NextIntN(bound int32) int32
	NextFloat() float32
	NextBool() bool
}

// goSpawnRandom adapts a math/rand source to spawnRandom.
type goSpawnRandom struct{ r *rand.Rand }

func (g goSpawnRandom) NextIntN(n int32) int32 { return g.r.Int31n(n) }
func (g goSpawnRandom) NextFloat() float32     { return g.r.Float32() }
func (g goSpawnRandom) NextBool() bool         { return g.r.Intn(2) == 1 }

func (s *Server) spawnRng() spawnRandom {
	s.naturalSpawn.once.Do(func() {
		s.naturalSpawn.rng = goSpawnRandom{rand.New(rand.NewSource(time.Now().UnixNano()))}
	})
	return s.naturalSpawn.rng
}

// naturalSpawnState is the server's spawner bookkeeping: per-biome
// spawn settings parsed from the biome registry, per-dimension column
// heightmap caches and the queue of chunks awaiting their
// chunk-generation spawns.
type naturalSpawnState struct {
	once sync.Once
	rng  spawnRandom

	biomeMu sync.Mutex
	biomes  map[string]*biomeSpawnSettings
	tags    map[string]map[string]bool
	// catMasks caches per-chunk spawnable-category masks (biomeMu).
	catMasks map[*dimension]*chunkCatMasks

	colMu sync.Mutex
	cols  map[*world.World]map[[2]int]*spawnColumns

	genMu      sync.Mutex
	genPending []pendingGenSpawn

	litMu sync.Mutex
	// inChunkGen marks a spawnMobsForChunkGeneration pass: its checks
	// read a WorldGenRegion (sky darken 0, no entities).
	inChunkGen bool
	lit        map[*world.World]map[[2]int]bool
}

// pendingGenSpawn is one freshly generated chunk awaiting
// NaturalSpawner.spawnMobsForChunkGeneration.
type pendingGenSpawn struct {
	dim    string
	cx, cz int
	biome  string
}

// spawnerEntry is MobSpawnSettings$SpawnerData: one weighted entry of
// a biome's spawners list.
type spawnerEntry struct {
	typ      string
	weight   int
	min, max int
}

// biomeSpawnSettings is Biome.getMobSettings: the per-category
// weighted lists, spawn_costs and creature_spawn_probability (0.1
// when the biome omits it — MobSpawnSettings' codec default).
type biomeSpawnSettings struct {
	lists        [catCount][]spawnerEntry
	totals       [catCount]int
	costs        map[string][2]float64 // charge, energy_budget
	creatureProb float32
}

func (b *biomeSpawnSettings) empty(c mobCategory) bool { return b == nil || b.totals[c] == 0 }

// pick is WeightedList.getRandom: nextInt(totalWeight), then the
// entry whose cumulative weight passes it.
func (b *biomeSpawnSettings) pick(c mobCategory, r spawnRandom) (spawnerEntry, bool) {
	if b.empty(c) {
		return spawnerEntry{}, false
	}
	i := int(r.NextIntN(int32(b.totals[c])))
	for _, e := range b.lists[c] {
		i -= e.weight
		if i < 0 {
			return e, true
		}
	}
	return spawnerEntry{}, false
}

func (b *biomeSpawnSettings) contains(c mobCategory, e spawnerEntry) bool {
	if b == nil {
		return false
	}
	for _, x := range b.lists[c] {
		if x == e {
			return true
		}
	}
	return false
}

// worldgenData is the biome/tag registry the server spawns from: the
// bundled 26.2 datapack with the configured overlay on top.
func (s *Server) worldgenData() worldgen.Data {
	if len(s.WorldgenOverlay) != 0 {
		return worldgen.Overlay(worldgen.Vanilla(), s.WorldgenOverlay)
	}
	return worldgen.Vanilla()
}

// biomeSpawns parses (once) a biome's spawn settings.
func (s *Server) biomeSpawns(biome string) *biomeSpawnSettings {
	name := strings.TrimPrefix(biome, "minecraft:")
	st := &s.naturalSpawn
	st.biomeMu.Lock()
	defer st.biomeMu.Unlock()
	if b, ok := st.biomes[name]; ok {
		return b
	}
	if st.biomes == nil {
		st.biomes = map[string]*biomeSpawnSettings{}
	}
	var out *biomeSpawnSettings
	if raw, ok := s.worldgenData().Get("biome/" + name); ok {
		var doc struct {
			Spawners map[string][]struct {
				Type     string `json:"type"`
				Weight   int    `json:"weight"`
				MinCount int    `json:"minCount"`
				MaxCount int    `json:"maxCount"`
			} `json:"spawners"`
			SpawnCosts map[string]struct {
				Charge float64 `json:"charge"`
				Budget float64 `json:"energy_budget"`
			} `json:"spawn_costs"`
			CreatureProb *float32 `json:"creature_spawn_probability"`
		}
		if json.Unmarshal(raw, &doc) == nil {
			out = &biomeSpawnSettings{creatureProb: 0.1, costs: map[string][2]float64{}}
			if doc.CreatureProb != nil {
				out.creatureProb = *doc.CreatureProb
			}
			for k, list := range doc.Spawners {
				c, ok := mobCategoryByName(k)
				if !ok {
					continue
				}
				for _, e := range list {
					t := strings.TrimPrefix(e.Type, "minecraft:")
					out.lists[c] = append(out.lists[c], spawnerEntry{t, e.Weight, e.MinCount, e.MaxCount})
					out.totals[c] += e.Weight
				}
			}
			for k, v := range doc.SpawnCosts {
				out.costs[strings.TrimPrefix(k, "minecraft:")] = [2]float64{v.Charge, v.Budget}
			}
		}
	}
	st.biomes[name] = out
	return out
}

// biomeHasTag resolves a worldgen biome tag (nested #tags included).
func (s *Server) biomeHasTag(tag, biome string) bool {
	name := strings.TrimPrefix(biome, "minecraft:")
	st := &s.naturalSpawn
	st.biomeMu.Lock()
	defer st.biomeMu.Unlock()
	if st.tags == nil {
		st.tags = map[string]map[string]bool{}
	}
	set, ok := st.tags[tag]
	if !ok {
		set = map[string]bool{}
		var walk func(t string, depth int)
		walk = func(t string, depth int) {
			if depth > 8 {
				return
			}
			raw, ok := s.worldgenData().Get("tags/worldgen/biome/" + t)
			if !ok {
				return
			}
			var doc struct {
				Values []json.RawMessage `json:"values"`
			}
			if json.Unmarshal(raw, &doc) != nil {
				return
			}
			for _, v := range doc.Values {
				var id string
				if json.Unmarshal(v, &id) != nil {
					var obj struct {
						ID string `json:"id"`
					}
					if json.Unmarshal(v, &obj) != nil {
						continue
					}
					id = obj.ID
				}
				if strings.HasPrefix(id, "#") {
					walk(strings.TrimPrefix(strings.TrimPrefix(id, "#"), "minecraft:"), depth+1)
					continue
				}
				set[strings.TrimPrefix(id, "minecraft:")] = true
			}
		}
		walk(tag, 0)
		st.tags[tag] = set
	}
	return set[name]
}

// quartBiome is ChunkAccess.getNoiseBiome: the /fillbiome override
// cell, else the generated chunk's cell, else the level's single
// biome.
func (s *Server) quartBiome(d *dimension, qx, qy, qz int) string {
	key := [3]int{qx, qy, qz}
	d.biomesMu.RLock()
	b, ok := d.biomes[key]
	d.biomesMu.RUnlock()
	if ok {
		return b
	}
	minQ := d.minY >> 2
	maxQ := minQ + d.height/4 - 1
	qy = min(max(qy, minQ), maxQ)
	return d.w.BiomeAt(qx*4, qy*4, qz*4)
}

// spawnBiomeAt is Level.getBiome — the zoomed BiomeManager lookup
// getRandomSpawnMobAt and the biome-tag predicates read.
func (s *Server) spawnBiomeAt(d *dimension, p world.Pos) string {
	return worldgen.ZoomBiome(s.WorldSeed, int32(p.X), int32(p.Y), int32(p.Z), func(qx, qy, qz int32) string {
		return s.quartBiome(d, int(qx), int(qy), int(qz))
	})
}

// roughBiomeAt is NaturalSpawner.getRoughBiome: the chunk's noise
// biome at the position's quart, unzoomed — the spawn-cost lookup.
func (s *Server) roughBiomeAt(d *dimension, p world.Pos) string {
	return s.quartBiome(d, p.X>>2, p.Y>>2, p.Z>>2)
}

// spawnColumns caches a chunk's per-column heights: the WORLD_SURFACE
// height (getRandomPosWithin), MOTION_BLOCKING and
// MOTION_BLOCKING_NO_LEAVES (getTopNonCollidingPos). A height is the
// first cell above the column's top matching block (minY when none).
type spawnColumns struct {
	valid    [256]bool
	surface  [256]int
	motion   [256]int
	noLeaves [256]int
}

// spawnColumnHeights returns column (x, z)'s heights, scanning it once
// and again only after a block write in that column.
func (s *Server) spawnColumnHeights(d *dimension, x, z int) (surface, motion, noLeaves int) {
	st := &s.naturalSpawn
	ck := [2]int{x >> 4, z >> 4}
	col := (z&15)*16 + x&15
	st.colMu.Lock()
	if st.cols == nil {
		st.cols = map[*world.World]map[[2]int]*spawnColumns{}
	}
	m := st.cols[d.w]
	if m == nil {
		m = map[[2]int]*spawnColumns{}
		st.cols[d.w] = m
		w := d.w
		w.SetChangeHook(func(p world.Pos) { s.spawnColumnDirty(w, p) })
	}
	c := m[ck]
	if c == nil {
		c = &spawnColumns{}
		m[ck] = c
	}
	if c.valid[col] {
		a, b, cc := c.surface[col], c.motion[col], c.noLeaves[col]
		st.colMu.Unlock()
		return a, b, cc
	}
	st.colMu.Unlock()
	top := d.minY + d.height - 1
	blocks := make([]world.Block, d.height)
	d.w.FillBox(world.Pos{X: x, Y: d.minY, Z: z}, world.Pos{X: x, Y: top, Z: z}, blocks)
	hp := world.HeightmapPredicates
	surface, motion, noLeaves = d.minY, d.minY, d.minY
	gotS, gotM, gotN := false, false, false
	for i := len(blocks) - 1; i >= 0 && !(gotS && gotM && gotN); i-- {
		b := blocks[i]
		if b == world.Air {
			continue
		}
		y := d.minY + i + 1
		if !gotS && hp.WorldSurface(b) {
			surface, gotS = y, true
		}
		if !gotM && hp.MotionBlocking(b) {
			motion, gotM = y, true
		}
		if !gotN && hp.MotionBlockingNoLeaves(b) {
			noLeaves, gotN = y, true
		}
	}
	st.colMu.Lock()
	c.valid[col] = true
	c.surface[col], c.motion[col], c.noLeaves[col] = surface, motion, noLeaves
	st.colMu.Unlock()
	return
}

func (s *Server) spawnColumnDirty(w *world.World, p world.Pos) {
	st := &s.naturalSpawn
	st.colMu.Lock()
	if m := st.cols[w]; m != nil {
		if c := m[[2]int{p.X >> 4, p.Z >> 4}]; c != nil {
			c.valid[(p.Z&15)*16+p.X&15] = false
		}
	}
	st.colMu.Unlock()
}

// dimSpawnFacts are the DimensionType fields the spawn rules read:
// ambient_light, monster_spawn_block_light_limit,
// monster_spawn_light_level (a uniform int provider), has_ceiling.
type dimSpawnFacts struct {
	ambient          float32
	blockLightLimit  int
	lightMin, lightM int
	hasCeiling       bool
}

func spawnFactsOf(d *dimension) dimSpawnFacts {
	switch d.name {
	case dimNether:
		return dimSpawnFacts{ambient: 0.1, blockLightLimit: 15, lightMin: 7, lightM: 7, hasCeiling: true}
	case dimEnd:
		return dimSpawnFacts{ambient: 0.25, blockLightLimit: 0, lightMin: 15, lightM: 15}
	}
	return dimSpawnFacts{ambient: 0, blockLightLimit: 0, lightMin: 0, lightM: 7}
}

// spawnSkyDarken is Level.skyDarken = (int)(15 - SKY_LIGHT_LEVEL),
// the environment attribute: the overworld's day timeline multiplies
// the default 15 by its keyframes (133:1, 11867:1, 13670:0.26666668,
// 22330:0.26666668, linear and periodic over 24000), then the weather
// layers alpha-blend toward 4.0 (rain alpha 0.3125 weighted by
// rainLevel - thunderLevel, thunder 0.52734375 weighted by
// thunderLevel). The nether's dimension attribute pins 4.0; the end
// keeps the 15 default.
func (s *Server) spawnSkyDarken(d *dimension) int {
	if s.naturalSpawn.inChunkGen {
		// WorldGenRegion.getSkyDarken is 0.
		return 0
	}
	switch d.name {
	case dimNether:
		return 11
	case dimEnd:
		return 0
	}
	v := float32(15) * daySkyLightFactor(s.dayTime())
	rain := float32(s.weatherRain.Load()) / 1000
	thunder := float32(s.weatherThundLv.Load()) / 1000 * rain
	if w := rain - thunder; w > 0 {
		mod := v + 0.3125*(4-v)
		v = v + w*(mod-v)
	}
	if thunder > 0 {
		mod := v + 0.52734375*(4-v)
		v = v + thunder*(mod-v)
	}
	return int(15 - v)
}

// daySkyLightFactor samples the day timeline's sky_light_level track.
func daySkyLightFactor(dayTime int64) float32 {
	t := float32(((dayTime % 24000) + 24000) % 24000)
	const night = 0.26666668
	switch {
	case t >= 133 && t <= 11867:
		return 1
	case t > 11867 && t < 13670:
		return 1 + (t-11867)/(13670-11867)*(night-1)
	case t >= 13670 && t <= 22330:
		return night
	case t > 22330:
		return night + (t-22330)/(24133-22330)*(1-night)
	default: // t < 133: the wrap segment from 22330 to 24133.
		return night + (t+24000-22330)/(24133-22330)*(1-night)
	}
}

// spawnLight reads (sky, block) light at p from the level's light
// engine, materializing the chunk's light first.
func (s *Server) spawnLight(d *dimension, p world.Pos) (sky, block int) {
	if p.Y < d.minY || p.Y >= d.minY+d.height {
		if d.skylight && p.Y >= d.minY+d.height {
			return 15, 0
		}
		return 0, 0
	}
	d.lightMu.Lock()
	defer d.lightMu.Unlock()
	if d.w.Provided() {
		cx, cz := p.X>>4, p.Z>>4
		st := &s.naturalSpawn
		st.litMu.Lock()
		if st.lit == nil {
			st.lit = map[*world.World]map[[2]int]bool{}
		}
		m := st.lit[d.w]
		if m == nil {
			m = map[[2]int]bool{}
			st.lit[d.w] = m
		}
		done := m[[2]int{cx, cz}]
		st.litMu.Unlock()
		if !done {
			// Light only what exists: materializing a missing
			// neighbour would generate it (and its spawns) just to
			// answer a spawn check.
			all := true
			for dz := -1; dz <= 1; dz++ {
				for dx := -1; dx <= 1; dx++ {
					if d.w.PeekBaseChunk(cx+dx, cz+dz) == nil {
						all = false
						continue
					}
					ensureSkyChunk(d, cx+dx, cz+dz)
					seedGenBlockLight(d, cx+dx, cz+dz)
				}
			}
			if all {
				st.litMu.Lock()
				m[[2]int{cx, cz}] = true
				st.litMu.Unlock()
			}
		}
	}
	if d.skylight {
		sky = skyEffective(d, p)
	}
	return sky, int(d.blockLight.get(p))
}

// rawBrightness is LevelLightEngine.getRawBrightness(pos, amount):
// max(sky - amount, block).
func (s *Server) rawBrightness(d *dimension, p world.Pos, amount int) int {
	sky, blk := s.spawnLight(d, p)
	return max(sky-amount, blk)
}

// maxLocalRawBrightness is getMaxLocalRawBrightness(pos) — the raw
// brightness darkened by the level's skyDarken.
func (s *Server) maxLocalRawBrightness(d *dimension, p world.Pos) int {
	return s.rawBrightness(d, p, s.spawnSkyDarken(d))
}

// lightMagic is getLightLevelDependentMagicValue: the v/(4-3v) curve
// of the max local raw brightness, lerped toward 1 by ambient light.
func (s *Server) lightMagic(d *dimension, p world.Pos) float32 {
	v := float32(s.maxLocalRawBrightness(d, p)) / 15
	f := v / (4 - 3*v)
	a := spawnFactsOf(d).ambient
	return f + a*(1-f)
}

// spawnPlayer is one player as the spawner sees it.
type spawnPlayer struct {
	x, y, z   float64
	cx, cz    int
	spectator bool
	tracked   bool // DistanceManager ticket (skipPlayer false)
	key       int  // index into the local-count table
}

// spawnTickContext is one dimension's SpawnState for one tick.
type spawnTickContext struct {
	d           *dimension
	players     []spawnPlayer
	chunkCount  int
	counts      [catCount]int
	local       []map[mobCategory]int // per player key; nil = no MobCounts yet
	charges     []spawnCharge
	sim         int
	spawn       world.Pos
	spawnInDim  bool
	lastPos     world.Pos
	lastType    string
	lastCharge  float64
	lastChecked bool
	catMask     map[[2]int]uint8
}

type spawnCharge struct {
	p      world.Pos
	charge float64
}

// naturalSpawnTickStep is ServerChunkCache.tickChunks' spawning half,
// once per level tick for every dimension holding players.
func (s *Server) naturalSpawnTickStep() {
	if !s.gameRuleBool("minecraft:spawn_mobs") {
		return
	}
	spawnEnemies := s.gameRuleBool("minecraft:spawn_monsters")
	// tickTime has already advanced the level clock when vanilla's
	// chunk source ticks; mc-go counts its tick at the end of the
	// step, so the cadence reads one ahead.
	spawnPersistent := (s.gameTime()+1)%400 == 0
	for _, name := range []string{dimOverworld, dimNether, dimEnd} {
		d := s.dims[name]
		if d == nil {
			continue
		}
		s.naturalSpawnDim(d, spawnEnemies, spawnPersistent)
	}
}

func (s *Server) spawnPlayersIn(d *dimension) []spawnPlayer {
	var out []spawnPlayer
	genSpec := s.gameRuleBool("minecraft:spectators_generate_chunks")
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.state.Load() != StatePlay || !p.spawned.Load() || p.dimName() != d.name {
			return true
		}
		x, y, z, _, _, _ := p.Pos()
		spec := p.mode() == GameModeSpectator
		out = append(out, spawnPlayer{x: x, y: y, z: z,
			cx: int(math.Floor(x)) >> 4, cz: int(math.Floor(z)) >> 4,
			spectator: spec, tracked: !spec || genSpec, key: len(out)})
		return true
	})
	return out
}

// chunkLevel is the natural-spawn tracker's distance for a chunk: the
// smallest Chebyshev distance to a tracked player's chunk.
func (c *spawnTickContext) chunkLevel(cx, cz int) int {
	best := math.MaxInt32
	for _, p := range c.players {
		if !p.tracked {
			continue
		}
		dx, dz := cx-p.cx, cz-p.cz
		if dx < 0 {
			dx = -dx
		}
		if dz < 0 {
			dz = -dz
		}
		best = min(best, max(dx, dz))
	}
	return best
}

// closeForSpawning is ChunkMap.playerIsCloseEnoughForSpawning: a
// non-spectator within 128 blocks (horizontal) of the chunk centre.
func closeForSpawning(p spawnPlayer, cx, cz int) bool {
	if p.spectator {
		return false
	}
	dx, dz := float64(cx*16+8)-p.x, float64(cz*16+8)-p.z
	return dx*dx+dz*dz < 16384
}

// playersCloseForSpawning is ChunkMap.getPlayersCloseForSpawning.
func (c *spawnTickContext) playersCloseForSpawning(cx, cz int) []spawnPlayer {
	if c.chunkLevel(cx, cz) > 8 {
		return nil
	}
	var out []spawnPlayer
	for _, p := range c.players {
		if closeForSpawning(p, cx, cz) {
			out = append(out, p)
		}
	}
	return out
}

// anyPlayerCloseForSpawning is ChunkMap.anyPlayerCloseEnoughForSpawning:
// level <= floor(8/sqrt2) = 5 answers true outright, > 8 false.
func (c *spawnTickContext) anyPlayerCloseForSpawning(cx, cz int) bool {
	lv := c.chunkLevel(cx, cz)
	if lv <= 5 {
		return true
	}
	if lv > 8 {
		return false
	}
	for _, p := range c.players {
		if closeForSpawning(p, cx, cz) {
			return true
		}
	}
	return false
}

// entityTicking is DistanceManager.inEntityTickingRange for the
// player simulation tickets: Chebyshev distance <= simulation
// distance from a tracked player's chunk.
func (c *spawnTickContext) entityTicking(cx, cz int) bool {
	for _, p := range c.players {
		if !p.tracked {
			continue
		}
		dx, dz := cx-p.cx, cz-p.cz
		if dx < 0 {
			dx = -dx
		}
		if dz < 0 {
			dz = -dz
		}
		if max(dx, dz) <= c.sim {
			return true
		}
	}
	return false
}

// chunkLoaded reports whether a chunk exists without generating it.
func chunkLoaded(d *dimension, cx, cz int) bool {
	if !d.w.Provided() {
		return true
	}
	return d.w.PeekBaseChunk(cx, cz) != nil
}

// chunkSpawnReady is a loaded chunk whose eight neighbours are loaded
// too — vanilla's ticking chunks always are. Spawn checks read light
// and blocks across the border; in a generated world reading a
// missing neighbour would generate it on the spot.
func chunkSpawnReady(d *dimension, cx, cz int) bool {
	if !d.w.Provided() {
		return true
	}
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			if d.w.PeekBaseChunk(cx+dx, cz+dz) == nil {
				return false
			}
		}
	}
	return true
}

// dimMaySpawn is a cheap early-out for single-biome levels: when the
// level's biome and every /fillbiome cell list nothing, no position
// can roll a spawner entry (the flat comparison world's the_void).
func (s *Server) dimMaySpawn(d *dimension) bool {
	if d.w.Provided() {
		return true
	}
	spawnable := func(b string) bool {
		bs := s.biomeSpawns(b)
		for c := mobCategory(0); c < catMisc; c++ {
			if !bs.empty(c) {
				return true
			}
		}
		return false
	}
	if spawnable(d.w.Biome()) {
		return true
	}
	d.biomesMu.RLock()
	defer d.biomesMu.RUnlock()
	checked := map[string]bool{}
	for _, b := range d.biomes {
		if checked[b] {
			continue
		}
		checked[b] = true
		if spawnable(b) {
			return true
		}
	}
	return false
}

func (s *Server) naturalSpawnDim(d *dimension, spawnEnemies, spawnPersistent bool) {
	if !s.dimMaySpawn(d) {
		return
	}
	players := s.spawnPlayersIn(d)
	if len(players) == 0 {
		return
	}
	c := &spawnTickContext{d: d, players: players, local: make([]map[mobCategory]int, len(players))}
	c.sim = s.SimulationDistance
	if c.sim == 0 {
		c.sim = 10
	}
	// DistanceManager.getNaturalSpawnChunkCount: the union of every
	// tracked player's 17x17 square.
	seen := map[[2]int]bool{}
	for _, p := range players {
		if !p.tracked {
			continue
		}
		for dx := -8; dx <= 8; dx++ {
			for dz := -8; dz <= 8; dz++ {
				seen[[2]int{p.cx + dx, p.cz + dz}] = true
			}
		}
	}
	c.chunkCount = len(seen)
	if d.name == dimOverworld {
		sx, sy, sz := s.WorldSpawn()
		c.spawn = world.Pos{X: int(math.Floor(sx)), Y: int(math.Floor(sy)), Z: int(math.Floor(sz))}
		c.spawnInDim = true
	}
	// Spawn candidates: natural-spawn chunks that are loaded, ticking
	// and have a player in reach (ChunkMap.collectSpawningChunks).
	var cands [][2]int
	for k := range seen {
		if !c.entityTicking(k[0], k[1]) || !chunkSpawnReady(d, k[0], k[1]) || !c.anyPlayerCloseForSpawning(k[0], k[1]) {
			continue
		}
		cands = append(cands, k)
	}
	if len(cands) == 0 {
		return
	}
	c.catMask = map[[2]int]uint8{}
	// Only chunks whose biomes list anything matter; skip the whole
	// state build when no candidate could spawn at all.
	any := false
	for _, k := range cands {
		if s.chunkMaySpawn(c, k) {
			any = true
			break
		}
	}
	if !any {
		return
	}
	s.buildSpawnCounts(c)
	var cats []mobCategory
	for cat := mobCategory(0); cat < catMisc; cat++ {
		info := mobCategoryInfo[cat]
		if !spawnEnemies && !info.friendly {
			continue
		}
		if !spawnPersistent && info.persistent {
			continue
		}
		if c.counts[cat] >= info.max*c.chunkCount/289 {
			continue
		}
		cats = append(cats, cat)
	}
	if len(cats) == 0 {
		return
	}
	r := s.spawnRng()
	// Util.shuffle(spawningChunks, level.random).
	for i := len(cands) - 1; i > 0; i-- {
		j := int(r.NextIntN(int32(i + 1)))
		cands[i], cands[j] = cands[j], cands[i]
	}
	for _, k := range cands {
		if !s.chunkMaySpawn(c, k) {
			continue
		}
		for _, cat := range cats {
			if !c.canSpawnLocal(cat, k[0], k[1]) {
				continue
			}
			if !s.chunkMaySpawnCat(c, k, cat) {
				continue
			}
			s.spawnCategoryForChunk(c, cat, k[0], k[1], r)
		}
	}
}

// chunkCatMasks is a per-dimension cache: for every chunk, the bitmask
// of categories its own quart biomes list anything for. Generated
// biome cells never change; /fillbiome bumps biomesRev and rebuilds
// the override index.
type chunkCatMasks struct {
	w         *world.World
	rev       uint64
	overrides map[[2]int]map[string]bool
	masks     map[[2]int]uint8
}

func (s *Server) biomeCatMask(b string) uint8 {
	bs := s.biomeSpawns(b)
	var m uint8
	for c := mobCategory(0); c < catMisc; c++ {
		if !bs.empty(c) {
			m |= 1 << c
		}
	}
	return m
}

// ownCatMask is the category mask of chunk k's own biome cells.
func (s *Server) ownCatMask(d *dimension, k [2]int) uint8 {
	st := &s.naturalSpawn
	st.biomeMu.Lock()
	if st.catMasks == nil {
		st.catMasks = map[*dimension]*chunkCatMasks{}
	}
	cm := st.catMasks[d]
	st.biomeMu.Unlock()
	d.biomesMu.RLock()
	rev := d.biomesRev
	if cm == nil || cm.w != d.w || cm.rev != rev {
		cm = &chunkCatMasks{w: d.w, rev: rev, overrides: map[[2]int]map[string]bool{}, masks: map[[2]int]uint8{}}
		for q, b := range d.biomes {
			ck := [2]int{q[0] >> 2, q[2] >> 2}
			if cm.overrides[ck] == nil {
				cm.overrides[ck] = map[string]bool{}
			}
			cm.overrides[ck][b] = true
		}
		st.biomeMu.Lock()
		st.catMasks[d] = cm
		st.biomeMu.Unlock()
	}
	d.biomesMu.RUnlock()
	if m, ok := cm.masks[k]; ok {
		return m
	}
	var m uint8
	if bc := d.w.PeekBaseChunk(k[0], k[1]); d.w.Provided() && bc != nil {
		seen := map[string]bool{}
		for _, b := range bc.Biomes {
			if !seen[b] {
				seen[b] = true
				m |= s.biomeCatMask(b)
			}
		}
	} else {
		m = s.biomeCatMask(d.w.Biome())
	}
	for b := range cm.overrides[k] {
		m |= s.biomeCatMask(b)
	}
	if !d.w.Provided() || d.w.PeekBaseChunk(k[0], k[1]) != nil {
		cm.masks[k] = m
	}
	return m
}

// chunkCatMask is the categories a candidate chunk could spawn: its
// own cells and its neighbours' (the biome zoom reaches across a chunk
// border).
func (s *Server) chunkCatMask(c *spawnTickContext, k [2]int) uint8 {
	if m, ok := c.catMask[k]; ok {
		return m
	}
	var m uint8
	for dx := -1; dx <= 1; dx++ {
		for dz := -1; dz <= 1; dz++ {
			m |= s.ownCatMask(c.d, [2]int{k[0] + dx, k[1] + dz})
		}
	}
	c.catMask[k] = m
	return m
}

func (s *Server) chunkMaySpawn(c *spawnTickContext, k [2]int) bool {
	return s.chunkCatMask(c, k) != 0
}

func (s *Server) chunkMaySpawnCat(c *spawnTickContext, k [2]int, cat mobCategory) bool {
	return s.chunkCatMask(c, k)&(1<<cat) != 0
}

// buildSpawnCounts is NaturalSpawner.createState's entity pass.
func (s *Server) buildSpawnCounts(c *spawnTickContext) {
	d := c.d
	s.entities.mu.RLock()
	defer s.entities.mu.RUnlock()
	for _, e := range s.entities.byID {
		if e.Dim != d.name || !e.Living || e.Dormant || e.presenceOnly {
			continue
		}
		name, _ := EntityTypeName(e.Type)
		if name == "player" {
			continue
		}
		cat := entityCategoryOf(name)
		if cat == catMisc {
			continue
		}
		if e.PersistenceRequired || mobRequiresCustomPersistence(e, name) {
			continue
		}
		p := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)), Z: int(math.Floor(e.Z))}
		cx, cz := p.X>>4, p.Z>>4
		if !chunkLoaded(d, cx, cz) {
			continue
		}
		if cost, ok := s.biomeSpawns(s.roughBiomeAt(d, p)).costFor(name); ok && cost[0] != 0 {
			c.charges = append(c.charges, spawnCharge{p, cost[0]})
		}
		for _, pl := range c.playersCloseForSpawning(cx, cz) {
			if c.local[pl.key] == nil {
				c.local[pl.key] = map[mobCategory]int{}
			}
			c.local[pl.key][cat]++
		}
		c.counts[cat]++
	}
}

func (b *biomeSpawnSettings) costFor(name string) ([2]float64, bool) {
	if b == nil {
		return [2]float64{}, false
	}
	v, ok := b.costs[name]
	return v, ok
}

// canSpawnLocal is SpawnState.canSpawnForCategoryLocal: some player
// near the chunk whose MobCounts is absent or below the category max.
func (c *spawnTickContext) canSpawnLocal(cat mobCategory, cx, cz int) bool {
	for _, p := range c.playersCloseForSpawning(cx, cz) {
		m := c.local[p.key]
		if m == nil || m[cat] < mobCategoryInfo[cat].max {
			return true
		}
	}
	return false
}

// potentialChange is PotentialCalculator.getPotentialEnergyChange.
func (c *spawnTickContext) potentialChange(p world.Pos, charge float64) float64 {
	if charge == 0 {
		return 0
	}
	sum := 0.0
	for _, pc := range c.charges {
		dx, dy, dz := float64(pc.p.X-p.X), float64(pc.p.Y-p.Y), float64(pc.p.Z-p.Z)
		d2 := dx*dx + dy*dy + dz*dz
		if d2 == 0 {
			sum += math.Inf(1)
			continue
		}
		sum += pc.charge / math.Sqrt(d2)
	}
	return sum * charge
}

// canSpawnCharge is SpawnState.canSpawn — the spawn_costs budget.
func (s *Server) canSpawnCharge(c *spawnTickContext, name string, p world.Pos) bool {
	c.lastPos, c.lastType, c.lastChecked = p, name, true
	cost, ok := s.biomeSpawns(s.roughBiomeAt(c.d, p)).costFor(name)
	if !ok {
		c.lastCharge = 0
		return true
	}
	c.lastCharge = cost[0]
	return c.potentialChange(p, cost[0]) <= cost[1]
}

// afterSpawn is SpawnState.afterSpawn: the new mob's charge, its
// category count and every nearby player's local count.
func (s *Server) afterSpawn(c *spawnTickContext, name string, p world.Pos) {
	charge := 0.0
	if c.lastChecked && p == c.lastPos && name == c.lastType {
		charge = c.lastCharge
	} else if cost, ok := s.biomeSpawns(s.roughBiomeAt(c.d, p)).costFor(name); ok {
		charge = cost[0]
	}
	if charge != 0 {
		c.charges = append(c.charges, spawnCharge{p, charge})
	}
	cat := entityCategoryOf(name)
	c.counts[cat]++
	for _, pl := range c.playersCloseForSpawning(p.X>>4, p.Z>>4) {
		if c.local[pl.key] == nil {
			c.local[pl.key] = map[mobCategory]int{}
		}
		c.local[pl.key][cat]++
	}
}

// spawnCategoryForChunk picks the random position (getRandomPosWithin:
// x/z uniform in the chunk, y uniform in [minY, WORLD_SURFACE+1]) and
// gives up below minY+1.
func (s *Server) spawnCategoryForChunk(c *spawnTickContext, cat mobCategory, cx, cz int, r spawnRandom) {
	d := c.d
	x := cx*16 + int(r.NextIntN(16))
	z := cz*16 + int(r.NextIntN(16))
	surface, _, _ := s.spawnColumnHeights(d, x, z)
	top := surface + 1
	y := int(r.NextIntN(int32(top-d.minY+1))) + d.minY
	if y < d.minY+1 {
		return
	}
	s.spawnCategoryForPosition(c, cat, cx, cz, world.Pos{X: x, Y: y, Z: z}, r)
}

// nearestSpawnPlayer is getNearestPlayer(x, y, z, -1, false): the
// closest non-spectator, unbounded.
func (c *spawnTickContext) nearestSpawnPlayer(x, y, z float64) (float64, bool) {
	best, ok := math.MaxFloat64, false
	for _, p := range c.players {
		if p.spectator {
			continue
		}
		dx, dy, dz := p.x-x, p.y-y, p.z-z
		if d := dx*dx + dy*dy + dz*dz; d < best {
			best, ok = d, true
		}
	}
	return best, ok
}

// spawnCategoryForPosition is NaturalSpawner.spawnCategoryForPosition:
// a redstone-conductor start cell aborts; then three packs, each of
// ceil(nextFloat*4) attempts walking ±5 (nextInt(6)-nextInt(6)) on x
// and z at the start height. The first attempt that clears the
// player/spawn distance rolls the pack's spawner entry (and its size,
// min + nextInt(1+max-min)); each attempt then runs the type's
// position checks, the spawn-cost budget, the mob checks and
// finalizeSpawn(NATURAL). The chunk call stops once
// getMaxSpawnClusterSize mobs spawned in it; a pack stops once
// isMaxGroupSizeReached (non-schooling tropical fish).
func (s *Server) spawnCategoryForPosition(c *spawnTickContext, cat mobCategory, cx, cz int, pos world.Pos, r spawnRandom) {
	d := c.d
	if worldgen.RedstoneConductor(d.w.GetBlock(pos)) {
		return
	}
	k := 0
	for pack := 0; pack < 3; pack++ {
		x, z := pos.X, pos.Z
		var entry spawnerEntry
		have := false
		n := int(math.Ceil(float64(r.NextFloat() * 4)))
		group := 0
		for a := 0; a < n; a++ {
			x += int(r.NextIntN(6)) - int(r.NextIntN(6))
			z += int(r.NextIntN(6)) - int(r.NextIntN(6))
			mp := world.Pos{X: x, Y: pos.Y, Z: z}
			fx, fz := float64(x)+0.5, float64(z)+0.5
			dist, ok := c.nearestSpawnPlayer(fx, float64(pos.Y), fz)
			if !ok {
				continue
			}
			if !s.rightDistanceForSpawn(c, cx, cz, mp, dist) {
				continue
			}
			if !have {
				e, ok := s.randomSpawnEntry(d, cat, mp, r)
				if !ok {
					break
				}
				entry, have = e, true
				n = e.min + int(r.NextIntN(int32(1+e.max-e.min)))
			}
			if !s.validSpawnPositionForType(d, cat, entry, mp, dist, r) {
				continue
			}
			if !s.canSpawnCharge(c, entry.typ, mp) {
				continue
			}
			if _, known := EntityTypeID(entry.typ); !known {
				return
			}
			yaw := r.NextFloat() * 360
			if !s.validPositionForMob(d, entry.typ, fx, float64(pos.Y), fz, dist) {
				continue
			}
			e := s.naturalSpawnMob(d, entry.typ, fx, float64(pos.Y), fz, yaw, false, r)
			if e == nil {
				return
			}
			k++
			group++
			s.afterSpawn(c, entry.typ, mp)
			if k >= maxSpawnClusterSize(entry.typ) {
				return
			}
			// TropicalFish.isMaxGroupSizeReached is !isSchool: the
			// pack's first fish (no group data yet) rolls nextFloat <
			// 0.9 for a common-variant school, else a lone fish.
			if entry.typ == "tropical_fish" && group == 1 && r.NextFloat() >= 0.9 {
				break
			}
		}
	}
}

// rightDistanceForSpawn is isRightDistanceToPlayerAndSpawnPoint: more
// than 24 blocks from the nearest player, not within 24 of the world
// spawn's block centre (overworld only), and in this chunk or a chunk
// that can spawn entities (entity-ticking).
func (s *Server) rightDistanceForSpawn(c *spawnTickContext, cx, cz int, p world.Pos, dist float64) bool {
	if dist <= 576 {
		return false
	}
	if c.spawnInDim {
		dx := float64(c.spawn.X) + 0.5 - (float64(p.X) + 0.5)
		dy := float64(c.spawn.Y) + 0.5 - float64(p.Y)
		dz := float64(c.spawn.Z) + 0.5 - (float64(p.Z) + 0.5)
		if dx*dx+dy*dy+dz*dz < 576 {
			return false
		}
	}
	pcx, pcz := p.X>>4, p.Z>>4
	if pcx == cx && pcz == cz {
		return true
	}
	return c.entityTicking(pcx, pcz) && chunkSpawnReady(c.d, pcx, pcz)
}

// randomSpawnEntry is getRandomSpawnMobAt: water ambient in a
// reduce_water_ambient_spawns biome skips 98% of rolls; otherwise the
// zoomed biome's weighted list (no structure overrides — mc-go
// generates none).
func (s *Server) randomSpawnEntry(d *dimension, cat mobCategory, p world.Pos, r spawnRandom) (spawnerEntry, bool) {
	biome := s.spawnBiomeAt(d, p)
	if cat == catWaterAmbient && s.biomeHasTag("reduce_water_ambient_spawns", biome) && r.NextFloat() < 0.98 {
		return spawnerEntry{}, false
	}
	return s.biomeSpawns(biome).pick(cat, r)
}

// validSpawnPositionForType is isValidSpawnPostitionForType.
func (s *Server) validSpawnPositionForType(d *dimension, cat mobCategory, e spawnerEntry, p world.Pos, dist float64, r spawnRandom) bool {
	tc := entityCategoryOf(e.typ)
	if tc == catMisc {
		return false
	}
	dd := float64(mobCategoryInfo[tc].despawn)
	if !spawnFarFromPlayer[e.typ] && dist > dd*dd {
		return false
	}
	// canSpawnMobAt: the attempt's own biome must still list the
	// pack's entry.
	if !s.biomeSpawns(s.spawnBiomeAt(d, p)).contains(cat, e) {
		return false
	}
	if !s.spawnPositionOk(d, e.typ, p) {
		return false
	}
	if !s.checkSpawnRules(d, e.typ, p, r, false) {
		return false
	}
	w, h := spawnAABBDims(e.typ)
	return !s.spawnBoxCollides(d, float64(p.X)+0.5-w/2, float64(p.Y), float64(p.Z)+0.5-w/2,
		float64(p.X)+0.5+w/2, float64(p.Y)+h, float64(p.Z)+0.5+w/2)
}

// spawnAABBDims is EntityType.getSpawnAABB's size: the type's base
// dimensions times spawnDimensionsScale.
func spawnAABBDims(name string) (w, h float64) {
	id, _ := EntityTypeID(name)
	b := mobBounds(id)
	sc := 1.0
	if v, ok := spawnDimsScale[name]; ok {
		sc = v
	}
	return b[0] * sc, b[1] * sc
}

// validPositionForMob is isValidPositionForMob on the fresh mob:
// beyond the category despawn distance a removeWhenFarAway mob is
// refused, then Mob.checkSpawnRules (PathfinderMob: walk target >= 0)
// and checkSpawnObstruction.
func (s *Server) validPositionForMob(d *dimension, name string, x, y, z, dist float64) bool {
	dd := float64(mobCategoryInfo[entityCategoryOf(name)].despawn)
	if dist > dd*dd && freshRemoveWhenFarAway(name) {
		return false
	}
	p := world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}
	if s.walkTargetValue(d, name, p) < 0 {
		return false
	}
	return s.spawnObstructionOk(d, name, x, y, z, true)
}

// freshRemoveWhenFarAway is removeWhenFarAway for a just-created mob
// (tickCount 0, untamed, not from a bucket, unnamed).
func freshRemoveWhenFarAway(name string) bool {
	switch name {
	case "ocelot", "cat", "villager", "wandering_trader", "iron_golem",
		"snow_golem", "allay", "warden", "chicken":
		return false
	case "camel_husk", "zombie_horse", "hoglin", "nautilus", "zombie_nautilus",
		"axolotl":
		return true
	}
	if despawnAnimalTypes[name] {
		return false
	}
	return true
}

// naturalSpawnMob creates and finalizes one naturally spawned mob:
// snapTo(x, y, z, yaw, 0), then Mob.finalizeSpawn's NATURAL (or
// CHUNK_GENERATION) tail — variant rolls against the spawn biome,
// equipment, attribute rolls, the cube-mob size — and the fresh
// entity's pairing broadcast.
func (s *Server) naturalSpawnMob(d *dimension, name string, x, y, z float64, yaw float32, chunkGen bool, r spawnRandom) *Entity {
	e, ok := s.summonMobRaw(d, name, x, y, z, false, true)
	if !ok {
		return nil
	}
	e.Yaw = yaw
	e.HeadYaw, e.LookYaw = yaw, yaw
	pos := world.Pos{X: int(math.Floor(x)), Y: int(math.Floor(y)), Z: int(math.Floor(z))}
	biome := strings.TrimPrefix(s.spawnBiomeAt(d, pos), "minecraft:")
	if v := rollSpawnVariant(name, biome); v != "" {
		e.VariantName = v
	}
	e.SoundVariant = rollSoundVariant(name)
	s.mobDefaultEquip(e)
	if raidRaiderType(name) {
		// Raider.finalizeSpawn: canJoinRaid = !isWitch || reason !=
		// NATURAL.
		e.CanJoinRaid = name != "witch" || chunkGen
	}
	if name == "slime" || name == "magma_cube" {
		// AbstractCubeMob.setSpawnSize: nextInt(3), bumped once on a
		// 0.5·special roll below 2, size 1 << i.
		i := rand.Intn(3)
		if i < 2 && rand.Float64() < 0.5*s.specialMultiplier() {
			i++
		}
		size := int32(1) << i
		e.CubeSize = size
		if e.AttrBase == nil {
			e.AttrBase = map[string]float64{}
		}
		e.AttrBase["max_health"] = float64(size * size)
		e.AttrBase["movement_speed"] = float64(float32(0.2) + float32(0.1)*float32(size))
		if name == "slime" {
			e.AttrBase["attack_damage"] = float64(size)
		}
		e.Health = float32(size * size)
		e.MaxHealth = e.Health
	}
	if !e.Dormant {
		s.broadcastFreshMob(e)
	}
	return e
}

// mobRequiresCustomPersistence is Mob.requiresCustomPersistence
// (passenger or leashed) plus the per-type overrides: EnderMan
// carrying a block, AbstractFish/Axolotl from a bucket, a tamed
// AbstractNautilus, a SulfurCube holding a body item or bucketed, a
// Raider in a raid.
func mobRequiresCustomPersistence(e *Entity, name string) bool {
	if e.Riding != 0 || e.LeashHolder != 0 {
		return true
	}
	switch name {
	case "enderman":
		return e.EnderCarryName != ""
	case "cod", "salmon", "pufferfish", "tropical_fish", "tadpole", "axolotl":
		return e.FromBucket
	case "nautilus", "zombie_nautilus":
		return e.Tamed
	case "sulfur_cube":
		return !e.BodyItem.Empty() || e.FromBucket
	}
	if e.Raid != nil && raidRaiderType(name) {
		return true
	}
	return false
}
