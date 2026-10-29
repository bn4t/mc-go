package worldgen

import (
	"fmt"

	"mc-go/internal/world"
)

// vpBase is the shared state and the block helpers of the hand-built
// (non-jigsaw) StructurePiece subclasses: type id, bounding box,
// orientation with its derived mirror and rotation, generation depth,
// and the local-to-world coordinate mapping and box writers measured
// from StructurePiece (getWorldX/Y/Z, placeBlock, generateBox,
// generateMaybeBox, fillColumnDown, createChest, ...).
type vpBase struct {
	typ   string
	bb    BoundingBox
	ori   int // mc-go direction (dirNorth...), -1 for none
	depth int32
	mir   int
	rot   int
}

func (p *vpBase) Type() string             { return p.typ }
func (p *vpBase) BoundingBox() BoundingBox { return p.bb }
func (p *vpBase) GenDepth() int32          { return p.depth }

// Orientation is the saved "O": Direction.get2DDataValue (south 0,
// west 1, north 2, east 3), -1 without one.
func (p *vpBase) Orientation() int32 { return dirTo2D(p.ori) }

func (p *vpBase) Move(dx, dy, dz int32) { p.bb = p.bb.Moved(dx, dy, dz) }

func dirTo2D(d int) int32 {
	switch d {
	case dirSouth:
		return 0
	case dirWest:
		return 1
	case dirNorth:
		return 2
	case dirEast:
		return 3
	}
	return -1
}

func dirFrom2D(v int32) int {
	switch v & 3 {
	case 0:
		return dirSouth
	case 1:
		return dirWest
	case 2:
		return dirNorth
	}
	return dirEast
}

// setOrientation is StructurePiece.setOrientation: south mirrors
// left-right, west mirrors and turns clockwise, east turns clockwise.
func (p *vpBase) setOrientation(d int) {
	p.ori = d
	p.mir, p.rot = mirNone, rotNone
	switch d {
	case dirSouth:
		p.mir = mirLeftRight
	case dirWest:
		p.mir, p.rot = mirLeftRight, rotCW90
	case dirEast:
		p.rot = rotCW90
	}
}

// loadVPBase reads the common StructurePiece fields of a saved piece.
func loadVPBase(t map[string]any) (vpBase, error) {
	var b vpBase
	id, _ := t["id"].(string)
	b.typ = trimNS(id)
	bb, ok := t["BB"].([]int32)
	if !ok || len(bb) != 6 {
		return b, fmt.Errorf("worldgen: piece %s without BB", id)
	}
	b.bb = BoundingBox{bb[0], bb[1], bb[2], bb[3], bb[4], bb[5]}
	b.depth = tagInt(t, "GD")
	if o := tagInt(t, "O"); o == -1 {
		b.setOrientation(-1)
	} else {
		b.setOrientation(dirFrom2D(o))
	}
	return b, nil
}

// makeBoundingBox is StructurePiece.makeBoundingBox: a box of size
// (w, h, d) at the corner, depth along the facing axis.
func makeBoundingBox(x, y, z int32, dir int, w, h, d int32) BoundingBox {
	if dir == dirNorth || dir == dirSouth {
		return BoundingBox{x, y, z, x + w - 1, y + h - 1, z + d - 1}
	}
	return BoundingBox{x, y, z, x + d - 1, y + h - 1, z + w - 1}
}

// orientBox is BoundingBox.orientBox: a box of size (w, h, d) placed
// relative to (x, y, z) with the local offset (ox, oy, oz) turned to
// face dir.
func orientBox(x, y, z, ox, oy, oz, w, h, d int32, dir int) BoundingBox {
	switch dir {
	case dirNorth:
		return BoundingBox{x + ox, y + oy, z - d + 1 + oz, x + w - 1 + ox, y + h - 1 + oy, z + oz}
	case dirWest:
		return BoundingBox{x - d + 1 + oz, y + oy, z + ox, x + oz, y + h - 1 + oy, z + w - 1 + ox}
	case dirEast:
		return BoundingBox{x + oz, y + oy, z + ox, x + d - 1 + oz, y + h - 1 + oy, z + w - 1 + ox}
	}
	// south and default
	return BoundingBox{x + ox, y + oy, z + oz, x + w - 1 + ox, y + h - 1 + oy, z + d - 1 + oz}
}

func (p *vpBase) worldX(x, z int32) int32 {
	switch p.ori {
	case dirNorth, dirSouth:
		return p.bb.MinX + x
	case dirWest:
		return p.bb.MaxX - z
	case dirEast:
		return p.bb.MinX + z
	}
	return x
}

func (p *vpBase) worldY(y int32) int32 {
	if p.ori < 0 {
		return y
	}
	return y + p.bb.MinY
}

func (p *vpBase) worldZ(x, z int32) int32 {
	switch p.ori {
	case dirNorth:
		return p.bb.MaxZ - z
	case dirSouth:
		return p.bb.MinZ + z
	case dirWest, dirEast:
		return p.bb.MinZ + x
	}
	return z
}

func (p *vpBase) worldPos(x, y, z int32) BlockPos {
	return BlockPos{p.worldX(x, z), p.worldY(y), p.worldZ(x, z)}
}

// shapeCheckBlocks is StructurePiece.SHAPE_CHECK_BLOCKS: blocks
// placeBlock marks for post-processing.
var shapeCheckBlocks = map[string]bool{
	"nether_brick_fence": true, "torch": true, "wall_torch": true, "oak_fence": true, "spruce_fence": true,
	"dark_oak_fence": true, "pale_oak_fence": true, "acacia_fence": true, "birch_fence": true,
	"jungle_fence": true, "ladder": true, "iron_bars": true,
}

// placeBlock is StructurePiece.placeBlock: the local position mapped,
// clipped to box, the state mirrored and rotated by the orientation.
func (p *vpBase) placeBlock(c *PieceContext, st world.Block, x, y, z int32, bb BoundingBox) {
	wp := p.worldPos(x, y, z)
	if !bb.IsInside(wp) {
		return
	}
	if p.mir != mirNone {
		st = mirrorState(st, p.mir)
	}
	if p.rot != rotNone {
		st = rotateState(st, p.rot)
	}
	c.Region.set(wp, st)
	if n, _ := world.StateOwner(st); shapeCheckBlocks[n] {
		c.Region.markPostProcess(wp)
	}
}

// getBlock is StructurePiece.getBlock: air outside box.
func (p *vpBase) getBlock(c *PieceContext, x, y, z int32, bb BoundingBox) world.Block {
	wp := p.worldPos(x, y, z)
	if !bb.IsInside(wp) {
		return world.Air
	}
	return c.Region.at(wp)
}

// isInterior is StructurePiece.isInterior: inside the box and below
// the ocean floor heightmap one above.
func (p *vpBase) isInterior(c *PieceContext, x, y, z int32, bb BoundingBox) bool {
	wp := p.worldPos(x, y+1, z)
	if !bb.IsInside(wp) {
		return false
	}
	return wp[1] < c.Region.Height(HMOceanFloorWG, wp[0], wp[2])
}

// generateBox is StructurePiece.generateBox: edge cells take edge,
// the rest inner; skipAir leaves air cells alone.
func (p *vpBase) generateBox(c *PieceContext, bb BoundingBox, x0, y0, z0, x1, y1, z1 int32, edge, inner world.Block, skipAir bool) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if skipAir && isAir(p.getBlock(c, x, y, z, bb)) {
					continue
				}
				if y == y0 || y == y1 || x == x0 || x == x1 || z == z0 || z == z1 {
					p.placeBlock(c, edge, x, y, z, bb)
				} else {
					p.placeBlock(c, inner, x, y, z, bb)
				}
			}
		}
	}
}

// blockSelector is StructurePiece.BlockSelector: next(random, x, y, z,
// isEdge) picks the state the box writes.
type blockSelector func(r Random, x, y, z int32, edge bool) world.Block

// generateBoxSel is the BlockSelector overload of generateBox.
func (p *vpBase) generateBoxSel(c *PieceContext, bb BoundingBox, x0, y0, z0, x1, y1, z1 int32, skipAir bool, r Random, sel blockSelector) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if skipAir && isAir(p.getBlock(c, x, y, z, bb)) {
					continue
				}
				e := y == y0 || y == y1 || x == x0 || x == x1 || z == z0 || z == z1
				p.placeBlock(c, sel(r, x, y, z, e), x, y, z, bb)
			}
		}
	}
}

// generateAirBox is StructurePiece.generateAirBox.
func (p *vpBase) generateAirBox(c *PieceContext, bb BoundingBox, x0, y0, z0, x1, y1, z1 int32) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				p.placeBlock(c, world.Air, x, y, z, bb)
			}
		}
	}
}

// generateMaybeBox is StructurePiece.generateMaybeBox: each cell drawn
// against chance first.
func (p *vpBase) generateMaybeBox(c *PieceContext, bb BoundingBox, r Random, chance float32, x0, y0, z0, x1, y1, z1 int32, edge, inner world.Block, skipAir, interiorOnly bool) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if r.NextFloat() > chance {
					continue
				}
				if skipAir && isAir(p.getBlock(c, x, y, z, bb)) {
					continue
				}
				if interiorOnly && !p.isInterior(c, x, y, z, bb) {
					continue
				}
				if y == y0 || y == y1 || x == x0 || x == x1 || z == z0 || z == z1 {
					p.placeBlock(c, edge, x, y, z, bb)
				} else {
					p.placeBlock(c, inner, x, y, z, bb)
				}
			}
		}
	}
}

// maybeGenerateBlock is StructurePiece.maybeGenerateBlock.
func (p *vpBase) maybeGenerateBlock(c *PieceContext, bb BoundingBox, r Random, chance float32, x, y, z int32, st world.Block) {
	if r.NextFloat() < chance {
		p.placeBlock(c, st, x, y, z, bb)
	}
}

// isReplaceableByStructures is StructurePiece.isReplaceableByStructures.
func isReplaceableByStructures(b world.Block) bool {
	return isAir(b) || isLiquidState(b) || isBlock(b, "glow_lichen") || isBlock(b, "seagrass") || isBlock(b, "tall_seagrass")
}

// isLiquidState is BlockState.liquid: a LiquidBlock (water or lava
// block, not a waterlogged one).
func isLiquidState(b world.Block) bool { return props(b)&(1<<bpLiquidBlock) != 0 }

// fillColumnDown is StructurePiece.fillColumnDown: st written from the
// local cell down through replaceable cells, above the level floor.
func (p *vpBase) fillColumnDown(c *PieceContext, st world.Block, x, y, z int32, bb BoundingBox) {
	wp := p.worldPos(x, y, z)
	if !bb.IsInside(wp) {
		return
	}
	for isReplaceableByStructures(c.Region.at(wp)) && wp[1] > c.Region.MinY()+1 {
		c.Region.set(wp, st)
		wp[1]--
	}
}

// createChestAt is StructurePiece.createChest at a world position: a
// chest (reoriented when st is not a chest state) with the loot table and a
// seed drawn from r, unless outside bb or already a chest.
func createChestAt(c *PieceContext, bb BoundingBox, r Random, pos BlockPos, table string, st world.Block) bool {
	if !bb.IsInside(pos) || isBlock(c.Region.at(pos), "chest") {
		return false
	}
	if !isBlock(st, "chest") {
		st = reorientChest(c.Region, pos, chestBlock())
	}
	c.Region.set(pos, st)
	if isBlock(c.Region.at(pos), "chest") {
		structLootBlockEntity(c.Region, pos, table, r.NextLong())
	}
	return true
}

// createChest is the local-coordinate createChest.
func (p *vpBase) createChest(c *PieceContext, bb BoundingBox, r Random, x, y, z int32, table string) bool {
	return createChestAt(c, bb, r, p.worldPos(x, y, z), table, 0)
}

// createDispenser is StructurePiece.createDispenser.
func (p *vpBase) createDispenser(c *PieceContext, bb BoundingBox, r Random, x, y, z int32, facing int, table string) bool {
	wp := p.worldPos(x, y, z)
	if !bb.IsInside(wp) || isBlock(c.Region.at(wp), "dispenser") {
		return false
	}
	st, _ := world.BlockStateOf("dispenser", map[string]string{"facing": dirNameOf[facing]})
	p.placeBlock(c, st, x, y, z, bb)
	if isBlock(c.Region.at(wp), "dispenser") {
		structLootBlockEntity(c.Region, wp, table, r.NextLong())
	}
	return true
}

// stateOf builds a block state from a name and properties (panics on a
// bad literal: these are code constants).
func stateOf(name string, kv ...string) world.Block {
	if len(kv) == 0 {
		return blockNamed(name)
	}
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	b, ok := world.BlockStateOf(name, m)
	if !ok {
		panic("worldgen: bad state " + name + fmt.Sprint(kv))
	}
	return b
}
