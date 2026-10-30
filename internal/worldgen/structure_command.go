package worldgen

import "mc-go/internal/world"

// CommandStructurePlan is a fresh, unregistered structure start. Explicit
// placement may generate outside the structure's normal biome distribution;
// it does not install structure-start/reference metadata into terrain caches.
type CommandStructurePlan struct {
	generator *Generator
	start     *StructureStart
}

// PlanCommandStructure currently supports the swamp-hut builder. Other
// structures require their own runtime terrain, template and block-entity pins.
func PlanCommandStructure(data Data, seed int64, name string, cx, cz, minY, height int32, baseHeight func(int32, int32, int) int32) (*CommandStructurePlan, error) {
	if name != "swamp_hut" {
		return nil, nil
	}
	g, err := NewGenerator(data, "overworld", "overworld", seed)
	if err != nil {
		return nil, err
	}
	g.RS.SetLevelBounds(minY, height)
	g.runtimeBaseHeight = baseHeight
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
	return &CommandStructurePlan{generator: g, start: start}, nil
}

func (p *CommandStructurePlan) Bounds() BoundingBox { return p.start.BoundingBox() }

type CommandStructureWrite struct {
	Position BlockPos
	State    world.Block
}

// Place processes each covered chunk over detached snapshots of live blocks.
// The caller verifies readiness before capture and applies the ordered writes
// and emitted residents on its simulation owner after planning completes.
func (p *CommandStructurePlan) Place(chunks map[[2]int32]*ProtoChunk) ([]CommandStructureWrite, []GenEntity) {
	bounds := p.Bounds()
	var writes []CommandStructureWrite
	p.generator.WriteLog = func(at BlockPos, _ [2]int32, state world.Block) {
		writes = append(writes, CommandStructureWrite{at, state})
	}
	random := NewLegacy(0) // The supported hut's block blueprint makes no random draws.
	for cx := bounds.MinX >> 4; cx <= bounds.MaxX>>4; cx++ {
		for cz := bounds.MinZ >> 4; cz <= bounds.MaxZ>>4; cz++ {
			center := chunks[[2]int32{cx, cz}]
			if center == nil {
				continue
			}
			region := p.generator.NewRegion(center, chunks)
			box := BoundingBox{cx * 16, center.MinY, cz * 16, cx*16 + 15, center.MinY + center.Height - 1, cz*16 + 15}
			p.start.placeInChunk(p.generator, region, random, box, [2]int32{cx, cz})
		}
	}
	var entities []GenEntity
	for cx := bounds.MinX >> 4; cx <= bounds.MaxX>>4; cx++ {
		for cz := bounds.MinZ >> 4; cz <= bounds.MaxZ>>4; cz++ {
			if chunk := chunks[[2]int32{cx, cz}]; chunk != nil {
				entities = append(entities, chunk.Entities...)
			}
		}
	}
	return writes, entities
}
