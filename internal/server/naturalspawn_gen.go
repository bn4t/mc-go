package server

import (
	"math"
	"strings"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// Chunk-generation spawns — NaturalSpawner.spawnMobsForChunkGeneration,
// run by NoiseBasedChunkGenerator.spawnOriginalMobs at the SPAWN chunk
// status (skipped when the noise settings disable_mob_generation; only
// the end's do). Measured on 26.2 by bytecode inspection:
//
//   - the biome is the region's zoomed getBiome at (minX, maxY, minZ);
//     only its CREATURE list is used, and nothing happens with an empty
//     list or with spawn_mobs off;
//   - the random is a WorldgenRandom seeded setDecorationSeed(levelSeed,
//     minX, minZ), so what spawns where is a pure function of the
//     world seed and the chunk;
//   - while nextFloat < creature_spawn_probability, a pack: a weighted
//     entry, min + nextInt(1+max-min) members, a start column x/z =
//     min + nextInt(16); each member gets up to 4 tries at
//     getTopNonCollidingPos (the type's heightmap, walked down under a
//     ceiling, then SpawnPlacementType.adjustSpawnPosition — ON_GROUND
//     steps onto a pathfindable block below), the placement check, the
//     spawn AABB at the x/z clamped [min+width, min+16-width], the
//     spawn rules (CHUNK_GENERATION), the mob's own checks and
//     finalizeSpawn; between tries the column walks ±4
//     (nextInt(5)-nextInt(5)) until it lands back in the chunk, the
//     retry starting over from the pack's start column.

// queueChunkGenSpawn records a freshly generated chunk; ProvideChunk
// calls it from whatever goroutine generated the chunk.
func (s *Server) queueChunkGenSpawn(dim string, cx, cz int, biome string) {
	st := &s.naturalSpawn
	st.genMu.Lock()
	st.genPending = append(st.genPending, pendingGenSpawn{dim: dim, cx: cx, cz: cz, biome: biome})
	st.genMu.Unlock()
}

// chunkGenSpawnStep drains the queue under simulation ownership. It
// runs on the wall tick too: chunk generation is not paused by /tick
// freeze, and neither are its spawns.
func (s *Server) chunkGenSpawnStep() {
	st := &s.naturalSpawn
	st.genMu.Lock()
	pend := st.genPending
	st.genPending = nil
	st.genMu.Unlock()
	if len(pend) == 0 {
		return
	}
	var keep []pendingGenSpawn
	windows := map[string][][3]int{}
	s.sessions.Range(func(_, v any) bool {
		p := v.(*Session)
		if p.state.Load() != StatePlay {
			return true
		}
		x, _, z, _, _, _ := p.Pos()
		r := max(p.viewRadius(s.ViewDistance), 1)
		windows[p.dimName()] = append(windows[p.dimName()], [3]int{int(math.Floor(x)) >> 4, int(math.Floor(z)) >> 4, r})
		return true
	})
	for _, p := range pend {
		d := s.dims[p.dim]
		if d == nil || d.terrain == nil {
			continue
		}
		// A chunk reaches the SPAWN status only on its way to FULL:
		// chunks generated merely as neighbours (light, features)
		// wait until a player's window takes them in and their 3x3 is
		// there — otherwise the spawn checks' light reads would pull
		// in ever more neighbours.
		if !chunkGenSpawnReady(d, p.cx, p.cz, windows[p.dim]) {
			keep = append(keep, p)
			continue
		}
		s.spawnMobsForChunkGeneration(d, p.cx, p.cz, p.biome)
	}
	if len(keep) > 0 {
		st.genMu.Lock()
		st.genPending = append(keep, st.genPending...)
		st.genMu.Unlock()
	}
}

func chunkGenSpawnReady(d *dimension, cx, cz int, windows [][3]int) bool {
	in := false
	for _, w := range windows {
		if absi(cx-w[0]) <= w[2] && absi(cz-w[1]) <= w[2] {
			in = true
			break
		}
	}
	if !in {
		return false
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

// chunkGenSpawnHeight is getTopNonCollidingPos: the type's heightmap
// height; under a ceiling the column walks down to the first air below
// the roof and on through it to the floor; ON_GROUND then steps down
// onto a pathfindable (LAND) block below.
func (s *Server) chunkGenSpawnHeight(d *dimension, name string, x, z int) world.Pos {
	_, motion, noLeaves := s.spawnColumnHeights(d, x, z)
	y := noLeaves
	if spawnPlacements[name].hm == hmMotionBlocking {
		y = motion
	}
	p := world.Pos{X: x, Y: y, Z: z}
	if spawnFactsOf(d).hasCeiling {
		for {
			p.Y--
			if worldgen.IsAir(d.w.GetBlock(p)) {
				break
			}
		}
		for {
			p.Y--
			if !worldgen.IsAir(d.w.GetBlock(p)) || p.Y <= d.minY {
				break
			}
		}
	}
	if spawnPlacements[name].place == placeOnGround {
		if b := below(p); pathfindableLand(d.w.GetBlock(b)) {
			return b
		}
	}
	return p
}

// pathfindableLand is BlockState.isPathfindable(LAND): no full
// collision cube by default; liquids unless lava; snow below 5
// layers; powder snow always; mud, soul sand, slabs, stairs, dirt
// path, farmland and cactus never.
func pathfindableLand(b world.Block) bool {
	n := blockName(b)
	switch n {
	case "water", "bubble_column":
		return true
	case "lava":
		return false
	case "snow":
		l, _ := world.StateProperty(b, "layers")
		return l < "5" && len(l) == 1
	case "powder_snow":
		return true
	case "mud", "soul_sand", "dirt_path", "farmland", "cactus":
		return false
	}
	if strings.HasSuffix(n, "_slab") || strings.HasSuffix(n, "_stairs") {
		return false
	}
	return !worldgen.FullCollision(b)
}

func (s *Server) spawnMobsForChunkGeneration(d *dimension, cx, cz int, biome string) {
	if !s.gameRuleBool("minecraft:spawn_mobs") || d.name == dimEnd {
		return
	}
	settings := s.biomeSpawns(biome)
	s.naturalSpawn.inChunkGen = true
	defer func() { s.naturalSpawn.inChunkGen = false }()
	if settings.empty(catCreature) {
		return
	}
	r := worldgen.ChunkGenSpawnRandom(s.WorldSeed, int32(cx), int32(cz))
	minX, minZ := cx*16, cz*16
	for r.NextFloat() < settings.creatureProb {
		e, ok := settings.pick(catCreature, r)
		if !ok {
			continue
		}
		n := e.min + int(r.NextIntN(int32(1+e.max-e.min)))
		x := minX + int(r.NextIntN(16))
		z := minZ + int(r.NextIntN(16))
		x0, z0 := x, z
		_, known := EntityTypeID(e.typ)
		for m := 0; m < n; m++ {
			placed := false
			for try := 0; !placed && try < 4; try++ {
				p := s.chunkGenSpawnHeight(d, e.typ, x, z)
				if known && s.spawnPositionOk(d, e.typ, p) {
					w, h := spawnAABBDims(e.typ)
					id, _ := EntityTypeID(e.typ)
					width := float64(float32(mobBounds(id)[0]))
					fx := math.Min(math.Max(float64(x), float64(minX)+width), float64(minX)+16-width)
					fz := math.Min(math.Max(float64(z), float64(minZ)+width), float64(minZ)+16-width)
					fy := float64(p.Y)
					// A colliding spawn box or a failed spawn rule
					// skips the walk: the next try reuses the column.
					if s.boxCollides(d, fx-w/2, fy, fz-w/2, fx+w/2, fy+h, fz+w/2) {
						continue
					}
					bp := world.Pos{X: int(math.Floor(fx)), Y: p.Y, Z: int(math.Floor(fz))}
					if !s.checkSpawnRules(d, e.typ, bp, s.spawnRng(), true) {
						continue
					}
					yaw := r.NextFloat() * 360
					if s.walkTargetValue(d, e.typ, bp) >= 0 && s.spawnObstructionOk(d, e.typ, fx, fy, fz, false) {
						if s.naturalSpawnMob(d, e.typ, fx, fy, fz, yaw, true, s.spawnRng()) != nil {
							placed = true
						}
					}
				}
				// The walk: ±4 per axis, rerolled from the pack start
				// until the column is back inside the chunk.
				x += int(r.NextIntN(5)) - int(r.NextIntN(5))
				z += int(r.NextIntN(5)) - int(r.NextIntN(5))
				for x < minX || x >= minX+16 || z < minZ || z >= minZ+16 {
					x = x0 + int(r.NextIntN(5)) - int(r.NextIntN(5))
					z = z0 + int(r.NextIntN(5)) - int(r.NextIntN(5))
				}
			}
		}
	}
}
