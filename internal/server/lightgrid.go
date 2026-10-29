package server

import (
	"sort"

	"mc-go/internal/world"
)

// blockLightSection is one section's stored block light and how many
// of its cells are non-zero.
type blockLightSection struct {
	cells [4096]uint8
	lit   int
}

// blockLightGrid is a dimension's block-light storage: per-chunk,
// per-section (absolute section y) cell arrays, so a generated world's
// lava and lichen light stays a few arrays per chunk instead of a map
// entry per lit cell.
type blockLightGrid struct {
	secs map[[2]int]map[int]*blockLightSection
	n    int
}

func blockLightIndex(p world.Pos) int { return (p.Y&15)<<8 | (p.Z&15)<<4 | p.X&15 }

func (g *blockLightGrid) section(p world.Pos, create bool) *blockLightSection {
	ck := [2]int{p.X >> 4, p.Z >> 4}
	col := g.secs[ck]
	if col == nil {
		if !create {
			return nil
		}
		if g.secs == nil {
			g.secs = map[[2]int]map[int]*blockLightSection{}
		}
		col = map[int]*blockLightSection{}
		g.secs[ck] = col
	}
	s := col[p.Y>>4]
	if s == nil && create {
		s = &blockLightSection{}
		col[p.Y>>4] = s
	}
	return s
}

// get returns the stored block light at p.
func (g *blockLightGrid) get(p world.Pos) uint8 {
	if s := g.section(p, false); s != nil {
		return s.cells[blockLightIndex(p)]
	}
	return 0
}

// set stores v (non-zero) at p.
func (g *blockLightGrid) set(p world.Pos, v uint8) {
	s := g.section(p, true)
	i := blockLightIndex(p)
	if s.cells[i] == 0 {
		s.lit++
		g.n++
	}
	s.cells[i] = v
}

// del zeroes p, reporting whether it was lit.
func (g *blockLightGrid) del(p world.Pos) bool {
	s := g.section(p, false)
	if s == nil {
		return false
	}
	i := blockLightIndex(p)
	if s.cells[i] == 0 {
		return false
	}
	s.cells[i] = 0
	s.lit--
	g.n--
	return true
}

// len is the number of lit cells.
func (g *blockLightGrid) len() int { return g.n }

// litSections returns the absolute section ys of column (cx, cz)
// holding a lit cell, ascending.
func (g *blockLightGrid) litSections(cx, cz int) []int {
	var out []int
	for sy, s := range g.secs[[2]int{cx, cz}] {
		if s.lit > 0 {
			out = append(out, sy)
		}
	}
	sort.Ints(out)
	return out
}
