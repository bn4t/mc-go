package worldgen

import (
	"fmt"
	"sync"

	"mc-go/internal/world"
)

// Nether fortresses (NetherFortressStructure, NetherFortressPieces):
// a bridge crossing start ("nestart") two blocks into the chunk at
// y 64 facing a random direction grows children forward, left and
// right; pending children are expanded in random order, each child
// drawn by weight from the bridge or castle piece list (per-start
// place counts, limits, the no-repeat rule, five tries, an end filler
// when nothing fits or beyond 112 blocks of the start). The finished
// fortress moves to a random height inside 48..70.
//
// The piece geometry below is the measured box / block layout of each
// vanilla piece type (local coordinates, oriented by StructurePiece).

// fortressWeight is NetherFortressPieces.PieceWeight.
type fortressWeight struct {
	kind       int
	weight     int32
	maxCount   int32
	allowInRow bool
}

// Piece kinds.
const (
	nfBridgeStraight = iota
	nfBridgeCrossing
	nfRoomCrossing
	nfStairsRoom
	nfMonsterThrone
	nfCastleEntrance
	nfCastleSmallCorridor
	nfCastleSmallCorridorCrossing
	nfCastleRightTurn
	nfCastleLeftTurn
	nfCastleCorridorStairs
	nfCastleTBalcony
	nfCastleStalkRoom
	nfBridgeEndFiller
	nfStart
)

// nfKind is one piece type: its id, its box relative to the attach
// point (BoundingBox.orientBox offsets and size) and whether its
// constructor draws from the generation random.
type nfKind struct {
	id         string
	ox, oy, oz int32
	w, h, d    int32
}

var nfKinds = [...]nfKind{
	nfBridgeStraight:              {"nebs", -1, -3, 0, 5, 10, 19},
	nfBridgeCrossing:              {"nebcr", -8, -3, 0, 19, 10, 19},
	nfRoomCrossing:                {"nerc", -2, 0, 0, 7, 9, 7},
	nfStairsRoom:                  {"nesr", -2, 0, 0, 7, 11, 7},
	nfMonsterThrone:               {"nemt", -2, 0, 0, 7, 8, 9},
	nfCastleEntrance:              {"nece", -5, -3, 0, 13, 14, 13},
	nfCastleSmallCorridor:         {"nesc", -1, 0, 0, 5, 7, 5},
	nfCastleSmallCorridorCrossing: {"nescsc", -1, 0, 0, 5, 7, 5},
	nfCastleRightTurn:             {"nescrt", -1, 0, 0, 5, 7, 5},
	nfCastleLeftTurn:              {"nesclt", -1, 0, 0, 5, 7, 5},
	nfCastleCorridorStairs:        {"neccs", -1, -7, 0, 5, 14, 10},
	nfCastleTBalcony:              {"nectb", -3, 0, 0, 9, 7, 9},
	nfCastleStalkRoom:             {"necsr", -5, -3, 0, 13, 14, 13},
	nfBridgeEndFiller:             {"nebef", -1, -3, 0, 5, 10, 8},
	nfStart:                       {"nestart", 0, 0, 0, 19, 10, 19},
}

var nfKindByID = func() map[string]int {
	m := map[string]int{}
	for i, k := range nfKinds {
		m[k.id] = i
	}
	return m
}()

// The weight tables (NetherFortressPieces.BRIDGE_PIECE_WEIGHTS and
// CASTLE_PIECE_WEIGHTS).
var (
	nfBridgeWeights = []fortressWeight{
		{nfBridgeStraight, 30, 0, true}, {nfBridgeCrossing, 10, 4, false}, {nfRoomCrossing, 10, 4, false},
		{nfStairsRoom, 10, 3, false}, {nfMonsterThrone, 5, 2, false}, {nfCastleEntrance, 5, 1, false},
	}
	nfCastleWeights = []fortressWeight{
		{nfCastleSmallCorridor, 25, 0, true}, {nfCastleSmallCorridorCrossing, 15, 5, false},
		{nfCastleRightTurn, 5, 10, false}, {nfCastleLeftTurn, 5, 10, false}, {nfCastleCorridorStairs, 10, 3, true},
		{nfCastleTBalcony, 7, 2, false}, {nfCastleStalkRoom, 5, 2, false},
	}
)

// nfWeightState is a start's per-weight place count.
type nfWeightState struct {
	fortressWeight
	placed int32
}

func (w *nfWeightState) canPlace() bool { return w.maxCount == 0 || w.placed < w.maxCount }

// fortressGen is the start piece's generation state
// (NetherFortressPieces.StartPiece fields) plus the piece list.
type fortressGen struct {
	r       Random
	start   *fortressPiece
	prev    *nfWeightState
	bridge  []*nfWeightState
	castle  []*nfWeightState
	pending []*fortressPiece
	pieces  []*fortressPiece
}

func init() {
	structureTypes["fortress"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		return func(c *GenContext) (*GenerationStub, bool) {
			pos := BlockPos{c.ChunkX * 16, 64, c.ChunkZ * 16}
			return &GenerationStub{Pos: pos, Generate: func(add func(StructurePiece)) {
				for _, p := range fortressPieces(c.Random, c.ChunkX*16+2, c.ChunkZ*16+2) {
					add(p)
				}
			}}, true
		}
	}
	for _, k := range nfKinds {
		pieceLoaders[k.id] = loadFortressPiece
	}
}

func loadFortressPiece(g *Generator, t map[string]any) (StructurePiece, error) {
	b, err := loadVPBase(t)
	if err != nil {
		return nil, err
	}
	k, ok := nfKindByID[b.typ]
	if !ok {
		return nil, fmt.Errorf("worldgen: unknown fortress piece %s", b.typ)
	}
	p := &fortressPiece{vpBase: b, kind: k}
	switch k {
	case nfBridgeEndFiller:
		p.seed = tagInt(t, "Seed")
	case nfCastleLeftTurn, nfCastleRightTurn:
		p.chest = tagInt(t, "Chest") != 0
	case nfMonsterThrone:
		p.mob = tagInt(t, "Mob") != 0
	}
	return p, nil
}

// fortressPieces is NetherFortressStructure.generatePieces.
func fortressPieces(r Random, x, z int32) []*fortressPiece {
	gen := &fortressGen{r: r}
	dir := horizontalPlane[r.NextIntN(4)]
	// StartPiece is constructed as a bridge crossing: it saves as one.
	start := &fortressPiece{kind: nfBridgeCrossing}
	start.typ = "nebcr"
	start.bb = makeBoundingBox(x, 64, z, dir, 19, 10, 19)
	start.setOrientation(dir)
	gen.start = start
	for _, w := range nfBridgeWeights {
		gen.bridge = append(gen.bridge, &nfWeightState{fortressWeight: w})
	}
	for _, w := range nfCastleWeights {
		gen.castle = append(gen.castle, &nfWeightState{fortressWeight: w})
	}
	gen.pieces = append(gen.pieces, start)
	gen.addChildren(start)
	for len(gen.pending) > 0 {
		i := r.NextIntN(int32(len(gen.pending)))
		p := gen.pending[i]
		gen.pending = append(gen.pending[:i], gen.pending[i+1:]...)
		gen.addChildren(p)
	}
	// StructurePiecesBuilder.moveInsideHeights(random, 48, 70).
	bb := gen.pieces[0].bb
	for _, p := range gen.pieces[1:] {
		bb = bb.Encapsulate(p.bb)
	}
	span := 70 - 48 + 1 - bb.YSpan()
	y := int32(48)
	if span > 1 {
		y = 48 + r.NextIntN(span)
	}
	for _, p := range gen.pieces {
		p.Move(0, y-bb.MinY, 0)
	}
	return gen.pieces
}

// collides is StructurePieceAccessor.findCollisionPiece != null.
func (gen *fortressGen) collides(b BoundingBox) bool {
	for _, p := range gen.pieces {
		if p.bb.Intersects(b) {
			return true
		}
	}
	return false
}

// create is the piece type's createPiece: the oriented box, rejected
// below y 11 or on a collision, then the constructor (whose draws
// happen only for an accepted box).
func (gen *fortressGen) create(kind int, x, y, z int32, dir int, depth int32) *fortressPiece {
	k := nfKinds[kind]
	b := orientBox(x, y, z, k.ox, k.oy, k.oz, k.w, k.h, k.d, dir)
	if b.MinY <= 10 || gen.collides(b) {
		return nil
	}
	p := &fortressPiece{kind: kind}
	p.typ = k.id
	p.bb = b
	p.depth = depth
	p.setOrientation(dir)
	switch kind {
	case nfBridgeEndFiller:
		p.seed = gen.r.NextInt()
	case nfCastleLeftTurn, nfCastleRightTurn:
		p.chest = gen.r.NextIntN(3) == 0
	}
	return p
}

// generatePiece is NetherBridgePiece.generatePiece.
func (gen *fortressGen) generatePiece(list *[]*nfWeightState, x, y, z int32, dir int, depth int32) *fortressPiece {
	total := int32(0)
	valid := false
	for _, w := range *list {
		if w.maxCount > 0 && w.placed < w.maxCount {
			valid = true
		}
		total += w.weight
	}
	if !valid {
		total = -1
	}
	if total > 0 && depth <= 30 {
		for try := 0; try < 5; try++ {
			n := gen.r.NextIntN(total)
		pick:
			for _, w := range *list {
				n -= w.weight
				if n >= 0 {
					continue
				}
				if !w.canPlace() || (w == gen.prev && !w.allowInRow) {
					break pick
				}
				p := gen.create(w.kind, x, y, z, dir, depth)
				if p == nil {
					continue
				}
				w.placed++
				gen.prev = w
				if !w.canPlace() {
					for i, o := range *list {
						if o == w {
							*list = append((*list)[:i], (*list)[i+1:]...)
							break
						}
					}
				}
				return p
			}
		}
	}
	return gen.create(nfBridgeEndFiller, x, y, z, dir, depth)
}

// generateAndAddPiece is NetherBridgePiece.generateAndAddPiece.
func (gen *fortressGen) generateAndAddPiece(x, y, z int32, dir int, depth int32, castle bool) {
	if absI32(x-gen.start.bb.MinX) > 112 || absI32(z-gen.start.bb.MinZ) > 112 {
		// The filler is made (drawing its seed) but never added.
		gen.create(nfBridgeEndFiller, x, y, z, dir, depth)
		return
	}
	list := &gen.bridge
	if castle {
		list = &gen.castle
	}
	if p := gen.generatePiece(list, x, y, z, dir, depth+1); p != nil {
		gen.pieces = append(gen.pieces, p)
		gen.pending = append(gen.pending, p)
	}
}

// childForward / childLeft / childRight are generateChildForward,
// generateChildLeft and generateChildRight.
func (gen *fortressGen) childForward(p *fortressPiece, h, v int32, castle bool) {
	b := p.bb
	switch p.ori {
	case dirNorth:
		gen.generateAndAddPiece(b.MinX+h, b.MinY+v, b.MinZ-1, p.ori, p.depth, castle)
	case dirSouth:
		gen.generateAndAddPiece(b.MinX+h, b.MinY+v, b.MaxZ+1, p.ori, p.depth, castle)
	case dirWest:
		gen.generateAndAddPiece(b.MinX-1, b.MinY+v, b.MinZ+h, p.ori, p.depth, castle)
	case dirEast:
		gen.generateAndAddPiece(b.MaxX+1, b.MinY+v, b.MinZ+h, p.ori, p.depth, castle)
	}
}

func (gen *fortressGen) childLeft(p *fortressPiece, v, h int32, castle bool) {
	b := p.bb
	switch p.ori {
	case dirNorth, dirSouth:
		gen.generateAndAddPiece(b.MinX-1, b.MinY+v, b.MinZ+h, dirWest, p.depth, castle)
	case dirWest, dirEast:
		gen.generateAndAddPiece(b.MinX+h, b.MinY+v, b.MinZ-1, dirNorth, p.depth, castle)
	}
}

func (gen *fortressGen) childRight(p *fortressPiece, v, h int32, castle bool) {
	b := p.bb
	switch p.ori {
	case dirNorth, dirSouth:
		gen.generateAndAddPiece(b.MaxX+1, b.MinY+v, b.MinZ+h, dirEast, p.depth, castle)
	case dirWest, dirEast:
		gen.generateAndAddPiece(b.MinX+h, b.MinY+v, b.MaxZ+1, dirSouth, p.depth, castle)
	}
}

// addChildren is each piece type's addChildren.
func (gen *fortressGen) addChildren(p *fortressPiece) {
	switch p.kind {
	case nfStart, nfBridgeCrossing:
		gen.childForward(p, 8, 3, false)
		gen.childLeft(p, 3, 8, false)
		gen.childRight(p, 3, 8, false)
	case nfBridgeStraight:
		gen.childForward(p, 1, 3, false)
	case nfRoomCrossing:
		gen.childForward(p, 2, 0, false)
		gen.childLeft(p, 0, 2, false)
		gen.childRight(p, 0, 2, false)
	case nfStairsRoom:
		gen.childRight(p, 6, 2, false)
	case nfCastleEntrance:
		gen.childForward(p, 5, 3, true)
	case nfCastleSmallCorridor, nfCastleCorridorStairs:
		gen.childForward(p, 1, 0, true)
	case nfCastleSmallCorridorCrossing:
		gen.childForward(p, 1, 0, true)
		gen.childLeft(p, 0, 1, true)
		gen.childRight(p, 0, 1, true)
	case nfCastleRightTurn:
		gen.childRight(p, 0, 1, true)
	case nfCastleLeftTurn:
		gen.childLeft(p, 0, 1, true)
	case nfCastleTBalcony:
		h := int32(1)
		if p.ori == dirWest || p.ori == dirNorth {
			h = 5
		}
		gen.childLeft(p, 0, h, gen.r.NextIntN(8) > 0)
		gen.childRight(p, 0, h, gen.r.NextIntN(8) > 0)
	case nfCastleStalkRoom:
		gen.childForward(p, 5, 3, true)
		gen.childForward(p, 5, 11, true)
	}
}

// fortressPiece is a NetherBridgePiece of any kind.
type fortressPiece struct {
	vpBase
	kind  int
	seed  int32 // BridgeEndFiller.selfSeed
	chest bool  // isNeedingChest of the corridor turns
	mob   bool  // MonsterThrone.hasPlacedSpawner
}

// SaveData is the kinds' addAdditionalSaveData.
func (p *fortressPiece) SaveData(t map[string]any) {
	switch p.kind {
	case nfBridgeEndFiller:
		t["Seed"] = p.seed
	case nfCastleLeftTurn, nfCastleRightTurn:
		t["Chest"] = boolByte(p.chest)
	case nfMonsterThrone:
		t["Mob"] = boolByte(p.mob)
	}
}

var netherBricks = sync.OnceValue(func() world.Block { return blockNamed("nether_bricks") })

// fence is a nether brick fence with the given sides connected.
func fence(sides string) world.Block {
	props := map[string]string{}
	for _, c := range sides {
		switch c {
		case 'n':
			props["north"] = "true"
		case 'e':
			props["east"] = "true"
		case 's':
			props["south"] = "true"
		case 'w':
			props["west"] = "true"
		}
	}
	b, _ := world.BlockStateOf("nether_brick_fence", props)
	return b
}

func netherStairs(facing string) world.Block {
	b, _ := world.BlockStateOf("nether_brick_stairs", map[string]string{"facing": facing})
	return b
}

// nfCtx carries one postProcess call's writers.
type nfCtx struct {
	p *fortressPiece
	c *PieceContext
}

func (x nfCtx) box(x0, y0, z0, x1, y1, z1 int32, st world.Block) {
	x.p.generateBox(x.c, x.c.ChunkBox, x0, y0, z0, x1, y1, z1, st, st, false)
}

func (x nfCtx) air(x0, y0, z0, x1, y1, z1 int32) { x.box(x0, y0, z0, x1, y1, z1, world.Air) }

func (x nfCtx) bricks(x0, y0, z0, x1, y1, z1 int32) {
	x.box(x0, y0, z0, x1, y1, z1, netherBricks())
}

func (x nfCtx) set(st world.Block, px, py, pz int32) {
	x.p.placeBlock(x.c, st, px, py, pz, x.c.ChunkBox)
}

func (x nfCtx) pillar(px, pz int32) {
	x.p.fillColumnDown(x.c, netherBricks(), px, -1, pz, x.c.ChunkBox)
}

// pillars fills the columns of the rectangle down from y -1.
func (x nfCtx) pillars(x0, x1, z0, z1 int32) {
	for i := x0; i <= x1; i++ {
		for j := z0; j <= z1; j++ {
			x.pillar(i, j)
		}
	}
}

// PostProcess draws the piece's measured layout.
func (p *fortressPiece) PostProcess(c *PieceContext) {
	x := nfCtx{p, c}
	ns, we := fence("ns"), fence("we")
	switch p.kind {
	case nfStart, nfBridgeCrossing:
		x.bricks(7, 3, 0, 11, 4, 18)
		x.bricks(0, 3, 7, 18, 4, 11)
		x.air(8, 5, 0, 10, 7, 18)
		x.air(0, 5, 8, 18, 7, 10)
		x.bricks(7, 5, 0, 7, 5, 7)
		x.bricks(7, 5, 11, 7, 5, 18)
		x.bricks(11, 5, 0, 11, 5, 7)
		x.bricks(11, 5, 11, 11, 5, 18)
		x.bricks(0, 5, 7, 7, 5, 7)
		x.bricks(11, 5, 7, 18, 5, 7)
		x.bricks(0, 5, 11, 7, 5, 11)
		x.bricks(11, 5, 11, 18, 5, 11)
		x.bricks(7, 2, 0, 11, 2, 5)
		x.bricks(7, 2, 13, 11, 2, 18)
		x.bricks(7, 0, 0, 11, 1, 3)
		x.bricks(7, 0, 15, 11, 1, 18)
		for i := int32(7); i <= 11; i++ {
			for j := int32(0); j <= 2; j++ {
				x.pillar(i, j)
				x.pillar(i, 18-j)
			}
		}
		x.bricks(0, 2, 7, 5, 2, 11)
		x.bricks(13, 2, 7, 18, 2, 11)
		x.bricks(0, 0, 7, 3, 1, 11)
		x.bricks(15, 0, 7, 18, 1, 11)
		for i := int32(0); i <= 2; i++ {
			for j := int32(7); j <= 11; j++ {
				x.pillar(i, j)
				x.pillar(18-i, j)
			}
		}
	case nfBridgeStraight:
		x.bricks(0, 3, 0, 4, 4, 18)
		x.air(1, 5, 0, 3, 7, 18)
		x.bricks(0, 5, 0, 0, 5, 18)
		x.bricks(4, 5, 0, 4, 5, 18)
		x.bricks(0, 2, 0, 4, 2, 5)
		x.bricks(0, 2, 13, 4, 2, 18)
		x.bricks(0, 0, 0, 4, 1, 3)
		x.bricks(0, 0, 15, 4, 1, 18)
		for i := int32(0); i <= 4; i++ {
			for j := int32(0); j <= 2; j++ {
				x.pillar(i, j)
				x.pillar(i, 18-j)
			}
		}
		nse, nsw := fence("nse"), fence("nsw")
		x.box(0, 1, 1, 0, 4, 1, nse)
		x.box(0, 3, 4, 0, 4, 4, nse)
		x.box(0, 3, 14, 0, 4, 14, nse)
		x.box(0, 1, 17, 0, 4, 17, nse)
		x.box(4, 1, 1, 4, 4, 1, nsw)
		x.box(4, 3, 4, 4, 4, 4, nsw)
		x.box(4, 3, 14, 4, 4, 14, nsw)
		x.box(4, 1, 17, 4, 4, 17, nsw)
	case nfBridgeEndFiller:
		// A random of its own, from the saved seed.
		r := NewLegacy(int64(p.seed))
		for i := int32(0); i <= 4; i++ {
			for j := int32(3); j <= 4; j++ {
				n := r.NextIntN(8)
				x.bricks(i, j, 0, i, j, n)
			}
		}
		n := r.NextIntN(8)
		x.bricks(0, 5, 0, 0, 5, n)
		n = r.NextIntN(8)
		x.bricks(4, 5, 0, 4, 5, n)
		for i := int32(0); i <= 4; i++ {
			n := r.NextIntN(5)
			x.bricks(i, 2, 0, i, 2, n)
		}
		for i := int32(0); i <= 4; i++ {
			for j := int32(0); j <= 1; j++ {
				n := r.NextIntN(3)
				x.bricks(i, j, 0, i, j, n)
			}
		}
	case nfRoomCrossing:
		x.bricks(0, 0, 0, 6, 1, 6)
		x.air(0, 2, 0, 6, 7, 6)
		x.bricks(0, 2, 0, 1, 6, 0)
		x.bricks(0, 2, 6, 1, 6, 6)
		x.bricks(5, 2, 0, 6, 6, 0)
		x.bricks(5, 2, 6, 6, 6, 6)
		x.bricks(0, 2, 0, 0, 6, 1)
		x.bricks(0, 2, 5, 0, 6, 6)
		x.bricks(6, 2, 0, 6, 6, 1)
		x.bricks(6, 2, 5, 6, 6, 6)
		x.bricks(2, 6, 0, 4, 6, 0)
		x.box(2, 5, 0, 4, 5, 0, we)
		x.bricks(2, 6, 6, 4, 6, 6)
		x.box(2, 5, 6, 4, 5, 6, we)
		x.bricks(0, 6, 2, 0, 6, 4)
		x.box(0, 5, 2, 0, 5, 4, ns)
		x.bricks(6, 6, 2, 6, 6, 4)
		x.box(6, 5, 2, 6, 5, 4, ns)
		x.pillars(0, 6, 0, 6)
	case nfStairsRoom:
		x.bricks(0, 0, 0, 6, 1, 6)
		x.air(0, 2, 0, 6, 10, 6)
		x.bricks(0, 2, 0, 1, 8, 0)
		x.bricks(5, 2, 0, 6, 8, 0)
		x.bricks(0, 2, 1, 0, 8, 6)
		x.bricks(6, 2, 1, 6, 8, 6)
		x.bricks(1, 2, 6, 5, 8, 6)
		x.box(0, 3, 2, 0, 5, 4, ns)
		x.box(6, 3, 2, 6, 5, 2, ns)
		x.box(6, 3, 4, 6, 5, 4, ns)
		x.set(netherBricks(), 5, 2, 5)
		x.bricks(4, 2, 5, 4, 3, 5)
		x.bricks(3, 2, 5, 3, 4, 5)
		x.bricks(2, 2, 5, 2, 5, 5)
		x.bricks(1, 2, 5, 1, 6, 5)
		x.bricks(1, 7, 1, 5, 7, 4)
		x.air(6, 8, 2, 6, 8, 4)
		x.bricks(2, 6, 0, 4, 8, 0)
		x.box(2, 5, 0, 4, 5, 0, we)
		x.pillars(0, 6, 0, 6)
	case nfMonsterThrone:
		x.air(0, 2, 0, 6, 7, 7)
		x.bricks(1, 0, 0, 5, 1, 7)
		x.bricks(1, 2, 1, 5, 2, 7)
		x.bricks(1, 3, 2, 5, 3, 7)
		x.bricks(1, 4, 3, 5, 4, 7)
		x.bricks(1, 2, 0, 1, 4, 2)
		x.bricks(5, 2, 0, 5, 4, 2)
		x.bricks(1, 5, 2, 1, 5, 3)
		x.bricks(5, 5, 2, 5, 5, 3)
		x.bricks(0, 5, 3, 0, 5, 8)
		x.bricks(6, 5, 3, 6, 5, 8)
		x.bricks(1, 5, 8, 5, 5, 8)
		x.set(fence("w"), 1, 6, 3)
		x.set(fence("e"), 5, 6, 3)
		x.set(fence("en"), 0, 6, 3)
		x.set(fence("wn"), 6, 6, 3)
		x.box(0, 6, 4, 0, 6, 7, ns)
		x.box(6, 6, 4, 6, 6, 7, ns)
		x.set(fence("es"), 0, 6, 8)
		x.set(fence("ws"), 6, 6, 8)
		x.box(1, 6, 8, 5, 6, 8, we)
		x.set(fence("e"), 1, 7, 8)
		x.box(2, 7, 8, 4, 7, 8, we)
		x.set(fence("w"), 5, 7, 8)
		x.set(fence("e"), 2, 8, 8)
		x.set(we, 3, 8, 8)
		x.set(fence("w"), 4, 8, 8)
		if !p.mob {
			wp := p.worldPos(3, 5, 5)
			if c.ChunkBox.IsInside(wp) {
				p.mob = true
				c.Region.set(wp, spawnerBlock())
				structSpawnerBlockEntity(c.Region, wp, "minecraft:blaze")
			}
		}
		x.pillars(0, 6, 0, 6)
	case nfCastleEntrance:
		p.castleHall(x)
		x.bricks(5, 5, 5, 7, 5, 7)
		x.air(6, 1, 6, 6, 4, 6)
		x.set(netherBricks(), 6, 0, 6)
		x.set(blockNamed("lava"), 6, 5, 6)
	case nfCastleStalkRoom:
		p.castleWalls(x)
		nsw, nse := fence("nsw"), fence("nse")
		for i := int32(3); i <= 9; i += 2 {
			x.box(1, 7, i, 1, 8, i, nsw)
			x.box(11, 7, i, 11, 8, i, nse)
		}
		north := netherStairs("north")
		for i := int32(0); i <= 6; i++ {
			z := i + 4
			for j := int32(5); j <= 7; j++ {
				x.set(north, j, 5+i, z)
			}
			if z >= 5 && z <= 8 {
				x.bricks(5, 5, z, 7, i+4, z)
			} else if z >= 9 && z <= 10 {
				x.bricks(5, 8, z, 7, i+4, z)
			}
			if i >= 1 {
				x.air(5, 6+i, z, 7, 9+i, z)
			}
		}
		for j := int32(5); j <= 7; j++ {
			x.set(north, j, 12, 11)
		}
		x.box(5, 6, 7, 5, 7, 7, nse)
		x.box(7, 6, 7, 7, 7, 7, nsw)
		x.air(5, 13, 12, 7, 13, 12)
		x.bricks(2, 5, 2, 3, 5, 3)
		x.bricks(2, 5, 9, 3, 5, 10)
		x.bricks(2, 5, 4, 2, 5, 8)
		x.bricks(9, 5, 2, 10, 5, 3)
		x.bricks(9, 5, 9, 10, 5, 10)
		x.bricks(10, 5, 4, 10, 5, 8)
		east, west := netherStairs("east"), netherStairs("west")
		x.set(west, 4, 5, 2)
		x.set(west, 4, 5, 3)
		x.set(west, 4, 5, 9)
		x.set(west, 4, 5, 10)
		x.set(east, 8, 5, 2)
		x.set(east, 8, 5, 3)
		x.set(east, 8, 5, 9)
		x.set(east, 8, 5, 10)
		ss, wart := blockNamed("soul_sand"), blockNamed("nether_wart")
		x.box(3, 4, 4, 4, 4, 8, ss)
		x.box(8, 4, 4, 9, 4, 8, ss)
		x.box(3, 5, 4, 4, 5, 8, wart)
		x.box(8, 5, 4, 9, 5, 8, wart)
		p.castleFoundation(x)
	case nfCastleSmallCorridor:
		x.bricks(0, 0, 0, 4, 1, 4)
		x.air(0, 2, 0, 4, 5, 4)
		x.bricks(0, 2, 0, 0, 5, 4)
		x.bricks(4, 2, 0, 4, 5, 4)
		x.box(0, 3, 1, 0, 4, 1, ns)
		x.box(0, 3, 3, 0, 4, 3, ns)
		x.box(4, 3, 1, 4, 4, 1, ns)
		x.box(4, 3, 3, 4, 4, 3, ns)
		x.bricks(0, 6, 0, 4, 6, 4)
		x.pillars(0, 4, 0, 4)
	case nfCastleSmallCorridorCrossing:
		x.bricks(0, 0, 0, 4, 1, 4)
		x.air(0, 2, 0, 4, 5, 4)
		x.bricks(0, 2, 0, 0, 5, 0)
		x.bricks(4, 2, 0, 4, 5, 0)
		x.bricks(0, 2, 4, 0, 5, 4)
		x.bricks(4, 2, 4, 4, 5, 4)
		x.bricks(0, 6, 0, 4, 6, 4)
		x.pillars(0, 4, 0, 4)
	case nfCastleRightTurn:
		x.bricks(0, 0, 0, 4, 1, 4)
		x.air(0, 2, 0, 4, 5, 4)
		x.bricks(0, 2, 0, 0, 5, 4)
		x.box(0, 3, 1, 0, 4, 1, ns)
		x.box(0, 3, 3, 0, 4, 3, ns)
		x.bricks(4, 2, 0, 4, 5, 0)
		x.bricks(1, 2, 4, 4, 5, 4)
		x.box(1, 3, 4, 1, 4, 4, we)
		x.box(3, 3, 4, 3, 4, 4, we)
		p.turnChest(x, 1, 2, 3)
		x.bricks(0, 6, 0, 4, 6, 4)
		x.pillars(0, 4, 0, 4)
	case nfCastleLeftTurn:
		x.bricks(0, 0, 0, 4, 1, 4)
		x.air(0, 2, 0, 4, 5, 4)
		x.bricks(4, 2, 0, 4, 5, 4)
		x.box(4, 3, 1, 4, 4, 1, ns)
		x.box(4, 3, 3, 4, 4, 3, ns)
		x.bricks(0, 2, 0, 0, 5, 0)
		x.bricks(0, 2, 4, 3, 5, 4)
		x.box(1, 3, 4, 1, 4, 4, we)
		x.box(3, 3, 4, 3, 4, 4, we)
		p.turnChest(x, 3, 2, 3)
		x.bricks(0, 6, 0, 4, 6, 4)
		x.pillars(0, 4, 0, 4)
	case nfCastleCorridorStairs:
		south := netherStairs("south")
		for i := int32(0); i <= 9; i++ {
			lo := max(1, 7-i)
			hi := min(max(lo+5, 14-i), 13)
			z := i
			x.bricks(0, 0, z, 4, lo, z)
			x.air(1, lo+1, z, 3, hi-1, z)
			if i <= 6 {
				x.set(south, 1, lo+1, z)
				x.set(south, 2, lo+1, z)
				x.set(south, 3, lo+1, z)
			}
			x.bricks(0, hi, z, 4, hi, z)
			x.bricks(0, lo+1, z, 0, hi-1, z)
			x.bricks(4, lo+1, z, 4, hi-1, z)
			if i&1 == 0 {
				x.box(0, lo+2, z, 0, lo+3, z, ns)
				x.box(4, lo+2, z, 4, lo+3, z, ns)
			}
			for j := int32(0); j <= 4; j++ {
				x.pillar(j, z)
			}
		}
	case nfCastleTBalcony:
		x.bricks(0, 0, 0, 8, 1, 8)
		x.air(0, 2, 0, 8, 5, 8)
		x.bricks(0, 6, 0, 8, 6, 5)
		x.bricks(0, 2, 0, 2, 5, 0)
		x.bricks(6, 2, 0, 8, 5, 0)
		x.box(1, 3, 0, 1, 4, 0, we)
		x.box(7, 3, 0, 7, 4, 0, we)
		x.bricks(0, 2, 4, 8, 2, 8)
		x.air(1, 1, 4, 2, 2, 4)
		x.air(6, 1, 4, 7, 2, 4)
		x.box(1, 3, 8, 7, 3, 8, we)
		x.set(fence("es"), 0, 3, 8)
		x.set(fence("ws"), 8, 3, 8)
		x.box(0, 3, 6, 0, 3, 7, ns)
		x.box(8, 3, 6, 8, 3, 7, ns)
		x.bricks(0, 3, 4, 0, 5, 5)
		x.bricks(8, 3, 4, 8, 5, 5)
		x.bricks(1, 3, 5, 2, 5, 5)
		x.bricks(6, 3, 5, 7, 5, 5)
		x.box(1, 4, 5, 1, 5, 5, we)
		x.box(7, 4, 5, 7, 5, 5, we)
		for j := int32(0); j <= 5; j++ {
			for i := int32(0); i <= 8; i++ {
				x.pillar(i, j)
			}
		}
	}
}

// castleWalls is the shared hall shell of the castle entrance and the
// stalk room: floor, air, walls with window gaps, the roof, the fence
// railings along the top with brick posts every other block and the
// corner fences.
func (p *fortressPiece) castleWalls(x nfCtx) {
	x.bricks(0, 3, 0, 12, 4, 12)
	x.air(0, 5, 0, 12, 13, 12)
	x.bricks(0, 5, 0, 1, 12, 12)
	x.bricks(11, 5, 0, 12, 12, 12)
	x.bricks(2, 5, 11, 4, 12, 12)
	x.bricks(8, 5, 11, 10, 12, 12)
	x.bricks(5, 9, 11, 7, 12, 12)
	x.bricks(2, 5, 0, 4, 12, 1)
	x.bricks(8, 5, 0, 10, 12, 1)
	x.bricks(5, 9, 0, 7, 12, 1)
	x.bricks(2, 11, 2, 10, 12, 10)
	if p.kind == nfCastleEntrance {
		x.box(5, 8, 0, 7, 8, 0, blockNamed("nether_brick_fence"))
	}
	we, ns := fence("we"), fence("ns")
	for i := int32(1); i <= 11; i += 2 {
		x.box(i, 10, 0, i, 11, 0, we)
		x.box(i, 10, 12, i, 11, 12, we)
		x.box(0, 10, i, 0, 11, i, ns)
		x.box(12, 10, i, 12, 11, i, ns)
		x.set(netherBricks(), i, 13, 0)
		x.set(netherBricks(), i, 13, 12)
		x.set(netherBricks(), 0, 13, i)
		x.set(netherBricks(), 12, 13, i)
		if i != 11 {
			x.set(we, i+1, 13, 0)
			x.set(we, i+1, 13, 12)
			x.set(ns, 0, 13, i+1)
			x.set(ns, 12, 13, i+1)
		}
	}
	x.set(fence("ne"), 0, 13, 0)
	x.set(fence("se"), 0, 13, 12)
	x.set(fence("sw"), 12, 13, 12)
	x.set(fence("nw"), 12, 13, 0)
}

// castleHall is the castle entrance's shell, side windows and
// foundation.
func (p *fortressPiece) castleHall(x nfCtx) {
	p.castleWalls(x)
	nsw, nse := fence("nsw"), fence("nse")
	for i := int32(3); i <= 9; i += 2 {
		x.box(1, 7, i, 1, 8, i, nsw)
		x.box(11, 7, i, 11, 8, i, nse)
	}
	p.castleFoundation(x)
}

// castleFoundation is the cross-shaped base and its pillars.
func (p *fortressPiece) castleFoundation(x nfCtx) {
	x.bricks(4, 2, 0, 8, 2, 12)
	x.bricks(0, 2, 4, 12, 2, 8)
	x.bricks(4, 0, 0, 8, 1, 3)
	x.bricks(4, 0, 9, 8, 1, 12)
	x.bricks(0, 0, 4, 3, 1, 8)
	x.bricks(9, 0, 4, 12, 1, 8)
	for i := int32(4); i <= 8; i++ {
		for j := int32(0); j <= 2; j++ {
			x.pillar(i, j)
			x.pillar(i, 12-j)
		}
	}
	for i := int32(0); i <= 2; i++ {
		for j := int32(4); j <= 8; j++ {
			x.pillar(i, j)
			x.pillar(12-i, j)
		}
	}
}

// turnChest places a corridor turn's chest once (the flag clears when
// the chest's chunk places it).
func (p *fortressPiece) turnChest(x nfCtx, cx, cy, cz int32) {
	if p.chest && x.c.ChunkBox.IsInside(p.worldPos(cx, cy, cz)) {
		p.chest = false
		p.createChest(x.c, x.c.ChunkBox, x.c.Random, cx, cy, cz, "minecraft:chests/nether_bridge")
	}
}
