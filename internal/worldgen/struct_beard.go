package worldgen

import (
	"math"
	"sync"
)

// Terrain adaptation (Beardifier): the density added to final_density
// around structure pieces with terrain adaptation and around jigsaw
// junctions, so the noise stage builds the ground up under (beard) or
// carves it away around (bury, encapsulate) structures.

const beardKernelRadius = 12

// beardKernel is Beardifier.BEARD_KERNEL: e^(-|(x, y+0.5, z)|^2/16)
// stored as float at ((z+12)*24 + (x+12))*24 + (y+12).
var beardKernel = sync.OnceValue(func() []float32 {
	k := make([]float32, 24*24*24)
	for i := 0; i < 24; i++ {
		for j := 0; j < 24; j++ {
			for l := 0; l < 24; l++ {
				x, y, z := float64(j-12), float64(l-12)+0.5, float64(i-12)
				d := float64(x*x) + float64(y*y) + float64(z*z)
				k[i*576+j*24+l] = float32(math.Pow(2.718281828459045, -d/16))
			}
		}
	}
	return k
})

func inKernel(v int32) bool { return v >= 0 && v < 24 }

// beardContribution is Beardifier.getBeardContribution.
func beardContribution(dx, dy, dz, dyRaw int32) float64 {
	i, j, k := dx+beardKernelRadius, dy+beardKernelRadius, dz+beardKernelRadius
	if !inKernel(i) || !inKernel(j) || !inKernel(k) {
		return 0
	}
	d := float64(dyRaw) + 0.5
	lsq := float64(float64(dx)*float64(dx)) + float64(d*d) + float64(float64(dz)*float64(dz))
	g := float64(-d*fastInvSqrt(lsq/2)) / 2
	return float64(g * float64(beardKernel()[k*576+i*24+j]))
}

// fastInvSqrt is Mth.fastInvSqrt (one Newton step).
func fastInvSqrt(x float64) float64 {
	half := 0.5 * x
	i := int64(math.Float64bits(x))
	i = 6910469410427058090 - i>>1
	y := math.Float64frombits(uint64(i))
	return float64(y * float64(1.5-float64(float64(half*y)*y)))
}

// buryContribution is Beardifier.getBuryContribution.
func buryContribution(x, y, z float64) float64 {
	l := math.Sqrt(float64(x*x) + float64(y*y) + float64(z*z))
	return clampedMap(l, 0, 6, 1, 0)
}

type beardRigid struct {
	box   BoundingBox
	adapt int
	gld   int32
}

type beardifier struct {
	rigids    []beardRigid
	junctions []JigsawJunction
	box       BoundingBox
}

// beardifierFor is Beardifier.forStructuresInChunk: rigid pieces (all
// non-jigsaw pieces, jigsaw pieces with rigid projection) within 12
// blocks of the chunk and jigsaw junctions within 12 blocks, affecting
// their union inflated by 24; nil when there are none.
func (g *Generator) beardifierFor(cx, cz int32) *beardifier {
	starts := g.startsReferencing(cx, cz, func(d *structureDef) bool { return d.adaptation != adaptNone })
	if len(starts) == 0 {
		return nil
	}
	minX, minZ := cx*16, cz*16
	b := &beardifier{}
	var union BoundingBox
	have := false
	include := func(x BoundingBox) {
		if !have {
			union, have = x, true
		} else {
			union = union.Encapsulate(x)
		}
	}
	for _, s := range starts {
		adapt := s.def.adaptation
		for _, p := range s.Pieces {
			pb := p.BoundingBox()
			if !pb.IntersectsXZ(minX-12, minZ-12, minX+15+12, minZ+15+12) {
				continue
			}
			jp, ok := p.(*JigsawPiece)
			if !ok {
				b.rigids = append(b.rigids, beardRigid{pb, adapt, 0})
				include(pb)
				continue
			}
			if jp.element.projection() == projRigid {
				b.rigids = append(b.rigids, beardRigid{pb, adapt, jp.GroundLvl})
				include(pb)
			}
			for _, j := range jp.Junctions {
				if j.SourceX > minX-12 && j.SourceZ > minZ-12 && j.SourceX < minX+15+12 && j.SourceZ < minZ+15+12 {
					b.junctions = append(b.junctions, j)
					include(BoundingBox{j.SourceX, j.SourceGroundY, j.SourceZ, j.SourceX, j.SourceGroundY, j.SourceZ})
				}
			}
		}
	}
	if !have {
		return nil
	}
	b.box = union.Inflated(24)
	return b
}

// compute is Beardifier.compute.
func (b *beardifier) compute(x, y, z int32) float64 {
	if !b.box.IsInside(BlockPos{x, y, z}) {
		return 0
	}
	d := 0.0
	for _, r := range b.rigids {
		bx := r.box
		dx := max(0, max(bx.MinX-x, x-bx.MaxX))
		dz := max(0, max(bx.MinZ-z, z-bx.MaxZ))
		minY := bx.MinY + r.gld
		dyRaw := y - minY
		var dy int32
		switch r.adapt {
		case adaptBury, adaptBeardThin:
			dy = dyRaw
		case adaptBeardBox:
			dy = max(0, max(minY-y, y-bx.MaxY))
		case adaptEncapsulate:
			dy = max(0, max(bx.MinY-y, y-bx.MaxY))
		}
		var c float64
		switch r.adapt {
		case adaptBury:
			c = buryContribution(float64(dx), float64(dy)/2, float64(dz))
		case adaptBeardThin, adaptBeardBox:
			c = float64(beardContribution(dx, dy, dz, dyRaw) * 0.8)
		case adaptEncapsulate:
			c = float64(buryContribution(float64(dx)/2, float64(dy)/2, float64(dz)/2) * 0.8)
		}
		d += c
	}
	for _, j := range b.junctions {
		dx, dy, dz := x-j.SourceX, y-j.SourceGroundY, z-j.SourceZ
		d += float64(beardContribution(dx, dy, dz, dy) * 0.4)
	}
	return d
}
