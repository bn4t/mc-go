package worldgen

import "strings"

// Structure locating, measured from the 26.2 jar
// (ChunkGenerator.findNearestMapStructure and its helpers, as /locate
// structure and the explorer-map / eye-of-ender callers use it):
//
//   - The wanted structures are grouped by placement, in holder-set
//     order: every possible structure set that lists a structure adds
//     the structure under that set's placement (insertion-ordered map
//     and sets). No placement at all is no result.
//   - Concentric-rings placements test their ring chunks in ring order:
//     a chunk is a candidate when its centre (x*16+8, 32, z*16+8) is
//     strictly nearer the source than the best so far, and it counts
//     only when one of the structures generates a start there.
//   - Random-spread placements then search square rings of grid cells
//     around the source chunk, r = 0..radius: for each placement the
//     border cells of ring r (x by x, z by z, cell offset spacing*d
//     chunks) give their potential chunk, and the first one holding a
//     start ends that placement's ring. The nearest result so far
//     (rings included) is returned as soon as a ring finds anything.
//   - A found structure is reported at its placement's locate position:
//     the start chunk's minimum block corner at y 0 plus locate_offset.
//     Distances are BlockPos.distSqr to the source (y included).
//
// Whether a chunk holds a start is StructureCheck.checkStart's answer,
// which for a chunk not yet generated runs the frequency reduction and
// the structure's generation point, then generates the chunk's starts;
// that equals the chunk's STRUCTURE_STARTS result, which StructureStarts
// computes.

// FindNearestMapStructure is ChunkGenerator.findNearestMapStructure
// with skipKnownStructures false: the nearest of the structures
// (holder-set order, namespace trimmed) to pos within radius placement
// cells, its locate position and which structure it is.
func (g *Generator) FindNearestMapStructure(structures []string, pos BlockPos, radius int32) (BlockPos, string, bool) {
	if !g.Structures {
		return BlockPos{}, "", false
	}
	st := g.structureState()
	type group struct {
		p     *structPlacement
		names []string
	}
	var groups []*group
	byPlacement := map[*structPlacement]*group{}
	for _, name := range structures {
		for _, set := range st.sets {
			for _, e := range set.entries {
				if e.Structure != name {
					continue
				}
				gr := byPlacement[set.placement]
				if gr == nil {
					gr = &group{p: set.placement}
					byPlacement[set.placement] = gr
					groups = append(groups, gr)
				}
				dup := false
				for _, n := range gr.names {
					dup = dup || n == name
				}
				if !dup {
					gr.names = append(gr.names, name)
				}
			}
		}
	}
	if len(groups) == 0 {
		return BlockPos{}, "", false
	}
	distSqr := func(a BlockPos) float64 {
		dx, dy, dz := float64(a[0]-pos[0]), float64(a[1]-pos[1]), float64(a[2]-pos[2])
		return dx*dx + dy*dy + dz*dz
	}
	var best BlockPos
	bestName := ""
	found := false
	bestDist := 1.7976931348623157e308
	var spread []*group
	for _, gr := range groups {
		switch gr.p.kind {
		case placeConcentricRings:
			if at, name, ok := g.nearestRingStructure(gr.names, gr.p, pos); ok {
				if d := distSqr(at); d < bestDist {
					bestDist, best, bestName, found = d, at, name, true
				}
			}
		default:
			spread = append(spread, gr)
		}
	}
	if len(spread) == 0 {
		return best, bestName, found
	}
	cx, cz := pos[0]>>4, pos[2]>>4
	for r := int32(0); r <= radius; r++ {
		any := false
		for _, gr := range spread {
			at, name, ok := g.spreadRingStructure(gr.names, gr.p, cx, cz, r)
			if !ok {
				continue
			}
			any = true
			if d := distSqr(at); d < bestDist {
				bestDist, best, bestName, found = d, at, name, true
			}
		}
		if any {
			return best, bestName, found
		}
	}
	return best, bestName, found
}

// nearestRingStructure is the concentric-rings
// getNearestGeneratedStructure.
func (g *Generator) nearestRingStructure(names []string, p *structPlacement, pos BlockPos) (BlockPos, string, bool) {
	g.ringPositions(p)
	var best BlockPos
	bestName := ""
	found := false
	bestDist := 1.7976931348623157e308
	for _, c := range p.ringList {
		dx, dy, dz := float64(c[0]*16+8-pos[0]), float64(32-pos[1]), float64(c[1]*16+8-pos[2])
		d := dx*dx + dy*dy + dz*dz
		if found && !(d < bestDist) {
			continue
		}
		if at, name, ok := g.structureGeneratingAt(names, p, c); ok {
			best, bestName, found, bestDist = at, name, true, d
		}
	}
	return best, bestName, found
}

// spreadRingStructure is the random-spread
// getNearestGeneratedStructure for ring r around chunk (cx, cz).
func (g *Generator) spreadRingStructure(names []string, p *structPlacement, cx, cz, r int32) (BlockPos, string, bool) {
	for dx := -r; dx <= r; dx++ {
		edgeX := dx == -r || dx == r
		for dz := -r; dz <= r; dz++ {
			if !edgeX && dz != -r && dz != r {
				continue
			}
			c := p.potentialChunk(g.RS.Seed, cx+p.spacing*dx, cz+p.spacing*dz)
			if at, name, ok := g.structureGeneratingAt(names, p, c); ok {
				return at, name, true
			}
		}
	}
	return BlockPos{}, "", false
}

// structureGeneratingAt is ChunkGenerator.getStructureGeneratingAt:
// the first of names with a start in chunk c, at its locate position.
func (g *Generator) structureGeneratingAt(names []string, p *structPlacement, c [2]int32) (BlockPos, string, bool) {
	starts := g.StructureStarts(c[0], c[1])
	for _, name := range names {
		for _, s := range starts {
			if s.Structure == name && len(s.Pieces) > 0 {
				return BlockPos{s.ChunkX*16 + p.locateOffset[0], p.locateOffset[1], s.ChunkZ*16 + p.locateOffset[2]}, name, true
			}
		}
	}
	return BlockPos{}, "", false
}

// StructureTag resolves a structure tag (namespace trimmed) to its
// structures in tag order, nested tags expanded in place; false when
// the tag does not exist.
func (g *Generator) StructureTag(tag string) ([]string, bool) {
	raw, ok := g.RS.data.Get("tags/worldgen/structure/" + trimNS(tag))
	if !ok {
		return nil, false
	}
	var t struct {
		Values []any `json:"values"`
	}
	jsonUnmarshal(raw, &t)
	var out []string
	seen := map[string]bool{}
	for _, v := range t.Values {
		id, _ := v.(string)
		if m, ok := v.(map[string]any); ok {
			id, _ = m["id"].(string)
		}
		if strings.HasPrefix(id, "#") {
			inner, _ := g.StructureTag(id[1:])
			for _, n := range inner {
				if !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
			}
			continue
		}
		if n := trimNS(id); n != "" && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, true
}
