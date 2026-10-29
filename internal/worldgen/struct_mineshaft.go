package worldgen

import (
	"encoding/json"
	"sync"
	"sync/atomic"

	"mc-go/internal/world"
)

// Mineshafts (MineshaftStructure + MineshaftPieces), measured from the
// 26.2 bytecode:
//
//   - findGenerationPoint skips one nextDouble (the legacy_type_3
//     frequency draw), builds every piece right away from a room at the
//     chunk's block (2, 50, 2) and moves them: the normal type below sea
//     level (moveBelowSeaLevel with offset 10), the mesa type so the
//     pieces' center sits at a random height between sea level and the
//     surface (WORLD_SURFACE_WG base height). The biome check samples
//     (middle x, 50 + that shift, min z).
//   - children: generateAndAddPiece stops past depth 8 or 80 blocks from
//     the room; createRandomShaftPiece rolls nextInt(100): >= 80 a
//     crossing, >= 70 stairs, else a corridor, each giving up (no
//     fallback) when its box collides.
//   - postProcess of every piece returns early in an invalid location
//     (a mineshaft_blocking biome at the clipped box center, or any
//     liquid block on the clipped box's faces); canBeReplaced spares
//     planks, logs, fences and chains.
//
// The chest minecarts corridors spawn (MinecartChest entities) are not
// modeled; their rail and the random draws (rail shape nextBoolean,
// loot seed nextLong) are.

// mineshaftType is MineshaftStructure.Type: wood, planks and fence.
type mineshaftType struct{ wood, planks, fence world.Block }

var mineshaftTypes = sync.OnceValue(func() [2]mineshaftType {
	return [2]mineshaftType{
		{blockNamed("oak_log"), blockNamed("oak_planks"), blockNamed("oak_fence")},
		{blockNamed("dark_oak_log"), blockNamed("dark_oak_planks"), blockNamed("dark_oak_fence")},
	}
})

var mineshaftBlockingBiomes = sync.OnceValue(func() map[string]bool {
	return biomeHolderSet(Vanilla(), json.RawMessage(`"#minecraft:mineshaft_blocking"`))
})

// msBase is MineShaftPiece.
type msBase struct {
	legacyPiece
	mtype int32
}

func (p *msBase) wood() world.Block   { return mineshaftTypes()[p.mtype].wood }
func (p *msBase) planks() world.Block { return mineshaftTypes()[p.mtype].planks }
func (p *msBase) fence() world.Block  { return mineshaftTypes()[p.mtype].fence }

func (p *msBase) init(typ string, depth, mtype int32, b BoundingBox) {
	p.legacyPiece = newLegacyPiece(typ, depth, b)
	p.mtype = mtype
	p.canReplace = p.replaceable
}

// replaceable is MineShaftPiece.canBeReplaced.
func (p *msBase) replaceable(c *PieceContext, x, y, z int32, bb BoundingBox) bool {
	b := p.getBlock(c, x, y, z, bb)
	n, _ := world.StateOwner(b)
	t := mineshaftTypes()[p.mtype]
	return !isBlock(t.planks, n) && !isBlock(t.wood, n) && !isBlock(t.fence, n) && n != "iron_chain"
}

func (p *msBase) SaveData(t map[string]any) { t["MST"] = p.mtype }

func (p *msBase) loadBase(typ string, t map[string]any) error {
	lp, err := loadLegacyBase(typ, t)
	if err != nil {
		return err
	}
	p.legacyPiece = lp
	p.mtype = tagInt(t, "MST")
	if p.mtype < 0 || p.mtype > 1 {
		p.mtype = 0
	}
	p.canReplace = p.replaceable
	return nil
}

// isSupportingBox is MineShaftPiece.isSupportingBox: no air above any
// cell of the row.
func (p *msBase) isSupportingBox(c *PieceContext, bb BoundingBox, x0, x1, y, z int32) bool {
	for x := x0; x <= x1; x++ {
		if isAir(p.getBlock(c, x, y+1, z, bb)) {
			return false
		}
	}
	return true
}

// isInInvalidLocation is MineShaftPiece.isInInvalidLocation.
func (p *msBase) isInInvalidLocation(c *PieceContext, bb BoundingBox) bool {
	b := p.Box
	x0, y0, z0 := max(b.MinX-1, bb.MinX), max(b.MinY-1, bb.MinY), max(b.MinZ-1, bb.MinZ)
	x1, y1, z1 := min(b.MaxX+1, bb.MaxX), min(b.MaxY+1, bb.MaxY), min(b.MaxZ+1, bb.MaxZ)
	if mineshaftBlockingBiomes()[c.Region.Biome((x0+x1)/2, (y0+y1)/2, (z0+z1)/2)] {
		return true
	}
	liquid := func(x, y, z int32) bool { return props(c.Region.Block(x, y, z))&(1<<bpLiquidBlock) != 0 }
	for x := x0; x <= x1; x++ {
		for z := z0; z <= z1; z++ {
			if liquid(x, y0, z) || liquid(x, y1, z) {
				return true
			}
		}
	}
	for x := x0; x <= x1; x++ {
		for y := y0; y <= y1; y++ {
			if liquid(x, y, z0) || liquid(x, y, z1) {
				return true
			}
		}
	}
	for z := z0; z <= z1; z++ {
		for y := y0; y <= y1; y++ {
			if liquid(x0, y, z) || liquid(x1, y, z) {
				return true
			}
		}
	}
	return false
}

// setPlanksBlock is MineShaftPiece.setPlanksBlock: planks under an
// interior cell whose top face is not sturdy.
func (p *msBase) setPlanksBlock(c *PieceContext, bb BoundingBox, st world.Block, x, y, z int32) {
	if !p.isInterior(c, x, y, z, bb) {
		return
	}
	pos := p.worldPos(x, y, z)
	if !sturdyFull(c.Region.at(pos), dirUp) {
		c.Region.set(pos, st)
	}
}

// msChild is a mineshaft piece that adds children.
type msChild interface {
	StructurePiece
	addChildren(start *msRoom, acc *[]StructurePiece, r Random)
}

// msGenerateAndAdd is MineshaftPieces.generateAndAddPiece.
func msGenerateAndAdd(start *msRoom, acc *[]StructurePiece, r Random, x, y, z int32, d int, depth int32) msChild {
	if depth > 8 {
		return nil
	}
	if iabs32(x-start.Box.MinX) > 80 || iabs32(z-start.Box.MinZ) > 80 {
		return nil
	}
	p := msCreateRandom(acc, r, x, y, z, d, depth+1, start.mtype)
	if p != nil {
		*acc = append(*acc, p)
		p.addChildren(start, acc, r)
	}
	return p
}


// msCreateRandom is MineshaftPieces.createRandomShaftPiece.
func msCreateRandom(acc *[]StructurePiece, r Random, x, y, z int32, d int, depth, mtype int32) msChild {
	n := r.NextIntN(100)
	switch {
	case n >= 80:
		if b, ok := msFindCrossing(*acc, r, x, y, z, d); ok {
			return newMSCrossing(depth, b, d, mtype)
		}
	case n >= 70:
		if b, ok := msFindStairs(*acc, x, y, z, d); ok {
			return newMSStairs(depth, b, d, mtype)
		}
	default:
		if b, ok := msFindCorridorSize(*acc, r, x, y, z, d); ok {
			return newMSCorridor(depth, r, b, d, mtype)
		}
	}
	return nil
}

// msRoom is MineShaftRoom.
type msRoom struct {
	msBase
	entrances []BoundingBox
}

func newMSRoom(depth int32, r Random, x, z int32, mtype int32) *msRoom {
	x1 := x + 7 + r.NextIntN(6)
	y1 := 54 + r.NextIntN(6)
	z1 := z + 7 + r.NextIntN(6)
	p := &msRoom{}
	p.init("msroom", depth, mtype, BoundingBox{x, 50, z, x1, y1, z1})
	return p
}

func (p *msRoom) addChildren(start *msRoom, acc *[]StructurePiece, r Random) {
	depth := p.Depth
	k := p.Box.YSpan() - 3 - 1
	if k <= 0 {
		k = 1
	}
	b := p.Box
	xs := b.MaxX - b.MinX + 1
	zs := b.MaxZ - b.MinZ + 1
	for j := int32(0); j < xs; j += 4 {
		j += r.NextIntN(xs)
		if j+3 > xs {
			break
		}
		if c := msGenerateAndAdd(start, acc, r, b.MinX+j, b.MinY+r.NextIntN(k)+1, b.MinZ-1, dirNorth, depth); c != nil {
			cb := c.BoundingBox()
			p.entrances = append(p.entrances, BoundingBox{cb.MinX, cb.MinY, b.MinZ, cb.MaxX, cb.MaxY, b.MinZ + 1})
		}
	}
	for j := int32(0); j < xs; j += 4 {
		j += r.NextIntN(xs)
		if j+3 > xs {
			break
		}
		if c := msGenerateAndAdd(start, acc, r, b.MinX+j, b.MinY+r.NextIntN(k)+1, b.MaxZ+1, dirSouth, depth); c != nil {
			cb := c.BoundingBox()
			p.entrances = append(p.entrances, BoundingBox{cb.MinX, cb.MinY, b.MaxZ - 1, cb.MaxX, cb.MaxY, b.MaxZ})
		}
	}
	for j := int32(0); j < zs; j += 4 {
		j += r.NextIntN(zs)
		if j+3 > zs {
			break
		}
		if c := msGenerateAndAdd(start, acc, r, b.MinX-1, b.MinY+r.NextIntN(k)+1, b.MinZ+j, dirWest, depth); c != nil {
			cb := c.BoundingBox()
			p.entrances = append(p.entrances, BoundingBox{b.MinX, cb.MinY, cb.MinZ, b.MinX + 1, cb.MaxY, cb.MaxZ})
		}
	}
	for j := int32(0); j < zs; j += 4 {
		j += r.NextIntN(zs)
		if j+3 > zs {
			break
		}
		if c := msGenerateAndAdd(start, acc, r, b.MaxX+1, b.MinY+r.NextIntN(k)+1, b.MinZ+j, dirEast, depth); c != nil {
			cb := c.BoundingBox()
			p.entrances = append(p.entrances, BoundingBox{b.MaxX - 1, cb.MinY, cb.MinZ, b.MaxX, cb.MaxY, cb.MaxZ})
		}
	}
}

func (p *msRoom) PostProcess(c *PieceContext) {
	bb := c.ChunkBox
	if p.isInInvalidLocation(c, bb) {
		return
	}
	b := p.Box
	air := caveAir()
	p.generateBox(c, bb, b.MinX, b.MinY+1, b.MinZ, b.MaxX, min(b.MinY+3, b.MaxY), b.MaxZ, air, air, false)
	for _, e := range p.entrances {
		p.generateBox(c, bb, e.MinX, e.MaxY-2, e.MinZ, e.MaxX, e.MaxY, e.MaxZ, air, air, false)
	}
	p.generateUpperHalfSphere(c, bb, b.MinX, b.MinY+4, b.MinZ, b.MaxX, b.MaxY, b.MaxZ, air, false)
}

// Move is MineShaftRoom.move: the entrances move with the room.
func (p *msRoom) Move(dx, dy, dz int32) {
	p.msBase.Move(dx, dy, dz)
	for i := range p.entrances {
		p.entrances[i] = p.entrances[i].Moved(dx, dy, dz)
	}
}

func (p *msRoom) SaveData(t map[string]any) {
	p.msBase.SaveData(t)
	t["Entrances"] = boxListTag(p.entrances)
}


// msCorridor is MineShaftCorridor.
type msCorridor struct {
	msBase
	hasRails, spider bool
	placedSpider     atomic.Bool
	numSections      int32
}

func newMSCorridor(depth int32, r Random, b BoundingBox, d int, mtype int32) *msCorridor {
	p := &msCorridor{}
	p.init("mscorridor", depth, mtype, b)
	p.setOrientation(d)
	p.hasRails = r.NextIntN(3) == 0
	p.spider = !p.hasRails && r.NextIntN(23) == 0
	if d == dirNorth || d == dirSouth {
		p.numSections = (b.MaxZ - b.MinZ + 1) / 5
	} else {
		p.numSections = (b.MaxX - b.MinX + 1) / 5
	}
	return p
}

// msFindCorridorSize is MineShaftCorridor.findCorridorSize: 2 to 4
// sections, shortened until the box is free.
func msFindCorridorSize(acc []StructurePiece, r Random, x, y, z int32, d int) (BoundingBox, bool) {
	for i := r.NextIntN(3) + 2; i > 0; i-- {
		l := i * 5
		var b BoundingBox
		switch d {
		case dirSouth:
			b = BoundingBox{0, 0, 0, 2, 2, l - 1}
		case dirWest:
			b = BoundingBox{-(l - 1), 0, 0, 0, 2, 2}
		case dirEast:
			b = BoundingBox{0, 0, 0, l - 1, 2, 2}
		default:
			b = BoundingBox{0, 0, -(l - 1), 2, 2, 0}
		}
		b = b.Moved(x, y, z)
		if findCollisionPiece(acc, b) == nil {
			return b, true
		}
	}
	return BoundingBox{}, false
}

func (p *msCorridor) addChildren(start *msRoom, acc *[]StructurePiece, r Random) {
	depth := p.Depth
	n := r.NextIntN(4)
	b := p.Box
	o := p.orient
	switch o {
	case dirSouth:
		switch {
		case n <= 1:
			msGenerateAndAdd(start, acc, r, b.MinX, b.MinY-1+r.NextIntN(3), b.MaxZ+1, o, depth)
		case n == 2:
			msGenerateAndAdd(start, acc, r, b.MinX-1, b.MinY-1+r.NextIntN(3), b.MaxZ-3, dirWest, depth)
		default:
			msGenerateAndAdd(start, acc, r, b.MaxX+1, b.MinY-1+r.NextIntN(3), b.MaxZ-3, dirEast, depth)
		}
	case dirWest:
		switch {
		case n <= 1:
			msGenerateAndAdd(start, acc, r, b.MinX-1, b.MinY-1+r.NextIntN(3), b.MinZ, o, depth)
		case n == 2:
			msGenerateAndAdd(start, acc, r, b.MinX, b.MinY-1+r.NextIntN(3), b.MinZ-1, dirNorth, depth)
		default:
			msGenerateAndAdd(start, acc, r, b.MinX, b.MinY-1+r.NextIntN(3), b.MaxZ+1, dirSouth, depth)
		}
	case dirEast:
		switch {
		case n <= 1:
			msGenerateAndAdd(start, acc, r, b.MaxX+1, b.MinY-1+r.NextIntN(3), b.MinZ, o, depth)
		case n == 2:
			msGenerateAndAdd(start, acc, r, b.MaxX-3, b.MinY-1+r.NextIntN(3), b.MinZ-1, dirNorth, depth)
		default:
			msGenerateAndAdd(start, acc, r, b.MaxX-3, b.MinY-1+r.NextIntN(3), b.MaxZ+1, dirSouth, depth)
		}
	default: // north
		switch {
		case n <= 1:
			msGenerateAndAdd(start, acc, r, b.MinX, b.MinY-1+r.NextIntN(3), b.MinZ-1, o, depth)
		case n == 2:
			msGenerateAndAdd(start, acc, r, b.MinX-1, b.MinY-1+r.NextIntN(3), b.MinZ, dirWest, depth)
		default:
			msGenerateAndAdd(start, acc, r, b.MaxX+1, b.MinY-1+r.NextIntN(3), b.MinZ, dirEast, depth)
		}
	}
	if depth >= 8 {
		return
	}
	if o == dirNorth || o == dirSouth {
		for z := b.MinZ + 3; z+3 <= b.MaxZ; z += 5 {
			switch r.NextIntN(5) {
			case 0:
				msGenerateAndAdd(start, acc, r, b.MinX-1, b.MinY, z, dirWest, depth+1)
			case 1:
				msGenerateAndAdd(start, acc, r, b.MaxX+1, b.MinY, z, dirEast, depth+1)
			}
		}
	} else {
		for x := b.MinX + 3; x+3 <= b.MaxX; x += 5 {
			switch r.NextIntN(5) {
			case 0:
				msGenerateAndAdd(start, acc, r, x, b.MinY, b.MinZ-1, dirNorth, depth+1)
			case 1:
				msGenerateAndAdd(start, acc, r, x, b.MinY, b.MaxZ+1, dirSouth, depth+1)
			}
		}
	}
}

// createChest is MineShaftCorridor.createChest: a rail (random shape)
// for a chest minecart over a non-air floor. The minecart entity is not
// spawned; its loot seed is drawn.
func (p *msCorridor) createChest(c *PieceContext, bb BoundingBox, r Random, x, y, z int32) bool {
	pos := p.worldPos(x, y, z)
	if !bb.IsInside(pos) || !isAir(c.Region.at(pos)) || isAir(c.Region.at(offset(pos, 0, -1, 0))) {
		return false
	}
	shape := "east_west"
	if r.NextBool() {
		shape = "north_south"
	}
	p.placeBlock(c, stateOf("rail", "shape", shape), x, y, z, bb)
	r.NextLong() // MinecartChest.setLootTable seed
	return true
}

func (p *msCorridor) PostProcess(c *PieceContext) {
	bb := c.ChunkBox
	if p.isInInvalidLocation(c, bb) {
		return
	}
	r := c.Random
	m := p.numSections*5 - 1
	planks := p.planks()
	air := caveAir()
	p.generateBox(c, bb, 0, 0, 0, 2, 1, m, air, air, false)
	p.generateMaybeBox(c, bb, r, 0.8, 0, 2, 0, 2, 2, m, air, air, false, false)
	if p.spider {
		p.generateMaybeBox(c, bb, r, 0.6, 0, 0, 0, 2, 1, m, blockNamed("cobweb"), air, false, true)
	}
	for i := int32(0); i < p.numSections; i++ {
		z := 2 + i*5
		p.placeSupport(c, bb, 0, 0, z, 2, 2, r)
		p.maybePlaceCobWeb(c, bb, r, 0.1, 0, 2, z-1)
		p.maybePlaceCobWeb(c, bb, r, 0.1, 2, 2, z-1)
		p.maybePlaceCobWeb(c, bb, r, 0.1, 0, 2, z+1)
		p.maybePlaceCobWeb(c, bb, r, 0.1, 2, 2, z+1)
		p.maybePlaceCobWeb(c, bb, r, 0.05, 0, 2, z-2)
		p.maybePlaceCobWeb(c, bb, r, 0.05, 2, 2, z-2)
		p.maybePlaceCobWeb(c, bb, r, 0.05, 0, 2, z+2)
		p.maybePlaceCobWeb(c, bb, r, 0.05, 2, 2, z+2)
		if r.NextIntN(100) == 0 {
			p.createChest(c, bb, r, 2, 0, z-1)
		}
		if r.NextIntN(100) == 0 {
			p.createChest(c, bb, r, 0, 0, z+1)
		}
		if p.spider && !p.placedSpider.Load() {
			sz := z - 1 + r.NextIntN(3)
			pos := p.worldPos(1, 0, sz)
			if bb.IsInside(pos) && p.isInterior(c, 1, 0, sz, bb) {
				p.placedSpider.Store(true)
				c.Region.set(pos, spawnerBlock())
				// SpawnerBlockEntity.setEntityId(CAVE_SPIDER): the empty
				// spawn potentials draw nothing.
			}
		}
	}
	for x := int32(0); x <= 2; x++ {
		for z := int32(0); z <= m; z++ {
			p.setPlanksBlock(c, bb, planks, x, -1, z)
		}
	}
	p.placeDoubleLowerOrUpperSupport(c, bb, 0, -1, 2)
	if p.numSections > 1 {
		p.placeDoubleLowerOrUpperSupport(c, bb, 0, -1, m-2)
	}
	if p.hasRails {
		rail := stateOf("rail", "shape", "north_south")
		for z := int32(0); z <= m; z++ {
			st := p.getBlock(c, 1, -1, z, bb)
			if !isAir(st) && solidRender(st) {
				prob := float32(0.9)
				if p.isInterior(c, 1, 0, z, bb) {
					prob = 0.7
				}
				p.maybeGenerateBlock(c, bb, r, prob, 1, 0, z, rail)
			}
		}
	}
}

// placeDoubleLowerOrUpperSupport is the corridor's pillar / chain pair
// under planks at x and x + 2.
func (p *msCorridor) placeDoubleLowerOrUpperSupport(c *PieceContext, bb BoundingBox, x, y, z int32) {
	wood, planks := p.wood(), p.planks()
	if isBlock(p.getBlock(c, x, y, z, bb), blockName(planks)) {
		p.fillPillarDownOrChainUp(c, wood, x, y, z, bb)
	}
	if isBlock(p.getBlock(c, x+2, y, z, bb), blockName(planks)) {
		p.fillPillarDownOrChainUp(c, wood, x+2, y, z, bb)
	}
}

func blockName(b world.Block) string { n, _ := world.StateOwner(b); return n }

// fillPillarDownOrChainUp is MineShaftCorridor.fillPillarDownOrChainUp:
// a log pillar down to the first sturdy top within 20 blocks, else a
// fence and chains up to a center-supporting ceiling within 50.
func (p *msCorridor) fillPillarDownOrChainUp(c *PieceContext, st world.Block, x, y, z int32, bb BoundingBox) {
	pos := p.worldPos(x, y, z)
	if !bb.IsInside(pos) {
		return
	}
	rg := c.Region
	y0 := pos[1]
	down, up := true, true
	for i := int32(1); down || up; i++ {
		if down {
			pos[1] = y0 - i
			s := rg.at(pos)
			repl := isReplaceableByStructures(s) && !isBlock(s, "lava")
			if !repl && sturdyFull(s, dirUp) {
				for yy := y0 - i + 1; yy < y0; yy++ {
					rg.set(BlockPos{pos[0], yy, pos[2]}, st)
				}
				return
			}
			down = i <= 20 && repl && pos[1] > rg.MinY()+1
		}
		if up {
			pos[1] = y0 + i
			s := rg.at(pos)
			repl := isReplaceableByStructures(s)
			if !repl && sturdyCenter(s, dirDown) && !hasClass(blockName(s), "FallingBlock") {
				rg.set(BlockPos{pos[0], y0 + 1, pos[2]}, p.fence())
				chain := blockNamed("iron_chain")
				for yy := y0 + 2; yy < y0+i; yy++ {
					rg.set(BlockPos{pos[0], yy, pos[2]}, chain)
				}
				return
			}
			up = i <= 50 && repl && pos[1] < rg.MaxY()
		}
	}
}

func iabs32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

// placeSupport is MineShaftCorridor.placeSupport: two fence posts under
// a planks beam (or two lone planks one time in four), with a 5%
// chance of a wall torch on each side of the beam.
func (p *msCorridor) placeSupport(c *PieceContext, bb BoundingBox, x0, y0, z, y1, x1 int32, r Random) {
	if !p.isSupportingBox(c, bb, x0, x1, y1, z) {
		return
	}
	planks, fence := p.planks(), p.fence()
	air := caveAir()
	p.generateBox(c, bb, x0, y0, z, x0, y1-1, z, withProp(fence, "west", "true"), air, false)
	p.generateBox(c, bb, x1, y0, z, x1, y1-1, z, withProp(fence, "east", "true"), air, false)
	if r.NextIntN(4) == 0 {
		p.generateBox(c, bb, x0, y1, z, x0, y1, z, planks, air, false)
		p.generateBox(c, bb, x1, y1, z, x1, y1, z, planks, air, false)
	} else {
		p.generateBox(c, bb, x0, y1, z, x1, y1, z, planks, air, false)
		p.maybeGenerateBlock(c, bb, r, 0.05, x0+1, y1, z-1, stateOf("wall_torch", "facing", "south"))
		p.maybeGenerateBlock(c, bb, r, 0.05, x0+1, y1, z+1, stateOf("wall_torch", "facing", "north"))
	}
}

// maybePlaceCobWeb is MineShaftCorridor.maybePlaceCobWeb.
func (p *msCorridor) maybePlaceCobWeb(c *PieceContext, bb BoundingBox, r Random, prob float32, x, y, z int32) {
	if p.isInterior(c, x, y, z, bb) && r.NextFloat() < prob && p.hasSturdyNeighbours(c, bb, x, y, z, 2) {
		p.placeBlock(c, blockNamed("cobweb"), x, y, z, bb)
	}
}

// hasSturdyNeighbours is MineShaftCorridor.hasSturdyNeighbours: at
// least n neighbours (in bb) with a sturdy face toward the cell.
func (p *msCorridor) hasSturdyNeighbours(c *PieceContext, bb BoundingBox, x, y, z int32, n int) bool {
	pos := p.worldPos(x, y, z)
	k := 0
	for d := 0; d < 6; d++ {
		o := dirOffsets[d]
		q := offset(pos, o[0], o[1], o[2])
		if bb.IsInside(q) && sturdyFull(c.Region.at(q), oppositeDir(d)) {
			k++
			if k >= n {
				return true
			}
		}
	}
	return false
}

func (p *msCorridor) SaveData(t map[string]any) {
	p.msBase.SaveData(t)
	t["hr"] = boolByte(p.hasRails)
	t["sc"] = boolByte(p.spider)
	t["hps"] = boolByte(p.placedSpider.Load())
	t["Num"] = p.numSections
}

// msCrossing is MineShaftCrossing (no orientation: world coordinates).
type msCrossing struct {
	msBase
	direction   int
	isTwoFloored bool
}

func newMSCrossing(depth int32, b BoundingBox, d int, mtype int32) *msCrossing {
	p := &msCrossing{direction: d, isTwoFloored: b.YSpan() > 3}
	p.init("mscrossing", depth, mtype, b)
	return p
}

// msFindCrossing is MineShaftCrossing.findCrossing.
func msFindCrossing(acc []StructurePiece, r Random, x, y, z int32, d int) (BoundingBox, bool) {
	h := int32(2)
	if r.NextIntN(4) == 0 {
		h = 6
	}
	var b BoundingBox
	switch d {
	case dirSouth:
		b = BoundingBox{-1, 0, 0, 3, h, 4}
	case dirWest:
		b = BoundingBox{-4, 0, -1, 0, h, 3}
	case dirEast:
		b = BoundingBox{0, 0, -1, 4, h, 3}
	default:
		b = BoundingBox{-1, 0, -4, 3, h, 0}
	}
	b = b.Moved(x, y, z)
	if findCollisionPiece(acc, b) != nil {
		return BoundingBox{}, false
	}
	return b, true
}

func (p *msCrossing) addChildren(start *msRoom, acc *[]StructurePiece, r Random) {
	depth := p.Depth
	b := p.Box
	switch p.direction {
	case dirSouth:
		msGenerateAndAdd(start, acc, r, b.MinX+1, b.MinY, b.MaxZ+1, dirSouth, depth)
		msGenerateAndAdd(start, acc, r, b.MinX-1, b.MinY, b.MinZ+1, dirWest, depth)
		msGenerateAndAdd(start, acc, r, b.MaxX+1, b.MinY, b.MinZ+1, dirEast, depth)
	case dirWest:
		msGenerateAndAdd(start, acc, r, b.MinX+1, b.MinY, b.MinZ-1, dirNorth, depth)
		msGenerateAndAdd(start, acc, r, b.MinX+1, b.MinY, b.MaxZ+1, dirSouth, depth)
		msGenerateAndAdd(start, acc, r, b.MinX-1, b.MinY, b.MinZ+1, dirWest, depth)
	case dirEast:
		msGenerateAndAdd(start, acc, r, b.MinX+1, b.MinY, b.MinZ-1, dirNorth, depth)
		msGenerateAndAdd(start, acc, r, b.MinX+1, b.MinY, b.MaxZ+1, dirSouth, depth)
		msGenerateAndAdd(start, acc, r, b.MaxX+1, b.MinY, b.MinZ+1, dirEast, depth)
	default:
		msGenerateAndAdd(start, acc, r, b.MinX+1, b.MinY, b.MinZ-1, dirNorth, depth)
		msGenerateAndAdd(start, acc, r, b.MinX-1, b.MinY, b.MinZ+1, dirWest, depth)
		msGenerateAndAdd(start, acc, r, b.MaxX+1, b.MinY, b.MinZ+1, dirEast, depth)
	}
	if p.isTwoFloored {
		if r.NextBool() {
			msGenerateAndAdd(start, acc, r, b.MinX+1, b.MinY+3+1, b.MinZ-1, dirNorth, depth)
		}
		if r.NextBool() {
			msGenerateAndAdd(start, acc, r, b.MinX-1, b.MinY+3+1, b.MinZ+1, dirWest, depth)
		}
		if r.NextBool() {
			msGenerateAndAdd(start, acc, r, b.MaxX+1, b.MinY+3+1, b.MinZ+1, dirEast, depth)
		}
		if r.NextBool() {
			msGenerateAndAdd(start, acc, r, b.MinX+1, b.MinY+3+1, b.MaxZ+1, dirSouth, depth)
		}
	}
}

func (p *msCrossing) PostProcess(c *PieceContext) {
	bb := c.ChunkBox
	if p.isInInvalidLocation(c, bb) {
		return
	}
	planks := p.planks()
	air := caveAir()
	b := p.Box
	if p.isTwoFloored {
		p.generateBox(c, bb, b.MinX+1, b.MinY, b.MinZ, b.MaxX-1, b.MinY+3-1, b.MaxZ, air, air, false)
		p.generateBox(c, bb, b.MinX, b.MinY, b.MinZ+1, b.MaxX, b.MinY+3-1, b.MaxZ-1, air, air, false)
		p.generateBox(c, bb, b.MinX+1, b.MaxY-2, b.MinZ, b.MaxX-1, b.MaxY, b.MaxZ, air, air, false)
		p.generateBox(c, bb, b.MinX, b.MaxY-2, b.MinZ+1, b.MaxX, b.MaxY, b.MaxZ-1, air, air, false)
		p.generateBox(c, bb, b.MinX+1, b.MinY+3, b.MinZ+1, b.MaxX-1, b.MinY+3, b.MaxZ-1, air, air, false)
	} else {
		p.generateBox(c, bb, b.MinX+1, b.MinY, b.MinZ, b.MaxX-1, b.MaxY, b.MaxZ, air, air, false)
		p.generateBox(c, bb, b.MinX, b.MinY, b.MinZ+1, b.MaxX, b.MaxY, b.MaxZ-1, air, air, false)
	}
	p.placeSupportPillar(c, bb, b.MinX+1, b.MinY, b.MinZ+1, b.MaxY)
	p.placeSupportPillar(c, bb, b.MinX+1, b.MinY, b.MaxZ-1, b.MaxY)
	p.placeSupportPillar(c, bb, b.MaxX-1, b.MinY, b.MinZ+1, b.MaxY)
	p.placeSupportPillar(c, bb, b.MaxX-1, b.MinY, b.MaxZ-1, b.MaxY)
	y := b.MinY - 1
	for x := b.MinX; x <= b.MaxX; x++ {
		for z := b.MinZ; z <= b.MaxZ; z++ {
			p.setPlanksBlock(c, bb, planks, x, y, z)
		}
	}
}

// placeSupportPillar is MineShaftCrossing.placeSupportPillar: a planks
// column under a non-air ceiling.
func (p *msCrossing) placeSupportPillar(c *PieceContext, bb BoundingBox, x, y, z, y1 int32) {
	if !isAir(p.getBlock(c, x, y1+1, z, bb)) {
		p.generateBox(c, bb, x, y, z, x, y1, z, p.planks(), caveAir(), false)
	}
}

func (p *msCrossing) SaveData(t map[string]any) {
	p.msBase.SaveData(t)
	t["tf"] = boolByte(p.isTwoFloored)
	t["D"] = dir2D(p.direction)
}

// msStairs is MineShaftStairs.
type msStairs struct{ msBase }

func newMSStairs(depth int32, b BoundingBox, d int, mtype int32) *msStairs {
	p := &msStairs{}
	p.init("msstairs", depth, mtype, b)
	p.setOrientation(d)
	return p
}

// msFindStairs is MineShaftStairs.findStairs.
func msFindStairs(acc []StructurePiece, x, y, z int32, d int) (BoundingBox, bool) {
	var b BoundingBox
	switch d {
	case dirSouth:
		b = BoundingBox{0, -5, 0, 2, 2, 8}
	case dirWest:
		b = BoundingBox{-8, -5, 0, 0, 2, 2}
	case dirEast:
		b = BoundingBox{0, -5, 0, 8, 2, 2}
	default:
		b = BoundingBox{0, -5, -8, 2, 2, 0}
	}
	b = b.Moved(x, y, z)
	if findCollisionPiece(acc, b) != nil {
		return BoundingBox{}, false
	}
	return b, true
}

func (p *msStairs) addChildren(start *msRoom, acc *[]StructurePiece, r Random) {
	depth := p.Depth
	b := p.Box
	switch p.orient {
	case dirSouth:
		msGenerateAndAdd(start, acc, r, b.MinX, b.MinY, b.MaxZ+1, dirSouth, depth)
	case dirWest:
		msGenerateAndAdd(start, acc, r, b.MinX-1, b.MinY, b.MinZ, dirWest, depth)
	case dirEast:
		msGenerateAndAdd(start, acc, r, b.MaxX+1, b.MinY, b.MinZ, dirEast, depth)
	default:
		msGenerateAndAdd(start, acc, r, b.MinX, b.MinY, b.MinZ-1, dirNorth, depth)
	}
}

func (p *msStairs) PostProcess(c *PieceContext) {
	bb := c.ChunkBox
	if p.isInInvalidLocation(c, bb) {
		return
	}
	air := caveAir()
	p.generateBox(c, bb, 0, 5, 0, 2, 7, 1, air, air, false)
	p.generateBox(c, bb, 0, 0, 7, 2, 2, 8, air, air, false)
	for i := int32(0); i < 5; i++ {
		lo := 5 - i
		if i < 4 {
			lo--
		}
		p.generateBox(c, bb, 0, lo, 2+i, 2, 7-i, 2+i, air, air, false)
	}
}

func init() {
	structureTypes["mineshaft"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		mtype := int32(0)
		mesa := d.raw.str("mineshaft_type") == "mesa"
		if mesa {
			mtype = 1
		}
		return func(c *GenContext) (*GenerationStub, bool) {
			r := c.Random
			r.NextDouble()
			x0, z0 := c.ChunkX*16, c.ChunkZ*16
			room := newMSRoom(0, r, x0+2, z0+2, mtype)
			pieces := []StructurePiece{room}
			room.addChildren(room, &pieces, r)
			sea := g.RS.settings.seaLevel
			var dy int32
			if mesa {
				ctr := piecesBox(pieces).Center()
				h := g.BaseHeight(ctr[0], ctr[2], HMWorldSurfaceWG)
				y := sea
				if h > sea {
					y = sea + r.NextIntN(h-sea+1)
				}
				dy = y - ctr[1]
				offsetPiecesVertically(pieces, dy)
			} else {
				dy = moveBelowSeaLevel(pieces, sea, g.RS.lvMinY, r, 10)
			}
			return &GenerationStub{Pos: BlockPos{x0 + 8, 50 + dy, z0}, Generate: func(add func(StructurePiece)) {
				for _, p := range pieces {
					add(p)
				}
			}}, true
		}
	}
	pieceLoaders["msroom"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		p := &msRoom{}
		if err := p.loadBase("msroom", t); err != nil {
			return nil, err
		}
		p.entrances = loadBoxList(t["Entrances"])
		return p, nil
	}
	pieceLoaders["mscorridor"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		p := &msCorridor{}
		if err := p.loadBase("mscorridor", t); err != nil {
			return nil, err
		}
		p.hasRails, p.spider = tagBool(t, "hr"), tagBool(t, "sc")
		p.placedSpider.Store(tagBool(t, "hps"))
		p.numSections = tagInt(t, "Num")
		return p, nil
	}
	pieceLoaders["mscrossing"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		p := &msCrossing{}
		if err := p.loadBase("mscrossing", t); err != nil {
			return nil, err
		}
		p.isTwoFloored = tagBool(t, "tf")
		p.direction = dirSouth
		if _, ok := t["D"]; ok {
			p.direction = dirFrom2D(tagInt(t, "D"))
		}
		return p, nil
	}
	pieceLoaders["msstairs"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		p := &msStairs{}
		if err := p.loadBase("msstairs", t); err != nil {
			return nil, err
		}
		return p, nil
	}
}
