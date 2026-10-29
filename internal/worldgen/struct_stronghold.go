package worldgen

import (
	"fmt"
	"sync"

	"mc-go/internal/world"
)

// Strongholds (StrongholdStructure + StrongholdPieces), measured from
// the 26.2 bytecode:
//
//   - generatePieces retries with the large-feature seed of world seed
//     + attempt until the pieces include a portal room: a start
//     (StairsDown flagged as source, at the chunk's block (2, 64, 2)
//     facing a random direction) whose first child is imposed to be a
//     five-way crossing, then pending children expanded in random order
//     (nextInt over the pending list), then the whole set moved below
//     sea level (offset 10). The biome check samples the chunk's world
//     origin.
//   - child selection (generatePieceFromSmallDoor): up to five weighted
//     draws over the piece weights still available (straight 40, prison
//     hall 5/5, left and right turns 20, room crossing 10/6, straight
//     stairs down 5/5, stairs down 5/5, five crossing 5/4, chest
//     corridor 5/4, library 10/2 from depth 5, portal room 20/1 from
//     depth 6), skipping the previous piece type; a draw whose box does
//     not fit falls through to the next types in the list; after five
//     draws a filler corridor reaching the colliding piece. Depth is
//     capped at 50 and the distance from the start at 112 blocks; every
//     box must start above y 10.
//   - walls use SmoothStoneSelector on edges (nextFloat: < 0.2 cracked,
//     < 0.5 mossy, < 0.55 infested, else stone bricks) and cave air
//     inside, mostly skipping cells that are already air (no draw).

const (
	shStraight = iota
	shPrisonHall
	shLeftTurn
	shRightTurn
	shRoomCrossing
	shStraightStairsDown
	shStairsDown
	shFiveCrossing
	shChestCorridor
	shLibrary
	shPortalRoom
	shFillerCorridor
	shStart
)

var shTypeIDs = [...]string{"shs", "shph", "shlt", "shrt", "shrc", "shssd", "shsd", "sh5c", "shcc", "shli", "shpr", "shfc", "shstart"}

// Small door types (StrongholdPiece.SmallDoorType).
const (
	doorOpening = iota
	doorWood
	doorGrates
	doorIron
)

var doorNames = [4]string{"OPENING", "WOOD_DOOR", "GRATES", "IRON_DOOR"}

type shWeight struct {
	kind, weight, max int32
	placed            int32
}

func (w *shWeight) doPlace(depth int32) bool {
	ok := w.max == 0 || w.placed < w.max
	switch w.kind {
	case shLibrary:
		return ok && depth > 4
	case shPortalRoom:
		return ok && depth > 5
	}
	return ok
}

func (w *shWeight) isValid() bool { return w.max == 0 || w.placed < w.max }

// shGen is the generation state StrongholdPieces keeps in statics.
type shGen struct {
	pieces   []StructurePiece
	weights  []*shWeight
	imposed  int // -1: none
	total    int32
	start    *shPiece
	previous *shWeight
	pending  []*shPiece
	portal   *shPiece
}

func newSHGen() *shGen {
	g := &shGen{imposed: -1}
	for _, w := range [][3]int32{{shStraight, 40, 0}, {shPrisonHall, 5, 5}, {shLeftTurn, 20, 0}, {shRightTurn, 20, 0},
		{shRoomCrossing, 10, 6}, {shStraightStairsDown, 5, 5}, {shStairsDown, 5, 5}, {shFiveCrossing, 5, 4},
		{shChestCorridor, 5, 4}, {shLibrary, 10, 2}, {shPortalRoom, 20, 1}} {
		g.weights = append(g.weights, &shWeight{kind: w[0], weight: w[1], max: w[2]})
	}
	return g
}

func (g *shGen) updatePieceWeight() bool {
	any := false
	g.total = 0
	for _, w := range g.weights {
		if w.max > 0 && w.placed < w.max {
			any = true
		}
		g.total += w.weight
	}
	return any
}

type shPiece struct {
	legacyPiece
	kind  int
	door  int
	flags [4]bool // Source / Left, Right / Chest / leftLow.. / Tall / Mob
	num   int32   // Steps (filler) / Type (room crossing)
	mu    sync.Mutex
}

func (p *shPiece) init(kind int, depth int32, b BoundingBox, dir int) {
	p.legacyPiece = newLegacyPiece(shTypeIDs[kind], depth, b)
	p.kind = kind
	p.setOrientation(dir)
}

// randomSmallDoor is StrongholdPiece.randomSmallDoor.
func randomSmallDoor(r Random) int {
	switch r.NextIntN(5) {
	case 2:
		return doorWood
	case 3:
		return doorGrates
	case 4:
		return doorIron
	}
	return doorOpening
}

func isOkBox(b BoundingBox) bool { return b.MinY > 10 }

// shCreate is findAndCreatePieceFactory: kind's createPiece.
func (g *shGen) create(kind int, r Random, x, y, z int32, d int, depth int32) *shPiece {
	type dims struct{ ox, oy, oz, w, h, dd int32 }
	fit := func(dm dims) (BoundingBox, bool) {
		b := orientBox(x, y, z, dm.ox, dm.oy, dm.oz, dm.w, dm.h, dm.dd, d)
		return b, isOkBox(b) && findCollisionPiece(g.pieces, b) == nil
	}
	var sizes = map[int]dims{
		shStraight: {-1, -1, 0, 5, 5, 7}, shPrisonHall: {-1, -1, 0, 9, 5, 11}, shLeftTurn: {-1, -1, 0, 5, 5, 5},
		shRightTurn: {-1, -1, 0, 5, 5, 5}, shRoomCrossing: {-4, -1, 0, 11, 7, 11}, shStraightStairsDown: {-1, -7, 0, 5, 11, 8},
		shStairsDown: {-1, -7, 0, 5, 11, 5}, shFiveCrossing: {-4, -3, 0, 10, 9, 11}, shChestCorridor: {-1, -1, 0, 5, 5, 7},
		shPortalRoom: {-4, -1, 0, 11, 8, 16},
	}
	var b BoundingBox
	if kind == shLibrary {
		var ok bool
		if b, ok = fit(dims{-4, -1, 0, 14, 11, 15}); !ok {
			if b, ok = fit(dims{-4, -1, 0, 14, 6, 15}); !ok {
				return nil
			}
		}
	} else {
		var ok bool
		if b, ok = fit(sizes[kind]); !ok {
			return nil
		}
	}
	p := &shPiece{}
	p.init(kind, depth, b, d)
	if kind == shPortalRoom {
		return p
	}
	p.door = randomSmallDoor(r)
	switch kind {
	case shStraight:
		p.flags[0] = r.NextIntN(2) == 0
		p.flags[1] = r.NextIntN(2) == 0
	case shFiveCrossing:
		p.flags[0] = r.NextBool()
		p.flags[1] = r.NextBool()
		p.flags[2] = r.NextBool()
		p.flags[3] = r.NextIntN(3) > 0
	case shRoomCrossing:
		p.num = r.NextIntN(5)
	case shLibrary:
		p.flags[0] = b.YSpan() > 6
	}
	return p
}

// findFillerBox is FillerCorridor.findPieceBox.
func (g *shGen) findFillerBox(x, y, z int32, d int) (BoundingBox, bool) {
	b := orientBox(x, y, z, -1, -1, 0, 5, 5, 4, d)
	col := findCollisionPiece(g.pieces, b)
	if col == nil {
		return BoundingBox{}, false
	}
	if col.BoundingBox().MinY == b.MinY {
		for n := int32(2); n >= 1; n-- {
			b = orientBox(x, y, z, -1, -1, 0, 5, 5, n, d)
			if !col.BoundingBox().Intersects(b) {
				return orientBox(x, y, z, -1, -1, 0, 5, 5, n+1, d), true
			}
		}
	}
	return BoundingBox{}, false
}

// fromSmallDoor is StrongholdPieces.generatePieceFromSmallDoor.
func (g *shGen) fromSmallDoor(r Random, x, y, z int32, d int, depth int32) *shPiece {
	if !g.updatePieceWeight() {
		return nil
	}
	if g.imposed >= 0 {
		p := g.create(g.imposed, r, x, y, z, d, depth)
		g.imposed = -1
		if p != nil {
			return p
		}
	}
	for try := 0; try < 5; try++ {
		n := r.NextIntN(g.total)
		for i := 0; i < len(g.weights); i++ {
			w := g.weights[i]
			n -= w.weight
			if n >= 0 {
				continue
			}
			if !w.doPlace(depth) || w == g.previous {
				break
			}
			p := g.create(int(w.kind), r, x, y, z, d, depth)
			if p == nil {
				continue
			}
			w.placed++
			g.previous = w
			if !w.isValid() {
				g.weights = append(g.weights[:i:i], g.weights[i+1:]...)
			}
			return p
		}
	}
	if b, ok := g.findFillerBox(x, y, z, d); ok && b.MinY > 1 {
		p := &shPiece{}
		p.init(shFillerCorridor, depth, b, d)
		if d == dirNorth || d == dirSouth {
			p.num = b.MaxZ - b.MinZ + 1
		} else {
			p.num = b.MaxX - b.MinX + 1
		}
		return p
	}
	return nil
}

// generateAndAdd is StrongholdPieces.generateAndAddPiece.
func (g *shGen) generateAndAdd(r Random, x, y, z int32, d int, depth int32) *shPiece {
	if depth > 50 {
		return nil
	}
	sb := g.start.Box
	if iabs32(x-sb.MinX) > 112 || iabs32(z-sb.MinZ) > 112 {
		return nil
	}
	p := g.fromSmallDoor(r, x, y, z, d, depth+1)
	if p != nil {
		g.pieces = append(g.pieces, p)
		g.pending = append(g.pending, p)
	}
	return p
}

func (p *shPiece) childForward(g *shGen, r Random, xo, yo int32) {
	b := p.Box
	switch p.orient {
	case dirNorth:
		g.generateAndAdd(r, b.MinX+xo, b.MinY+yo, b.MinZ-1, p.orient, p.Depth)
	case dirSouth:
		g.generateAndAdd(r, b.MinX+xo, b.MinY+yo, b.MaxZ+1, p.orient, p.Depth)
	case dirWest:
		g.generateAndAdd(r, b.MinX-1, b.MinY+yo, b.MinZ+xo, p.orient, p.Depth)
	case dirEast:
		g.generateAndAdd(r, b.MaxX+1, b.MinY+yo, b.MinZ+xo, p.orient, p.Depth)
	}
}

func (p *shPiece) childLeft(g *shGen, r Random, yo, ho int32) {
	b := p.Box
	switch p.orient {
	case dirNorth, dirSouth:
		g.generateAndAdd(r, b.MinX-1, b.MinY+yo, b.MinZ+ho, dirWest, p.Depth)
	case dirWest, dirEast:
		g.generateAndAdd(r, b.MinX+ho, b.MinY+yo, b.MinZ-1, dirNorth, p.Depth)
	}
}

func (p *shPiece) childRight(g *shGen, r Random, yo, ho int32) {
	b := p.Box
	switch p.orient {
	case dirNorth, dirSouth:
		g.generateAndAdd(r, b.MaxX+1, b.MinY+yo, b.MinZ+ho, dirEast, p.Depth)
	case dirWest, dirEast:
		g.generateAndAdd(r, b.MinX+ho, b.MinY+yo, b.MaxZ+1, dirSouth, p.Depth)
	}
}

func (p *shPiece) addChildren(g *shGen, r Random) {
	switch p.kind {
	case shStart, shStairsDown:
		if p.flags[0] {
			g.imposed = shFiveCrossing
		}
		p.childForward(g, r, 1, 1)
	case shStraight:
		p.childForward(g, r, 1, 1)
		if p.flags[0] {
			p.childLeft(g, r, 1, 2)
		}
		if p.flags[1] {
			p.childRight(g, r, 1, 2)
		}
	case shChestCorridor, shPrisonHall, shStraightStairsDown:
		p.childForward(g, r, 1, 1)
	case shFiveCrossing:
		a, b := int32(3), int32(5)
		if p.orient == dirWest || p.orient == dirNorth {
			a, b = 8-a, 8-b
		}
		p.childForward(g, r, 5, 1)
		if p.flags[0] {
			p.childLeft(g, r, a, 1)
		}
		if p.flags[1] {
			p.childLeft(g, r, b, 7)
		}
		if p.flags[2] {
			p.childRight(g, r, a, 1)
		}
		if p.flags[3] {
			p.childRight(g, r, b, 7)
		}
	case shLeftTurn, shRightTurn:
		left := p.orient == dirNorth || p.orient == dirEast
		if p.kind == shRightTurn {
			left = !left
		}
		if left {
			p.childLeft(g, r, 1, 1)
		} else {
			p.childRight(g, r, 1, 1)
		}
	case shRoomCrossing:
		p.childForward(g, r, 4, 1)
		p.childLeft(g, r, 1, 4)
		p.childRight(g, r, 1, 4)
	case shPortalRoom:
		g.portal = p
	}
}

// smoothStoneSelector is StrongholdPieces.SmoothStoneSelector.
type smoothStoneSelector struct{ b world.Block }

func (s *smoothStoneSelector) next(r Random, x, y, z int32, edge bool) {
	if !edge {
		s.b = caveAir()
		return
	}
	f := r.NextFloat()
	switch {
	case f < 0.2:
		s.b = blockNamed("cracked_stone_bricks")
	case f < 0.5:
		s.b = blockNamed("mossy_stone_bricks")
	case f < 0.55:
		s.b = blockNamed("infested_stone_bricks")
	default:
		s.b = blockNamed("stone_bricks")
	}
}

func (s *smoothStoneSelector) get() world.Block { return s.b }

var shPalette = map[string]string{
	"sb": "stone_bricks", "slab": "smooth_stone_slab", "dslab": "smooth_stone_slab[type=double]", "bslab": "stone_brick_slab",
	"cobble": "cobblestone", "planks": "oak_planks", "shelf": "bookshelf", "web": "cobweb", "lava": "lava", "water": "water",
	"air": "cave_air", "torch": "torch", "wt": "wall_torch", "wtE": "wall_torch[facing=east]", "wtW": "wall_torch[facing=west]",
	"wtS": "wall_torch[facing=south]", "wtN": "wall_torch[facing=north]",
	"cstS": "cobblestone_stairs[facing=south]", "sbstN": "stone_brick_stairs[facing=north]",
	"ladW": "ladder[facing=west]", "ladS": "ladder[facing=south]",
	"barsNS": "iron_bars[north=true,south=true]", "barsNSE": "iron_bars[north=true,south=true,east=true]",
	"barsWE": "iron_bars[west=true,east=true]",
	"idoorW": "iron_door[facing=west]", "idoorWU": "iron_door[facing=west,half=upper]",
	"fWE": "oak_fence[west=true,east=true]", "fNS": "oak_fence[north=true,south=true]",
	"fNE": "oak_fence[north=true,east=true]", "fSE": "oak_fence[south=true,east=true]",
	"fNW": "oak_fence[north=true,west=true]", "fSW": "oak_fence[south=true,west=true]",
	"fE": "oak_fence[east=true]", "fW": "oak_fence[west=true]", "fAll": "oak_fence[north=true,south=true,west=true,east=true]",
	"fEN": "oak_fence[east=true,north=true]", "fES": "oak_fence[east=true,south=true]",
	"fWN": "oak_fence[west=true,north=true]", "fWS": "oak_fence[west=true,south=true]",
}

func shBP(src string) func() blueprint {
	return sync.OnceValue(func() blueprint { return parseBlueprint(shPalette, src) })
}

var (
	shStairsDownBP = shBP(`
set 2 6 1 sb
set 1 5 1 sb
set 1 6 1 slab
set 1 5 2 sb
set 1 4 3 sb
set 1 5 3 slab
set 2 4 3 sb
set 3 3 3 sb
set 3 4 3 slab
set 3 3 2 sb
set 3 2 1 sb
set 3 3 1 slab
set 2 2 1 sb
set 1 1 1 sb
set 1 2 1 slab
set 1 1 2 sb
set 1 1 3 slab
`)
	shChestBP = shBP(`
box 3 1 2 3 1 4 sb sb
set 3 1 1 bslab
set 3 1 5 bslab
set 3 2 2 bslab
set 3 2 4 bslab
set 2 1 2 bslab
set 2 1 3 bslab
set 2 1 4 bslab
`)
	shFiveBP = shBP(`
box 5 1 10 7 3 10 air air
sel 1 2 1 8 2 6
sel 4 1 5 4 4 9
sel 8 1 5 8 4 9
sel 1 4 7 3 4 9
sel 1 3 5 3 3 6
box 1 3 4 3 3 4 slab slab
box 1 4 6 3 4 6 slab slab
sel 5 1 7 7 1 8
box 5 1 9 7 1 9 slab slab
box 5 2 7 7 2 7 slab slab
box 4 5 7 4 5 9 slab slab
box 8 5 7 8 5 9 slab slab
box 5 5 7 7 5 9 dslab dslab
set 6 5 6 wtS
`)
	shPrisonBP = shBP(`
box 1 1 10 3 3 10 air air
sel 4 1 1 4 3 1
sel 4 1 3 4 3 3
sel 4 1 7 4 3 7
sel 4 1 9 4 3 9
set 4 1 4 barsNS
set 4 1 5 barsNSE
set 4 1 6 barsNS
set 5 1 5 barsWE
set 6 1 5 barsWE
set 7 1 5 barsWE
set 4 2 4 barsNS
set 4 2 5 barsNSE
set 4 2 6 barsNS
set 5 2 5 barsWE
set 6 2 5 barsWE
set 7 2 5 barsWE
set 4 3 4 barsNS
set 4 3 5 barsNSE
set 4 3 6 barsNS
set 5 3 5 barsWE
set 6 3 5 barsWE
set 7 3 5 barsWE
set 4 3 2 barsNS
set 4 3 8 barsNS
set 4 1 2 idoorW
set 4 2 2 idoorWU
set 4 1 8 idoorW
set 4 2 8 idoorWU
`)
	shRoom0BP = shBP(`
set 5 1 5 sb
set 5 2 5 sb
set 5 3 5 sb
set 4 3 5 wtW
set 6 3 5 wtE
set 5 3 4 wtS
set 5 3 6 wtN
set 4 1 4 slab
set 4 1 5 slab
set 4 1 6 slab
set 6 1 4 slab
set 6 1 5 slab
set 6 1 6 slab
set 5 1 4 slab
set 5 1 6 slab
`)
	shRoom1BP = func() func() blueprint {
		var s string
		for i := 0; i < 5; i++ {
			s += fmt.Sprintf("set 3 1 %d sb\nset 7 1 %d sb\nset %d 1 3 sb\nset %d 1 7 sb\n", 3+i, 3+i, 3+i, 3+i)
		}
		return shBP(s + "set 5 1 5 sb\nset 5 2 5 sb\nset 5 3 5 sb\nset 5 4 5 water\n")
	}()
	shRoom2BP = func() func() blueprint {
		var s string
		for i := 1; i <= 9; i++ {
			s += fmt.Sprintf("set 1 3 %d cobble\nset 9 3 %d cobble\n", i, i)
		}
		for i := 1; i <= 9; i++ {
			s += fmt.Sprintf("set %d 3 1 cobble\nset %d 3 9 cobble\n", i, i)
		}
		s += "set 5 1 4 cobble\nset 5 1 6 cobble\nset 5 3 4 cobble\nset 5 3 6 cobble\nset 4 1 5 cobble\nset 6 1 5 cobble\nset 4 3 5 cobble\nset 6 3 5 cobble\n"
		for i := 1; i <= 3; i++ {
			s += fmt.Sprintf("set 4 %d 4 cobble\nset 6 %d 4 cobble\nset 4 %d 6 cobble\nset 6 %d 6 cobble\n", i, i, i, i)
		}
		s += "set 5 3 5 wt\n"
		for z := 2; z <= 8; z++ {
			s += fmt.Sprintf("set 2 3 %d planks\nset 3 3 %d planks\n", z, z)
			if z <= 3 || z >= 7 {
				s += fmt.Sprintf("set 4 3 %d planks\nset 5 3 %d planks\nset 6 3 %d planks\n", z, z, z)
			}
			s += fmt.Sprintf("set 7 3 %d planks\nset 8 3 %d planks\n", z, z)
		}
		return shBP(s + "set 9 1 3 ladW\nset 9 2 3 ladW\nset 9 3 3 ladW\n")
	}()
	shLibraryTallBP = shBP(`
box 1 5 1 3 5 13 planks planks
box 10 5 1 12 5 13 planks planks
box 4 5 1 9 5 2 planks planks
box 4 5 12 9 5 13 planks planks
set 9 5 11 planks
set 8 5 11 planks
set 9 5 10 planks
box 3 6 3 3 6 11 fNS fNS
box 10 6 3 10 6 9 fNS fNS
box 4 6 2 9 6 2 fWE fWE
box 4 6 12 7 6 12 fWE fWE
set 3 6 2 fNE
set 3 6 12 fSE
set 10 6 2 fNW
set 8 6 12 fSW
set 8 6 11 fNE
set 9 6 11 fSW
set 9 6 10 fNE
set 10 6 10 fSW
set 10 1 13 ladS
set 10 2 13 ladS
set 10 3 13 ladS
set 10 4 13 ladS
set 10 5 13 ladS
set 10 6 13 ladS
set 10 7 13 ladS
set 6 9 7 fE
set 7 9 7 fW
set 6 8 7 fE
set 7 8 7 fW
set 6 7 7 fAll
set 7 7 7 fAll
set 5 7 7 fE
set 8 7 7 fW
set 6 7 6 fEN
set 6 7 8 fES
set 7 7 6 fWN
set 7 7 8 fWS
set 5 8 7 torch
set 8 8 7 torch
set 6 8 6 torch
set 6 8 8 torch
set 7 8 6 torch
set 7 8 8 torch
`)
	shPortalBP = func() func() blueprint {
		s := `
sel 1 6 1 1 6 14
sel 9 6 1 9 6 14
sel 2 6 1 8 6 2
sel 2 6 14 8 6 14
sel 1 1 1 2 1 4
sel 8 1 1 9 1 4
box 1 1 1 1 1 3 lava lava
box 9 1 1 9 1 3 lava lava
sel 3 1 8 7 1 12
box 4 1 9 6 1 11 lava lava
`
		for z := 3; z < 14; z += 2 {
			s += fmt.Sprintf("box 0 3 %d 0 4 %d barsNS barsNS\nbox 10 3 %d 10 4 %d barsNS barsNS\n", z, z, z, z)
		}
		for x := 2; x < 9; x += 2 {
			s += fmt.Sprintf("box %d 3 15 %d 4 15 barsWE barsWE\n", x, x)
		}
		s += "sel 4 1 5 6 1 7\nsel 4 2 6 6 2 7\nsel 4 3 7 6 3 7\n"
		for x := 4; x <= 6; x++ {
			s += fmt.Sprintf("set %d 1 4 sbstN\nset %d 2 5 sbstN\nset %d 3 6 sbstN\n", x, x, x)
		}
		return shBP(s)
	}()
)

// shStraightStairsSteps is StraightStairsDown's six cobblestone stair
// rows over stone bricks.
func (p *shPiece) straightStairs(c *PieceContext, bb BoundingBox) {
	st := stateOf("cobblestone_stairs", "facing", "south")
	sb := blockNamed("stone_bricks")
	for i := int32(0); i < 6; i++ {
		for x := int32(1); x <= 3; x++ {
			p.placeBlock(c, st, x, 6-i, 1+i, bb)
		}
		if i < 5 {
			for x := int32(1); x <= 3; x++ {
				p.placeBlock(c, sb, x, 5-i, 1+i, bb)
			}
		}
	}
}

// generateSmallDoor is StrongholdPiece.generateSmallDoor.
func (p *shPiece) generateSmallDoor(c *PieceContext, bb BoundingBox, door int, x, y, z int32) {
	air := caveAir()
	sb := blockNamed("stone_bricks")
	frame := func() {
		p.placeBlock(c, sb, x, y, z, bb)
		p.placeBlock(c, sb, x, y+1, z, bb)
		p.placeBlock(c, sb, x, y+2, z, bb)
		p.placeBlock(c, sb, x+1, y+2, z, bb)
		p.placeBlock(c, sb, x+2, y+2, z, bb)
		p.placeBlock(c, sb, x+2, y+1, z, bb)
		p.placeBlock(c, sb, x+2, y, z, bb)
	}
	switch door {
	case doorOpening:
		p.generateBox(c, bb, x, y, z, x+3-1, y+3-1, z, air, air, false)
	case doorWood:
		frame()
		p.placeBlock(c, blockNamed("oak_door"), x+1, y, z, bb)
		p.placeBlock(c, stateOf("oak_door", "half", "upper"), x+1, y+1, z, bb)
	case doorGrates:
		p.placeBlock(c, air, x+1, y, z, bb)
		p.placeBlock(c, air, x+1, y+1, z, bb)
		w := stateOf("iron_bars", "west", "true")
		ew := stateOf("iron_bars", "east", "true", "west", "true")
		e := stateOf("iron_bars", "east", "true")
		p.placeBlock(c, w, x, y, z, bb)
		p.placeBlock(c, w, x, y+1, z, bb)
		p.placeBlock(c, ew, x, y+2, z, bb)
		p.placeBlock(c, ew, x+1, y+2, z, bb)
		p.placeBlock(c, ew, x+2, y+2, z, bb)
		p.placeBlock(c, e, x+2, y+1, z, bb)
		p.placeBlock(c, e, x+2, y, z, bb)
	case doorIron:
		frame()
		p.placeBlock(c, blockNamed("iron_door"), x+1, y, z, bb)
		p.placeBlock(c, stateOf("iron_door", "half", "upper"), x+1, y+1, z, bb)
		p.placeBlock(c, stateOf("stone_button", "facing", "north"), x+2, y+1, z+1, bb)
		p.placeBlock(c, stateOf("stone_button", "facing", "south"), x+2, y+1, z-1, bb)
	}
}

func (p *shPiece) PostProcess(c *PieceContext) {
	p.mu.Lock()
	defer p.mu.Unlock()
	bb := c.ChunkBox
	r := c.Random
	sel := &smoothStoneSelector{}
	air := caveAir()
	lp := &p.legacyPiece
	box := func(x0, y0, z0, x1, y1, z1 int32) { p.generateBoxSel(c, bb, x0, y0, z0, x1, y1, z1, true, r, sel) }
	switch p.kind {
	case shStart, shStairsDown:
		box(0, 0, 0, 4, 10, 4)
		p.generateSmallDoor(c, bb, p.door, 1, 7, 0)
		p.generateSmallDoor(c, bb, doorOpening, 1, 1, 4)
		shStairsDownBP().run(lp, c, bb)
	case shStraight:
		box(0, 0, 0, 4, 4, 6)
		p.generateSmallDoor(c, bb, p.door, 1, 1, 0)
		p.generateSmallDoor(c, bb, doorOpening, 1, 1, 6)
		e, w := stateOf("wall_torch", "facing", "east"), stateOf("wall_torch", "facing", "west")
		p.maybeGenerateBlock(c, bb, r, 0.1, 1, 2, 1, e)
		p.maybeGenerateBlock(c, bb, r, 0.1, 3, 2, 1, w)
		p.maybeGenerateBlock(c, bb, r, 0.1, 1, 2, 5, e)
		p.maybeGenerateBlock(c, bb, r, 0.1, 3, 2, 5, w)
		if p.flags[0] {
			p.generateBox(c, bb, 0, 1, 2, 0, 3, 4, air, air, false)
		}
		if p.flags[1] {
			p.generateBox(c, bb, 4, 1, 2, 4, 3, 4, air, air, false)
		}
	case shChestCorridor:
		box(0, 0, 0, 4, 4, 6)
		p.generateSmallDoor(c, bb, p.door, 1, 1, 0)
		p.generateSmallDoor(c, bb, doorOpening, 1, 1, 6)
		shChestBP().run(lp, c, bb)
		if !p.flags[0] && bb.IsInside(p.worldPos(3, 2, 3)) {
			p.flags[0] = true
			p.createChest(c, bb, r, 3, 2, 3, "minecraft:chests/stronghold_corridor")
		}
	case shFillerCorridor:
		sb := blockNamed("stone_bricks")
		for z := int32(0); z < p.num; z++ {
			for x := int32(0); x <= 4; x++ {
				p.placeBlock(c, sb, x, 0, z, bb)
			}
			for y := int32(1); y <= 3; y++ {
				p.placeBlock(c, sb, 0, y, z, bb)
				p.placeBlock(c, air, 1, y, z, bb)
				p.placeBlock(c, air, 2, y, z, bb)
				p.placeBlock(c, air, 3, y, z, bb)
				p.placeBlock(c, sb, 4, y, z, bb)
			}
			for x := int32(0); x <= 4; x++ {
				p.placeBlock(c, sb, x, 4, z, bb)
			}
		}
	case shFiveCrossing:
		box(0, 0, 0, 9, 8, 10)
		p.generateSmallDoor(c, bb, p.door, 4, 3, 0)
		if p.flags[0] {
			p.generateBox(c, bb, 0, 3, 1, 0, 5, 3, air, air, false)
		}
		if p.flags[2] {
			p.generateBox(c, bb, 9, 3, 1, 9, 5, 3, air, air, false)
		}
		if p.flags[1] {
			p.generateBox(c, bb, 0, 5, 7, 0, 7, 9, air, air, false)
		}
		if p.flags[3] {
			p.generateBox(c, bb, 9, 5, 7, 9, 7, 9, air, air, false)
		}
		shFiveBP().runSel(lp, c, bb, sel)
	case shLeftTurn, shRightTurn:
		box(0, 0, 0, 4, 4, 4)
		p.generateSmallDoor(c, bb, p.door, 1, 1, 0)
		west := p.orient == dirNorth || p.orient == dirEast
		if p.kind == shRightTurn {
			west = !west
		}
		if west {
			p.generateBox(c, bb, 0, 1, 1, 0, 3, 3, air, air, false)
		} else {
			p.generateBox(c, bb, 4, 1, 1, 4, 3, 3, air, air, false)
		}
	case shStraightStairsDown:
		box(0, 0, 0, 4, 10, 7)
		p.generateSmallDoor(c, bb, p.door, 1, 7, 0)
		p.generateSmallDoor(c, bb, doorOpening, 1, 1, 7)
		p.straightStairs(c, bb)
	case shPrisonHall:
		box(0, 0, 0, 8, 4, 10)
		p.generateSmallDoor(c, bb, p.door, 1, 1, 0)
		shPrisonBP().runSel(lp, c, bb, sel)
	case shRoomCrossing:
		box(0, 0, 0, 10, 6, 10)
		p.generateSmallDoor(c, bb, p.door, 4, 1, 0)
		p.generateBox(c, bb, 4, 1, 10, 6, 3, 10, air, air, false)
		p.generateBox(c, bb, 0, 1, 4, 0, 3, 6, air, air, false)
		p.generateBox(c, bb, 10, 1, 4, 10, 3, 6, air, air, false)
		switch p.num {
		case 0:
			shRoom0BP().run(lp, c, bb)
		case 1:
			shRoom1BP().run(lp, c, bb)
		case 2:
			shRoom2BP().run(lp, c, bb)
			p.createChest(c, bb, r, 3, 4, 8, "minecraft:chests/stronghold_crossing")
		}
	case shLibrary:
		h := int32(6)
		if p.flags[0] {
			h = 11
		}
		box(0, 0, 0, 13, h-1, 14)
		p.generateSmallDoor(c, bb, p.door, 4, 1, 0)
		web := blockNamed("cobweb")
		p.generateMaybeBox(c, bb, r, 0.07, 2, 1, 1, 11, 4, 13, web, web, false, false)
		planks, shelf := blockNamed("oak_planks"), blockNamed("bookshelf")
		e, w := stateOf("wall_torch", "facing", "east"), stateOf("wall_torch", "facing", "west")
		for z := int32(1); z <= 13; z++ {
			if (z-1)%4 == 0 {
				p.generateBox(c, bb, 1, 1, z, 1, 4, z, planks, planks, false)
				p.generateBox(c, bb, 12, 1, z, 12, 4, z, planks, planks, false)
				p.placeBlock(c, e, 2, 3, z, bb)
				p.placeBlock(c, w, 11, 3, z, bb)
				if p.flags[0] {
					p.generateBox(c, bb, 1, 6, z, 1, 9, z, planks, planks, false)
					p.generateBox(c, bb, 12, 6, z, 12, 9, z, planks, planks, false)
				}
			} else {
				p.generateBox(c, bb, 1, 1, z, 1, 4, z, shelf, shelf, false)
				p.generateBox(c, bb, 12, 1, z, 12, 4, z, shelf, shelf, false)
				if p.flags[0] {
					p.generateBox(c, bb, 1, 6, z, 1, 9, z, shelf, shelf, false)
					p.generateBox(c, bb, 12, 6, z, 12, 9, z, shelf, shelf, false)
				}
			}
		}
		for z := int32(3); z < 12; z += 2 {
			p.generateBox(c, bb, 3, 1, z, 4, 3, z, shelf, shelf, false)
			p.generateBox(c, bb, 6, 1, z, 7, 3, z, shelf, shelf, false)
			p.generateBox(c, bb, 9, 1, z, 10, 3, z, shelf, shelf, false)
		}
		if p.flags[0] {
			shLibraryTallBP().run(lp, c, bb)
		}
		p.createChest(c, bb, r, 3, 3, 5, "minecraft:chests/stronghold_library")
		if p.flags[0] {
			p.placeBlock(c, air, 12, 9, 1, bb)
			p.createChest(c, bb, r, 12, 8, 1, "minecraft:chests/stronghold_library")
		}
	case shPortalRoom:
		p.generateBoxSel(c, bb, 0, 0, 0, 10, 7, 15, false, r, sel)
		p.generateSmallDoor(c, bb, doorGrates, 4, 1, 0)
		shPortalBP().runSel(lp, c, bb, sel)
		var eyes [12]bool
		all := true
		for i := range eyes {
			eyes[i] = r.NextFloat() > 0.9
			all = all && eyes[i]
		}
		frame := func(f string, i int) world.Block {
			return stateOf("end_portal_frame", "facing", f, "eye", boolName(eyes[i]))
		}
		p.placeBlock(c, frame("north", 0), 4, 3, 8, bb)
		p.placeBlock(c, frame("north", 1), 5, 3, 8, bb)
		p.placeBlock(c, frame("north", 2), 6, 3, 8, bb)
		p.placeBlock(c, frame("south", 3), 4, 3, 12, bb)
		p.placeBlock(c, frame("south", 4), 5, 3, 12, bb)
		p.placeBlock(c, frame("south", 5), 6, 3, 12, bb)
		p.placeBlock(c, frame("east", 6), 3, 3, 9, bb)
		p.placeBlock(c, frame("east", 7), 3, 3, 10, bb)
		p.placeBlock(c, frame("east", 8), 3, 3, 11, bb)
		p.placeBlock(c, frame("west", 9), 7, 3, 9, bb)
		p.placeBlock(c, frame("west", 10), 7, 3, 10, bb)
		p.placeBlock(c, frame("west", 11), 7, 3, 11, bb)
		if all {
			ep := blockNamed("end_portal")
			for z := int32(9); z <= 11; z++ {
				for x := int32(4); x <= 6; x++ {
					p.placeBlock(c, ep, x, 3, z, bb)
				}
			}
		}
		if !p.flags[0] {
			pos := p.worldPos(5, 3, 6)
			if bb.IsInside(pos) {
				p.flags[0] = true
				c.Region.set(pos, spawnerBlock())
				// SpawnerBlockEntity.setEntityId(SILVERFISH): no draw.
			}
		}
	}
}

func (p *shPiece) BoundingBox() BoundingBox { return p.Box }

func (p *shPiece) SaveData(t map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t["EntryDoor"] = doorNames[p.door]
	f := func(k string, i int) { t[k] = boolByte(p.flags[i]) }
	switch p.kind {
	case shStart, shStairsDown:
		f("Source", 0)
	case shStraight:
		f("Left", 0)
		f("Right", 1)
	case shChestCorridor:
		f("Chest", 0)
	case shFillerCorridor:
		t["Steps"] = p.num
	case shFiveCrossing:
		f("leftLow", 0)
		f("leftHigh", 1)
		f("rightLow", 2)
		f("rightHigh", 3)
	case shLibrary:
		f("Tall", 0)
	case shPortalRoom:
		f("Mob", 0)
	case shRoomCrossing:
		t["Type"] = p.num
	}
}

var shSavedFlags = map[int][]string{
	shStart: {"Source"}, shStairsDown: {"Source"}, shStraight: {"Left", "Right"}, shChestCorridor: {"Chest"},
	shFiveCrossing: {"leftLow", "leftHigh", "rightLow", "rightHigh"}, shLibrary: {"Tall"}, shPortalRoom: {"Mob"},
}

// generateStronghold is StrongholdStructure.generatePieces.
func generateStronghold(g *Generator, c *GenContext) []StructurePiece {
	r := c.Random
	for i := int64(0); ; i++ {
		setLargeFeatureSeed(r, c.Seed+i, c.ChunkX, c.ChunkZ)
		sg := newSHGen()
		start := &shPiece{}
		x, z := c.ChunkX*16+2, c.ChunkZ*16+2
		d := randomHorizontalDir(r)
		start.init(shStart, 0, makeBoundingBox(x, 64, z, d, 5, 11, 5), d)
		start.flags[0] = true
		start.door = doorOpening
		sg.start = start
		sg.pieces = append(sg.pieces, start)
		start.addChildren(sg, r)
		for len(sg.pending) > 0 {
			k := r.NextIntN(int32(len(sg.pending)))
			p := sg.pending[k]
			sg.pending = append(sg.pending[:k], sg.pending[k+1:]...)
			p.addChildren(sg, r)
		}
		moveBelowSeaLevel(sg.pieces, g.RS.settings.seaLevel, g.RS.lvMinY, r, 10)
		if len(sg.pieces) > 0 && sg.portal != nil {
			return sg.pieces
		}
	}
}

func init() {
	structureTypes["stronghold"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		return func(c *GenContext) (*GenerationStub, bool) {
			return &GenerationStub{Pos: BlockPos{c.ChunkX * 16, 0, c.ChunkZ * 16}, Generate: func(add func(StructurePiece)) {
				for _, p := range generateStronghold(g, c) {
					add(p)
				}
			}}, true
		}
	}
	for kind, id := range shTypeIDs {
		kind, id := kind, id
		pieceLoaders[id] = func(g *Generator, t map[string]any) (StructurePiece, error) {
			lp, err := loadLegacyBase(id, t)
			if err != nil {
				return nil, err
			}
			p := &shPiece{legacyPiece: lp, kind: kind}
			ds, _ := t["EntryDoor"].(string)
			for i, n := range doorNames {
				if n == ds {
					p.door = i
				}
			}
			for i, k := range shSavedFlags[kind] {
				p.flags[i] = tagBool(t, k)
			}
			switch kind {
			case shFillerCorridor:
				p.num = tagInt(t, "Steps")
			case shRoomCrossing:
				p.num = tagInt(t, "Type")
			}
			return p, nil
		}
	}
}
