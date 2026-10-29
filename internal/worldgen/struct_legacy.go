package worldgen

import (
	"fmt"

	"mc-go/internal/nbt"
	"mc-go/internal/world"
)

// The StructurePiece base the legacy (non-jigsaw) structures build on:
// the piece's box, orientation (which also fixes the mirror and
// rotation every placed state goes through), gen depth, the local to
// world coordinate mapping and the block helpers (placeBlock,
// generateBox and its random-selector / maybe variants, the half
// sphere, fillColumnDown, createChest, createDispenser), all writing
// through the decorating region clipped to the piece context's box.
//
// Measured from StructurePiece (bytecode): getWorldX / getWorldZ by
// orientation (north and south keep x, west maps x to maxX - z, east
// to minX + z; north flips z to maxZ - z, south keeps it, west and east
// map z to minZ + x), setOrientation (south: mirror LEFT_RIGHT; west:
// mirror LEFT_RIGHT + rotation CLOCKWISE_90; east: CLOCKWISE_90; north
// or none: identity), placeBlock (box check, canBeReplaced, mirror then
// rotate the state, setBlock flag 2, the SHAPE_CHECK_BLOCKS marked for
// post-processing).

// legacyPiece is the StructurePiece state shared by the legacy pieces.
type legacyPiece struct {
	typ    string
	Box    BoundingBox
	orient int // a horizontal dir* value, -1 for none
	mirror int
	rot    int
	Depth  int32
	// canReplace is the piece's canBeReplaced override (nil: always).
	canReplace func(c *PieceContext, x, y, z int32, bb BoundingBox) bool
}

func newLegacyPiece(typ string, depth int32, b BoundingBox) legacyPiece {
	return legacyPiece{typ: typ, Box: b, orient: -1, Depth: depth}
}

func (p *legacyPiece) Type() string             { return p.typ }
func (p *legacyPiece) BoundingBox() BoundingBox { return p.Box }
func (p *legacyPiece) GenDepth() int32          { return p.Depth }

// Orientation is the saved "O": Direction.get2DDataValue, -1 for none.
func (p *legacyPiece) Orientation() int32 {
	if p.orient < 0 {
		return -1
	}
	return dir2D(p.orient)
}

func (p *legacyPiece) Move(dx, dy, dz int32) { p.Box = p.Box.Moved(dx, dy, dz) }

// dir2D is Direction.get2DDataValue (south 0, west 1, north 2, east 3).
func dir2D(d int) int32 {
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

// dirFrom2D is Direction.from2DDataValue (index taken mod 4).
func dirFrom2D(v int32) int {
	return [4]int{dirSouth, dirWest, dirNorth, dirEast}[((v%4)+4)%4]
}

// setOrientation is StructurePiece.setOrientation.
func (p *legacyPiece) setOrientation(d int) {
	p.orient = d
	p.mirror, p.rot = mirNone, rotNone
	switch d {
	case dirSouth:
		p.mirror = mirLeftRight
	case dirWest:
		p.mirror, p.rot = mirLeftRight, rotCW90
	case dirEast:
		p.rot = rotCW90
	}
}

// loadLegacyBase reads the StructurePiece(type, tag) fields: GD, BB, O.
func loadLegacyBase(typ string, t map[string]any) (legacyPiece, error) {
	bb, ok := t["BB"].([]int32)
	if !ok || len(bb) != 6 {
		return legacyPiece{}, fmt.Errorf("worldgen: %s piece without BB", typ)
	}
	p := newLegacyPiece(typ, tagInt(t, "GD"), BoundingBox{bb[0], bb[1], bb[2], bb[3], bb[4], bb[5]})
	if o := tagInt(t, "O"); o != -1 {
		p.setOrientation(dirFrom2D(o))
	}
	return p, nil
}

// randomHorizontalDir is Direction.Plane.HORIZONTAL.getRandomDirection
// (north, east, south, west by nextInt(4)).
func randomHorizontalDir(r Random) int { return horizontalDirs[r.NextIntN(4)] }

// makeBoundingBox is StructurePiece.makeBoundingBox: a w x h x d box at
// (x, y, z), w and d swapped for an east-west facing.
func makeBoundingBox(x, y, z int32, d int, w, h, depth int32) BoundingBox {
	if d == dirNorth || d == dirSouth {
		return BoundingBox{x, y, z, x + w - 1, y + h - 1, z + depth - 1}
	}
	return BoundingBox{x, y, z, x + depth - 1, y + h - 1, z + w - 1}
}

// orientBox is BoundingBox.orientBox: a local box (offset ox,oy,oz,
// size w,h,d) anchored at (x, y, z) facing d.
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

func (p *legacyPiece) worldX(x, z int32) int32 {
	switch p.orient {
	case dirNorth, dirSouth:
		return p.Box.MinX + x
	case dirWest:
		return p.Box.MaxX - z
	case dirEast:
		return p.Box.MinX + z
	}
	return x
}

func (p *legacyPiece) worldY(y int32) int32 {
	if p.orient < 0 {
		return y
	}
	return y + p.Box.MinY
}

func (p *legacyPiece) worldZ(x, z int32) int32 {
	switch p.orient {
	case dirNorth:
		return p.Box.MaxZ - z
	case dirSouth:
		return p.Box.MinZ + z
	case dirWest, dirEast:
		return p.Box.MinZ + x
	}
	return z
}

func (p *legacyPiece) worldPos(x, y, z int32) BlockPos {
	return BlockPos{p.worldX(x, z), p.worldY(y), p.worldZ(x, z)}
}

// transformState is placeBlock's mirror then rotation of a state.
func (p *legacyPiece) transformState(b world.Block) world.Block {
	if p.mirror != mirNone {
		b = legacyMirror(b, p.mirror)
	}
	if p.rot != rotNone {
		b = legacyRotate(b, p.rot)
	}
	return b
}

// shapeCheckBlocks is StructurePiece.SHAPE_CHECK_BLOCKS: placed states
// of these blocks are queued for post-processing (their neighbour
// shapes are resolved when the chunk turns full).
var shapeCheckBlocks = map[string]bool{
	"nether_brick_fence": true, "torch": true, "wall_torch": true, "oak_fence": true, "spruce_fence": true,
	"dark_oak_fence": true, "pale_oak_fence": true, "acacia_fence": true, "birch_fence": true, "jungle_fence": true,
	"ladder": true, "iron_bars": true,
}

// placeBlock is StructurePiece.placeBlock.
func (p *legacyPiece) placeBlock(c *PieceContext, b world.Block, x, y, z int32, bb BoundingBox) {
	pos := p.worldPos(x, y, z)
	if !bb.IsInside(pos) {
		return
	}
	if p.canReplace != nil && !p.canReplace(c, x, y, z, bb) {
		return
	}
	b = p.transformState(b)
	c.Region.set(pos, b)
	if n, _ := world.StateOwner(b); shapeCheckBlocks[n] {
		c.Region.markPostProcess(pos)
	}
}

// getBlock is StructurePiece.getBlock: air outside bb.
func (p *legacyPiece) getBlock(c *PieceContext, x, y, z int32, bb BoundingBox) world.Block {
	pos := p.worldPos(x, y, z)
	if !bb.IsInside(pos) {
		return world.Air
	}
	return c.Region.at(pos)
}

// isInterior is StructurePiece.isInterior: the cell above (x, y, z) is
// in bb and below the OCEAN_FLOOR_WG height.
func (p *legacyPiece) isInterior(c *PieceContext, x, y, z int32, bb BoundingBox) bool {
	pos := p.worldPos(x, y+1, z)
	if !bb.IsInside(pos) {
		return false
	}
	return pos[1] < c.Region.Height(HMOceanFloorWG, pos[0], pos[2])
}

func onBoxEdge(x, y, z, x0, y0, z0, x1, y1, z1 int32) bool {
	return y == y0 || y == y1 || x == x0 || x == x1 || z == z0 || z == z1
}

// generateBox is StructurePiece.generateBox with fixed edge and inner
// states (y, then x, then z).
func (p *legacyPiece) generateBox(c *PieceContext, bb BoundingBox, x0, y0, z0, x1, y1, z1 int32, edge, inner world.Block, skipAir bool) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if skipAir && isAir(p.getBlock(c, x, y, z, bb)) {
					continue
				}
				if onBoxEdge(x, y, z, x0, y0, z0, x1, y1, z1) {
					p.placeBlock(c, edge, x, y, z, bb)
				} else {
					p.placeBlock(c, inner, x, y, z, bb)
				}
			}
		}
	}
}

// generateBoxIn is generateBox over a local BoundingBox.
func (p *legacyPiece) generateBoxIn(c *PieceContext, bb, b BoundingBox, edge, inner world.Block, skipAir bool) {
	p.generateBox(c, bb, b.MinX, b.MinY, b.MinZ, b.MaxX, b.MaxY, b.MaxZ, edge, inner, skipAir)
}

// blockSelector is StructurePiece.BlockSelector.
type blockSelector interface {
	next(r Random, x, y, z int32, edge bool)
	get() world.Block
}

// generateBoxSel is generateBox with a block selector.
func (p *legacyPiece) generateBoxSel(c *PieceContext, bb BoundingBox, x0, y0, z0, x1, y1, z1 int32, skipAir bool, r Random, sel blockSelector) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if skipAir && isAir(p.getBlock(c, x, y, z, bb)) {
					continue
				}
				sel.next(r, x, y, z, onBoxEdge(x, y, z, x0, y0, z0, x1, y1, z1))
				p.placeBlock(c, sel.get(), x, y, z, bb)
			}
		}
	}
}

// generateMaybeBox is StructurePiece.generateMaybeBox: each cell drawn
// against prob first.
func (p *legacyPiece) generateMaybeBox(c *PieceContext, bb BoundingBox, r Random, prob float32, x0, y0, z0, x1, y1, z1 int32, edge, inner world.Block, skipAir, onlyInterior bool) {
	for y := y0; y <= y1; y++ {
		for x := x0; x <= x1; x++ {
			for z := z0; z <= z1; z++ {
				if r.NextFloat() > prob {
					continue
				}
				if skipAir && isAir(p.getBlock(c, x, y, z, bb)) {
					continue
				}
				if onlyInterior && !p.isInterior(c, x, y, z, bb) {
					continue
				}
				if onBoxEdge(x, y, z, x0, y0, z0, x1, y1, z1) {
					p.placeBlock(c, edge, x, y, z, bb)
				} else {
					p.placeBlock(c, inner, x, y, z, bb)
				}
			}
		}
	}
}

// maybeGenerateBlock is StructurePiece.maybeGenerateBlock.
func (p *legacyPiece) maybeGenerateBlock(c *PieceContext, bb BoundingBox, r Random, prob float32, x, y, z int32, b world.Block) {
	if r.NextFloat() < prob {
		p.placeBlock(c, b, x, y, z, bb)
	}
}

// generateUpperHalfSphere is StructurePiece.generateUpperHalfSphere
// (float arithmetic, inside when the squared radius is <= 1.05).
func (p *legacyPiece) generateUpperHalfSphere(c *PieceContext, bb BoundingBox, x0, y0, z0, x1, y1, z1 int32, b world.Block, skipAir bool) {
	w := float32(x1 - x0 + 1)
	h := float32(y1 - y0 + 1)
	d := float32(z1 - z0 + 1)
	cx := float32(x0) + w/2
	cz := float32(z0) + d/2
	for y := y0; y <= y1; y++ {
		fy := float32(y-y0) / h
		for x := x0; x <= x1; x++ {
			fx := (float32(x) - cx) / float32(w*0.5)
			for z := z0; z <= z1; z++ {
				fz := (float32(z) - cz) / float32(d*0.5)
				if skipAir && isAir(p.getBlock(c, x, y, z, bb)) {
					continue
				}
				if float32(float32(float32(fx*fx)+float32(fy*fy))+float32(fz*fz)) <= 1.05 {
					p.placeBlock(c, b, x, y, z, bb)
				}
			}
		}
	}
}

// isReplaceableByStructures is StructurePiece.isReplaceableByStructures.
func isReplaceableByStructures(b world.Block) bool {
	return isAir(b) || props(b)&(1<<bpLiquidBlock) != 0 || isBlock(b, "glow_lichen") || isBlock(b, "seagrass") || isBlock(b, "tall_seagrass")
}

// fillColumnDown is StructurePiece.fillColumnDown: from (x, y, z) down
// while the cell is replaceable and above the floor + 1 (the state is
// not transformed).
func (p *legacyPiece) fillColumnDown(c *PieceContext, b world.Block, x, y, z int32, bb BoundingBox) {
	pos := p.worldPos(x, y, z)
	if !bb.IsInside(pos) {
		return
	}
	for isReplaceableByStructures(c.Region.at(pos)) && pos[1] > c.Region.MinY()+1 {
		c.Region.set(pos, b)
		pos[1]--
	}
}

// createChest is StructurePiece.createChest at a world position: a
// chest (reoriented when no state is given) and its loot table with a
// seed drawn from r; false when outside bb or a chest is already there.
func createChestAt(c *PieceContext, bb BoundingBox, r Random, pos BlockPos, loot string, st world.Block) bool {
	if !bb.IsInside(pos) || isBlock(c.Region.at(pos), "chest") {
		return false
	}
	if st == world.Air {
		st = reorientChest(c.Region, pos, chestBlock())
	}
	c.Region.set(pos, st)
	if isBlock(c.Region.at(pos), "chest") {
		structLootBlockEntity(c.Region, pos, loot, r.NextLong())
	}
	return true
}

// setStructLootTable is RandomizableContainer.setLootTable on the block
// entity at q (kept with whatever the block entity already holds).
func setStructLootTable(rg *Region, q BlockPos, table string, seed int64) {
	structLootBlockEntity(rg, q, table, seed)
}

// createChest is StructurePiece.createChest at local coordinates.
func (p *legacyPiece) createChest(c *PieceContext, bb BoundingBox, r Random, x, y, z int32, loot string) bool {
	return createChestAt(c, bb, r, p.worldPos(x, y, z), loot, world.Air)
}

// createDispenser is StructurePiece.createDispenser: a dispenser facing
// dir placed through placeBlock, its loot table seeded from r.
func (p *legacyPiece) createDispenser(c *PieceContext, bb BoundingBox, r Random, x, y, z int32, dir int, loot string) bool {
	pos := p.worldPos(x, y, z)
	if !bb.IsInside(pos) || isBlock(c.Region.at(pos), "dispenser") {
		return false
	}
	st, _ := world.BlockStateOf("dispenser", map[string]string{"facing": dirNameOf[dir]})
	p.placeBlock(c, st, x, y, z, bb)
	if isBlock(c.Region.at(pos), "dispenser") {
		structLootBlockEntity(c.Region, pos, loot, r.NextLong())
	}
	return true
}

// findCollisionPiece is StructurePiece.findCollisionPiece.
func findCollisionPiece(pieces []StructurePiece, b BoundingBox) StructurePiece {
	for _, p := range pieces {
		if p.BoundingBox().Intersects(b) {
			return p
		}
	}
	return nil
}

// piecesBox is StructurePiece.createBoundingBox over pieces.
func piecesBox(pieces []StructurePiece) BoundingBox {
	b := pieces[0].BoundingBox()
	for _, p := range pieces[1:] {
		b = b.Encapsulate(p.BoundingBox())
	}
	return b
}

// offsetPiecesVertically is StructurePiecesBuilder.offsetPiecesVertically.
func offsetPiecesVertically(pieces []StructurePiece, dy int32) {
	for _, p := range pieces {
		p.Move(0, dy, 0)
	}
}

// moveBelowSeaLevel is StructurePiecesBuilder.moveBelowSeaLevel.
func moveBelowSeaLevel(pieces []StructurePiece, seaLevel, minY int32, r Random, off int32) int32 {
	top := seaLevel - off
	b := piecesBox(pieces)
	y := b.YSpan() + minY + 1
	if y < top {
		y += r.NextIntN(top - y)
	}
	dy := y - b.MaxY
	offsetPiecesVertically(pieces, dy)
	return dy
}

// moveInsideHeights is StructurePiecesBuilder.moveInsideHeights.
func moveInsideHeights(pieces []StructurePiece, r Random, lo, hi int32) {
	b := piecesBox(pieces)
	span := hi - lo + 1 - b.YSpan()
	y := lo
	if span > 1 {
		y = lo + r.NextIntN(span)
	}
	offsetPiecesVertically(pieces, y-b.MinY)
}

// boxListTag stores a list of boxes (BoundingBox.CODEC.listOf()).
func boxListTag(bs []BoundingBox) nbt.List {
	l := nbt.List{Elem: nbt.TagIntArray}
	if len(bs) == 0 {
		l.Elem = nbt.TagEnd
	}
	for _, b := range bs {
		l.Items = append(l.Items, bbArray(b))
	}
	return l
}

func loadBoxList(v any) []BoundingBox {
	l, _ := v.(nbt.List)
	var out []BoundingBox
	for _, it := range l.Items {
		if a, ok := it.([]int32); ok && len(a) == 6 {
			out = append(out, BoundingBox{a[0], a[1], a[2], a[3], a[4], a[5]})
		}
	}
	return out
}

func boolByte(b bool) int8 {
	if b {
		return 1
	}
	return 0
}

func tagBool(t map[string]any, k string) bool { return nbtInt(t[k]) != 0 }

// stateOf builds a block state from properties, panicking on a bad
// combination (the legacy pieces only name states that exist).
func stateOf(name string, kv ...string) world.Block {
	var m map[string]string
	if len(kv) > 0 {
		m = map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
	}
	b, ok := world.BlockStateOf(name, m)
	if !ok {
		panic(fmt.Sprintf("worldgen: bad state %s %v", name, kv))
	}
	return b
}

// railRotate / railMirror are RailShape under Rotation / Mirror
// (BaseRailBlock.rotate / mirror).
var railCW = map[string]string{
	"north_south": "east_west", "east_west": "north_south",
	"ascending_east": "ascending_south", "ascending_west": "ascending_north", "ascending_north": "ascending_east", "ascending_south": "ascending_west",
	"south_east": "south_west", "south_west": "north_west", "north_west": "north_east", "north_east": "south_east",
}

var railMirrorLR = map[string]string{
	"ascending_north": "ascending_south", "ascending_south": "ascending_north",
	"south_east": "north_east", "south_west": "north_west", "north_west": "south_west", "north_east": "south_east",
}

var railMirrorFB = map[string]string{
	"ascending_east": "ascending_west", "ascending_west": "ascending_east",
	"south_east": "south_west", "south_west": "south_east", "north_west": "north_east", "north_east": "north_west",
}

// legacyRotate is BlockState.rotate for the states legacy pieces place:
// the template families plus rail shapes.
func legacyRotate(b world.Block, rot int) world.Block {
	out := rotateState(b, rot)
	if s, ok := world.StateProperty(b, "shape"); ok && blockInTag(b, "rails") {
		for i := 0; i < rot; i++ {
			if n, ok := railCW[s]; ok {
				s = n
			}
		}
		out = withProp(out, "shape", s)
	}
	return out
}

// stairMirror is StairBlock.mirror's shape swap for a facing on the
// mirrored axis.
var stairShapeMirror = map[string]string{"inner_left": "inner_right", "inner_right": "inner_left", "outer_left": "outer_right", "outer_right": "outer_left"}

// legacyMirror is BlockState.mirror for the states legacy pieces place.
func legacyMirror(b world.Block, mirror int) world.Block {
	if mirror == mirNone {
		return b
	}
	out := mirrorState(b, mirror)
	if s, ok := world.StateProperty(b, "shape"); ok {
		if blockInTag(b, "rails") {
			m := railMirrorLR
			if mirror == mirFrontBack {
				m = railMirrorFB
			}
			if n, ok := m[s]; ok {
				out = withProp(out, "shape", n)
			}
		} else if blockInTag(b, "stairs") {
			// StairBlock.mirror: LEFT_RIGHT on a z facing swaps left and
			// right of both inner and outer shapes; FRONT_BACK on an x
			// facing swaps only the outer ones; otherwise the shape stays.
			f, _ := world.StateProperty(b, "facing")
			axisZ := f == "north" || f == "south"
			if n, ok := stairShapeMirror[s]; ok {
				if mirror == mirLeftRight && axisZ || mirror == mirFrontBack && !axisZ && (s == "outer_left" || s == "outer_right") {
					out = withProp(out, "shape", n)
				}
			}
		}
	}
	if h, ok := world.StateProperty(b, "hinge"); ok && mirror != mirNone {
		if h == "left" {
			out = withProp(out, "hinge", "right")
		} else {
			out = withProp(out, "hinge", "left")
		}
	}
	return out
}
