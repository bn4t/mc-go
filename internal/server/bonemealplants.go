package server

import "mc-go/internal/world"

// bonemealDoublePlant implements the measured SeagrassBlock and
// TallGrassBlock target gates. Their growth writes both halves with flag 2,
// so a half-written pair must not run neighbor survival between the writes.
func (s *Server) bonemealDoublePlant(d *dimension, pos world.Pos, name string) (bool, bool) {
	var grown string
	above := posAbove(pos, 1)
	switch name {
	case "seagrass":
		// The block identity matters: flowing water qualifies too, while
		// a waterlogged block does not.
		if !isBlockNamed(d.w.GetBlock(above), "water") {
			return false, true
		}
		grown = "tall_seagrass"
	case "short_grass", "fern":
		below, _ := world.StateOwner(d.w.GetBlock(posBelow(pos, 1)))
		if above.Y >= d.minY+d.height || !isAirCell(d.w.GetBlock(above)) || !blockTagHas("supports_vegetation", below) {
			return false, true
		}
		grown = "tall_grass"
		if name == "fern" {
			grown = "large_fern"
		}
	default:
		return false, false
	}
	lower, lok := world.BlockStateOf(grown, map[string]string{"half": "lower"})
	upper, uok := world.BlockStateOf(grown, map[string]string{"half": "upper"})
	if !lok || !uok {
		return false, true
	}
	s.setSilentBlock(d, pos, lower)
	s.setSilentBlock(d, above, upper)
	return true, true
}
