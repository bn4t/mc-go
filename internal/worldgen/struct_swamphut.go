package worldgen

import "sync"

// Swamp huts (SwampHutStructure + SwampHutPiece), measured from the
// 26.2 bytecode: a 7 x 7 x 9 piece on the chunk center's
// WORLD_SURFACE_WG height (no sea-level check), moved once to the
// average ground height of the first decorating chunk, with oak log
// stilts filled down from y -1. The witch and the cat (spawnedWitch /
// spawnedCat, set when the cell (2, 2, 5) lies in the decorating box)
// produce resident descriptors once; the server finalizes and admits them.

var swampHutBlueprint = sync.OnceValue(func() blueprint {
	return parseBlueprint(map[string]string{
		"pl": "spruce_planks", "log": "oak_log", "fence": "oak_fence", "air": "air",
		"sn": "spruce_stairs[facing=north]", "se": "spruce_stairs[facing=east]",
		"sw": "spruce_stairs[facing=west]", "sS": "spruce_stairs[facing=south]",
		"snR": "spruce_stairs[facing=north,shape=outer_right]", "snL": "spruce_stairs[facing=north,shape=outer_left]",
		"sSL": "spruce_stairs[facing=south,shape=outer_left]", "sSR": "spruce_stairs[facing=south,shape=outer_right]",
	}, `
box 1 1 1 5 1 7 pl pl
box 1 4 2 5 4 7 pl pl
box 2 1 0 4 1 0 pl pl
box 2 2 2 3 3 2 pl pl
box 1 2 3 1 3 6 pl pl
box 5 2 3 5 3 6 pl pl
box 2 2 7 4 3 7 pl pl
box 1 0 2 1 3 2 log log
box 5 0 2 5 3 2 log log
box 1 0 7 1 3 7 log log
box 5 0 7 5 3 7 log log
set 2 3 2 fence
set 3 3 7 fence
set 1 3 4 air
set 5 3 4 air
set 5 3 5 air
set 1 3 5 potted_red_mushroom
set 3 2 6 crafting_table
set 4 2 6 cauldron
set 1 2 1 fence
set 5 2 1 fence
box 0 4 1 6 4 1 sn sn
box 0 4 2 0 4 7 se se
box 6 4 2 6 4 7 sw sw
box 0 4 8 6 4 8 sS sS
set 0 4 1 snR
set 6 4 1 snL
set 0 4 8 sSL
set 6 4 8 sSR
`)
})

type swampHutPiece struct {
	scatteredPiece
	witch, cat bool
}

func (p *swampHutPiece) BoundingBox() BoundingBox {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Box
}

func (p *swampHutPiece) SaveData(t map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.saveScattered(t)
	t["Witch"] = boolByte(p.witch)
	t["Cat"] = boolByte(p.cat)
}

func (p *swampHutPiece) PostProcess(c *PieceContext) {
	p.mu.Lock()
	defer p.mu.Unlock()
	bb := c.ChunkBox
	if !p.updateAverageGroundHeight(c, bb, 0) {
		return
	}
	swampHutBlueprint().run(&p.legacyPiece, c, bb)
	log := blockNamed("oak_log")
	for z := int32(2); z <= 7; z += 5 {
		for x := int32(1); x <= 5; x += 4 {
			p.fillColumnDown(c, log, x, -1, z, bb)
		}
	}
	position := p.worldPos(2, 2, 5)
	in := bb.IsInside(position)
	resident := func(name string) {
		if chunk := c.Region.chunkAt(position[0], position[2]); chunk != nil {
			chunk.Entities = append(chunk.Entities, GenEntity{Structure: "swamp_hut", Type: name, X: float64(position[0]) + .5, Y: float64(position[1]), Z: float64(position[2]) + .5})
		}
	}
	if !p.witch && in {
		p.witch = true
		resident("witch")
	}
	if !p.cat && in {
		p.cat = true
		resident("cat")
	}
}

func init() {
	structureTypes["swamp_hut"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		return func(c *GenContext) (*GenerationStub, bool) {
			x0, z0 := c.ChunkX*16, c.ChunkZ*16
			mx, mz := x0+8, z0+8
			y := g.firstOccupiedHeight(mx, mz, HMWorldSurfaceWG)
			return &GenerationStub{Pos: BlockPos{mx, y, mz}, Generate: func(add func(StructurePiece)) {
				p := &swampHutPiece{}
				p.initScattered("tesh", x0, 64, z0, 7, 7, 9, randomHorizontalDir(c.Random))
				add(p)
			}}, true
		}
	}
	pieceLoaders["tesh"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		p := &swampHutPiece{}
		if err := p.loadScattered("tesh", t); err != nil {
			return nil, err
		}
		p.witch, p.cat = tagBool(t, "Witch"), tagBool(t, "Cat")
		return p, nil
	}
}
