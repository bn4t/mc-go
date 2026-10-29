package worldgen

import (
	"fmt"
	"strings"
	"sync"

	"mc-go/internal/world"
)

// Jungle temples (JungleTempleStructure + JungleTemplePiece), measured
// from the 26.2 bytecode: a 12 x 10 x 15 SinglePieceStructure piece
// moved (once) to the average ground height of its columns in the
// first decorating chunk, built from cobblestone / mossy cobblestone
// (MossStoneSelector: nextFloat < 0.4 cobblestone, one draw per cell of
// every selector box) with the tripwire and lever traps, two dispensers
// (placedTrap1 / placedTrap2) and two chests (placedMainChest /
// placedHiddenChest), each placed once.

var junglePalette = map[string]string{
	"air": "air", "moss": "mossy_cobblestone", "chis": "chiseled_stone_bricks",
	"se": "cobblestone_stairs[facing=east]", "sw": "cobblestone_stairs[facing=west]",
	"sS": "cobblestone_stairs[facing=south]", "sn": "cobblestone_stairs[facing=north]",
	"hookE":  "tripwire_hook[facing=east,attached=true]",
	"hookW":  "tripwire_hook[facing=west,attached=true]",
	"hookN":  "tripwire_hook[facing=north,attached=true]",
	"hookS":  "tripwire_hook[facing=south,attached=true]",
	"wireEW": "tripwire[east=true,west=true,attached=true]",
	"wireNS": "tripwire[north=true,south=true,attached=true]",
	"rNS":    "redstone_wire[north=side,south=side]",
	"rNW":    "redstone_wire[north=side,west=side]",
	"rEW":    "redstone_wire[east=side,west=side]",
	"rWS":    "redstone_wire[west=side,south=side]",
	"rNSup":  "redstone_wire[north=side,south=up]",
	"rAll":   "redstone_wire[north=side,south=side,east=side,west=side]",
	"vineS":  "vine[south=true]",
	"vineE":  "vine[east=true]",
	"lever":  "lever[facing=north,face=wall]",
	"pUp":    "sticky_piston[facing=up]",
	"pW":     "sticky_piston[facing=west]",
	"rep":    "repeater[facing=north]",
}

var jungleBlueprints = sync.OnceValue(func() [4]blueprint {
	var a strings.Builder
	a.WriteString(jungleShell)
	for _, z := range []int{0, 14} {
		for _, x := range []int{2, 4, 7, 9} {
			fmt.Fprintf(&a, "sel %d 4 %d %d 5 %d\n", x, z, x, z)
		}
	}
	a.WriteString("sel 5 6 0 6 6 0\n")
	for _, x := range []int{0, 11} {
		for z := 2; z <= 12; z += 2 {
			fmt.Fprintf(&a, "sel %d 4 %d %d 5 %d\n", x, z, x, z)
		}
		fmt.Fprintf(&a, "sel %d 6 5 %d 6 5\nsel %d 6 9 %d 6 9\n", x, x, x, x)
	}
	a.WriteString(jungleRoof)
	for i := 0; i < 4; i++ {
		fmt.Fprintf(&a, "set 5 %d %d sS\nset 6 %d %d sS\nbox 5 %d %d 6 %d %d air air\n", -i, 6+i, -i, 6+i, -i, 7+i, -i, 9+i)
	}
	a.WriteString("box 1 -3 12 10 -1 13 air air\nbox 1 -3 1 3 -1 13 air air\nbox 1 -3 1 9 -1 5 air air\n")
	for z := 1; z <= 13; z += 2 {
		fmt.Fprintf(&a, "sel 1 -3 %d 1 -2 %d\n", z, z)
	}
	for z := 2; z <= 12; z += 2 {
		fmt.Fprintf(&a, "sel 1 -1 %d 3 -1 %d\n", z, z)
	}
	a.WriteString(jungleTrap1)
	return [4]blueprint{
		parseBlueprint(junglePalette, a.String()),
		parseBlueprint(junglePalette, jungleTrap2),
		parseBlueprint(junglePalette, "set 8 -1 3 vineE\nset 8 -2 3 vineE\n"),
		parseBlueprint(junglePalette, jungleLevers),
	}
})

const jungleShell = `
sel 0 -4 0 11 0 14
sel 2 1 2 9 2 2
sel 2 1 12 9 2 12
sel 2 1 3 2 2 11
sel 9 1 3 9 2 11
sel 1 3 1 10 6 1
sel 1 3 13 10 6 13
sel 1 3 2 1 6 12
sel 10 3 2 10 6 12
sel 2 3 2 9 3 12
sel 2 6 2 9 6 12
sel 3 7 3 8 7 11
sel 4 8 4 7 8 10
box 3 1 3 8 2 11 air air
box 4 3 6 7 3 9 air air
box 2 4 2 9 5 12 air air
box 4 6 5 7 6 9 air air
box 5 7 6 6 7 8 air air
box 5 1 2 6 2 2 air air
box 5 2 12 6 2 12 air air
box 5 5 1 6 5 1 air air
box 5 5 13 6 5 13 air air
set 1 5 5 air
set 10 5 5 air
set 1 5 9 air
set 10 5 9 air
`

const jungleRoof = `
sel 2 7 2 2 9 2
sel 9 7 2 9 9 2
sel 2 7 12 2 9 12
sel 9 7 12 9 9 12
sel 4 9 4 4 9 4
sel 7 9 4 7 9 4
sel 4 9 10 4 9 10
sel 7 9 10 7 9 10
sel 5 9 7 6 9 7
set 5 9 6 sn
set 6 9 6 sn
set 5 9 8 sS
set 6 9 8 sS
set 4 0 0 sn
set 5 0 0 sn
set 6 0 0 sn
set 7 0 0 sn
set 4 1 8 sn
set 4 2 9 sn
set 4 3 10 sn
set 7 1 8 sn
set 7 2 9 sn
set 7 3 10 sn
sel 4 1 9 4 1 9
sel 7 1 9 7 1 9
sel 4 1 10 7 2 10
sel 5 4 5 6 4 5
set 4 4 5 se
set 7 4 5 sw
`

const jungleTrap1 = `
sel 2 -2 1 5 -2 1
sel 7 -2 1 9 -2 1
sel 6 -3 1 6 -3 1
sel 6 -1 1 6 -1 1
set 1 -3 8 hookE
set 4 -3 8 hookW
set 2 -3 8 wireEW
set 3 -3 8 wireEW
set 5 -3 7 rNS
set 5 -3 6 rNS
set 5 -3 5 rNS
set 5 -3 4 rNS
set 5 -3 3 rNS
set 5 -3 2 rNS
set 5 -3 1 rNW
set 4 -3 1 rEW
set 3 -3 1 moss
`

const jungleTrap2 = `
set 3 -2 2 vineS
set 7 -3 1 hookN
set 7 -3 5 hookS
set 7 -3 2 wireNS
set 7 -3 3 wireNS
set 7 -3 4 wireNS
set 8 -3 6 rEW
set 9 -3 6 rWS
set 9 -3 5 rNSup
set 9 -3 4 moss
set 9 -2 4 rNS
`

const jungleLevers = `
set 9 -3 2 moss
set 8 -3 1 moss
set 4 -3 5 moss
set 5 -2 5 moss
set 5 -1 5 moss
set 6 -3 5 moss
set 7 -2 5 moss
set 7 -1 5 moss
set 8 -3 5 moss
sel 9 -1 1 9 -1 5
box 8 -3 8 10 -1 10 air air
set 8 -2 11 chis
set 9 -2 11 chis
set 10 -2 11 chis
set 8 -2 12 lever
set 9 -2 12 lever
set 10 -2 12 lever
sel 8 -3 8 8 -3 10
sel 10 -3 8 10 -3 10
set 10 -2 9 moss
set 8 -2 9 rNS
set 8 -2 10 rNS
set 10 -1 9 rAll
set 9 -2 8 pUp
set 10 -2 8 pW
set 10 -1 8 pW
set 10 -2 10 rep
`

// mossStoneSelector is JungleTemplePiece.MossStoneSelector.
type mossStoneSelector struct{ b world.Block }

func (s *mossStoneSelector) next(r Random, x, y, z int32, edge bool) {
	if r.NextFloat() < 0.4 {
		s.b = blockNamed("cobblestone")
	} else {
		s.b = blockNamed("mossy_cobblestone")
	}
}

func (s *mossStoneSelector) get() world.Block { return s.b }

type jungleTemplePiece struct {
	scatteredPiece
	mainChest, hiddenChest, trap1, trap2 bool
}

func newJungleTemple(r Random, x, z int32) *jungleTemplePiece {
	p := &jungleTemplePiece{}
	p.initScattered("tejp", x, 64, z, 12, 10, 15, randomHorizontalDir(r))
	return p
}

func (p *jungleTemplePiece) BoundingBox() BoundingBox {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.Box
}

func (p *jungleTemplePiece) SaveData(t map[string]any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.saveScattered(t)
	t["placedMainChest"] = boolByte(p.mainChest)
	t["placedHiddenChest"] = boolByte(p.hiddenChest)
	t["placedTrap1"] = boolByte(p.trap1)
	t["placedTrap2"] = boolByte(p.trap2)
}

func (p *jungleTemplePiece) PostProcess(c *PieceContext) {
	p.mu.Lock()
	defer p.mu.Unlock()
	bb := c.ChunkBox
	if !p.updateAverageGroundHeight(c, bb, 0) {
		return
	}
	r := c.Random
	sel := &mossStoneSelector{}
	bps := jungleBlueprints()
	lp := &p.legacyPiece
	const disp = "minecraft:chests/jungle_temple_dispenser"
	const chest = "minecraft:chests/jungle_temple"
	bps[0].runSel(lp, c, bb, sel)
	if !p.trap1 {
		p.trap1 = p.createDispenser(c, bb, r, 3, -2, 1, dirNorth, disp)
	}
	bps[1].runSel(lp, c, bb, sel)
	if !p.trap2 {
		p.trap2 = p.createDispenser(c, bb, r, 9, -2, 3, dirWest, disp)
	}
	bps[2].runSel(lp, c, bb, sel)
	if !p.mainChest {
		p.mainChest = p.createChest(c, bb, r, 8, -3, 3, chest)
	}
	bps[3].runSel(lp, c, bb, sel)
	if !p.hiddenChest {
		p.hiddenChest = p.createChest(c, bb, r, 9, -3, 10, chest)
	}
}

func init() {
	structureTypes["jungle_temple"] = func(g *Generator, d *structureDef) func(c *GenContext) (*GenerationStub, bool) {
		return singlePieceStructure(g, 12, 15, func(r Random, x, z int32) StructurePiece { return newJungleTemple(r, x, z) })
	}
	pieceLoaders["tejp"] = func(g *Generator, t map[string]any) (StructurePiece, error) {
		p := &jungleTemplePiece{}
		if err := p.loadScattered("tejp", t); err != nil {
			return nil, err
		}
		p.mainChest, p.hiddenChest = tagBool(t, "placedMainChest"), tagBool(t, "placedHiddenChest")
		p.trap1, p.trap2 = tagBool(t, "placedTrap1"), tagBool(t, "placedTrap2")
		return p, nil
	}
}
