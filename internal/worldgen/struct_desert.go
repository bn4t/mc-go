package worldgen

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Desert pyramids (DesertPyramidStructure + DesertPyramidPiece),
// measured from the 26.2 bytecode:
//
//   - a 21 x 15 x 21 SinglePieceStructure piece; postProcess draws
//     nextInt(3) and (once) lowers the box to the lowest ground height
//     minus that, then writes the measured blueprint below, a sandstone
//     column down from y -5 under every cell, the four trap-room chests
//     (once each, hasPlacedChest by 2D direction) and the cellar: stairs
//     with one region-random sand / sandstone pair, cut and chiseled
//     walls over non-air only, a collapsed sand / sandstone roof
//     (region random nextFloat < 0.33 for sandstone) and the recorded
//     potential suspicious sand cells.
//   - afterPlace: the roof's random cell (a positional random from the
//     world seed at the roof corner) and min(cells, 5 + nextInt(3)) of
//     the potential cells (sorted by y, z, x and shuffled with a
//     positional random from the world seed at the pieces' box center)
//     become suspicious sand with the desert_pyramid archaeology table
//     and the position as seed; the other potential cells become sand.

const desertW, desertD = 21, 21

var desertPalette = map[string]string{
	"ss": "sandstone", "cut": "cut_sandstone", "chis": "chiseled_sandstone", "air": "air",
	"or": "orange_terracotta", "blue": "blue_terracotta", "slab": "sandstone_slab",
	"sn": "sandstone_stairs[facing=north]", "sS": "sandstone_stairs[facing=south]",
	"se": "sandstone_stairs[facing=east]", "sw": "sandstone_stairs[facing=west]",
	"plate": "stone_pressure_plate", "tnt": "tnt", "sand": "sand",
}

// desertBlueprints: before the sandstone columns, and after them up to
// the trap room (DesertPyramidPiece.postProcess, measured in order).
var desertBlueprints = sync.OnceValue(func() [2]blueprint {
	var pre strings.Builder
	pre.WriteString("box 0 -4 0 20 0 20 ss ss\n")
	for i := 1; i <= 9; i++ {
		fmt.Fprintf(&pre, "box %d %d %d %d %d %d ss ss\n", i, i, i, 20-i, i, 20-i)
		fmt.Fprintf(&pre, "box %d %d %d %d %d %d air air\n", i+1, i, i+1, 19-i, i, 19-i)
	}
	var post strings.Builder
	post.WriteString(desertMain)
	for z := 5; z <= 17; z += 2 {
		fmt.Fprintf(&post, "set 4 1 %d cut\nset 4 2 %d chis\nset 16 1 %d cut\nset 16 2 %d chis\n", z, z, z, z)
	}
	post.WriteString(desertFloor)
	for _, x := range []int{0, 20} {
		for _, row := range [][4]string{
			{"2", "cut", "or", "cut"}, {"3", "cut", "or", "cut"}, {"4", "or", "chis", "or"}, {"5", "cut", "or", "cut"},
			{"6", "or", "chis", "or"}, {"7", "or", "or", "or"}, {"8", "cut", "cut", "cut"},
		} {
			fmt.Fprintf(&post, "set %d %s 1 %s\nset %d %s 2 %s\nset %d %s 3 %s\n", x, row[0], row[1], x, row[0], row[2], x, row[0], row[3])
		}
	}
	for _, x := range []int{2, 18} {
		for _, row := range [][4]string{
			{"2", "cut", "or", "cut"}, {"3", "cut", "or", "cut"}, {"4", "or", "chis", "or"}, {"5", "cut", "or", "cut"},
			{"6", "or", "chis", "or"}, {"7", "or", "or", "or"}, {"8", "cut", "cut", "cut"},
		} {
			fmt.Fprintf(&post, "set %d %s 0 %s\nset %d %s 0 %s\nset %d %s 0 %s\n", x-1, row[0], row[1], x, row[0], row[2], x+1, row[0], row[3])
		}
	}
	post.WriteString(desertTrap)
	return [2]blueprint{parseBlueprint(desertPalette, pre.String()), parseBlueprint(desertPalette, post.String())}
})

const desertMain = `
box 0 0 0 4 9 4 ss air
box 1 10 1 3 10 3 ss ss
set 2 10 0 sn
set 2 10 4 sS
set 0 10 2 se
set 4 10 2 sw
box 16 0 0 20 9 4 ss air
box 17 10 1 19 10 3 ss ss
set 18 10 0 sn
set 18 10 4 sS
set 16 10 2 se
set 20 10 2 sw
box 8 0 0 12 4 4 ss air
box 9 1 0 11 3 4 air air
set 9 1 1 cut
set 9 2 1 cut
set 9 3 1 cut
set 10 3 1 cut
set 11 3 1 cut
set 11 2 1 cut
set 11 1 1 cut
box 4 1 1 8 3 3 ss air
box 4 1 2 8 2 2 air air
box 12 1 1 16 3 3 ss air
box 12 1 2 16 2 2 air air
box 5 4 5 15 4 15 ss ss
box 9 4 9 11 4 11 air air
box 8 1 8 8 3 8 cut cut
box 12 1 8 12 3 8 cut cut
box 8 1 12 8 3 12 cut cut
box 12 1 12 12 3 12 cut cut
box 1 1 5 4 4 11 ss ss
box 16 1 5 19 4 11 ss ss
box 6 7 9 6 7 11 ss ss
box 14 7 9 14 7 11 ss ss
box 5 5 9 5 7 11 cut cut
box 15 5 9 15 7 11 cut cut
set 5 5 10 air
set 5 6 10 air
set 6 6 10 air
set 15 5 10 air
set 15 6 10 air
set 14 6 10 air
box 2 4 4 2 6 4 air air
box 18 4 4 18 6 4 air air
set 2 4 5 sn
set 2 3 4 sn
set 18 4 5 sn
set 18 3 4 sn
box 1 1 3 2 2 3 ss ss
box 18 1 3 19 2 3 ss ss
set 1 1 2 ss
set 19 1 2 ss
set 1 2 2 slab
set 19 2 2 slab
set 2 1 2 sw
set 18 1 2 se
box 4 3 5 4 3 17 ss ss
box 16 3 5 16 3 17 ss ss
box 3 1 5 4 2 16 air air
box 15 1 5 16 2 16 air air
`

const desertFloor = `
set 10 0 7 or
set 10 0 8 or
set 9 0 9 or
set 11 0 9 or
set 8 0 10 or
set 12 0 10 or
set 7 0 10 or
set 13 0 10 or
set 9 0 11 or
set 11 0 11 or
set 10 0 12 or
set 10 0 13 or
set 10 0 10 blue
`

const desertTrap = `
box 8 4 0 12 6 0 cut cut
set 8 6 0 air
set 12 6 0 air
set 9 5 0 or
set 10 5 0 chis
set 11 5 0 or
box 8 -14 8 12 -11 12 cut cut
box 8 -10 8 12 -10 12 chis chis
box 8 -9 8 12 -9 12 cut cut
box 8 -8 8 12 -1 12 ss ss
box 9 -11 9 11 -1 11 air air
set 10 -11 10 plate
box 9 -13 9 11 -13 11 tnt air
set 8 -11 10 air
set 8 -10 10 air
set 7 -10 10 chis
set 7 -11 10 cut
set 12 -11 10 air
set 12 -10 10 air
set 13 -10 10 chis
set 13 -11 10 cut
set 10 -11 8 air
set 10 -10 8 air
set 10 -10 7 chis
set 10 -11 7 cut
set 10 -11 12 air
set 10 -10 12 air
set 10 -10 13 chis
set 10 -11 13 cut
`

// The cellar (addCellarStairs + addCellarRoom at local (16, -4, 13)):
// stairs, then walls over non-air only.
var desertCellar = sync.OnceValue(func() [2]blueprint {
	stairs := `
set 13 -1 17 sw
set 14 -2 17 sw
set 15 -3 17 sw
`
	sandRow := `
set 12 0 17 sand
set 13 0 17 sand
set 14 0 17 sand
set 15 0 17 sand
set 16 0 17 sand
set 14 -1 17 sand
`
	var walls strings.Builder
	for _, row := range []struct {
		y   int
		blk string
	}{{-3, "cut"}, {-2, "chis"}, {-1, "cut"}} {
		fmt.Fprintf(&walls, "box 13 %d 10 13 %d 15 %s %s skip\n", row.y, row.y, row.blk, row.blk)
		fmt.Fprintf(&walls, "box 19 %d 10 19 %d 15 %s %s skip\n", row.y, row.y, row.blk, row.blk)
		fmt.Fprintf(&walls, "box 13 %d 10 19 %d 11 %s %s skip\n", row.y, row.y, row.blk, row.blk)
		fmt.Fprintf(&walls, "box 13 %d 16 19 %d 16 %s %s skip\n", row.y, row.y, row.blk, row.blk)
	}
	return [2]blueprint{parseBlueprint(desertPalette, stairs+sandRow), parseBlueprint(desertPalette, walls.String())}
})

// desertPotentialSand is the cellar's placeSand cells in recording
// order: the 5 x 3 x 5 room (y, x, z), then the three side niches.
var desertPotentialSand = func() [][3]int32 {
	var out [][3]int32
	for y := int32(-3); y <= -1; y++ {
		for x := int32(14); x <= 18; x++ {
			for z := int32(11); z <= 15; z++ {
				out = append(out, [3]int32{x, y, z})
			}
		}
	}
	return out
}()

type desertPyramidPiece struct {
	scatteredPiece
	placedChest  [4]bool
	potential    []BlockPos
	collapsedPos BlockPos
}

func newDesertPyramid(r Random, x, z int32) *desertPyramidPiece {
	p := &desertPyramidPiece{}
	p.initScattered("tedp", x, 64, z, desertW, 15, desertD, randomHorizontalDir(r))
	return p
}

func (p *desertPyramidPiece) SaveData(t map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.saveScattered(t)
	for i, v := range p.placedChest {
		t[fmt.Sprintf("hasPlacedChest%d", i)] = boolByte(v)
	}
}

func (p *desertPyramidPiece) BoundingBox() BoundingBox {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Box
}

func (p *desertPyramidPiece) PostProcess(c *PieceContext) {
	p.mu.Lock()
	defer p.mu.Unlock()
	bb := c.ChunkBox
	r := c.Random
	if !p.updateHeightPositionToLowestGroundHeight(c, -r.NextIntN(3)) {
		return
	}
	bps := desertBlueprints()
	bps[0].run(&p.legacyPiece, c, bb)
	ss := blockNamed("sandstone")
	for x := int32(0); x < p.width; x++ {
		for z := int32(0); z < p.depth; z++ {
			p.fillColumnDown(c, ss, x, -5, z, bb)
		}
	}
	bps[1].run(&p.legacyPiece, c, bb)
	for _, d := range horizontalDirs {
		i := dir2D(d)
		if p.placedChest[i] {
			continue
		}
		o := dirOffsets[d]
		p.placedChest[i] = p.createChest(c, bb, r, 10+o[0]*2, -11, 10+o[2]*2, "minecraft:chests/desert_pyramid")
	}
	p.addCellar(c, bb)
}

func (p *desertPyramidPiece) addCellar(c *PieceContext, bb BoundingBox) {
	cel := desertCellar()
	rr := regionRandom(c.Region)
	cel[0].run(&p.legacyPiece, c, bb)
	b := rr.NextBool()
	sand, ss := blockNamed("sand"), blockNamed("sandstone")
	if b {
		p.placeBlock(c, sand, 15, -1, 17, bb)
		p.placeBlock(c, ss, 16, -1, 17, bb)
	} else {
		p.placeBlock(c, ss, 15, -1, 17, bb)
		p.placeBlock(c, sand, 16, -1, 17, bb)
	}
	p.placeBlock(c, sand, 15, -2, 17, bb)
	p.placeBlock(c, ss, 16, -2, 17, bb)
	p.placeBlock(c, sand, 16, -3, 17, bb)
	// addCellarRoom
	cel[1].run(&p.legacyPiece, c, bb)
	for _, q := range desertPotentialSand {
		p.potential = append(p.potential, p.worldPos(q[0], q[1], q[2]))
	}
	// placeCollapsedRoof over x 14..18, z 11..15 at y 0.
	for x := int32(14); x <= 18; x++ {
		for z := int32(11); z <= 15; z++ {
			if rr.NextFloat() < 0.33 {
				p.placeBlock(c, ss, x, 0, z, bb)
			} else {
				p.placeBlock(c, sand, x, 0, z, bb)
			}
		}
	}
	corner := p.worldPos(14, 0, 11)
	pr := NewLegacy(c.G.RS.Seed).ForkPositional().At(corner[0], corner[1], corner[2])
	rx := 14 + pr.NextIntN(5)
	rz := 11 + pr.NextIntN(5)
	p.collapsedPos = p.worldPos(rx, 0, rz)
	or, cut, chis, blue := blockNamed("orange_terracotta"), blockNamed("cut_sandstone"), blockNamed("chiseled_sandstone"), blockNamed("blue_terracotta")
	p.placeBlock(c, blue, 16, -4, 13, bb)
	for _, q := range [][3]int32{{17, -4, 12}, {17, -4, 14}, {15, -4, 12}, {15, -4, 14}, {18, -4, 13}, {14, -4, 13}, {16, -4, 15}, {16, -4, 11}} {
		p.placeBlock(c, or, q[0], q[1], q[2], bb)
	}
	niche := func(x, z, wx, wz int32) {
		p.placeBlock(c, or, x, -4, z, bb)
		p.potential = append(p.potential, p.worldPos(x, -3, z), p.worldPos(x, -2, z))
		if wx != 0 || wz != 0 {
			p.placeBlock(c, cut, wx, -3, wz, bb)
			p.placeBlock(c, chis, wx, -2, wz, bb)
		}
	}
	niche(19, 13, 20, 13)
	niche(13, 13, 12, 13)
	niche(16, 16, 0, 0)
	niche(16, 10, 16, 9)
}

// desertAfterPlace is DesertPyramidStructure.afterPlace.
func desertAfterPlace(c *PieceContext, s *StructureStart) {
	set := map[BlockPos]bool{}
	var cells []BlockPos
	for _, pc := range s.Pieces {
		p, ok := pc.(*desertPyramidPiece)
		if !ok {
			continue
		}
		p.mu.Lock()
		for _, q := range p.potential {
			if !set[q] {
				set[q] = true
				cells = append(cells, q)
			}
		}
		roof := p.collapsedPos
		p.mu.Unlock()
		placeSuspiciousSand(c, roof)
	}
	sort.Slice(cells, func(i, j int) bool {
		a, b := cells[i], cells[j]
		if a[1] != b[1] {
			return a[1] < b[1]
		}
		if a[2] != b[2] {
			return a[2] < b[2]
		}
		return a[0] < b[0]
	})
	ctr := piecesBox(s.Pieces).Center()
	r := NewLegacy(c.G.RS.Seed).ForkPositional().At(ctr[0], ctr[1], ctr[2])
	for i := int32(len(cells)); i > 1; i-- {
		j := r.NextIntN(i)
		cells[i-1], cells[j] = cells[j], cells[i-1]
	}
	n := min(int32(len(set)), 5+r.NextIntN(3))
	sand := blockNamed("sand")
	for _, q := range cells {
		if n > 0 {
			n--
			placeSuspiciousSand(c, q)
		} else if c.ChunkBox.IsInside(q) {
			c.Region.set(q, sand)
		}
	}
}

func placeSuspiciousSand(c *PieceContext, q BlockPos) {
	if !c.ChunkBox.IsInside(q) {
		return
	}
	c.Region.set(q, blockNamed("suspicious_sand"))
	structLootBlockEntity(c.Region, q, "minecraft:archaeology/desert_pyramid", packPos(q[0], q[1], q[2]))
}

func init() {
	structureTypes["desert_pyramid"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		d.afterPlace = desertAfterPlace
		return singlePieceStructure(g, desertW, desertD, func(r Random, x, z int32) StructurePiece { return newDesertPyramid(r, x, z) })
	}
	pieceLoaders["tedp"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		p := &desertPyramidPiece{}
		if err := p.loadScattered("tedp", t); err != nil {
			return nil, err
		}
		for i := range p.placedChest {
			p.placedChest[i] = tagBool(t, fmt.Sprintf("hasPlacedChest%d", i))
		}
		return p, nil
	}
}
