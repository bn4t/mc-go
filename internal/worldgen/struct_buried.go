package worldgen

import "mc-go/internal/world"

// Buried treasure (BuriedTreasureStructure, BuriedTreasurePieces): the
// stub sits on the ocean-floor noise height at the chunk middle; the
// single "btp" piece is a one-block box at (chunk+9, 90, chunk+9).

func init() {
	structureTypes["buried_treasure"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		return func(c *GenContext) (*GenerationStub, bool) {
			return onTopOfChunkCenter(c, HMOceanFloorWG, func(add func(StructurePiece)) {
				x, z := c.ChunkX*16+9, c.ChunkZ*16+9
				add(&buriedTreasurePiece{vpBase{typ: "btp", bb: BoundingBox{x, 90, z, x, 90, z}, ori: -1}})
			}), true
		}
	}
	pieceLoaders["btp"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		b, err := loadVPBase(t)
		return &buriedTreasurePiece{b}, err
	}
}

// onTopOfChunkCenter is Structure.onTopOfChunkCenter: the stub at the
// chunk's middle block on the first occupied noise height.
func onTopOfChunkCenter(c *GenContext, hm int, gen func(add func(StructurePiece))) *GenerationStub {
	x, z := c.ChunkX*16+8, c.ChunkZ*16+8
	y := c.G.firstOccupiedHeight(x, z, hm)
	return &GenerationStub{Pos: BlockPos{x, y, z}, Generate: gen}
}

type buriedTreasurePiece struct{ vpBase }

func isWaterOrLavaBlock(b world.Block) bool { return isBlock(b, "water") || isBlock(b, "lava") }

// PostProcess is BuriedTreasurePiece.postProcess: from the ocean floor
// height down, the first cell standing on sandstone or stone (or a
// stone variant) takes the chest; its open neighbours are filled (with
// the floor block where they hang over open space, sand where the
// cell itself is open), and the piece box shrinks to the chest.
func (p *buriedTreasurePiece) PostProcess(c *PieceContext) {
	rg := c.Region
	pos := BlockPos{p.bb.MinX, rg.Height(HMOceanFloorWG, p.bb.MinX, p.bb.MinZ), p.bb.MinZ}
	for ; pos[1] > rg.MinY(); pos[1]-- {
		st := rg.at(pos)
		below := rg.at(offset(pos, 0, -1, 0))
		if !(isBlock(below, "sandstone") || isBlock(below, "stone") || isBlock(below, "andesite") ||
			isBlock(below, "granite") || isBlock(below, "diorite")) {
			continue
		}
		fill := st
		if isAir(st) || isWaterOrLavaBlock(st) {
			fill = blockNamed("sand")
		}
		for d := 0; d < 6; d++ {
			o := dirOffsets[d]
			q := offset(pos, o[0], o[1], o[2])
			s := rg.at(q)
			if !(isAir(s) || isWaterOrLavaBlock(s)) {
				continue
			}
			u := rg.at(offset(q, 0, -1, 0))
			if (isAir(u) || isWaterOrLavaBlock(u)) && d != dirUp {
				rg.set(q, below)
			} else {
				rg.set(q, fill)
			}
		}
		p.bb = BoundingBox{pos[0], pos[1], pos[2], pos[0], pos[1], pos[2]}
		createChestAt(c, c.ChunkBox, c.Random, pos, "minecraft:chests/buried_treasure", world.Air)
		return
	}
}
