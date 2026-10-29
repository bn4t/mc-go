package worldgen

import "sync"

// Noise-only column heights for structure placement
// (NoiseBasedChunkGenerator.getBaseHeight / iterateNoiseColumn): a
// one-cell NoiseChunk at the column's cell, walked from the top cell
// down, each block's state from the aquifer and ore veins, the first
// block passing the heightmap's predicate ending the walk. No
// beardifier (BeardifierMarker), no surface, carvers or features.

type baseHeightKey struct {
	x, z int32
	hm   int
}

type baseHeightCache struct {
	mu sync.Mutex
	m  map[baseHeightKey]int32
}

// BaseHeight is ChunkGenerator.getBaseHeight (getFirstFreeHeight):
// one above the highest block of the noise column at (x, z) that the
// heightmap type counts (HMWorldSurfaceWG: not air; HMOceanFloorWG:
// blocks motion), the level floor when none does.
func (g *Generator) BaseHeight(x, z int32, hm int) int32 {
	k := baseHeightKey{x, z, hm}
	g.baseHeights.mu.Lock()
	v, ok := g.baseHeights.m[k]
	g.baseHeights.mu.Unlock()
	if ok {
		return v
	}
	v = g.RS.iterateNoiseColumn(x, z, hm)
	g.baseHeights.mu.Lock()
	// A pure cache: dropped wholesale when it grows large.
	if g.baseHeights.m == nil || len(g.baseHeights.m) >= 1<<20 {
		g.baseHeights.m = map[baseHeightKey]int32{}
	}
	g.baseHeights.m[k] = v
	g.baseHeights.mu.Unlock()
	return v
}

// firstOccupiedHeight is ChunkGenerator.getFirstOccupiedHeight.
func (g *Generator) firstOccupiedHeight(x, z int32, hm int) int32 { return g.BaseHeight(x, z, hm) - 1 }

func (rs *RandomState) iterateNoiseColumn(x, z int32, hm int) int32 {
	s := rs.settings
	// NoiseSettings.clampToHeightAccessor against the level bounds.
	minY := max(s.minY, rs.lvMinY)
	top := min(s.minY+s.height, rs.lvMinY+rs.lvHeight)
	cellW, cellH := s.sizeHorizontal*4, s.sizeVertical*4
	cellMinY := floorDiv(minY, cellH)
	cellCountY := floorDiv(top-minY, cellH)
	if cellCountY <= 0 {
		return rs.lvMinY
	}
	cellX, cellZ := floorDiv(x, cellW), floorDiv(z, cellW)
	fx := float64(x-cellX*cellW) / float64(cellW)
	fz := float64(z-cellZ*cellW) / float64(cellW)
	nc := newNoiseChunkCells(rs, 1, cellX*cellW, cellZ*cellW, false)
	nc.lazyCell = true
	pred := notAir
	if hm == HMOceanFloorWG || hm == HMOceanFloor {
		pred = blocksMotion
	}
	subs := substanceBlocks()
	def := blockNamed(s.defaultBlock)
	nc.initializeForFirstCellX()
	nc.advanceCellX(0)
	defer func() { nc.interpolating = false }()
	for cy := cellCountY - 1; cy >= 0; cy-- {
		nc.selectCellYZ(cy, 0)
		for yy := cellH - 1; yy >= 0; yy-- {
			y := (cellMinY+cy)*cellH + yy
			nc.updateForY(y, float64(yy)/float64(cellH))
			nc.updateForX(x, fx)
			nc.updateForZ(z, fz)
			sub, _ := nc.substance()
			b := def
			if sub != SubDefault {
				b = subs[sub]
			}
			if pred(b) {
				return y + 1
			}
		}
	}
	return rs.lvMinY
}
