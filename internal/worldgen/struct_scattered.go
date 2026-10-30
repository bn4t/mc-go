package worldgen

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"mc-go/internal/world"
)

// ScatteredFeaturePiece and SinglePieceStructure, the base of the
// temples and the swamp hut, measured from the 26.2 bytecode:
//
//   - SinglePieceStructure.findGenerationPoint: no start when the
//     lowest WORLD_SURFACE_WG first-occupied height of the four corners
//     (chunk min, + width, + depth) is below sea level; else the stub
//     sits on the chunk center's first occupied height and builds the
//     piece at the chunk's min block x / z (y 64) with the context
//     random.
//   - ScatteredFeaturePiece: a makeBoundingBox box facing a random
//     horizontal direction, saved Width / Height / Depth / HPos.
//     updateAverageGroundHeight (mean MOTION_BLOCKING_NO_LEAVES height
//     over the box columns inside the chunk box) and
//     updateHeightPositionToLowestGroundHeight (lowest over all box
//     columns) run once: the first decorating chunk fixes HPos and moves
//     the box to it plus an offset.

type scatteredPiece struct {
	legacyPiece
	width, height, depth int32
	hpos                 int32
	mu                   sync.Mutex
}

func (p *scatteredPiece) initScattered(typ string, x, y, z, w, h, d int32, dir int) {
	p.legacyPiece = newLegacyPiece(typ, 0, makeBoundingBox(x, y, z, dir, w, h, d))
	p.width, p.height, p.depth, p.hpos = w, h, d, -1
	p.setOrientation(dir)
}

func (p *scatteredPiece) loadScattered(typ string, t map[string]any) error {
	lp, err := loadLegacyBase(typ, t)
	if err != nil {
		return err
	}
	p.legacyPiece = lp
	p.width, p.height, p.depth, p.hpos = tagInt(t, "Width"), tagInt(t, "Height"), tagInt(t, "Depth"), tagInt(t, "HPos")
	return nil
}

func (p *scatteredPiece) saveScattered(t map[string]any) {
	t["Width"], t["Height"], t["Depth"], t["HPos"] = p.width, p.height, p.depth, p.hpos
}

// updateAverageGroundHeight is ScatteredFeaturePiece.updateAverageGroundHeight.
func (p *scatteredPiece) updateAverageGroundHeight(c *PieceContext, bb BoundingBox, off int32) bool {
	if p.hpos >= 0 {
		return true
	}
	sum, n := int32(0), int32(0)
	for z := p.Box.MinZ; z <= p.Box.MaxZ; z++ {
		for x := p.Box.MinX; x <= p.Box.MaxX; x++ {
			if bb.IsInside(BlockPos{x, 64, z}) {
				sum += c.Region.Height(HMMotionBlockingNoLeaves, x, z)
				n++
			}
		}
	}
	if n == 0 {
		return false
	}
	p.hpos = sum / n
	p.Box = p.Box.Moved(0, p.hpos-p.Box.MinY+off, 0)
	return true
}

// updateHeightPositionToLowestGroundHeight is the ScatteredFeaturePiece
// method of that name.
func (p *scatteredPiece) updateHeightPositionToLowestGroundHeight(c *PieceContext, off int32) bool {
	if p.hpos >= 0 {
		return true
	}
	lo := c.Region.MaxY() + 1
	any := false
	for z := p.Box.MinZ; z <= p.Box.MaxZ; z++ {
		for x := p.Box.MinX; x <= p.Box.MaxX; x++ {
			lo = min(lo, c.Region.Height(HMMotionBlockingNoLeaves, x, z))
			any = true
		}
	}
	if !any {
		return false
	}
	p.hpos = lo
	p.Box = p.Box.Moved(0, p.hpos-p.Box.MinY+off, 0)
	return true
}

// singlePieceStructure is SinglePieceStructure's generation point for a
// width x depth piece built by mk.
func singlePieceStructure(g *Generator, width, depth int32, mk func(r Random, x, z int32) StructurePiece) func(c *GenContext) (*GenerationStub, bool) {
	return func(c *GenContext) (*GenerationStub, bool) {
		x0, z0 := c.ChunkX*16, c.ChunkZ*16
		if lowestCornerY(g, x0, z0, width, depth) < g.seaLevel() {
			return nil, false
		}
		mx, mz := x0+8, z0+8
		y := g.firstOccupiedHeight(mx, mz, HMWorldSurfaceWG)
		return &GenerationStub{Pos: BlockPos{mx, y, mz}, Generate: func(add func(StructurePiece)) {
			add(mk(c.Random, x0, z0))
		}}, true
	}
}

// lowestCornerY is Structure.getLowestY: the lowest WORLD_SURFACE_WG
// first-occupied height of (x, z), (x, z + d), (x + w, z), (x + w, z + d).
func lowestCornerY(g *Generator, x, z, w, d int32) int32 {
	h := func(x, z int32) int32 { return g.firstOccupiedHeight(x, z, HMWorldSurfaceWG) }
	return min(min(h(x, z), h(x, z+d)), min(h(x+w, z), h(x+w, z+d)))
}

// blueprint is a measured run of fixed block writes in piece-local
// coordinates, one op per line:
//
//	box x0 y0 z0 x1 y1 z1 edge inner [skip]   generateBox (skip: skipAir)
//	set x y z state                            placeBlock
//	sel x0 y0 z0 x1 y1 z1                      generateBox with the run's
//	                                           block selector (skipAir off)
//
// States name entries of the piece's palette (or a block id).
type blueprint []bpOp

type bpOp struct {
	box, sel               bool
	skipAir                bool
	x0, y0, z0, x1, y1, z1 int32
	a, b                   world.Block
}

func parseBlueprint(palette map[string]string, src string) blueprint {
	state := func(tok string) world.Block {
		if s, ok := palette[tok]; ok {
			tok = s
		}
		b, ok := parseBlockState(tok)
		if !ok {
			panic("worldgen: blueprint state " + tok)
		}
		return b
	}
	num := func(s string) int32 {
		v, err := strconv.Atoi(s)
		if err != nil {
			panic("worldgen: blueprint number " + s)
		}
		return int32(v)
	}
	var out blueprint
	for _, line := range strings.Split(src, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || strings.HasPrefix(f[0], "#") {
			continue
		}
		switch f[0] {
		case "box":
			op := bpOp{box: true, x0: num(f[1]), y0: num(f[2]), z0: num(f[3]), x1: num(f[4]), y1: num(f[5]), z1: num(f[6]), a: state(f[7]), b: state(f[8])}
			op.skipAir = len(f) > 9 && f[9] == "skip"
			out = append(out, op)
		case "sel":
			out = append(out, bpOp{sel: true, x0: num(f[1]), y0: num(f[2]), z0: num(f[3]), x1: num(f[4]), y1: num(f[5]), z1: num(f[6])})
		case "set":
			out = append(out, bpOp{x0: num(f[1]), y0: num(f[2]), z0: num(f[3]), a: state(f[4])})
		default:
			panic(fmt.Sprintf("worldgen: blueprint op %q", line))
		}
	}
	return out
}

func (bp blueprint) run(p *legacyPiece, c *PieceContext, bb BoundingBox) { bp.runSel(p, c, bb, nil) }

// runSel runs the blueprint with sel drawing from the piece random for
// the selector boxes.
func (bp blueprint) runSel(p *legacyPiece, c *PieceContext, bb BoundingBox, sel blockSelector) {
	for _, op := range bp {
		if op.sel {
			p.generateBoxSel(c, bb, op.x0, op.y0, op.z0, op.x1, op.y1, op.z1, false, c.Random, sel)
		} else if op.box {
			p.generateBox(c, bb, op.x0, op.y0, op.z0, op.x1, op.y1, op.z1, op.a, op.b, op.skipAir)
		} else {
			p.placeBlock(c, op.a, op.x0, op.y0, op.z0, bb)
		}
	}
}
