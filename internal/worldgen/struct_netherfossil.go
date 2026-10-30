package worldgen

import "mc-go/internal/world"

// Nether fossils (NetherFossilStructure, NetherFossilPieces): a random
// column of the chunk, a height from the structure's height provider
// walked down its noise base column to the first air over soul sand or
// a sturdy top (above sea level, else no start), one of fourteen
// fossil templates at a random rotation, structure blocks and air
// ignored. Every chunk the fossil touches places the whole template
// (the chunk box is widened over it), then maybe one dried ghast on
// the template's bottom layer from a random positioned at the box
// center off the world seed.

func init() {
	structureTypes["nether_fossil"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		height := parseHeightProvider(d.raw["height"])
		return func(c *GenContext) (*GenerationStub, bool) {
			g, r := c.G, c.Random
			x := c.ChunkX*16 + r.NextIntN(16)
			z := c.ChunkZ*16 + r.NextIntN(16)
			sea := g.seaLevel()
			s := g.RS.settings
			// WorldGenerationContext over the level and the generator.
			ctx := genCtx{max(g.RS.lvMinY, s.minY), min(g.RS.lvHeight, s.height)}
			y := height(r, ctx)
			col := g.RS.baseColumn(x, z)
			for y > sea {
				above := col.at(y)
				y--
				below := col.at(y)
				if isAir(above) && (isBlock(below, "soul_sand") || sturdyFull(below, dirUp)) {
					break
				}
			}
			if y <= sea {
				return nil, false
			}
			pos := BlockPos{x, y, z}
			return &GenerationStub{Pos: pos, Generate: func(add func(StructurePiece)) {
				rot := int(r.NextIntN(4))
				name := "minecraft:nether_fossils/fossil_" + itoa(int(r.NextIntN(14))+1)
				add(&netherFossilPiece{tplPiece: newTplPiece("nefos", name, netherFossilSettings(rot), pos, 0), rot: rot})
			}}, true
		}
	}
	pieceLoaders["nefos"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		rot := rotationFromName(fmt3str(t["Rot"]))
		tp, err := loadTplPiece(t, func(string) placeSettings { return netherFossilSettings(rot) })
		if err != nil {
			return nil, err
		}
		return &netherFossilPiece{tplPiece: tp, rot: rot}, nil
	}
}

func netherFossilSettings(rot int) placeSettings {
	s := *newPlaceSettings()
	s.rotation = rot
	s.mirror = mirNone
	s.processors = []tplProcessor{ignoreBlocksProc(map[string]bool{"structure_block": true, "air": true})}
	return s
}

type netherFossilPiece struct {
	tplPiece
	rot int
}

// SaveData is NetherFossilPiece.addAdditionalSaveData.
func (p *netherFossilPiece) SaveData(t map[string]any) {
	p.saveTpl(t)
	t["Rot"] = rotationEnumNames[p.rot]
}

// PostProcess is NetherFossilPiece.postProcess.
func (p *netherFossilPiece) PostProcess(c *PieceContext) {
	s := p.set
	tb := bbFrom(p.tpl.boundingBox(&s, p.pos))
	wide := *c
	wide.ChunkBox = c.ChunkBox.Encapsulate(tb)
	p.placeTemplate(&wide)
	// placeDriedGhast.
	r := NewLegacy(c.G.RS.Seed).ForkPositional()
	ctr := tb.Center()
	pr := r.At(ctr[0], ctr[1], ctr[2])
	if pr.NextFloat() < 0.5 {
		q := BlockPos{tb.MinX + pr.NextIntN(tb.MaxX-tb.MinX+1), tb.MinY, tb.MinZ + pr.NextIntN(tb.MaxZ-tb.MinZ+1)}
		if isAir(c.Region.at(q)) && wide.ChunkBox.IsInside(q) {
			st := rotateState(blockNamed("dried_ghast"), int(pr.NextIntN(4)))
			c.Region.set(q, st)
		}
	}
}

var _ = world.Air
