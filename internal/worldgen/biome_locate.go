package worldgen

import "encoding/json"

// FindClosestBiome3d is BiomeSource.findClosestBiome3d (measured from
// the 26.2 jar): nothing when no possible biome is wanted; else columns
// in BlockPos.spiralAround order (east, then south, ring by ring) out
// to radius/step cells, each column at pos + cell*step, sampled at the
// heights Mth.outFromOrigin gives - the source y clamped into
// [minY+1, maxY+1], then alternately above and below by vstep while
// either side stays in range - with uncached quart biome lookups. The
// first wanted biome found wins.
func (g *Generator) FindClosestBiome3d(pos BlockPos, radius, step, vstep int32, want func(string) bool) (BlockPos, string, bool) {
	possible := false
	for _, b := range g.Biomes.possibleBiomes() {
		possible = possible || want(b)
	}
	if !possible {
		return BlockPos{}, "", false
	}
	minY, maxY := g.RS.lvMinY, g.RS.lvMinY+g.RS.lvHeight-1
	ys := outFromOrigin(pos[1], minY+1, maxY+1, vstep)
	cells := floorDiv(radius, step)
	var out BlockPos
	name := ""
	found := false
	spiralCells(cells, func(dx, dz int32) bool {
		x, z := pos[0]+dx*step, pos[2]+dz*step
		qx, qz := x>>2, z>>2
		for _, y := range ys {
			if b := g.Biomes.Biome(g.RS, nil, qx, y>>2, qz); want(b) {
				out, name, found = BlockPos{x, y, z}, b, true
				return false
			}
		}
		return true
	})
	return out, name, found
}

// outFromOrigin is Mth.outFromOrigin(origin, lo, hi, step).
func outFromOrigin(origin, lo, hi, step int32) []int32 {
	s := min(max(origin, lo), hi)
	abs := func(v int32) int32 {
		if v < 0 {
			return -v
		}
		return v
	}
	var out []int32
	for v := s; ; {
		d := abs(s - v)
		if !(s-d >= lo || s+d <= hi) {
			break
		}
		out = append(out, v)
		below := v <= s
		up := s + d + step
		switch {
		case below && up <= hi:
			v = up
		default:
			n := s - d
			if below {
				n -= step
			}
			if n >= lo {
				v = n
			} else {
				v = up
			}
		}
	}
	return out
}

// spiralCells visits BlockPos.spiralAround(ZERO, r, EAST, SOUTH) until
// visit returns false.
func spiralCells(r int32, visit func(dx, dz int32) bool) {
	if !visit(0, 0) {
		return
	}
	x, z := int32(0), int32(0)
	for k := int32(1); k <= r; k++ {
		x++
		if !visit(x, z) {
			return
		}
		for _, leg := range [4]struct{ dx, dz, n int32 }{{0, 1, 2*k - 1}, {-1, 0, 2 * k}, {0, -1, 2 * k}, {1, 0, 2 * k}} {
			for i := int32(0); i < leg.n; i++ {
				x, z = x+leg.dx, z+leg.dz
				if !visit(x, z) {
					return
				}
			}
		}
	}
}

// BiomeTag resolves a biome tag (namespace trimmed) to its biomes;
// false when it does not exist.
func (g *Generator) BiomeTag(tag string) (map[string]bool, bool) {
	if _, ok := g.RS.data.Get("tags/worldgen/biome/" + trimNS(tag)); !ok {
		return nil, false
	}
	raw, _ := json.Marshal("#" + trimNS(tag))
	return biomeHolderSet(g.RS.data, raw), true
}
