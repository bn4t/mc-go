package server

import (
	"fmt"
	"math"
	"strings"

	"mc-go/internal/world"
	"mc-go/internal/worldgen"
)

// applyLevelType switches the dimensions to the configured generator.
// "flat" (the default) keeps the flat overworld preset and the nether
// and End stand-ins; "normal" generates vanilla's noise terrain in all
// three — biomes, terrain shape, aquifers, ore veins and the surface —
// from WorldSeed.
func (s *Server) applyLevelType() error {
	switch s.LevelType {
	case "", "flat":
		return nil
	case "normal", "minecraft:normal":
	default:
		return fmt.Errorf("unsupported level type %q", s.LevelType)
	}
	for _, name := range []string{dimOverworld, dimNether, dimEnd} {
		if err := s.useNoiseTerrain(s.dims[name]); err != nil {
			return err
		}
	}
	d := s.dims[dimOverworld]
	d.w.SetBiome("minecraft:plains")
	d.biome = "minecraft:plains"
	s.spawnMu.Lock()
	have := s.haveSpawn
	s.spawnMu.Unlock()
	if !have {
		x, y, z := s.findNoiseSpawn(d)
		s.setWorldSpawn(x, y, z)
	}
	return nil
}

// useNoiseTerrain gives a dimension its vanilla generator. A world that
// is not a flat world reports flat=false for every dimension.
func (s *Server) useNoiseTerrain(d *dimension) error {
	gen, err := worldgen.NewDimensionGenerator(d.name, s.WorldSeed)
	if err != nil {
		return err
	}
	terrain := world.NewNoiseTerrain(gen)
	d.w.SetGenerator(terrain.Block)
	d.w.SetBiomeSource(terrain.Biome)
	d.seaLevel = int32(gen.RandomState().Settings.SeaLevel)
	d.flat = false
	d.arrivalY = int(d.seaLevel)
	d.terrain = terrain
	return nil
}

// terrainReady reports whether column (cx, cz) can be read without
// generating it on the caller's goroutine; if not, it is queued.
func (d *dimension) terrainReady(cx, cz int) bool {
	if d.terrain == nil || d.terrain.Ready(cx, cz) {
		return true
	}
	d.terrain.Prefetch(cx, cz)
	return false
}

// adoptGenerated takes on column (cx, cz)'s generated block entities
// and entities, once, as its terrain is first served: loot chests armed
// with their table and seed, bee nests with their bees, End crystals.
// Each one's anchor block (its own, or the block under an entity)
// becomes a stored block, so the column saves with them and a reload
// that finds the anchor stored knows they were taken on already. A
// generated block entity whose block has since been replaced is not.
// Runs under simulation ownership.
func (s *Server) adoptGenerated(d *dimension, cx, cz int) {
	if d.terrain == nil {
		return
	}
	key := [2]int{cx, cz}
	if d.adopted[key] {
		return
	}
	if d.adopted == nil {
		d.adopted = map[[2]int]bool{}
	}
	d.adopted[key] = true
	c := d.terrain.Chunk(cx, cz)
	for p, be := range c.BlockEntities {
		pos := world.Pos{X: int(p.X), Y: int(p.Y), Z: int(p.Z)}
		if d.w.HasOverride(pos) {
			continue
		}
		switch be.ID {
		case "minecraft:chest":
			if s.peekChest(d, pos) != nil {
				continue
			}
			ch := s.chestAt(d, pos)
			d.chests.mu.Lock()
			ch.lootTable, ch.lootSeed = be.LootTable, be.LootSeed
			d.chests.mu.Unlock()
		case "minecraft:beehive", "minecraft:bee_nest":
			if d.bees == nil {
				d.bees = map[world.Pos]int32{}
			}
			d.bees[pos] = int32(len(be.BeeTicks))
		default:
			// Spawners and End gateways keep no state here yet.
			continue
		}
		d.w.SetBlock(pos, d.w.GetBlock(pos))
	}
	// Fluids the features queued and the aquifers marked flow once the
	// column is live.
	for _, ft := range c.FluidTicks {
		s.scheduleFluidTick(d, world.Pos{X: int(ft.Pos.X), Y: int(ft.Pos.Y), Z: int(ft.Pos.Z)}, 1)
	}
	for _, e := range c.Entities {
		anchor := world.Pos{X: int(math.Floor(e.X)), Y: int(math.Floor(e.Y)) - 1, Z: int(math.Floor(e.Z))}
		if d.w.HasOverride(anchor) {
			continue
		}
		name := strings.TrimPrefix(e.ID, "minecraft:")
		if !summonPlainNames[name] {
			continue
		}
		s.entities.Spawn(&Entity{Type: mustEntityType(name), Dim: d.name, X: e.X, Y: e.Y, Z: e.Z, Yaw: e.Yaw,
			Invulnerable: e.Invulnerable})
		d.w.SetBlock(anchor, d.w.GetBlock(anchor))
	}
}

// findNoiseSpawn is the initial world spawn of a noise overworld:
// the chunk holding the climate spawn search's answer, its middle at
// the generator's spawn height (64) as the fallback, and in a spiral of
// up to 121 steps around it — only chunks within five of it tried —
// the first chunk with a column a player can respawn on.
func (s *Server) findNoiseSpawn(d *dimension) (x, y, z int) {
	gx, gz := d.terrain.Generator().RandomState().FindSpawn()
	cx, cz := int(gx)>>4, int(gz)>>4
	x, y, z = cx*16+8, 64, cz*16+8
	sx, sz, dx, dz := 0, 0, 0, -1
	for i := 0; i < 11*11; i++ {
		if sx >= -5 && sx <= 5 && sz >= -5 && sz <= 5 {
			if p, ok := respawnPosInChunk(d, cx+sx, cz+sz); ok {
				return p.X, p.Y, p.Z
			}
		}
		if sx == sz || (sx < 0 && sx == -sz) || (sx > 0 && sx == 1-sz) {
			dx, dz = -dz, dx
		}
		sx, sz = sx+dx, sz+dz
	}
	return x, y, z
}

// respawnPosInChunk is the first respawnable column of a chunk, x
// outer, z inner.
func respawnPosInChunk(d *dimension, cx, cz int) (world.Pos, bool) {
	for x := cx * 16; x < cx*16+16; x++ {
		for z := cz * 16; z < cz*16+16; z++ {
			if p, ok := levelRespawnPos(d, x, z); ok {
				return p, true
			}
		}
	}
	return world.Pos{}, false
}

// levelRespawnPos is where a player can respawn in column (x, z) of a
// dimension without a ceiling: not on a water or lava surface, and
// otherwise on the first block, from just above the MOTION_BLOCKING
// height down, whose collision covers its top face — no position when a
// fluid comes first.
func levelRespawnPos(d *dimension, x, z int) (world.Pos, bool) {
	maxY := d.minY + d.height - 1
	h := d.w.TopMotionBlocking(x, z, d.minY, maxY) + 1
	if h < d.minY {
		return world.Pos{}, false
	}
	ws := d.minY
	for y := maxY; y >= d.minY; y-- {
		if d.w.GetBlock(world.Pos{X: x, Y: y, Z: z}) != world.Air {
			ws = y + 1
			break
		}
	}
	if ws <= h && ws > d.minY {
		// The surface is motion-blocking; it is below the ocean floor
		// heightmap only when it does not block motion — a fluid.
		top := d.w.GetBlock(world.Pos{X: x, Y: ws - 1, Z: z})
		if _, _, fluid := fluidOf(top); fluid && world.NonSolid(top) {
			return world.Pos{}, false
		}
	}
	for y := h + 1; y >= d.minY; y-- {
		b := d.w.GetBlock(world.Pos{X: x, Y: y, Z: z})
		if _, _, fluid := fluidOf(b); fluid {
			return world.Pos{}, false
		}
		if topFaceFull(b) {
			return world.Pos{X: x, Y: y + 1, Z: z}, true
		}
	}
	return world.Pos{}, false
}
