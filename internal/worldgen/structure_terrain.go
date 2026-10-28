package worldgen

// Structures measure the ground before any chunk exists: the generator
// runs the noise stage down single columns, without surface, carvers or
// features.

// The heightmaps a structure can measure the noise terrain with.
const (
	// worldSurfaceWG tops the highest block that is not air.
	worldSurfaceWG = iota
	// oceanFloorWG tops the highest block that blocks motion: not air
	// and not a fluid.
	oceanFloorWG
)

func (b NoiseBlock) opaqueFor(heightmap int) bool {
	switch b {
	case NBAir:
		return false
	case NBWater, NBLava:
		return heightmap == worldSurfaceWG
	}
	return true
}

// baseHeight is the height of the noise terrain at block (x, z) by a
// heightmap: one above the highest block it counts, or the dimension's
// lowest Y if it counts none.
func (g *Generator) baseHeight(x, z int32, heightmap int) int32 {
	h := int32(g.rs.Settings.MinY)
	w := <-g.workers
	w.filler.Column(x, z, func(y int32, b NoiseBlock) bool {
		if b.opaqueFor(heightmap) {
			h = y + 1
			return false
		}
		return true
	})
	g.workers <- w
	return h
}

// firstOccupiedHeight is the Y of the highest block a heightmap counts
// at block (x, z).
func (g *Generator) firstOccupiedHeight(x, z int32, heightmap int) int32 {
	return g.baseHeight(x, z, heightmap) - 1
}

// baseColumn is the noise terrain's column at block (x, z), by Y from the
// dimension's lowest.
func (g *Generator) baseColumn(x, z int32) []StateID {
	set := g.rs.Settings
	out := make([]StateID, set.Height)
	w := <-g.workers
	states := w.filler.noiseStates()
	w.filler.Column(x, z, func(y int32, b NoiseBlock) bool {
		out[y-int32(set.MinY)] = states[b]
		return true
	})
	g.workers <- w
	return out
}
