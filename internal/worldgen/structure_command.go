package worldgen

import "mc-go/internal/world"

// CommandStructurePlan is a fresh, unregistered structure start. Explicit
// placement may generate outside the structure's normal biome distribution;
// it does not install structure-start/reference metadata into terrain caches.
type CommandStructurePlan struct {
	generator *Generator
	start     *StructureStart
	lazyWG    bool
}

// commandStructureNames are the structures whose pieces the command planner
// can reproduce: measured blueprints with no template or world-state
// dependency beyond the live chunk snapshots. The desert pyramid's cellar
// and afterPlace pass, the jungle temple's selector/trap pass and the
// igloo's bundled-template top/laboratory pass and the shipwreck's
// ocean/beached template pass, the ocean ruins' cold/warm template
// passes, the jungle/swamp/desert ruined-portal setups, the ocean portal
// with measured flat/normal-overworld generator context and the
// nether fortress pieces are included.
var commandStructureNames = map[string]bool{"swamp_hut": true, "desert_pyramid": true, "jungle_pyramid": true, "igloo": true, "shipwreck": true, "shipwreck_beached": true, "ocean_ruin_cold": true, "ocean_ruin_warm": true, "ruined_portal_jungle": true, "ruined_portal_swamp": true, "fortress": true, "ruined_portal_desert": true, "ruined_portal_ocean": true}

// CommandStructureSource identifies the original chunk generator independently
// of edited live chunks and their /fillbiome overrides. FlatBiome is constant
// across a flat generator's noise-biome coordinates. NormalOverworld confirms
// the caller uses the same overworld settings, biome source, data, seed and
// level bounds as the detached planner's standard noise generator.
type CommandStructureSource struct {
	BaseBlock       func(int32, int32, int32) world.Block
	FlatBiome       string
	NormalOverworld bool
}

// PlanCommandStructure builds a command-placed structure start. The
// generator stands in for the level's chunk generator: the caller supplies
// its height and sea level, since the flat command world is not the noise
// settings NewGenerator would otherwise use. Optional source supplies the
// actual unmodified chunk-generator column (including air and fluids) for
// structures that search beneath the heightmap surface, plus its flat biome.
func PlanCommandStructure(data Data, seed int64, name string, cx, cz, minY, height, seaLevel int32, baseHeight func(int32, int32, int) int32, source ...CommandStructureSource) (*CommandStructurePlan, error) {
	if !commandStructureNames[name] {
		return nil, nil
	}
	g, err := NewGenerator(data, "overworld", "overworld", seed)
	if err != nil {
		return nil, err
	}
	g.RS.SetLevelBounds(minY, height)
	g.runtimeBaseHeight = baseHeight
	if len(source) > 0 {
		g.runtimeBaseBlock = source[0].BaseBlock
		g.runtimeFlatBiome = trimNS(source[0].FlatBiome)
	}
	// Other generator types have not been verified by the command planner.
	matchedNormal := len(source) > 0 && source[0].NormalOverworld
	if name == "ruined_portal_ocean" && g.runtimeFlatBiome == "" && !matchedNormal {
		return nil, nil
	}
	g.runtimeSeaLevel, g.runtimeSeaSet = seaLevel, true
	g.structureState()
	definition := g.structure(name)
	if definition.find == nil {
		return nil, nil
	}
	random := NewLegacy(0)
	setLargeFeatureSeed(random, seed, cx, cz)
	context := &GenContext{G: g, Random: random, ChunkX: cx, ChunkZ: cz, Seed: seed, Def: definition}
	stub, ok := definition.find(context)
	if !ok {
		return nil, nil
	}
	start := &StructureStart{Structure: name, ChunkX: cx, ChunkZ: cz, def: definition}
	stub.Generate(func(piece StructurePiece) { start.Pieces = append(start.Pieces, piece) })
	if len(start.Pieces) == 0 {
		return nil, nil
	}
	return &CommandStructurePlan{generator: g, start: start, lazyWG: name == "ruined_portal_ocean" && matchedNormal}, nil
}

func (p *CommandStructurePlan) Bounds() BoundingBox { return p.start.BoundingBox() }

type CommandStructureWrite struct {
	Position BlockPos
	State    world.Block
}

// Place processes each covered chunk over detached snapshots of live blocks.
// The caller verifies readiness before capture and applies the ordered writes,
// residents and block entities on its simulation owner after planning
// completes. random stands in for the level random source vanilla passes to
// every chunk: the same stream drives postProcess and the region random.
func (p *CommandStructurePlan) Place(chunks map[[2]int32]*ProtoChunk, random Random, retained ...CommandWGHeightmaps) ([]CommandStructureWrite, []GenEntity, []GenBlockEntity) {
	bounds := p.Bounds()
	var commandHeight func(int, int32, int32) int32
	if p.lazyWG {
		var cache CommandWGHeightmaps
		if len(retained) > 0 {
			cache = retained[0]
		}
		commandHeight = commandChunkHeight(chunks, p.generator.RS.lvMinY, cache)
	}
	var writes []CommandStructureWrite
	p.generator.WriteLog = func(at BlockPos, _ [2]int32, state world.Block) {
		writes = append(writes, CommandStructureWrite{at, state})
	}
	for cx := bounds.MinX >> 4; cx <= bounds.MaxX>>4; cx++ {
		for cz := bounds.MinZ >> 4; cz <= bounds.MaxZ>>4; cz++ {
			center := chunks[[2]int32{cx, cz}]
			if center == nil {
				continue
			}
			region := p.generator.NewRegion(center, chunks)
			if p.lazyWG {
				region.runtimeHeight = commandHeight
			}
			region.rand = random
			// PlaceCommand's chunk box spans the whole level, maxY + 1
			// inclusive, not the decoration box.
			box := BoundingBox{cx * 16, center.MinY, cz * 16, cx*16 + 15, center.MinY + center.Height, cz*16 + 15}
			p.start.placeInChunk(p.generator, region, random, box, [2]int32{cx, cz})
		}
	}
	var entities []GenEntity
	var blockEntities []GenBlockEntity
	for cx := bounds.MinX >> 4; cx <= bounds.MaxX>>4; cx++ {
		for cz := bounds.MinZ >> 4; cz <= bounds.MaxZ>>4; cz++ {
			if chunk := chunks[[2]int32{cx, cz}]; chunk != nil {
				entities = append(entities, chunk.Entities...)
				blockEntities = append(blockEntities, chunk.BlockEntities()...)
			}
		}
	}
	return writes, entities, blockEntities
}

// CommandWGHeightmaps holds first-use FULL-chunk worldgen heightmaps. Callers
// may retain these samples across commands only for the same resident chunk
// identity; they are runtime data and must not be serialized.
type CommandWGHeightmaps map[[3]int32][256]int32

// commandChunkHeight models missing _WG maps on FULL chunks: first access
// primes the entire requested map from the blocks at that moment. Subsequent
// FULL chunk writes update final maps only, leaving this lazily created map
// fixed. Natural ProtoChunk stage maps retain their separate freeze rules.
func commandChunkHeight(chunks map[[2]int32]*ProtoChunk, minY int32, cached CommandWGHeightmaps) func(int, int32, int32) int32 {
	if cached == nil {
		cached = CommandWGHeightmaps{}
	}
	return func(kind int, x, z int32) int32 {
		c := chunks[[2]int32{x >> 4, z >> 4}]
		if c == nil {
			return minY
		}
		if kind != HMWorldSurfaceWG && kind != HMOceanFloorWG {
			c.primeHeightmaps()
			return c.HeightAt(kind, x&15, z&15) + 1
		}
		key := [3]int32{x >> 4, z >> 4, int32(kind)}
		heights, ok := cached[key]
		if !ok {
			pred := notAir
			if kind == HMOceanFloorWG {
				pred = blocksMotion
			}
			for localZ := int32(0); localZ < 16; localZ++ {
				for localX := int32(0); localX < 16; localX++ {
					index := localZ*16 + localX
					heights[index] = c.MinY
					for y := c.MinY + c.Height - 1; y >= c.MinY; y-- {
						if pred(c.Get(localX, y, localZ)) {
							heights[index] = y + 1
							break
						}
					}
				}
			}
			cached[key] = heights
		}
		return heights[(z&15)*16+(x&15)]
	}
}
